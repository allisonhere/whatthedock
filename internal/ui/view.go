package ui

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	sp "github.com/allisonhere/cli-spinners"
	"github.com/allisonhere/tideui"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/config"
	"github.com/allisonhere/whatthedock/internal/domain"
	"github.com/allisonhere/whatthedock/internal/systems"
)

func (m Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	renderer := tideui.NewRenderer(m.theme, tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners, ModalShadow: m.settings.ModalShadow})
	topbar := m.renderTopbar(renderer)
	if m.height == 1 {
		return topbar
	}
	activityTitle, activityHint := m.activityHeader()
	inspectorTitle := "Inspector"
	if selected := m.selectedContainer(); selected != nil {
		inspectorTitle = "Inspector: " + containerTitle(*selected)
	}
	panes := [3]tideui.Pane{
		{Title: "Projects", Hint: "tree", Content: m.renderTree(renderer), Focused: m.focus == paneTree},
		{Title: activityTitle, Hint: activityHint, Content: m.renderActivity(renderer), Focused: m.focus == paneActivity},
		{Title: inspectorTitle, Hint: "details", Content: m.renderInspector(renderer), Focused: m.focus == paneInspector},
	}
	modal := m.renderOverlay(renderer)
	status := &tideui.StatusBar{
		Left:  m.statusLeft(renderer),
		Right: "j/k move  space expand  / filter  n create  l logs  p problems  g stats  S systems  , settings  ctrl+k commands  ? help  A about  q quit",
	}
	return topbar + "\n" + renderer.Render(tideui.Layout{
		Width:        m.width,
		Height:       m.height - 1,
		Mode:         tideui.ThreeColumn,
		Panes:        panes,
		Status:       status,
		Modal:        modal,
		ColumnRatios: [3]float64{3, 5, 4},
	})
}

func (m Model) activityHeader() (string, string) {
	switch m.mode {
	case activityProblems:
		return "Problems", "activity"
	case activityStats:
		if selected := m.selectedContainer(); selected != nil {
			return "Stats: " + containerTitle(*selected), "activity"
		}
		return "Stats", "activity"
	default:
		if selected := m.selectedContainer(); selected != nil {
			return "Logs: " + containerTitle(*selected), "logs"
		}
		return "Activity", "logs"
	}
}

func containerTitle(ctr domain.Container) string {
	return ctr.DisplayName()
}

func (m Model) renderTopbar(renderer tideui.Renderer) string {
	width := max(1, m.width)
	left := renderer.Styles.StatusNotice.Render(" WHAT THE DOCK?! ") +
		renderer.Styles.StatusBar.Render(" "+m.provider.Host().Name)
	right := fmt.Sprintf("Docker connected · %d projects · %d standalone · %d problems",
		len(m.snapshot.Projects), len(m.snapshot.Standalone), len(m.snapshotProblems()))
	if m.statusErr {
		right = m.status
	}
	return renderer.Styles.StatusBar.Width(width).Render(alignText(left, right, width))
}

func (m Model) renderTree(renderer tideui.Renderer) string {
	if m.statusErr && len(m.rows) == 0 {
		return renderer.Styles.DetailMeta.Render("Docker state is unavailable.\n\n" + m.status)
	}
	if len(m.rows) == 0 {
		if m.loading {
			return renderer.Styles.DetailMeta.Render("Loading containers...")
		}
		if strings.TrimSpace(m.filter) != "" {
			return renderer.Styles.DetailMeta.Render("No projects or containers match " + m.filter + ".")
		}
		return renderer.Styles.DetailMeta.Render("No containers found on this host.")
	}
	width := m.leftPaneWidth() - 4
	if width < 12 {
		width = 12
	}
	lines := make([]string, 0, len(m.rows))
	for i, row := range m.rows {
		selected := i == m.cursor
		prefix := strings.Repeat("  ", row.depth)
		suffix := ""
		text := row.label
		switch row.kind {
		case rowProject:
			if m.collapsed[row.project] {
				prefix += "▸ "
			} else {
				prefix += "▾ "
			}
		case rowService:
			prefix += "  "
		case rowContainer:
			healthColor := inspectorStatusColor(*row.container)
			baseFg := rowForeground(renderer, selected, row.muted)
			prefix += healthSpan(statusGlyph(*row.container), healthColor, baseFg) + " "
			suffix = healthSpan(statusText(*row.container), healthColor, baseFg)
			text = containerTitle(*row.container)
			// Dead is the one state that's actually gone, not just
			// stopped/unhealthy — coloring only the small glyph and the
			// trailing status word (as every other state does) left it
			// easy to miss in a scrolling list; the name itself needs to
			// read as red too, the same way the log pane's own health
			// header already colors a selected container's full title.
			if row.container.State == domain.StateDead {
				text = healthSpan(text, healthColor, baseFg)
			}
		case rowSection:
			prefix += ""
		}
		lines = append(lines, renderer.RenderRow(tideui.Row{Prefix: prefix, Text: text, Suffix: suffix, Selected: selected, Muted: row.muted}, width))
	}
	start, end := visibleRange(len(lines), m.cursor, m.treeVisibleRows())
	content := strings.Join(lines[start:end], "\n")
	return m.withPaneActionStrip(renderer, paneTree, width, content)
}

// visibleRange returns the [start, end) window of size at most limit that
// keeps cursor in view, centering on it when the content overflows the
// window. Mirrors tideui's own picker.go windowing so the tree pane scrolls
// to follow the selection the same way tideui's built-in list widgets do.
func visibleRange(total, cursor, limit int) (int, int) {
	if limit >= total {
		return 0, total
	}
	start := cursor - limit/2
	start = clamp(start, 0, total-limit)
	return start, start + limit
}

// rowForeground returns the foreground color RenderRow will use for a row in
// the given state, so a colored span within that row can restore it exactly.
func rowForeground(renderer tideui.Renderer, selected, muted bool) lipgloss.Color {
	style := renderer.Styles.Item
	switch {
	case selected:
		style = renderer.Styles.ItemSelected
	case muted:
		style = renderer.Styles.ItemMuted
	}
	return styleForeground(style, renderer.Styles.Theme.Fg)
}

// styleForeground returns a style's resolved foreground color, so a colored
// span rendered through that style can restore it exactly.
func styleForeground(style lipgloss.Style, fallback lipgloss.Color) lipgloss.Color {
	if c, ok := style.GetForeground().(lipgloss.Color); ok {
		return c
	}
	return fallback
}

// healthSpan colors text without a trailing SGR reset, so it can sit inside a
// RenderRow prefix/suffix without clobbering the row's own background.
func healthSpan(text string, color, restore lipgloss.Color) string {
	return ansi.NewStyle().ForegroundColor(color).String() + text +
		ansi.NewStyle().ForegroundColor(restore).String()
}

func (m Model) renderActivity(renderer tideui.Renderer) string {
	if m.mode == activityStats {
		content, width := m.renderStatsContent(renderer)
		return m.withPaneActionStrip(renderer, paneActivity, width, content)
	}
	if m.mode == activityProblems {
		content, width := m.renderProblemsSplit(renderer)
		return m.withPaneActionStrip(renderer, paneActivity, width, content)
	}
	width := m.centerPaneWidth() - 4
	if width < 20 {
		width = 20
	}
	if m.selected == nil {
		return m.withPaneActionStrip(renderer, paneActivity, width, renderer.Styles.DetailMeta.Render("Select a container to view live logs."))
	}
	if m.logErr != nil {
		return m.withPaneActionStrip(renderer, paneActivity, width, renderer.Styles.StatusError.Render(friendlyDockerError(m.logErr)))
	}
	if len(m.logLines) == 0 {
		return m.withPaneActionStrip(renderer, paneActivity, width, renderer.Styles.DetailMeta.Render("Waiting for logs from "+containerTitle(*m.selected)+"..."))
	}
	filtered := m.visibleLogLines()
	if len(filtered) == 0 {
		return m.withPaneActionStrip(renderer, paneActivity, width, renderer.Styles.DetailMeta.Render(m.emptyLogFilterMessage()))
	}
	visible := m.logVisibleRows()
	start := m.logStartIndex(len(filtered), visible)
	end := min(len(filtered), start+visible)
	lines := make([]string, 0, end-start+1)
	header := m.logPositionIndicator(len(filtered), visible)
	if m.settings.LogHealthColor {
		healthColor := inspectorStatusColor(*m.selected)
		baseFg := styleForeground(renderer.Styles.DetailMeta, renderer.Styles.Theme.Dimmed)
		header = healthSpan(statusGlyph(*m.selected), healthColor, baseFg) + " " +
			healthSpan(containerTitle(*m.selected), healthColor, baseFg) + "  " + header
	}
	lines = append(lines, renderer.Styles.DetailMeta.Width(width).Render(header))
	for _, line := range filtered[start:end] {
		lines = append(lines, renderer.Styles.DetailBody.Width(width).Render(renderLogLine(renderer, m.settings.LogColor, m.logFilter, line)))
	}
	return m.withPaneActionStrip(renderer, paneActivity, width, strings.Join(lines, "\n"))
}

func (m Model) logStartIndex(total, visible int) int {
	if total <= 0 {
		return 0
	}
	maxStart := max(0, total-visible)
	if m.logFollow {
		return maxStart
	}
	return clamp(m.logScroll, 0, maxStart)
}

func (m Model) logPositionIndicator(total, visible int) string {
	if total == 0 {
		return strings.Join(append([]string{"tail"}, m.logFilterChips()...), " · ")
	}
	start := m.logStartIndex(total, visible)
	end := min(total, start+visible)
	chips := m.logFilterChips()
	if m.logFollow {
		position := fmt.Sprintf("tail %d/%d", total, total)
		if total > visible {
			position = "▲ " + position + " · bottom"
		}
		return strings.Join(append([]string{position}, chips...), " · ")
	}
	position := fmt.Sprintf("paused %d-%d/%d", start+1, end, total)
	switch {
	case start == 0 && end == total:
		// Everything fits on screen; nothing to scroll.
	case start == 0:
		position = "▼ " + position + " · top"
	case end == total:
		position = "▲ " + position + " · bottom"
	default:
		position = "▲▼ " + position
	}
	return strings.Join(append([]string{position}, chips...), " · ")
}

func (m Model) logFilterChips() []string {
	var chips []string
	if query := strings.TrimSpace(m.logFilter); query != "" {
		chips = append(chips, "filter "+query)
		if current, total := m.logMatchStatus(); total > 0 {
			chips = append(chips, fmt.Sprintf("match %d/%d", current, total))
		}
	}
	if m.logLevel != logSeverityAll {
		chips = append(chips, m.logLevel.String())
	}
	if len(chips) > 0 {
		chips = append(chips, "x clear")
	}
	return chips
}

func (m Model) emptyLogFilterMessage() string {
	query := strings.TrimSpace(m.logFilter)
	if query == "" && m.logLevel == logSeverityAll {
		return "No logs match."
	}
	var target string
	if query != "" {
		target = fmt.Sprintf("%q", query)
	}
	if m.logLevel != logSeverityAll {
		if target != "" {
			target += " · "
		}
		target += m.logLevel.String()
	}
	return "No logs match " + target + " · esc clear"
}

