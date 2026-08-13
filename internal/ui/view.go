package ui

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/allisonhere/tideui"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/allisonhere/whatthedock/internal/config"
	"github.com/allisonhere/whatthedock/internal/domain"
	"github.com/allisonhere/whatthedock/internal/systems"
)

func (m Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	renderer := tideui.NewRenderer(m.theme, tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
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
		Right: "j/k move  space expand  / filter  l logs  p problems  g stats  S systems  , settings  ctrl+k commands  ? help  q quit",
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
		content, width := m.renderProblemsContent(renderer)
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
		return strings.Join(append([]string{fmt.Sprintf("tail %d/%d", total, total)}, chips...), " · ")
	}
	return strings.Join(append([]string{fmt.Sprintf("paused %d-%d/%d", start+1, end, total)}, chips...), " · ")
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
	lines := []string{
		renderer.Styles.DetailMeta.Render(""),
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

func renderStatRow(renderer tideui.Renderer, settings appSettings, width int, label string, graph statGraph, suffix string, color lipgloss.Color) string {
	room := width - 9 - lipgloss.Width(suffix) - 2
	if room < 8 {
		return renderer.RenderRow(tideui.Row{Prefix: fmt.Sprintf("%-9s", label), Suffix: suffix}, width)
	}
	text := renderHybridGraph(renderer, settings, graph, color, room)
	return renderer.RenderRow(tideui.Row{Prefix: fmt.Sprintf("%-9s", label), Text: text, Suffix: suffix}, width)
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
	return strings.Join(parts, "  ")
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
	id       domain.ResourceID
	severity string
	name     string
	detail   string
}

func (m Model) renderProblemsContent(renderer tideui.Renderer) (string, int) {
	problems := m.snapshotProblems()
	width := m.centerPaneWidth() - 4
	if width < 20 {
		width = 20
	}
	if len(problems) == 0 {
		return renderer.Styles.DetailMeta.Render("No container problems detected."), width
	}
	lines := make([]string, 0, len(problems)+1)
	lines = append(lines, renderer.Styles.DetailMeta.Render(fmt.Sprintf("%d problem(s) found", len(problems))))
	for i, problem := range problems {
		selected := m.problemSelected(i, problem)
		muted := problem.severity == "warn"
		color := severityColor(problem.severity)
		baseFg := rowForeground(renderer, selected, muted)
		lines = append(lines, renderer.RenderRow(tideui.Row{
			Prefix:   healthSpan(problem.severity, color, baseFg) + "  ",
			Text:     problem.name,
			Suffix:   healthSpan(problem.detail, color, baseFg),
			Selected: selected,
			Muted:    muted,
		}, width))
	}
	if m.focus == paneActivity {
		limit := max(1, m.activityVisibleRows()-2)
		lines = lines[:min(len(lines), limit)]
	}
	return strings.Join(lines, "\n"), width
}

func (m Model) problemSelected(index int, problem problemRow) bool {
	if m.focus == paneActivity && m.mode == activityProblems {
		return index == m.problemCursor
	}
	return m.selectedID == problem.id
}

func (m Model) snapshotProblems() []problemRow {
	var problems []problemRow
	for _, ctr := range m.snapshotContainers() {
		name := ctr.DisplayName()
		switch {
		case ctr.Health == domain.HealthUnhealthy:
			problems = append(problems, problemRow{id: ctr.ID, severity: "crit", name: name, detail: "unhealthy"})
		case ctr.Restarting || ctr.State == domain.StateRestarting:
			detail := "restarting"
			if ctr.RestartCount > 0 {
				detail = fmt.Sprintf("restarting (%d restarts)", ctr.RestartCount)
			}
			problems = append(problems, problemRow{id: ctr.ID, severity: "crit", name: name, detail: detail})
		case ctr.State == domain.StateDead:
			problems = append(problems, problemRow{id: ctr.ID, severity: "crit", name: name, detail: "dead"})
		case ctr.RestartCount >= 5:
			problems = append(problems, problemRow{id: ctr.ID, severity: "warn", name: name, detail: fmt.Sprintf("%d restarts", ctr.RestartCount)})
		case ctr.State == domain.StateStopped || ctr.State == domain.StateExited:
			problems = append(problems, problemRow{id: ctr.ID, severity: "warn", name: name, detail: string(ctr.State)})
		case ctr.Health == domain.HealthUnknown:
			problems = append(problems, problemRow{id: ctr.ID, severity: "warn", name: name, detail: "health unknown"})
		case ctr.Compose.Project == "" && hasPublicPorts(ctr):
			problems = append(problems, problemRow{id: ctr.ID, severity: "warn", name: name, detail: "public ports"})
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
		}
	default:
		return nil
	}
}

func (m Model) renderOverlay(renderer tideui.Renderer) *tideui.Overlay {
	switch m.overlay {
	case overlayHelp:
		width := min(72, max(40, m.width-8))
		content := renderer.RenderSoftBody(width, helpText()+"\n\n"+
			renderer.RenderSoftHints(width-4, tideui.SoftHint{Key: "esc/?/q", Label: "close"}))
		overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "help", Content: content, Width: width})
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
	case overlayCopy:
		return m.copyOverlay(renderer)
	case overlayOpen:
		return m.openOverlay(renderer)
	default:
		return nil
	}
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

func (m Model) copyOverlay(renderer tideui.Renderer) *tideui.Overlay {
	width := min(82, max(46, m.width-8))
	contentWidth := width - 4
	rows := m.copyRows()
	lines := make([]string, 0, len(rows)+4)
	if len(rows) == 0 {
		lines = append(lines, renderer.Styles.DetailMeta.Render("No copyable details."))
	} else {
		for i, row := range rows {
			lines = append(lines, renderer.RenderSoftRow(tideui.SoftRow{
				Text:     row.label,
				Suffix:   short(row.value, max(12, contentWidth-24)),
				Selected: i == m.copyCursor,
			}, contentWidth))
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

func (m Model) settingsOverlay(renderer tideui.Renderer) *tideui.Overlay {
	width := min(76, max(44, m.width-8))
	contentWidth := width - 4
	rows := make([]string, 0, len(m.settingsRows())+4)
	for i, row := range m.settingsRows() {
		if row.kind == settingsRowSection {
			if len(rows) > 0 {
				rows = append(rows, "")
			}
			rows = append(rows, renderer.Styles.DetailMeta.Render(row.label))
			continue
		}
		suffix := row.value
		if row.kind == settingsRowAction {
			suffix = "enter"
		}
		rows = append(rows, renderer.RenderSoftRow(tideui.SoftRow{Text: row.label, Suffix: suffix, Selected: i == m.settingsCursor}, contentWidth))
	}
	if m.settingsPath != "" {
		rows = append(rows, "", renderer.Styles.DetailMeta.Width(contentWidth).Render("Config  "+m.settingsPath))
	}
	rows = append(rows, "", renderer.RenderSoftHints(contentWidth,
		tideui.SoftHint{Key: "enter/space", Label: "change"},
		tideui.SoftHint{Key: "h/l", Label: "previous/next"},
		tideui.SoftHint{Key: "ctrl+s", Label: "save"},
		tideui.SoftHint{Key: "esc", Label: "cancel"},
	))
	content := renderer.RenderSoftBody(width, strings.Join(rows, "\n"))
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
	if m.mode == activityLogs && (strings.TrimSpace(m.logFilter) != "" || m.logLevel != logSeverityAll) {
		return renderer.Styles.StatusNotice.Render(prefix+"logs: "+filterStatus(m.logFilter, m.logLevel)) + renderer.Styles.StatusBar.Render(" "+m.status)
	}
	if strings.TrimSpace(m.filter) != "" {
		return renderer.Styles.StatusNotice.Render(prefix+"filter: "+m.filter) + renderer.Styles.StatusBar.Render(" "+m.status)
	}
	return renderer.Styles.StatusBar.Render(prefix + m.status)
}

func helpText() string {
	return strings.Join([]string{
		"j / Down       next",
		"k / Up         previous",
		"Enter          select/open",
		"Space          expand/collapse project",
		"/              filter projects, services, containers",
		"s              start/stop selected container",
		"r              refresh",
		"Alt+r          restart selected container",
		"c              copy selected detail",
		"o              open port, mount, or compose path",
		"l              logs",
		"/              filter logs while logs pane is focused",
		"e / w / i / a  log errors, warnings, info, all",
		"n / N          next/previous log search match",
		"f / End        resume live log tail",
		"x / Esc        clear active log filter",
		"p              problems",
		"g              stats graphs",
		"T              theme picker",
		",              settings",
		"Ctrl+S         save settings/forms",
		"S              systems",
		"Systems: enter switch, t test, a add, e edit, d delete",
		"Ctrl+K         command palette",
		"?              keyboard help",
		"q              quit",
	}, "\n")
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