var (
	logTimestampPattern  = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T[^\s]+|\d{2}:\d{2}:\d{2})`)
	logSeverityPattern   = regexp.MustCompile(`(?i)(\[(?:ERR|ERROR|WRN|WARN|INF|INFO|DBG|DEBUG)\]|\b(?:ERROR|ERR|WARN|WRN|INFO|INF|DEBUG|DBG)\b)`)
	logHTTPStatusPattern = regexp.MustCompile(`\b([1-5][0-9]{2})\b`)
	logHTTPMethodPattern = regexp.MustCompile(`\b(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\b`)
)

func (m Model) visibleLogLines() []string {
	query := strings.ToLower(strings.TrimSpace(m.logFilter))
	if query == "" && m.logLevel == logSeverityAll {
		return m.logLines
	}
	lines := make([]string, 0, len(m.logLines))
	for _, line := range m.logLines {
		if query != "" && !strings.Contains(strings.ToLower(line), query) {
			continue
		}
		if !logLineMatchesSeverity(line, m.logLevel) {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func logLineMatchesSeverity(line string, filter logSeverityFilter) bool {
	if filter == logSeverityAll {
		return true
	}
	severity := logLineSeverity(line)
	switch filter {
	case logSeverityErrors:
		return severity == "ERROR" || severity == "ERR"
	case logSeverityWarnings:
		return severity == "WARN" || severity == "WRN"
	case logSeverityInfo:
		return severity == "INFO" || severity == "INF"
	default:
		return true
	}
}

func logLineSeverity(line string) string {
	for start := 0; start < len(line); {
		end := start
		if isLogSpace(line[start]) {
			start++
			continue
		}
		for end < len(line) && !isLogSpace(line[end]) {
			end++
		}
		token := line[start:end]
		if logSeverityPattern.MatchString(token) {
			return strings.Trim(strings.ToUpper(token), "[]")
		}
		start = end
	}
	return ""
}

func renderLogLine(renderer tideui.Renderer, mode logColorMode, query, line string) string {
	var out strings.Builder
	for start := 0; start < len(line); {
		end := start
		if isLogSpace(line[start]) {
			for end < len(line) && isLogSpace(line[end]) {
				end++
			}
			out.WriteString(line[start:end])
			start = end
			continue
		}
		for end < len(line) && !isLogSpace(line[end]) {
			end++
		}
		token := line[start:end]
		rendered := renderLogToken(renderer, mode, token, start == 0)
		out.WriteString(renderLogMatch(renderer, query, token, rendered))
		start = end
	}
	return out.String()
}

func renderLogToken(renderer tideui.Renderer, mode logColorMode, token string, first bool) string {
	if mode == logColorMono {
		return token
	}
	trimmed := strings.Trim(token, `[](),;:"'`)
	switch {
	case first && logTimestampPattern.MatchString(token) && (mode == logColorFull || mode == logColorHTTP || mode == logColorSeverity):
		return logStyle(renderer, "#8aadf4", false).Render(token)
	case logHTTPMethodPattern.MatchString(trimmed) && (mode == logColorFull || mode == logColorHTTP):
		return logStyle(renderer, "#7dcfff", true).Render(token)
	case logHTTPStatusPattern.MatchString(trimmed) && (mode == logColorFull || mode == logColorHTTP):
		return logStyle(renderer, httpStatusColor(trimmed), true).Render(token)
	case logSeverityPattern.MatchString(token) && (mode == logColorFull || mode == logColorSeverity):
		return logStyle(renderer, logSeverityColor(token), true).Render(token)
	default:
		return token
	}
}

func renderLogMatch(renderer tideui.Renderer, query, token, rendered string) string {
	query = strings.TrimSpace(query)
	if query == "" || !strings.Contains(strings.ToLower(token), strings.ToLower(query)) {
		return rendered
	}
	if ansi.Strip(rendered) != token {
		return lipgloss.NewStyle().
			Background(renderer.Styles.Theme.Selected).
			Foreground(renderer.Styles.Theme.Fg).
			Bold(true).
			Render(token)
	}
	return lipgloss.NewStyle().
		Background(renderer.Styles.Theme.Selected).
		Foreground(renderer.Styles.Theme.Fg).
		Bold(true).
		Render(rendered)
}

func isLogSpace(char byte) bool {
	return char == ' ' || char == '\t'
}

func logStyle(renderer tideui.Renderer, color lipgloss.Color, bold bool) lipgloss.Style {
	return lipgloss.NewStyle().Background(renderer.Styles.Theme.Bg).Foreground(color).Bold(bold)
}

func httpStatusColor(status string) lipgloss.Color {
	switch {
	case strings.HasPrefix(status, "5"):
		return "#e06c75"
	case strings.HasPrefix(status, "4"):
		return "#f5a97f"
	case strings.HasPrefix(status, "3"):
		return "#e8c170"
	case strings.HasPrefix(status, "2"):
		return "#80c990"
	default:
		return "#9aa6b2"
	}
}

func logSeverityColor(severity string) lipgloss.Color {
	normalized := strings.Trim(strings.ToUpper(severity), "[]")
	switch normalized {
	case "ERROR", "ERR":
		return "#e06c75"
	case "WARN", "WRN":
		return "#e8c170"
	case "DEBUG", "DBG":
		return "#9aa6b2"
	default:
		return "#80c990"
	}
}

func (m Model) renderStatsContent(renderer tideui.Renderer) (string, int) {
	ctr := m.selectedContainer()
	width := m.centerPaneWidth() - 4
	if width < 20 {
		width = 20
	}
	if ctr == nil {
		return renderer.Styles.DetailMeta.Render("Select a container to view stats."), width
	}
	stats := m.stats
	if stats != nil && stats.ID != ctr.ID {
		stats = nil
	}
	history := m.statsHistory[ctr.ID]
	// statsLoading only shows the spinner for a genuine first load (see
	// statsTickMsg in model.go — background polls never re-arm it), so this
	// stays a one-time animation, not a flash that repeats every poll. A
	// stats fetch that keeps failing keeps this a quiet blank spacer row
	// rather than surfacing statsErr as text (see
	// TestStatsViewOmitsSampledNoticeAfterStatsLoad) — deliberate, not an
	// oversight.
	header := renderer.Styles.DetailMeta.Render("")
	if m.statsLoading {
		header = renderer.Styles.DetailMeta.Render(spinnerGlyph(m.statusPulseFrame) + " loading stats…")
	}
	lines := []string{
		header,
		renderStatRow(renderer, m.settings, width, "CPU", cpuStatGraph(stats, history), formatCPU(stats), "#7dcfff"),
		renderStatRow(renderer, m.settings, width, "Memory", uintStatGraph(history.Memory, history.maxMemory, memoryLevel(stats), formatByteDelta), formatMemoryStats(stats), "#80c990"),
		renderStatRow(renderer, m.settings, width, "Net In", uintStatGraph(history.NetworkRx, history.maxNetwork, byteLevel(statsNetworkRx(stats)), formatByteDelta), formatBytes(statsNetworkRx(stats)), "#8aadf4"),
		renderStatRow(renderer, m.settings, width, "Net Out", uintStatGraph(history.NetworkTx, history.maxNetwork, byteLevel(statsNetworkTx(stats)), formatByteDelta), formatBytes(statsNetworkTx(stats)), "#8aadf4"),
		renderStatRow(renderer, m.settings, width, "Disk IO", uintStatGraph(history.BlockTotal, history.maxBlock, byteLevel(statsBlockTotal(stats)), formatByteDelta), formatBytes(statsBlockRead(stats))+" / "+formatBytes(statsBlockWrite(stats)), "#e8c170"),
		"",
		renderStatRow(renderer, m.settings, width, "Restarts", staticStatGraph(staticGraphGlyph(m.settings, restartLevel(ctr.RestartCount)), restartLevel(ctr.RestartCount)), fmt.Sprintf("%d", ctr.RestartCount), restartColor(ctr.RestartCount)),
		renderStatRow(renderer, m.settings, width, "Uptime", staticStatGraph(staticGraphGlyph(m.settings, uptimeLevel(ctr.Created)), uptimeLevel(ctr.Created)), formatDuration(ctr.Created), "#9aa6b2"),
		renderStatRow(renderer, m.settings, width, "PIDs", uintStatGraph(history.PIDs, history.maxPIDs, pidsLevel(stats), formatCountDelta), formatPIDs(stats), "#9aa6b2"),
		renderer.RenderRow(tideui.Row{Prefix: "State    ", Text: statusText(*ctr), Suffix: containerTitle(*ctr)}, width),
	}
	if m.focus == paneActivity {
		limit := max(1, m.activityVisibleRows()-2)
		lines = lines[:min(len(lines), limit)]
	}
	return strings.Join(lines, "\n"), width
}

type statGraph struct {
	values        []float64
	maxValue      float64
	fallbackLevel int
	delta         string
	static        string
}

func statsCPU(stats *domain.ContainerStats) float64 {
	if stats == nil {
		return 0
	}
	return stats.CPUPercent
}

func statsNetworkRx(stats *domain.ContainerStats) uint64 {
	if stats == nil {
		return 0
	}
	return stats.NetworkRx
}

func statsNetworkTx(stats *domain.ContainerStats) uint64 {
	if stats == nil {
		return 0
	}
	return stats.NetworkTx
}

func statsBlockRead(stats *domain.ContainerStats) uint64 {
	if stats == nil {
		return 0
	}
	return stats.BlockRead
}

func statsBlockWrite(stats *domain.ContainerStats) uint64 {
	if stats == nil {
		return 0
	}
	return stats.BlockWrite
}

func statsBlockTotal(stats *domain.ContainerStats) uint64 {
	return statsBlockRead(stats) + statsBlockWrite(stats)
}

func percentLevel(value float64) int {
	switch {
	case value >= 80:
		return 7
	case value >= 60:
		return 6
	case value >= 40:
		return 5
	case value >= 20:
		return 4
	case value > 0:
		return 3
	default:
		return 1
	}
}

func memoryLevel(stats *domain.ContainerStats) int {
	if stats == nil || stats.MemoryUsage == 0 {
		return 1
	}
	if stats.MemoryLimit == 0 {
		return byteLevel(stats.MemoryUsage)
	}
	return percentLevel(float64(stats.MemoryUsage) / float64(stats.MemoryLimit) * 100)
}

func byteLevel(value uint64) int {
	switch {
	case value >= 1<<30:
		return 7
	case value >= 512<<20:
		return 6
	case value >= 128<<20:
		return 5
	case value >= 32<<20:
		return 4
	case value > 0:
		return 3
	default:
		return 1
	}
}

func pidsLevel(stats *domain.ContainerStats) int {
	if stats == nil {
		return 1
	}
	switch {
	case stats.PIDs >= 100:
		return 7
	case stats.PIDs >= 50:
		return 6
	case stats.PIDs >= 20:
		return 5
	case stats.PIDs > 0:
		return 3
	default:
		return 1
	}
}

func cpuStatGraph(stats *domain.ContainerStats, history statsHistory) statGraph {
	if len(history.CPU) == 0 {
		return statGraph{fallbackLevel: percentLevel(statsCPU(stats))}
	}
	maxValue := history.maxCPU
	if maxValue < 100 {
		maxValue = 100
	}
	return statGraph{
		values:        history.CPU,
		maxValue:      maxValue,
		fallbackLevel: percentLevel(statsCPU(stats)),
		delta:         formatPercentDelta(floatDelta(history.CPU)),
	}
}

func uintStatGraph(values []uint64, maxValue uint64, fallbackLevel int, formatDelta func(int64) string) statGraph {
	if len(values) == 0 || maxValue == 0 {
		return statGraph{fallbackLevel: fallbackLevel}
	}
	asFloat := make([]float64, 0, len(values))
	for _, value := range values {
		asFloat = append(asFloat, float64(value))
	}
	return statGraph{
		values:        asFloat,
		maxValue:      float64(maxValue),
		fallbackLevel: fallbackLevel,
		delta:         formatDelta(uintDelta(values)),
	}
}

func staticStatGraph(value string, fallbackLevel int) statGraph {
	return statGraph{static: value, fallbackLevel: fallbackLevel}
}

// renderStatRow builds the row's Text itself instead of leaving Prefix/
// Text/Suffix alignment to RenderRow's alignRow: alignRow pads the gap
// between Text and Suffix with bare, unstyled spaces baked directly into
// the string, and RenderRow's own outer style only paints padding *it*
// appends via Width(), not padding already present in the string it's
// given — so that gap falls through to the raw terminal default the moment
// anything before it (the meter or sparkline glyphs) has already emitted
// its own background and reset. Computing the same budget alignRow would
// (prefixWidth=9, one space before a non-empty suffix) and filling every
// byte of it here, with an explicit background, leaves alignRow nothing
// unstyled to pad.
func renderStatRow(renderer tideui.Renderer, settings appSettings, width int, label string, graph statGraph, suffix string, color lipgloss.Color) string {
	bg := renderer.Styles.Theme.Bg
	const prefixWidth = 9
	remaining := max(0, width-prefixWidth)
	suffixWidth := lipgloss.Width(suffix)
	gap := 0
	if suffix != "" {
		gap = 1
	}
	room := max(0, remaining-suffixWidth-gap)

	var text string
	if room >= 8 {
		text = renderHybridGraph(renderer, settings, graph, color, room)
	}
	pad := lipgloss.NewStyle().Width(max(0, room-lipgloss.Width(text))).Background(bg).Render("")
	line := text + pad
	if suffix != "" {
		line += lipgloss.NewStyle().Background(bg).Render(" ") + renderer.Styles.DetailBody.Render(suffix)
	}
	return renderer.RenderRow(tideui.Row{Prefix: fmt.Sprintf("%-9s", label), Text: line}, width)
}

func renderHybridGraph(renderer tideui.Renderer, settings appSettings, graph statGraph, color lipgloss.Color, width int) string {
	if width < 12 {
		return renderSparkline(renderer, settings, graph, color, width)
	}
	meter := renderMeter(renderer, settings, graphLevel(settings, graph), color)
	sparkWidth := width - lipgloss.Width(meter) - 2
	delta := graph.delta
	if !settings.ShowDeltas {
		delta = ""
	}
	if delta != "" && width >= 24 {
		deltaWidth := lipgloss.Width(delta) + 2
		if sparkWidth-deltaWidth >= 6 {
			sparkWidth -= deltaWidth
		} else {
			delta = ""
		}
	} else {
		delta = ""
	}
	spark := renderSparkline(renderer, settings, graph, color, max(1, sparkWidth))
	parts := []string{meter, spark}
	if delta != "" {
		parts = append(parts, renderer.Styles.DetailMeta.Render(delta))
	}
	// The gap between parts is otherwise a bare, unstyled string — same
	// falls-through-to-default issue as the suffix above.
	gap := lipgloss.NewStyle().Background(renderer.Styles.Theme.Bg).Render("  ")
	return strings.Join(parts, gap)
}

func renderMeter(renderer tideui.Renderer, settings appSettings, level int, color lipgloss.Color) string {
	level = clamp(level, 1, 7)
	filled := clamp((level+1)/2, 1, 5)
	hotColor := statHeatColor(settings, level, color, renderer)
	full := lipgloss.NewStyle().Background(renderer.Styles.Theme.Bg).Foreground(hotColor).Bold(true).Render(strings.Repeat("▓", filled))
	empty := lipgloss.NewStyle().Background(renderer.Styles.Theme.Bg).Foreground(renderer.Styles.Theme.Dimmed).Render(strings.Repeat("░", 5-filled))
	return full + empty
}

func renderSparkline(renderer tideui.Renderer, settings appSettings, graph statGraph, color lipgloss.Color, width int) string {
	width = max(1, width)
	glyphs := graphGlyphs(settings)
	if graph.static != "" {
		return styleGraphGlyphs(renderer, settings, graph.static, graph.fallbackLevel, color)
	}
	if len(graph.values) == 0 || graph.maxValue <= 0 {
		level := clamp(graph.fallbackLevel, 1, len(glyphs))
		return styleGraphGlyphs(renderer, settings, strings.Join(glyphs[:level], ""), level, color)
	}
	values := graph.values
	if len(values) > width {
		values = values[len(values)-width:]
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		level := graphValueLevel(settings, value, graph.maxValue)
		glyph := glyphs[level-1]
		out = append(out, lipgloss.NewStyle().
			Background(renderer.Styles.Theme.Bg).
			Foreground(statGlyphColor(settings, glyph, color, renderer)).
			Bold(statGlyphBold(glyph)).
			Render(glyph))
	}
	return strings.Join(out, "")
}

func graphGlyphs(settings appSettings) []string {
	switch settings.GraphStyle {
	case graphStyleBlocks:
		return []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}
	case graphStyleBraille:
		return []string{"⣀", "⣤", "⣶", "⣿"}
	default:
		return []string{"▁", "▂", "▂", "▃", "▄", "▅", "▇", "█", "▆", "▄", "▃", "▅", "▆", "▇"}
	}
}

func staticGraphGlyph(settings appSettings, level int) string {
	glyphs := graphGlyphs(settings)
	return glyphs[clamp(level, 1, len(glyphs))-1]
}

func styleGraphGlyphs(renderer tideui.Renderer, settings appSettings, graph string, _ int, color lipgloss.Color) string {
	var out []string
	for _, glyph := range graph {
		glyph := string(glyph)
		out = append(out, lipgloss.NewStyle().
			Background(renderer.Styles.Theme.Bg).
			Foreground(statGlyphColor(settings, glyph, color, renderer)).
			Bold(statGlyphBold(glyph)).
			Render(glyph))
	}
	return strings.Join(out, "")
}

func graphLevel(settings appSettings, graph statGraph) int {
	if len(graph.values) == 0 || graph.maxValue <= 0 {
		return clamp(graph.fallbackLevel, 1, len(graphGlyphs(settings)))
	}
	return graphValueLevel(settings, graph.values[len(graph.values)-1], graph.maxValue)
}

func graphValueLevel(settings appSettings, value, maxValue float64) int {
	if maxValue <= 0 {
		return 1
	}
	glyphs := graphGlyphs(settings)
	level := int(value/maxValue*float64(len(glyphs)-1)) + 1
	return clamp(level, 1, len(glyphs))
}

func statHeatColor(settings appSettings, level int, color lipgloss.Color, renderer tideui.Renderer) lipgloss.Color {
	switch settings.GraphColor {
	case graphColorMetric:
		return color
	case graphColorMono:
		return renderer.Styles.Theme.Dimmed
	}
	colors := []lipgloss.Color{
		"#80c990",
		"#9dce7f",
		"#bbd36f",
		"#d8cb6f",
		"#e8c170",
		"#edad75",
		"#f29a7a",
		"#e06c75",
	}
	return colors[clamp(level, 1, len(colors))-1]
}

func statGlyphColor(settings appSettings, glyph string, color lipgloss.Color, renderer tideui.Renderer) lipgloss.Color {
	switch settings.GraphColor {
	case graphColorMetric:
		return color
	case graphColorMono:
		return renderer.Styles.Theme.Dimmed
	}
	switch glyph {
	case "▁":
		return "#80c990"
	case "▂":
		return "#a9d576"
	case "▃":
		return "#d1cd70"
	case "▄":
		return "#e8c170"
	case "▅":
		return "#efaa76"
	case "▆":
		return "#f28d78"
	case "▇":
		return "#e97876"
	case "█":
		return "#e06c75"
	case "⣀":
		return "#80c990"
	case "⣤":
		return "#d1cd70"
	case "⣶":
		return "#efaa76"
	case "⣿":
		return "#e06c75"
	default:
		return "#9aa6b2"
	}
}

func statGlyphBold(glyph string) bool {
	return glyph == "▅" || glyph == "▆" || glyph == "▇" || glyph == "█"
}

func floatDelta(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	return values[len(values)-1] - values[len(values)-2]
}

func uintDelta(values []uint64) int64 {
	if len(values) < 2 {
		return 0
	}
	current := values[len(values)-1]
	previous := values[len(values)-2]
	if current >= previous {
		return int64(current - previous)
	}
	return -int64(previous - current)
}

func formatPercentDelta(delta float64) string {
	if delta == 0 {
		return ""
	}
	if delta > 0 {
		return fmt.Sprintf("↗ %.1f%%", delta)
	}
	return fmt.Sprintf("↘ %.1f%%", -delta)
}

func formatByteDelta(delta int64) string {
	if delta == 0 {
		return ""
	}
	if delta > 0 {
		return "↗ +" + formatBytes(uint64(delta))
	}
	return "↘ -" + formatBytes(uint64(-delta))
}

func formatCountDelta(delta int64) string {
	if delta == 0 {
		return ""
	}
	if delta > 0 {
		return fmt.Sprintf("↗ +%d", delta)
	}
	return fmt.Sprintf("↘ %d", delta)
}

func formatCPU(stats *domain.ContainerStats) string {
	if stats == nil {
		return "pending stats"
	}
	return fmt.Sprintf("%.1f%%", stats.CPUPercent)
}

func formatMemoryStats(stats *domain.ContainerStats) string {
	if stats == nil {
		return "pending stats"
	}
	if stats.MemoryLimit == 0 {
		return formatBytes(stats.MemoryUsage)
	}
	return formatBytes(stats.MemoryUsage) + " / " + formatBytes(stats.MemoryLimit)
}

func formatPIDs(stats *domain.ContainerStats) string {
	if stats == nil {
		return "pending stats"
	}
	return fmt.Sprintf("%d", stats.PIDs)
}

func formatStatsAge(read time.Time) string {
	if read.IsZero() {
		return "just now"
	}
	age := time.Since(read).Round(time.Second)
	if age < time.Second {
		return "just now"
	}
	return age.String() + " ago"
}

func restartLevel(count int) int {
	switch {
	case count <= 0:
		return 1
	case count < 3:
		return 4
	case count < 6:
		return 6
	default:
		return 12
	}
}

func restartColor(count int) lipgloss.Color {
	if count >= 5 {
		return "#e06c75"
	}
	if count > 0 {
		return "#e8c170"
	}
	return "#80c990"
}

func uptimeLevel(created time.Time) int {
	if created.IsZero() {
		return 1
	}
	age := time.Since(created)
	switch {
	case age < time.Hour:
		return 1
	case age < 24*time.Hour:
		return 4
	case age < 7*24*time.Hour:
		return 6
	default:
		return 12
	}
}

type problemRow struct {
	id        domain.ResourceID
	severity  string
	name      string
	detail    string
	container domain.Container
}

func (m Model) problemSelected(index int, problem problemRow) bool {
	if m.focus == paneActivity && m.mode == activityProblems {
		return index == m.problemCursor
	}
	return m.selectedID == problem.id
}

// renderProblemsSplit renders the Problems pane: the problem list on top,
// plus a lower block showing problemInsight's
// rule-based guidance for whichever row is currently selected — tracking
// problemSelected's own notion of "current" exactly, so the insight always
// matches the highlighted row.
func (m Model) renderProblemsSplit(renderer tideui.Renderer) (string, int) {
	problems := m.snapshotProblems()
	width := m.centerPaneWidth() - 4
	if width < 20 {
		width = 20
	}
	if len(problems) == 0 {
		return renderer.Styles.DetailMeta.Render("No container problems detected."), width
	}

	listLines := make([]string, 0, len(problems)+1)
	listLines = append(listLines, renderer.Styles.DetailMeta.Render(fmt.Sprintf("%d problem(s) found", len(problems))))
	for i, problem := range problems {
		selected := m.problemSelected(i, problem)
		muted := problem.severity == "warn"
		color := severityColor(problem.severity)
		baseFg := rowForeground(renderer, selected, muted)
		listLines = append(listLines, renderer.RenderRow(tideui.Row{
			Prefix:   healthSpan(problem.severity, color, baseFg) + "  ",
			Text:     problem.name,
			Suffix:   healthSpan(problem.detail, color, baseFg),
			Selected: selected,
			Muted:    muted,
		}, width))
	}
	if m.focus == paneActivity {
		limit := max(1, m.problemsListRows())
		listLines = listLines[:min(len(listLines), limit)]
	}
	list := strings.Join(listLines, "\n")

	current := m.currentProblem(problems)
	if current == nil {
		return list, width
	}

	divider := renderer.Styles.DetailMeta.Render(strings.Repeat("─", width))
	insight := m.renderProblemInsight(renderer, *current, width)

	return lipgloss.JoinVertical(lipgloss.Left, list, divider, insight), width
}

// renderProblemInsight renders the insight block's body for row: the
// AI result/error/in-flight state when one belongs to row specifically
// (aiAnalysisFor == row.id — never a stale result left over from a row the
// cursor has since moved away from), falling back to problemInsight's
// plain rule-based text otherwise. An active AI state gets its own colored
// heading line ("AI Analysis" in the theme's accent color, "AI analysis
// failed" in its error color) so it reads as a distinct, deliberate result
// rather than blending into the rule-based text.
//
// Color is applied with foregroundSpan only to the heading, and only after
// wrapInsightText has already finished wrapping/truncating the body as
// plain text — never embedded into text before it goes through wrapping.
// wrapInsightText measures width in runes; ANSI escape sequences embedded
// beforehand inflate that count and corrupt the wrap, the exact bug fixed
// earlier in the About screen's ship animation (see about_ship.go).
func (m Model) renderProblemInsight(renderer tideui.Renderer, row problemRow, width int) string {
	if m.aiAnalysisFor != row.id {
		lines := wrapInsightText(problemInsight(row), width, m.problemsInsightRows())
		return renderer.Styles.DetailBody.Render(strings.Join(lines, "\n"))
	}

	baseFg := styleForeground(renderer.Styles.DetailBody, renderer.Styles.Theme.Fg)
	switch {
	case m.aiAnalyzing:
		heading := foregroundSpan("Analyzing with AI…", renderer.Styles.Theme.Dimmed, baseFg, false)
		return renderer.Styles.DetailBody.Render(heading)
	case m.aiAnalysisErr != nil:
		heading := foregroundSpan("AI analysis failed", renderer.Styles.Theme.Error, baseFg, true)
		body := wrapInsightText(m.aiAnalysisErr.Error(), width, max(1, m.problemsInsightRows()-1))
		lines := append([]string{heading}, body...)
		return renderer.Styles.DetailBody.Render(strings.Join(lines, "\n"))
	case m.aiAnalysis != "":
		heading := foregroundSpan("AI Analysis", renderer.Styles.Theme.BorderFocus, baseFg, true)
		body := wrapInsightText(m.aiAnalysis, width, max(1, m.problemsInsightRows()-1))
		lines := append([]string{heading}, body...)
		return renderer.Styles.DetailBody.Render(strings.Join(lines, "\n"))
	default:
		lines := wrapInsightText(problemInsight(row), width, m.problemsInsightRows())
		return renderer.Styles.DetailBody.Render(strings.Join(lines, "\n"))
	}
}

// currentProblem finds the problem row that renderProblemsSplit's insight
// block should describe — problemSelected's own selection notion, just
// returning the row itself instead of a per-index bool: the cursor when the
// Problems pane is focused, otherwise whichever row matches the tree's own
// selection, falling back to the top row so the insight block is never
// blank just because nothing happens to be selected yet.
func (m Model) currentProblem(problems []problemRow) *problemRow {
	if len(problems) == 0 {
		return nil
	}
	if m.focus == paneActivity && m.mode == activityProblems {
		idx := clamp(m.problemCursor, 0, len(problems)-1)
		return &problems[idx]
	}
	for i := range problems {
		if problems[i].id == m.selectedID {
			return &problems[i]
		}
	}
	return &problems[0]
}

// wrapInsightText word-wraps text to width and, if that overflows
// maxLines, truncates to exactly maxLines with a trailing ellipsis instead
// of silently cutting a sentence off mid-word with no sign anything was
// omitted — the truncation itself lands on a word boundary (dropping whole
// trailing words from the last line until "<line> …" fits), not a hard
// character cut, so a shortened suggestion still reads as a sentence
// fragment rather than garbled text.
func wrapInsightText(text string, width, maxLines int) []string {
	lines := strings.Split(lipgloss.NewStyle().Width(width).Render(text), "\n")
	if maxLines <= 0 || len(lines) <= maxLines {
		return lines
	}
	lines = lines[:maxLines]
	const ellipsis = "…"
	last := strings.TrimRight(lines[maxLines-1], " ")
	for len([]rune(last))+len([]rune(" "+ellipsis)) > width {
		idx := strings.LastIndex(last, " ")
		if idx < 0 {
			break
		}
		last = strings.TrimRight(last[:idx], " ")
	}
	last += " " + ellipsis
	if pad := width - len([]rune(last)); pad > 0 {
		last += strings.Repeat(" ", pad)
	}
	lines[maxLines-1] = last
	return lines
}

func (m Model) snapshotProblems() []problemRow {
	var problems []problemRow
	for _, ctr := range m.snapshotContainers() {
		name := ctr.DisplayName()
		switch {
		case ctr.Health == domain.HealthUnhealthy:
			problems = append(problems, problemRow{id: ctr.ID, severity: "crit", name: name, detail: "unhealthy", container: ctr})
		case ctr.Restarting || ctr.State == domain.StateRestarting:
			detail := "restarting"
			if ctr.RestartCount > 0 {
				detail = fmt.Sprintf("restarting (%d restarts)", ctr.RestartCount)
			}
			problems = append(problems, problemRow{id: ctr.ID, severity: "crit", name: name, detail: detail, container: ctr})
		case ctr.State == domain.StateDead:
			problems = append(problems, problemRow{id: ctr.ID, severity: "crit", name: name, detail: "dead", container: ctr})
		case ctr.RestartCount >= 5:
			problems = append(problems, problemRow{id: ctr.ID, severity: "warn", name: name, detail: fmt.Sprintf("%d restarts", ctr.RestartCount), container: ctr})
		case ctr.State == domain.StateStopped || ctr.State == domain.StateExited:
			problems = append(problems, problemRow{id: ctr.ID, severity: "warn", name: name, detail: string(ctr.State), container: ctr})
		case ctr.Health == domain.HealthUnknown:
			problems = append(problems, problemRow{id: ctr.ID, severity: "warn", name: name, detail: "health unknown", container: ctr})
		case ctr.Compose.Project == "" && hasPublicPorts(ctr):
			problems = append(problems, problemRow{id: ctr.ID, severity: "warn", name: name, detail: "public ports", container: ctr})
		}
	}
	sort.SliceStable(problems, func(i, j int) bool {
		if severityRank(problems[i].severity) != severityRank(problems[j].severity) {
			return severityRank(problems[i].severity) < severityRank(problems[j].severity)
		}
		return problems[i].name < problems[j].name
	})
	return problems
}

func hasPublicPorts(ctr domain.Container) bool {
	for _, port := range ctr.Ports {
		if port.Public > 0 {
			return true
		}
	}
	return false
}

func severityRank(severity string) int {
	switch severity {
	case "crit":
		return 0
	case "warn":
		return 1
	default:
		return 2
	}
}

// problemInsight is the rule-based, no-network baseline explanation for a
// problem row — synthesized purely from data whatthedock already has about
// the container (state, health, restart count, ports), so it updates
// instantly as the selection moves with no latency or cost. Mirrors
// snapshotProblems' own classification exactly so the insight text always
// matches the reason the row is listed in the first place.
func problemInsight(row problemRow) string {
	ctr := row.container
	name := row.name
	switch {
	case ctr.Health == domain.HealthUnhealthy:
		return name + " is failing its Docker health check. The health check command itself usually reveals more than the container's own logs — check what it runs and try it manually inside the container."
	case ctr.Restarting || ctr.State == domain.StateRestarting:
		if ctr.RestartCount > 0 {
			return fmt.Sprintf("%s has restarted %d times. This is almost always a crash loop — check its logs (l) for the error right before each restart, not just the latest one.", name, ctr.RestartCount)
		}
		return name + " is restarting. Check its logs (l) for the error that's causing it to exit."
	case ctr.State == domain.StateDead:
		return name + " is in Docker's \"dead\" state — the container failed to clean up properly, often after a host or daemon issue. Removing and recreating it is usually the only fix; check its logs first if you need to know why."
	case ctr.RestartCount >= 5:
		return fmt.Sprintf("%s has restarted %d times even though it's currently up. Worth checking its logs (l) for intermittent errors even though it looks healthy right now.", name, ctr.RestartCount)
	case ctr.State == domain.StateStopped || ctr.State == domain.StateExited:
		reason := "stopped"
		if strings.TrimSpace(ctr.Status) != "" {
			reason = ctr.Status
		}
		return fmt.Sprintf("%s is not running (%s). If this wasn't intentional, check its logs (l) for the exit reason, or its restart policy (%s) if you expected Docker to bring it back on its own.", name, reason, emptyAs(ctr.RestartPolicy, "no"))
	case ctr.Health == domain.HealthUnknown:
		return name + " has a health check configured but hasn't reported a status yet — usually just means it's still starting (the check's start period hasn't elapsed), but worth a second look if it stays this way."
	case ctr.Compose.Project == "" && hasPublicPorts(ctr):
		return name + " is a standalone container publishing ports to the host. Not necessarily wrong, but confirm this is intentional — standalone containers with public ports are easy to lose track of outside a Compose project."
	default:
		return "No specific guidance for this problem yet."
	}
}

func (m Model) snapshotContainers() []domain.Container {
	var containers []domain.Container
	for _, project := range m.snapshot.Projects {
		for _, service := range project.Services {
			containers = append(containers, service.Containers...)
		}
	}
	containers = append(containers, m.snapshot.Standalone...)
	return containers
}

func (m Model) renderInspector(renderer tideui.Renderer) string {
	if m.selected == nil {
		return renderer.Styles.DetailMeta.Render("No container selected.")
	}
	ctr := *m.selected
	width := max(12, m.rightPaneWidth()-4)
	var lines []string
	addSection := func(title string) {
		lines = append(lines, renderInspectorSection(renderer, width, title))
	}
	add := func(label, value, suffix string, color lipgloss.Color) {
		lines = append(lines, renderInspectorField(renderer, width, label, value, suffix, color)...)
	}
	lines = append(lines, renderInspectorTitle(renderer, width, containerTitle(ctr)))
	addSection("Runtime")
	add("Status", inspectorStatusText(ctr), "", inspectorStatusColor(ctr))
	add("Uptime", formatDuration(ctr.Created), "", "#9aa6b2")
	add("Restart", ctr.RestartPolicy, "", "")
	add("Restarts", fmt.Sprintf("%d", ctr.RestartCount), "", restartCountColor(ctr.RestartCount))

	addSection("Image")
	add("Image", ctr.Image, "c", "#7dcfff")
	add("Image ID", short(ctr.ImageID, 20), "c", "#9aa6b2")

	if ctr.Compose.Project != "" {
		addSection("Compose")
		add("Project", ctr.Compose.Project, "c", "#80c990")
		add("Service", ctr.Compose.Service, "c", "#80c990")
		add("Number", ctr.Compose.ContainerNumber, "c", "#9aa6b2")
		add("Config", ctr.Compose.ConfigFiles, "c/o", "#9aa6b2")
	}

	addSection("Network")
	add("Ports", formatPorts(ctr.Ports), detailHint(len(ctr.Ports) > 0, true), "#e5c07b")
	add("Networks", strings.Join(ctr.Networks, ", "), "", "#7dcfff")

	addSection("Files")
	add("Mounts", formatMounts(ctr.Mounts), detailHint(len(ctr.Mounts) > 0, true), "#c678dd")

	addSection("Metadata")
	add("Env", formatList(ctr.Env, 8), "", "#9aa6b2")
	add("Labels", formatMap(ctr.Labels, 8), detailHint(len(ctr.Labels) > 0, false), "#9aa6b2")
	if ctr.HealthCheck != nil {
		add("Health", strings.Join(ctr.HealthCheck.Test, " "), "", inspectorStatusColor(ctr))
	}

	budget := max(1, m.inspectorVisibleRows()-m.paneActionStripRows(paneInspector))
	start := clamp(m.inspectorScroll, 0, max(0, len(lines)-budget))
	end := min(len(lines), start+budget)
	return m.withPaneActionStrip(renderer, paneInspector, width, strings.Join(lines[start:end], "\n"))
}

func renderInspectorSection(renderer tideui.Renderer, width int, title string) string {
	return renderer.Styles.DetailMeta.Copy().
		Foreground(renderer.Styles.Theme.Fg).
		Bold(true).
		Italic(false).
		Width(width).
		Render(" " + strings.ToUpper(title))
}

func renderInspectorTitle(renderer tideui.Renderer, width int, title string) string {
	return renderer.Styles.DetailBody.Copy().
		Bold(true).
		Width(width).
		Render(title)
}

func renderInspectorField(renderer tideui.Renderer, width int, label, value, suffix string, color lipgloss.Color) []string {
	const labelWidth = 8
	value = strings.TrimSpace(value)
	muted := false
	if value == "" {
		value = "none"
		muted = true
		suffix = ""
	}
	values := strings.Split(value, "\n")
	baseFg := styleForeground(renderer.Styles.Item, renderer.Styles.Theme.Fg)
	valueFg := renderer.Styles.Theme.Fg
	if color != "" {
		valueFg = color
	}
	if muted {
		valueFg = renderer.Styles.Theme.Dimmed
	}

	out := make([]string, 0, len(values))
	for i, line := range values {
		prefix := strings.Repeat(" ", labelWidth+1)
		rowSuffix := ""
		if i == 0 {
			prefix = foregroundSpan(fmt.Sprintf("%-*s ", labelWidth, label), renderer.Styles.Theme.BorderFocus, baseFg, true)
			if suffix != "" {
				rowSuffix = foregroundSpan(suffix, renderer.Styles.Theme.Unread, baseFg, false)
			}
		}
		out = append(out, renderer.RenderRow(tideui.Row{
			Prefix: prefix,
			Text:   foregroundSpan(line, valueFg, baseFg, false),
			Suffix: rowSuffix,
			Muted:  muted,
		}, width))
	}
	return out
}

func foregroundSpan(text string, color, restore lipgloss.Color, bold bool) string {
	style := ansi.NewStyle().ForegroundColor(color)
	if bold {
		style = style.Bold()
	}
	restoreStyle := ansi.NewStyle().ForegroundColor(restore).Normal().Italic(false)
	return style.String() + text + restoreStyle.String()
}

func inspectorStatusText(ctr domain.Container) string {
	parts := []string{statusGlyph(ctr)}
	if ctr.State != "" {
		parts = append(parts, string(ctr.State))
	}
	if ctr.Health != "" {
		parts = append(parts, string(ctr.Health))
	}
	if ctr.Restarting {
		parts = append(parts, "restarting")
	}
	return strings.Join(parts, " ")
}

func inspectorStatusColor(ctr domain.Container) lipgloss.Color {
	if ctr.Restarting || ctr.State == domain.StateRestarting {
		return "#e5c07b"
	}
	switch ctr.Health {
	case domain.HealthHealthy:
		return "#80c990"
	case domain.HealthUnhealthy:
		return "#e06c75"
	case domain.HealthStarting, domain.HealthUnknown:
		return "#e5c07b"
	}
	switch ctr.State {
	case domain.StateRunning:
		return "#80c990"
	case domain.StateStopped, domain.StateExited:
		return "#9aa6b2"
	case domain.StateDead:
		return "#e06c75"
	default:
		return "#7dcfff"
	}
}

func severityColor(severity string) lipgloss.Color {
	switch severity {
	case "crit":
		return "#e06c75"
	case "warn":
		return "#e5c07b"
	default:
		return "#9aa6b2"
	}
}

func restartCountColor(count int) lipgloss.Color {
	if count <= 0 {
		return "#9aa6b2"
	}
	if count < 3 {
		return "#e5c07b"
	}
	return "#e06c75"
}

func detailHint(hasValue, openable bool) string {
	if !hasValue {
		return ""
	}
	if openable {
		return "c/o"
	}
	return "c"
}

type paneAction struct {
	key   string
	label string
}

func (m Model) withPaneActionStrip(renderer tideui.Renderer, pane pane, width int, content string) string {
	if m.focus != pane {
		return content
	}
	strip := m.renderPaneActionStrip(renderer, pane, width)
	if strip == "" {
		return content
	}
	contentRows := max(1, m.paneContentRows())
	footerRows := m.paneActionStripRows(pane)
	if footerRows == 0 || contentRows <= footerRows {
		return content
	}
	bodyRows := contentRows - footerRows
	lines := []string{}
	if content != "" {
		lines = strings.Split(content, "\n")
	}
	if len(lines) > bodyRows {
		lines = lines[:bodyRows]
	}
	blank := lipgloss.NewStyle().Background(renderer.Styles.Theme.Bg).Width(width).Render("")
	for len(lines) < bodyRows {
		lines = append(lines, blank)
	}
	lines = append(lines, strip)
	return strings.Join(lines, "\n")
}

func (m Model) renderPaneActionStrip(renderer tideui.Renderer, pane pane, width int) string {
	actions := m.paneActions(pane)
	if len(actions) == 0 {
		return ""
	}
	chipBg := renderer.Styles.Theme.StatusBar
	if chipBg == "" {
		chipBg = renderer.Styles.Theme.Border
	}
	keyStyle := lipgloss.NewStyle().
		Background(renderer.Styles.Theme.BorderFocus).
		Foreground(styleForeground(renderer.Styles.PaneHeaderActive, renderer.Styles.Theme.Fg)).
		Bold(true)
	labelStyle := lipgloss.NewStyle().
		Background(chipBg).
		Foreground(styleForeground(renderer.Styles.StatusBar, renderer.Styles.Theme.StatusFg))
	gap := lipgloss.NewStyle().Background(renderer.Styles.Theme.Bg).Render(" ")
	parts := make([]string, 0, len(actions))
	for _, action := range actions {
		if action.key == "" || action.label == "" {
			continue
		}
		chip := keyStyle.Render(" "+action.key+" ") + labelStyle.Render(strings.ToLower(action.label)+" ")
		parts = append(parts, chip)
	}
	if len(parts) == 0 {
		return ""
	}
	line := ""
	for _, part := range parts {
		candidate := part
		if line != "" {
			candidate = line + gap + part
		}
		if lipgloss.Width(ansi.Strip(candidate)) > width {
			break
		}
		line = candidate
	}
	if line == "" {
		return ""
	}
	return renderer.Styles.DetailMeta.Width(width).Render(line)
}

func (m Model) paneActions(pane pane) []paneAction {
	switch pane {
	case paneTree:
		actions := []paneAction{
			{key: "enter", label: "select"},
			{key: "space", label: "fold"},
			{key: "/", label: "filter"},
			{key: "r", label: "refresh"},
			{key: "n", label: "new"},
		}
		if row := m.currentRow(); row != nil && row.container != nil {
			actions = append(actions, paneAction{key: "s", label: "start/stop"})
		}
		return actions
	case paneActivity:
		switch m.mode {
		case activityProblems:
			return []paneAction{
				{key: "enter", label: "inspect"},
				{key: "a", label: "analyze with AI"},
				{key: "r", label: "refresh"},
				{key: "l", label: "logs"},
				{key: "g", label: "stats"},
			}
		case activityStats:
			return []paneAction{
				{key: "r", label: "refresh"},
				{key: "l", label: "logs"},
				{key: "p", label: "problems"},
			}
		default:
			follow := paneAction{key: "f", label: "live"}
			if m.logFollow {
				follow = paneAction{key: "k", label: "pause"}
			}
			return []paneAction{
				follow,
				{key: "/", label: "search"},
				{key: "x", label: "clear"},
				{key: "n/N", label: "match"},
			}
		}
	case paneInspector:
		return []paneAction{
			{key: "s", label: "start/stop"},
			{key: "alt+r", label: "restart"},
			{key: "l", label: "logs"},
			{key: "c", label: "copy"},
			{key: "o", label: "open"},
			{key: "e", label: "shell"},
			{key: "u", label: "replicate"},
			{key: "D", label: "delete"},
			{key: "C", label: "clone"},
			{key: "m", label: "edit"},
		}
	default:
		return nil
	}
}

func (m Model) renderOverlay(renderer tideui.Renderer) *tideui.Overlay {
	switch m.overlay {
	case overlayHelp:
		width := min(72, max(40, m.width-8))
		budget := m.helpBodyBudget()
		scroll := clamp(m.helpScroll, 0, max(0, len(helpLines)-budget))
		end := min(len(helpLines), scroll+budget)
		hints := []tideui.SoftHint{{Key: "esc/?/q", Label: "close"}}
		if len(helpLines) > budget {
			hints = append(hints, tideui.SoftHint{Key: "j/k", Label: fmt.Sprintf("scroll (%d/%d)", end, len(helpLines))})
		}
		content := renderer.RenderSoftBody(width, strings.Join(helpLines[scroll:end], "\n")+"\n\n"+
			strings.Join(statusLegendLines(renderer), "\n")+"\n\n"+
			renderer.RenderSoftHints(width-4, hints...))
		overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "help", Content: content, Width: width})
		return &overlay
	case overlayAppLog:
		width := min(96, max(40, m.width-8))
		budget := m.appLogBodyBudget()
		lines := m.appLogLines
		if len(lines) == 0 {
			lines = []string{"(nothing logged yet — turn on App log in Settings)"}
		}
		scroll := clamp(m.appLogScroll, 0, max(0, len(lines)-budget))
		end := min(len(lines), scroll+budget)
		hints := []tideui.SoftHint{{Key: "esc/q", Label: "close"}}
		if len(lines) > budget {
			hints = append(hints, tideui.SoftHint{Key: "j/k", Label: fmt.Sprintf("scroll (%d/%d)", end, len(lines))})
		}
		content := renderer.RenderSoftBody(width, strings.Join(lines[scroll:end], "\n")+"\n\n"+
			renderer.RenderSoftHints(width-4, hints...))
		overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "app log", Content: content, Width: width})
		return &overlay
	case overlayAbout:
		width := min(82, max(46, m.width-8))
		contentWidth := aboutContentWidth(m.width)
		content := renderer.RenderSoftBody(width, m.aboutText(renderer, contentWidth)+"\n"+
			m.aboutExtras(renderer, contentWidth)+"\n\n"+
			renderer.RenderSoftHints(width-4, tideui.SoftHint{Key: "esc/A/q", Label: "close"}))
		overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "about", Content: content, Width: width})
		return &overlay
	case overlayFilter:
		width := min(72, max(40, m.width-8))
		input := renderer.Styles.InputFocused.Width(max(20, width-8)).Render(m.filterDraft)
		content := renderer.RenderSoftBody(width, input+"\n\n"+
			renderer.RenderSoftHints(width-4,
				tideui.SoftHint{Key: "enter", Label: "apply"},
				tideui.SoftHint{Key: "esc", Label: "cancel"},
			))
		overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "filter", Content: content, Width: width})
		return &overlay
	case overlayLogFilter:
		width := min(72, max(40, m.width-8))
		input := renderer.Styles.InputFocused.Width(max(20, width-8)).Render(m.logDraft)
		content := renderer.RenderSoftBody(width,
			renderer.Styles.DetailMeta.Render("Severity  "+m.logLevel.String())+"\n"+
				input+"\n\n"+
				renderer.RenderSoftHints(width-4,
					tideui.SoftHint{Key: "enter", Label: "apply"},
					tideui.SoftHint{Key: "esc", Label: "cancel"},
				))
		overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "log filter", Content: content, Width: width})
		return &overlay
	case overlayCommandPalette:
		return m.commandPaletteOverlay(renderer)
	case overlayThemePicker:
		overlay := m.themes.SoftModal(renderer, min(72, max(40, m.width-8)), max(8, m.height-4), "whatthedock")
		return &overlay
	case overlaySettings:
		return m.settingsOverlay(renderer)
	case overlaySystems:
		return m.systemsOverlay(renderer)
	case overlayCreate:
		return m.createOverlay(renderer)
	case overlayCopy:
		return m.copyOverlay(renderer)
	case overlayOpen:
		return m.openOverlay(renderer)
	case overlayDelete:
		return m.deleteOverlay(renderer)
	case overlayReplicate:
		return m.replicateOverlay(renderer)
	case overlayUpdate:
		return m.updateOverlay(renderer)
	default:
		return nil
	}
}

// deleteOverlay confirms Delete. The prompt reads the same for Compose
// services and standalone containers — both are a real, permanent removal
// (see Model.startDelete) — even though the Compose path additionally has
// to delete the service's definition (override and/or base file block),
// not just its container.
func (m Model) deleteOverlay(renderer tideui.Renderer) *tideui.Overlay {
	width := min(72, max(40, m.width-8))
	contentWidth := width - 4
	prompt := "No container selected."
	if selected := m.selectedContainer(); selected != nil {
		if selected.Compose.Project != "" {
			prompt = "Delete service " + selected.Compose.Service + "? This stops and removes its container."
		} else {
			prompt = "Remove container " + selected.DisplayName() + "?"
		}
	}
	content := renderer.RenderSoftBody(width, strings.Join([]string{
		renderer.Styles.DetailMeta.Width(contentWidth).Render(prompt),
		"",
		renderer.RenderSoftHints(contentWidth,
			tideui.SoftHint{Key: "y", Label: "delete"},
			tideui.SoftHint{Key: "n/esc", Label: "cancel"},
		),
	}, "\n"))
	overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "delete", Content: content, Width: width})
	return &overlay
}

// replicateOverlay confirms Replicate — pulling a fresh image and
// recreating the container/service in place under the same identity.
func (m Model) replicateOverlay(renderer tideui.Renderer) *tideui.Overlay {
	width := min(72, max(40, m.width-8))
	contentWidth := width - 4
	prompt := "No container selected."
	if selected := m.selectedContainer(); selected != nil {
		target := selected.DisplayName()
		if selected.Compose.Project != "" {
			target = selected.Compose.Service
		}
		prompt = "Pull the latest image and recreate " + target + " in place?"
	}
	content := renderer.RenderSoftBody(width, strings.Join([]string{
		renderer.Styles.DetailMeta.Width(contentWidth).Render(prompt),
		"",
		renderer.RenderSoftHints(contentWidth,
			tideui.SoftHint{Key: "y", Label: "replicate"},
			tideui.SoftHint{Key: "n/esc", Label: "cancel"},
		),
	}, "\n"))
	overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "replicate", Content: content, Width: width})
	return &overlay
}

// updateOverlay confirms installing an available update — see
// Model.handleUpdateKey: y downloads and installs it in place, n/esc
// dismisses and remembers this version as ignored (see
// updateIgnoredVersion) so the automatic check won't prompt again for it.
func (m Model) updateOverlay(renderer tideui.Renderer) *tideui.Overlay {
	width := min(72, max(40, m.width-8))
	contentWidth := width - 4
	prompt := fmt.Sprintf("whatthedock %s is available (you have %s). Download and install it now?", m.updateAvailableVersion, m.appVersion)
	content := renderer.RenderSoftBody(width, strings.Join([]string{
		renderer.Styles.DetailMeta.Width(contentWidth).Render(prompt),
		"",
		renderer.RenderSoftHints(contentWidth,
			tideui.SoftHint{Key: "y", Label: "update"},
			tideui.SoftHint{Key: "n/esc", Label: "ignore"},
		),
	}, "\n"))
	overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "update available", Content: content, Width: width})
	return &overlay
}

func (m Model) openOverlay(renderer tideui.Renderer) *tideui.Overlay {
	width := min(82, max(46, m.width-8))
	contentWidth := width - 4
	rows := m.openRows()
	lines := make([]string, 0, len(rows)+4)
	if len(rows) == 0 {
		lines = append(lines, renderer.Styles.DetailMeta.Render("No openable details."))
	} else {
		for i, row := range rows {
			lines = append(lines, renderer.RenderSoftRow(tideui.SoftRow{
				Text:     row.label + "  " + row.value,
				Suffix:   short(row.target, max(12, contentWidth-30)),
				Selected: i == m.openCursor,
			}, contentWidth))
		}
	}
	lines = append(lines, "", renderer.RenderSoftHints(contentWidth,
		tideui.SoftHint{Key: "enter", Label: "open"},
		tideui.SoftHint{Key: "esc/o", Label: "close"},
	))
	content := renderer.RenderSoftBody(width, strings.Join(lines, "\n"))
	overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "open", Content: content, Width: width})
	return &overlay
}

// windowAroundCursor returns the [start, end) slice bounds into a total-item
// list that keep cursor visible within a budget-sized window — the same
// "viewport follows the selection" behavior a normal scrolling listbox has,
// without needing separately persisted scroll state the way helpScroll does
// (there's no cursor to follow in the plain-text help overlay).
func windowAroundCursor(total, cursor, budget int) (start, end int) {
	if total <= budget {
		return 0, total
	}
	start = clamp(cursor-budget+1, 0, total-budget)
	if cursor < start {
		start = cursor
	}
	return start, start + budget
}

func (m Model) copyOverlay(renderer tideui.Renderer) *tideui.Overlay {
	width := min(82, max(46, m.width-8))
	contentWidth := width - 4
	rows := m.copyRows()
	lines := make([]string, 0, len(rows)+4)
	if len(rows) == 0 {
		lines = append(lines, renderer.Styles.DetailMeta.Render("No copyable details."))
	} else {
		budget := m.softOverlayBodyBudget()
		rowBudget := budget
		if len(rows) > budget {
			// Reserve room for the "N more" indicator lines below so they
			// can't themselves push the list past the overlay's budget.
			rowBudget = max(1, budget-2)
		}
		start, end := windowAroundCursor(len(rows), m.copyCursor, rowBudget)
		if start > 0 {
			lines = append(lines, renderer.Styles.DetailMeta.Render(fmt.Sprintf("▲ %d more", start)))
		}
		for i := start; i < end; i++ {
			row := rows[i]
			lines = append(lines, renderer.RenderSoftRow(tideui.SoftRow{
				Text:     row.label,
				Suffix:   short(row.value, max(12, contentWidth-24)),
				Selected: i == m.copyCursor,
			}, contentWidth))
		}
		if end < len(rows) {
			lines = append(lines, renderer.Styles.DetailMeta.Render(fmt.Sprintf("▼ %d more", len(rows)-end)))
		}
	}
	lines = append(lines, "", renderer.RenderSoftHints(contentWidth,
		tideui.SoftHint{Key: "enter", Label: "copy"},
		tideui.SoftHint{Key: "esc/c", Label: "close"},
	))
	content := renderer.RenderSoftBody(width, strings.Join(lines, "\n"))
	overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "copy", Content: content, Width: width})
	return &overlay
}

// settingsBodyBudget is softOverlayBodyBudget's usual list-content
// allowance, minus 2 more when the Config path footer line is also going
// to be appended below the scrollable row list (see settingsOverlay) — the
// generic 6-line chrome reservation in softOverlayBodyBudget only accounts
// for one blank+hints footer, not an extra Config block on top of it.
func (m Model) settingsBodyBudget() int {
	budget := m.softOverlayBodyBudget()
	if m.settingsPath != "" {
		budget -= 2
	}
	return max(3, budget)
}

func (m Model) settingsOverlay(renderer tideui.Renderer) *tideui.Overlay {
	width := min(76, max(44, m.width-8))
	contentWidth := width - 4

	settingsRows := m.settingsRows()
	lines := make([]string, 0, len(settingsRows)+4)
	cursorLine := 0
	for i, row := range settingsRows {
		if row.kind == settingsRowSection {
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, renderer.Styles.DetailMeta.Render(row.label))
			continue
		}
		suffix := row.value
		// Reset defaults has nothing dynamic to say, so it keeps the plain
		// "enter" affordance — but Check for update's whole point is to
		// show what the last check found (checking…/vX.Y.Z available/up to
		// date/check failed), which "enter" was silently overwriting.
		if row.kind == settingsRowAction && row.action != settingsActionCheckUpdate {
			suffix = "enter"
		}
		if m.settingsEditingField == row.label {
			suffix = m.settingsEditValueWithCaret(row)
		}
		lines = append(lines, renderer.RenderSoftRow(tideui.SoftRow{Text: row.label, Suffix: suffix, Selected: i == m.settingsCursor}, contentWidth))
		if i == m.settingsCursor {
			cursorLine = len(lines) - 1
		}
	}

	// The row list — unlike Help/AppLog's plain scrolled text — is
	// cursor-driven: up/down already moves the selection, so the viewport
	// auto-follows the cursor (windowAroundCursor, the same convention the
	// Copy overlay uses) instead of needing separate scroll keys/state.
	budget := m.settingsBodyBudget()
	rowBudget := budget
	if len(lines) > budget {
		rowBudget = max(1, budget-2)
	}
	start, end := windowAroundCursor(len(lines), cursorLine, rowBudget)
	visible := make([]string, 0, end-start+6)
	if start > 0 {
		visible = append(visible, renderer.Styles.DetailMeta.Render(fmt.Sprintf("▲ %d more", start)))
	}
	visible = append(visible, lines[start:end]...)
	if end < len(lines) {
		visible = append(visible, renderer.Styles.DetailMeta.Render(fmt.Sprintf("▼ %d more", len(lines)-end)))
	}

	if m.settingsPath != "" {
		visible = append(visible, "", renderer.Styles.DetailMeta.Width(contentWidth).Render("Config  "+m.settingsPath))
	}
	if m.settingsEditingField != "" {
		visible = append(visible, "", renderer.RenderSoftHints(contentWidth,
			tideui.SoftHint{Key: "enter", Label: "save"},
			tideui.SoftHint{Key: "esc", Label: "cancel"},
		))
	} else {
		visible = append(visible, "", renderer.RenderSoftHints(contentWidth,
			tideui.SoftHint{Key: "enter/space", Label: "change"},
			tideui.SoftHint{Key: "h/l", Label: "previous/next"},
			tideui.SoftHint{Key: "ctrl+s", Label: "save"},
			tideui.SoftHint{Key: "esc", Label: "cancel"},
		))
	}
	content := renderer.RenderSoftBody(width, strings.Join(visible, "\n"))
	overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "settings", Content: content, Width: width})
	return &overlay
}

func (m Model) systemsOverlay(renderer tideui.Renderer) *tideui.Overlay {
	width := min(86, max(50, m.width-8))
	contentWidth := width - 4
	var rows []string
	switch m.systemMode {
	case systemModeEdit:
		title := "add system"
		if !m.systemDraftNew {
			title = "edit system"
		}
		for _, field := range m.visibleSystemFields() {
			label, value := m.systemFieldDisplay(field)
			if field == m.systemField && !m.isSystemChoiceField() {
				value = m.systemFieldValueWithCaret()
			}
			rows = append(rows, renderer.RenderSoftRow(tideui.SoftRow{
				Text:     label,
				Suffix:   short(value, max(12, contentWidth-22)),
				Selected: field == m.systemField,
			}, contentWidth))
		}
		rows = append(rows, "", renderer.RenderSoftHints(contentWidth,
			tideui.SoftHint{Key: "ctrl+s", Label: "save"},
			tideui.SoftHint{Key: "enter", Label: "next/change"},
			tideui.SoftHint{Key: "esc", Label: "cancel"},
			tideui.SoftHint{Key: "h/l", Label: "change choice"},
			tideui.SoftHint{Key: "ctrl+u", Label: "clear"},
		))
		content := renderer.RenderSoftBody(width, strings.Join(rows, "\n"))
		overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: title, Content: content, Width: width})
		return &overlay
	case systemModeDelete:
		system := m.currentSystem()
		rows = append(rows,
			renderer.Styles.DetailMeta.Width(contentWidth).Render("Delete "+system.Name+"?"),
			"",
			renderer.RenderSoftHints(contentWidth,
				tideui.SoftHint{Key: "y", Label: "delete"},
				tideui.SoftHint{Key: "n/esc", Label: "cancel"},
			),
		)
		content := renderer.RenderSoftBody(width, strings.Join(rows, "\n"))
		overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "delete system", Content: content, Width: width})
		return &overlay
	default:
		systems := config.NormalizeSystems(config.Settings{ActiveSystem: m.activeSystem, Systems: m.systems}).Systems
		for i, system := range systems {
			prefix := "  "
			if system.ID == m.activeSystem {
				prefix = "* "
			}
			rows = append(rows, renderer.RenderSoftRow(tideui.SoftRow{
				Text:     prefix + system.Name,
				Suffix:   systemSummary(system),
				Selected: i == m.systemsCursor,
			}, contentWidth))
		}
		rows = append(rows, "", renderer.RenderSoftHints(contentWidth,
			tideui.SoftHint{Key: "enter", Label: "switch"},
			tideui.SoftHint{Key: "t", Label: "test"},
			tideui.SoftHint{Key: "a", Label: "add"},
			tideui.SoftHint{Key: "e", Label: "edit"},
			tideui.SoftHint{Key: "d", Label: "delete"},
			tideui.SoftHint{Key: "esc", Label: "close"},
		))
		content := renderer.RenderSoftBody(width, strings.Join(rows, "\n"))
		overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "systems", Content: content, Width: width})
		return &overlay
	}
}

func (m Model) systemFieldDisplay(field systemField) (string, string) {
	switch field {
	case systemFieldName:
		return "Name", m.systemDraft.Name
	case systemFieldKind:
		return "Kind", m.systemDraft.Kind
	case systemFieldDockerHost:
		return "Docker host", emptyAsDefault(m.systemDraft.DockerHost)
	case systemFieldSSHHost:
		return "Host", m.systemDraft.SSHHost
	case systemFieldSSHUser:
		return "User", emptyAsDefault(m.systemDraft.SSHUser)
	case systemFieldSSHPort:
		return "Port", emptyAsDefault(m.systemDraft.SSHPort)
	case systemFieldSSHAuth:
		return "Auth", systemAuthLabel(m.systemDraft.SSHAuth)
	case systemFieldRemoteSocket:
		return "Remote socket", m.systemDraft.RemoteSocket
	case systemFieldLocalSocket:
		return "Local socket", m.systemDraft.LocalSocket
	default:
		return "", ""
	}
}

func systemSummary(system config.System) string {
	switch system.Kind {
	case "ssh":
		if system.SSHHost != "" {
			return "ssh " + systems.SSHTarget(system)
		}
		return "ssh"
	case "local", "":
		return emptyAsDefault(system.DockerHost)
	default:
		return system.Kind
	}
}

func emptyAsDefault(value string) string {
	if strings.TrimSpace(value) == "" {
		return "default"
	}
	return value
}

func (m Model) commandPaletteOverlay(renderer tideui.Renderer) *tideui.Overlay {
	items := m.filteredCommands()
	width := min(76, max(44, m.width-8))
	contentWidth := width - 4
	input := renderer.Styles.InputFocused.Width(contentWidth - 4).Render(m.commandFilter)
	var rows []string
	rows = append(rows, input)
	for i, item := range items {
		rows = append(rows, renderer.RenderSoftRow(tideui.SoftRow{
			Text:     item.Name,
			Suffix:   item.Shortcut,
			Selected: i == m.commandCursor,
			Muted:    !item.Enabled,
		}, contentWidth))
	}
	if len(items) == 0 {
		rows = append(rows, renderer.Styles.DetailMeta.Render("No matching commands."))
	}
	rows = append(rows, "", renderer.RenderSoftHints(contentWidth,
		tideui.SoftHint{Key: "enter", Label: "run"},
		tideui.SoftHint{Key: "esc", Label: "close"},
	))
	content := renderer.RenderSoftBody(width, strings.Join(rows, "\n"))
	overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "command", Content: content, Width: width})
	return &overlay
}

func (m Model) statusLeft(renderer tideui.Renderer) string {
	host := m.provider.Host().Name
	prefix := " " + host + " "
	if m.statusErr {
		return renderer.Styles.StatusError.Render(prefix + m.status)
	}
	if m.busy {
		// An in-flight action (delete/replicate/create-apply) is the most
		// relevant thing to show right after an outright error, since the
		// user just triggered it and previously saw nothing at all here for
		// up to two minutes.
		glyph := spinnerGlyph(m.statusPulseFrame)
		color := pulseDotColor(m.statusPulseFrame, renderer.Styles.Theme.Unread)
		spinner := lipgloss.NewStyle().Background(renderer.Styles.Theme.StatusBar).Foreground(lipgloss.Color(color)).Bold(true).Render(" " + glyph + " ")
		return renderer.Styles.StatusSuccess.Render(prefix) + spinner + renderer.Styles.StatusBar.Render(m.status)
	}
	if m.eventsReconnecting {
		// A dropped event stream is a real problem, but a quieter one than
		// something the user is actively waiting on.
		return renderer.Styles.StatusNotice.Render(prefix + spinnerGlyph(m.statusPulseFrame) + " reconnecting to Docker…")
	}
	if m.mode == activityLogs && (strings.TrimSpace(m.logFilter) != "" || m.logLevel != logSeverityAll) {
		return renderer.Styles.StatusNotice.Render(prefix+"logs: "+filterStatus(m.logFilter, m.logLevel)) + renderer.Styles.StatusBar.Render(" "+m.status)
	}
	if strings.TrimSpace(m.filter) != "" {
		return renderer.Styles.StatusNotice.Render(prefix+"filter: "+m.filter) + renderer.Styles.StatusBar.Render(" "+m.status)
	}
	if strings.TrimSpace(m.status) == "" {
		return renderer.Styles.StatusBar.Render(prefix + m.status)
	}
	if m.status == "Docker connected" {
		// A steady connection doesn't need to keep saying so in words — a
		// breathing dot in place of the text is the same "all good" signal
		// without permanently occupying the status line with static text.
		dotColor := pulseDotColor(m.statusPulseFrame, renderer.Styles.Theme.Unread)
		dot := lipgloss.NewStyle().Background(renderer.Styles.Theme.StatusBar).Foreground(lipgloss.Color(dotColor)).Bold(true).Render(" ● ")
		return renderer.Styles.StatusSuccess.Render(prefix) + dot
	}
	return renderer.Styles.StatusSuccess.Render(prefix + m.status)
}

// pulseDotColor computes the connected-status dot's current color: a smooth
// breathing fade between a dim base and the theme's own accent green, so
// the pulse rides the same green every other "success" indicator uses
// rather than a hardcoded color.
func pulseDotColor(frame int, bright lipgloss.Color) string {
	const period = 24.0 // ticks per full breathing cycle (150ms * 24 ≈ 3.6s)
	t := (math.Sin(2*math.Pi*float64(frame)/period) + 1) / 2
	return lerpHexColor("#16281c", string(bright), t)
}

// busySpinnerFrames sources its glyphs from the shared Tide-family
// cli-spinners library rather than hand-rolling a frame set. Only the frame
// data is used — the library's own Spinner/Start/Success machinery manages
// the terminal cursor directly, which assumes it owns the terminal; inside
// a Bubble Tea program the screen is already owned by the render loop, so
// the frames are animated through that loop instead (see spinnerGlyph,
// driven by the same m.statusPulseFrame tick as the connected-status dot).
var busySpinnerFrames = sp.All["braille"].Frames

func spinnerGlyph(frame int) string {
	if len(busySpinnerFrames) == 0 {
		return "•"
	}
	return busySpinnerFrames[frame%len(busySpinnerFrames)]
}

// helpLines is the full keyboard-help content. It's kept as a slice (not
// just a joined string) so the help overlay can window it against the
// terminal's actual height instead of relying on renderOverlay's silent
// bottom-truncation, which drops any line past the terminal edge (see
// placeBoxAt in tideui/layout.go: target >= totalHeight just skips the
// line, with no indication anything was cut).
var helpLines = []string{
	"j / Down       next",
	"k / Up         previous",
	"Enter          select/open",
	"Space          expand/collapse project",
	"/              filter projects, services, containers",
	"s              start/stop selected container",
	"n              create container or Compose service",
	"r              refresh",
	"Alt+r          restart selected container",
	"c              copy selected detail",
	"o              open port, mount, or compose path",
	"e              open shell in selected container",
	"l              logs",
	"/              filter logs while logs pane is focused",
	"e / w / i / a  log errors, warnings, info, all",
	"n / N          next/previous log search match",
	"f / End        resume live log tail",
	"x / Esc        clear active log filter",
	"u              replicate: pull latest image, recreate",
	"D              delete: real, permanent removal",
	"C              clone under a new name",
	"m              edit in place",
	"p              problems",
	"a in problems  analyze the selected problem with AI",
	"g              stats graphs",
	"T              theme picker",
	",              settings",
	"Ctrl+S         save settings/forms",
	"S              systems",
	"Systems: enter switch, t test, a add, e edit, d delete",
	"Ctrl+K         command palette",
	"?              keyboard help",
	"A              about screen",
	"q              quit",
}

func helpText() string {
	return strings.Join(helpLines, "\n")
}

// statusLegendEntries mirrors inspectorStatusColor/statusGlyph exactly —
// same glyphs, same colors, same order a container could actually be
// found in — so the legend never drifts out of sync with what the tree
// and inspector panes actually show.
var statusLegendEntries = []struct {
	glyph string
	color lipgloss.Color
	label string
}{
	{"●", "#80c990", "healthy / running"},
	{"▲", "#e5c07b", "restarting"},
	{"○", "#9aa6b2", "stopped, exited cleanly"},
	{"✖", "#e06c75", "dead"},
	{"!", "#e06c75", "unhealthy"},
}

// statusLegendLineCount is statusLegendLines' fixed output size (1 header
// + one row per statusLegendEntries entry) — helpBodyBudget needs this as
// a plain count, without a renderer, to reserve room for it.
var statusLegendLineCount = 1 + len(statusLegendEntries)

// statusLegendLines renders the same container-status glyphs and colors
// used in the tree/inspector panes as a small always-visible legend below
// the scrollable keybinding list, so "what does red/grey/yellow mean" has
// an answer right in Help instead of needing to be guessed at or asked.
// Every segment gets the panel's own background and foreground explicitly,
// the same way create_view.go's preview column does — RenderSoftBody skips
// applying its own default styling to any line that already contains an
// escape code (see its own source), so a line built from healthSpan
// (foreground-only, by design — see healthSpan's doc comment) needs its
// background supplied some other way, or the glyph and label would fall
// through to the terminal's raw background instead of the panel's.
func statusLegendLines(renderer tideui.Renderer) []string {
	panelBG := renderer.Styles.OverlayBody.GetBackground()
	textFg := styleForeground(renderer.Styles.OverlayBody, renderer.Styles.Theme.Fg)
	panel := lipgloss.NewStyle().Background(panelBG).Foreground(textFg)
	lines := make([]string, 0, statusLegendLineCount)
	lines = append(lines, panel.Render("Status colors:"))
	for _, entry := range statusLegendEntries {
		raw := healthSpan(entry.glyph, entry.color, textFg) + "  " + entry.label
		lines = append(lines, panel.Render(raw))
	}
	return lines
}

// softOverlayBodyBudget is how many body rows fit in a SoftPanelOverlay
// before tideui's overlay compositor silently truncates the bottom (see
// the comment on helpLines) — 4 lines of chrome outside the body content
// (the title-bearing top border, RenderSoftBody's blank top/bottom pad,
// and the bottom border) plus 2 more for the blank separator and hints
// line every soft overlay in this file appends after its list/text body.
func (m Model) softOverlayBodyBudget() int {
	return max(3, (m.height-1)-6)
}

// helpBodyBudget is how many helpLines rows fit in the help overlay at
// once — softOverlayBodyBudget's usual allowance, minus the always-visible
// status legend (statusLegendLineCount rows) and the blank line separating
// it from the scrollable keybinding list above it.
func (m Model) helpBodyBudget() int {
	return max(3, m.softOverlayBodyBudget()-statusLegendLineCount-1)
}

// appLogBodyBudget is how many appLogLines rows fit in the app-log overlay
// at once — no status legend to subtract for, unlike helpBodyBudget, so
// it's just the plain softOverlayBodyBudget allowance.
func (m Model) appLogBodyBudget() int {
	return m.softOverlayBodyBudget()
}

// aboutContentWidth mirrors the panel-width math in renderOverlay so the
// ember simulation (run in Update, which has no renderer) samples ignition
// at the same columns the view actually draws.
func aboutContentWidth(termWidth int) int {
	return min(82, max(46, termWidth-8)) - 4
}

func (m Model) aboutText(renderer tideui.Renderer, width int) string {
	// Every cell — including blanks and the row's own right-padding — gets
	// an explicit background matching the panel body. A Foreground-only
	// style (or a bare unstyled space) falls through to whatever's behind
	// it instead of the theme's own color, which looks fine by coincidence
	// in a dark theme and glaringly wrong in a light one.
	bg := renderer.Styles.OverlayBody.GetBackground()
	logo := aboutLogo()
	rows := len(logo)
	lines := make([]string, 0, rows)
	for row, line := range logo {
		cells := spotlightRowCells(line, row, rows, width, m.aboutSpotlights, m.aboutFrame)
		lines = append(lines, lipgloss.NewStyle().Width(width).Background(bg).Render(renderCells(cells, bg)))
	}
	return strings.Join(lines, "\n")
}

// aboutTagline is the slogan shown under the logo — a play on both meanings
// of "ship": the container kind and the idiom.
const aboutTagline = "Get Your Ship Together"

// aboutExtras renders the tagline and the sinking-ship storm animation
// beneath the logo. The ship starts only once spotlightRowCells' own reveal
// (search + converge + expand) has finished, so it doesn't compete with the
// logo animation for attention. Every style here carries its own explicit
// Background rather than relying on an outer wrap to fill the gaps — an
// outer Width/Background only paints padding it appends itself, not the
// interior of content that already carries its own embedded ANSI resets
// (the same bug this session already hit and fixed in the ember/burn
// reveal, the stats graphs, and the Ripple editor).
func (m Model) aboutExtras(renderer tideui.Renderer, width int) string {
	bg := renderer.Styles.OverlayBody.GetBackground()
	blank := lipgloss.NewStyle().Width(width).Background(bg).Render("")

	tagline := lipgloss.NewStyle().Width(width).Background(bg).Bold(true).Foreground(lipgloss.Color("#e7b2b2")).
		Render(centerPlainText(aboutTagline, width))

	lines := []string{blank, tagline, blank}
	lines = append(lines, aboutShipRows(width, m.aboutFrame, bg, m.aboutShipStatusText())...)
	return strings.Join(lines, "\n")
}

// aboutShipStatusText is the version line that fades into the ship
// animation's final settled-ocean frame (see overlayAboutShipStatusCells in
// about_ship.go). Update status is deliberately left out — it's already
// checked automatically on startup and shown in Settings, so repeating it
// here would just be stale-by-the-time-you-see-it duplication, not new info.
func (m Model) aboutShipStatusText() string {
	return "Version " + strings.TrimPrefix(m.appVersion, "v")
}

func aboutLogo() []string {
	return []string{
		" __    __ _           _  _____ _             ___           _    ",
		"/ / /\\ \\ \\ |__   __ _| |/__   \\ |__   ___   /   \\___   ___| | __",
		"\\ \\/  \\/ / '_ \\ / _` | __|/ /\\/ '_ \\ / _ \\ / /\\ / _ \\ / __| |/ /",
		" \\  /\\  /| | | | (_| | |_/ /  | | | |  __// /_// (_) | (__|   < ",
		"  \\/  \\/ |_| |_|\\__,_|\\__\\/   |_| |_|\\___/___,' \\___/ \\___|_|\\_\\",
		"                                                                ",
	}
}

// aboutCell is one rune of the about-screen render grid: a glyph plus the
// hex color it should be painted, computed fresh each frame from that
// cell's spotlight illumination.
type aboutCell struct {
	r     rune
	color string
}

func renderCells(cells []aboutCell, bg lipgloss.TerminalColor) string {
	blank := lipgloss.NewStyle().Background(bg).Render(" ")
	var out strings.Builder
	for _, c := range cells {
		if c.r == ' ' || c.color == "" {
			out.WriteString(blank)
			continue
		}
		out.WriteString(colorRune(c.r, c.color, bg))
	}
	return out.String()
}

// burnRevealCells renders one logo row at a given frame, using a
// precomputed per-column ignition delay (see aboutIgnitionOrder) rather
// than a formula, so the flame front follows the randomized connected burn
// spotlightRowCells renders one logo row at the given frame: characters
// lit by a nearby spotlight (search/converge phases) or by the expanding
// reveal from center (expand phase) show a bright highlight color with a
// soft falloff near the beam edge; everything else shows a dim, barely-lit
// base color — echoing the reference effect's "search the text area,
// illuminating characters, before converging... and expanding."
func spotlightRowCells(line string, row, rows, width int, spotlights []aboutSpotlight, frame int) []aboutCell {
	runes := []rune(centerPlainText(line, width))
	cells := make([]aboutCell, len(runes))
	expandStart := aboutSearchFrames + aboutConvergeFrames
	centerRow, centerCol := float64(rows-1)/2, float64(width-1)/2
	for col, r := range runes {
		if r == ' ' {
			cells[col] = aboutCell{r: ' '}
			continue
		}
		var brightness float64
		if frame >= expandStart {
			progress := float64(frame-expandStart) / float64(aboutExpandFrames)
			if progress > 1 {
				progress = 1
			}
			if progress >= 1 {
				brightness = 1
			} else {
				maxRadius := math.Hypot(float64(rows), float64(width)) / 2
				distance := math.Hypot(float64(row)-centerRow, float64(col)-centerCol)
				brightness = spotlightBrightness(distance, progress*maxRadius, aboutBeamFalloff)
			}
		} else {
			for _, sp := range spotlights {
				distance := math.Hypot(float64(row)-sp.row, float64(col)-sp.col)
				if b := spotlightBrightness(distance, aboutBeamRadius, aboutBeamFalloff); b > brightness {
					brightness = b
				}
			}
		}
		cells[col] = aboutCell{r: r, color: spotlightColor(row, rows, brightness)}
	}
	return cells
}

// spotlightBrightness returns 0 (unlit) to 1 (beam center) for a point at
// distance from a beam's center, given the beam's radius and the fraction
// of that radius (falloff) over which brightness fades from full to zero
// near the edge, instead of an abrupt cutoff.
func spotlightBrightness(distance, radius, falloff float64) float64 {
	if distance >= radius || radius <= 0 {
		return 0
	}
	fadeStart := radius * (1 - falloff)
	if distance <= fadeStart || fadeStart <= 0 {
		return 1
	}
	return 1 - (distance-fadeStart)/(radius-fadeStart)
}

// spotlightColor blends a dim unlit base toward spotlightFinalColor's
// row-based highlight by brightness.
func spotlightColor(row, rows int, brightness float64) string {
	const unlit = "#4a4550"
	if brightness <= 0 {
		return unlit
	}
	lit := spotlightFinalColor(row, rows)
	if brightness >= 1 {
		return lit
	}
	return lerpHexColor(unlit, lit, brightness)
}

// spotlightFinalColor mirrors the reference effect's default final
// gradient (purple -> pink -> cream) across the logo's rows, top to bottom.
func spotlightFinalColor(row, rows int) string {
	if rows <= 1 {
		return "#e7b2b2"
	}
	t := float64(row) / float64(rows-1)
	if t <= 0.5 {
		return lerpHexColor("#ab48ff", "#e7b2b2", t*2)
	}
	return lerpHexColor("#e7b2b2", "#fffebd", (t-0.5)*2)
}

func centerPlainText(line string, width int) string {
	runes := []rune(line)
	if width <= len(runes) {
		return line
	}
	left := (width - len(runes)) / 2
	right := width - len(runes) - left
	return strings.Repeat(" ", left) + line + strings.Repeat(" ", right)
}

func colorRune(r rune, color string, bg lipgloss.TerminalColor) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Background(bg).Render(string(r))
}

func lerpHexColor(from string, to string, t float64) string {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	fr, fg, fb := parseHexColor(from)
	tr, tg, tb := parseHexColor(to)
	return fmt.Sprintf("#%02x%02x%02x",
		int(float64(fr)+float64(tr-fr)*t+0.5),
		int(float64(fg)+float64(tg-fg)*t+0.5),
		int(float64(fb)+float64(tb-fb)*t+0.5),
	)
}

func parseHexColor(value string) (int, int, int) {
	value = strings.TrimPrefix(value, "#")
	if len(value) != 6 {
		return 255, 255, 255
	}
	r, _ := strconv.ParseInt(value[0:2], 16, 0)
	g, _ := strconv.ParseInt(value[2:4], 16, 0)
	b, _ := strconv.ParseInt(value[4:6], 16, 0)
	return int(r), int(g), int(b)
}

func formatPorts(ports []domain.Port) string {
	if len(ports) == 0 {
		return ""
	}
	out := make([]string, 0, len(ports))
	for _, port := range ports {
		private := fmt.Sprintf("%d/%s", port.Private, port.Type)
		if port.Public > 0 {
			out = append(out, fmt.Sprintf("%d -> %s", port.Public, private))
		} else {
			out = append(out, private)
		}
	}
	return strings.Join(out, ", ")
}

func formatMounts(mounts []domain.Mount) string {
	if len(mounts) == 0 {
		return ""
	}
	out := make([]string, 0, len(mounts))
	for _, mount := range mounts {
		out = append(out, mount.Source+" -> "+mount.Destination)
	}
	return strings.Join(out, "\n")
}

func formatList(values []string, limit int) string {
	if len(values) == 0 {
		return ""
	}
	values = append([]string(nil), values...)
	sort.Strings(values)
	if len(values) > limit {
		values = append(values[:limit], fmt.Sprintf("... %d more", len(values)-limit))
	}
	return strings.Join(values, "\n")
}

func formatMap(values map[string]string, limit int) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = append(keys[:limit], fmt.Sprintf("... %d more", len(keys)-limit))
	}
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		if strings.HasPrefix(key, "... ") {
			lines = append(lines, key)
			continue
		}
		lines = append(lines, key+"="+values[key])
	}
	return strings.Join(lines, "\n")
}

// formatPullProgress renders one PullProgress update as the single line
// shown in the status bar, e.g. "pulling redis:7 — layer 3f4a2b1c downloading 45%".
func formatPullProgress(image string, p app.PullProgress) string {
	label := "pulling " + image
	if p.ID == "" {
		return label
	}
	id := p.ID
	if len(id) > 12 {
		id = id[:12]
	}
	if p.Total > 0 {
		pct := int(100 * p.Current / p.Total)
		return fmt.Sprintf("%s — layer %s %s %d%%", label, id, p.Status, pct)
	}
	return fmt.Sprintf("%s — layer %s %s", label, id, p.Status)
}

func formatBytes(value uint64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	size := float64(value)
	unit := 0
	for size >= 1024 && unit < len(units)-1 {
		size /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d %s", value, units[unit])
	}
	return fmt.Sprintf("%.1f %s", size, units[unit])
}

func short(value string, n int) string {
	if len(value) <= n {
		return value
	}
	return value[:n]
}

func (m Model) leftPaneWidth() int {
	return max(1, m.width*3/12)
}

func (m Model) centerPaneWidth() int {
	return max(1, m.width*5/12)
}

func (m Model) rightPaneWidth() int {
	return max(1, m.width-m.leftPaneWidth()-m.centerPaneWidth())
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func alignText(left, right string, width int) string {
	if width <= 0 {
		return ""
	}
	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(right)
	if rightWidth >= width {
		return ansi.Truncate(right, width, "")
	}
	if leftWidth+rightWidth+1 > width {
		left = ansi.Truncate(left, max(0, width-rightWidth-1), "")
		leftWidth = lipgloss.Width(left)
	}
	gap := max(1, width-leftWidth-rightWidth)
	return left + strings.Repeat(" ", gap) + right
}
