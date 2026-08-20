package ui

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	sp "github.com/allisonhere/cli-spinners"
	"github.com/allisonhere/tideui"
	tea "github.com/charmbracelet/bubbletea"
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
	treeContent, inspectorContent := m.renderTree(renderer), m.renderInspector(renderer)
	if m.logsExpanded {
		// Skip the real (and, at sliver width, likely garbled) content
		// entirely — just an icon indicating each pane is collapsed, not
		// gone. See columnRatios' doc comment for why the width these
		// render against comes from the same source as the Layout call
		// below rather than a second guess at it.
		treeContent = renderCollapsedPane(renderer, m.leftPaneWidth(), m.paneContentRows(), "▸")
		inspectorContent = renderCollapsedPane(renderer, m.rightPaneWidth(), m.paneContentRows(), "◂")
	}
	panes := [3]tideui.Pane{
		{Title: "Projects", Hint: "tree", Content: treeContent, Focused: m.focus == paneTree},
		{Title: activityTitle, Hint: activityHint, Content: m.renderActivity(renderer), Focused: m.focus == paneActivity},
		{Title: inspectorTitle, Hint: "details", Content: inspectorContent, Focused: m.focus == paneInspector},
	}
	modal := m.renderOverlay(renderer)
	status := &tideui.StatusBar{
		Left:  m.statusLeft(renderer),
		Right: "j/k move  space expand  / filter  n create  l logs  L expand  p problems  g stats  S systems  , settings  ctrl+k commands  ? help  A about  q quit",
	}
	return topbar + "\n" + renderer.Render(tideui.Layout{
		Width:        m.width,
		Height:       m.height - 1,
		Mode:         tideui.ThreeColumn,
		Panes:        panes,
		Status:       status,
		Modal:        modal,
		ColumnRatios: m.columnRatios(),
	})
}

// renderCollapsedPane is Tree/Inspector's content while logsExpanded —
// just icon reusing the tree-row collapse/expand glyph language (▸/▾,
// see the project-row collapse feature), vertically centered in a
// background-filled column so the sliver still reads as "part of the
// layout, temporarily shrunk" rather than an empty gap.
func renderCollapsedPane(renderer tideui.Renderer, width, height int, icon string) string {
	width, height = max(1, width), max(1, height)
	blank := lipgloss.NewStyle().Background(renderer.Styles.Theme.Bg).Width(width).Render("")
	iconLine := lipgloss.NewStyle().Background(renderer.Styles.Theme.Bg).Foreground(renderer.Styles.Theme.Dimmed).Width(width).Align(lipgloss.Center).Render(icon)
	lines := make([]string, height)
	for i := range lines {
		lines[i] = blank
	}
	lines[height/2] = iconLine
	return strings.Join(lines, "\n")
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

// renderTopbar's left segment used to be built from two independent,
// self-contained Style.Render() calls (StatusNotice for the "WHAT THE
// DOCK?!" badge, StatusBar for the host name) concatenated together, with
// the WHOLE aligned left+gap+right string then wrapped in one more outer
// StatusBar.Width(width).Render() call. Each independent Render() emits
// its own absolute reset at its end — so the notice badge's reset (or the
// host-name segment's own) wiped out that outer wrap's background for
// everything after it, leaving the gap and the "Docker connected..."
// status text on the right with no background at all. Confirmed present
// on every screen and every theme (light themes make it starkly visible —
// a black gap in an otherwise light-colored bar). backgroundSpan restores
// explicitly back to StatusBar's own colors instead of resetting, so
// nothing after it needs to inherit anything from a wrap a reset could
// kill — the rest of the line can go back to being plain, unstyled text
// exactly as it was, now safely inside the one outer wrap all the way
// through.
func (m Model) renderTopbar(renderer tideui.Renderer) string {
	width := max(1, m.width)
	// StatusBar itself carries a real Padding(0, 1) (tideui's own style,
	// not something this func adds) that reserves 2 columns on top of
	// whatever Width(width) is given below — content built to the full
	// width here always overflowed the box by exactly those 2 columns,
	// which lipgloss's Render() silently fixed by word-wrapping onto a
	// second line instead of erroring, pushing the entire app down one
	// row on every single screen at every terminal size. contentWidth is
	// the actual usable space once that padding is accounted for.
	contentWidth := max(1, width-2)
	statusBarBG, _ := renderer.Styles.StatusBar.GetBackground().(lipgloss.Color)
	statusBarFG := styleForeground(renderer.Styles.StatusBar, renderer.Styles.Theme.Fg)
	noticeBG, _ := renderer.Styles.StatusNotice.GetBackground().(lipgloss.Color)
	noticeFG := styleForeground(renderer.Styles.StatusNotice, renderer.Styles.Theme.Fg)
	// The extra leading/trailing spaces folded into these two strings
	// (beyond " WHAT THE DOCK?! " and " "+hostname's own literal spaces)
	// replicate StatusNotice/StatusBar's own Padding(0,1) by hand —
	// backgroundSpan colors exactly the text it's given and doesn't apply
	// a Style's Padding, unlike the two independent Style.Render() calls
	// this used to be built from.
	left := backgroundSpan("  WHAT THE DOCK?!  ", noticeBG, noticeFG, statusBarBG, statusBarFG, true) +
		"  " + m.provider.Host().Name + " "
	right := fmt.Sprintf("Docker connected · %d projects · %d standalone · %d problems",
		len(m.snapshot.Projects), len(m.snapshot.Standalone), len(m.snapshotProblems()))
	if m.statusErr {
		right = topbarStatusLine(m.status)
	}
	return renderer.Styles.StatusBar.Width(width).Render(alignText(left, right, contentWidth))
}

// topbarStatusLine collapses a possibly multi-line status message (a
// failed docker/compose command's raw combined output is often several
// lines — a port conflict plus surrounding CLI context) down to the one
// line the topbar actually has room for. Passing multi-line text straight
// into alignText/Width().Render() below is exactly the shape of bug that
// made the topbar itself wrap onto a second line — content measuring
// wider (or, here, taller) than the box gets silently mangled rather than
// erroring. The full message is still readable in full via the app log
// (see recordAppLog), which is why this only needs to point there instead
// of trying to cram everything in.
func topbarStatusLine(status string) string {
	lines := strings.Split(strings.TrimSpace(status), "\n")
	first := strings.TrimSpace(lines[0])
	extra := 0
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) != "" {
			extra++
		}
	}
	if extra > 0 {
		first += fmt.Sprintf("  (+%d more line(s) — settings > view app log)", extra)
	}
	return first
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

// padPlainLine truncates plain (no embedded ANSI) text to width and
// renders it through style so the result is always exactly one line at
// exactly width, with style's own background covering every cell. A bare
// style.Width(width).Render(text) would word-wrap onto multiple lines
// once text exceeds width — fine for the AI-insight/problem-detail text
// this app deliberately wraps via wrapInsightText, but wrong for the
// short single-line status/placeholder messages this helper is for
// ("Select a container...", "No logs match...", etc.), which are meant
// to stay one line no matter how narrow the pane gets. Still fills the
// full width with style's background, which a bare style.Render(text)
// (no Width at all) does not — see this codebase's several "stray
// colored box" bug reports for what happens without it.
func padPlainLine(style lipgloss.Style, text string, width int) string {
	return style.Width(width).Render(ansi.Truncate(text, width, "…"))
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
		// ansi.Truncate first, then Width to pad (not wrap) — logVisibleRows/
		// logStartIndex both assume one log line occupies exactly one
		// terminal row, so a long line must be cut to fit, never wrapped
		// onto extra rows. Word-wrapping pre-styled content here also hit a
		// real rendering bug: lipgloss's wrap-width measurement miscounts
		// backgroundSpan's "restore to a specific color" close sequence
		// (not a plain reset), splitting words mid-token once any part of
		// the line carried a background span — reported live as "why does
		// it change wrapping" once live filter highlighting made that
		// common. Truncating first means Width() only ever pads, never
		// triggers that wrap path at all.
		rendered := ansi.Truncate(renderLogLine(renderer, m.settings.LogColor, m.logFilter, line), width, "…")
		lines = append(lines, renderer.Styles.DetailBody.Width(width).Render(rendered))
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
	}
	if m.logLevel != logSeverityAll {
		chips = append(chips, m.logLevel.String())
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

// renderLogToken colors one token via foregroundSpan rather than a
// self-contained lipgloss.NewStyle().Background(...).Render(...) call —
// the regression fix for a background bug reported live: renderLogLine
// concatenates many independently-built pieces (colored tokens, plain
// words, raw whitespace runs) into one line that's only ever wrapped in
// an outer background style ONCE, by its caller (renderActivity's
// DetailBody.Width(width).Render(...)). A self-contained Style.Render()
// call emits its own *absolute* reset at the end — not a "restore the
// outer style" reset — so every colored token (a timestamp, an HTTP
// method/status, a severity keyword) was wiping out that outer
// background for every character after it, until the next colored token
// re-established a background of its own. On any log line with at least
// one colored token — the common case under the default "full" log
// color mode — everything after the first one lost its background,
// showing raw terminal default instead of the theme's own. foregroundSpan
// only ever changes foreground/bold (see its own doc comment), so nesting
// it inside the one continuous outer Render call here can never touch
// that call's own background.
func renderLogToken(renderer tideui.Renderer, mode logColorMode, token string, first bool) string {
	if mode == logColorMono {
		return token
	}
	baseFg := styleForeground(renderer.Styles.DetailBody, renderer.Styles.Theme.Fg)
	trimmed := strings.Trim(token, `[](),;:"'`)
	switch {
	case first && logTimestampPattern.MatchString(token) && (mode == logColorFull || mode == logColorHTTP || mode == logColorSeverity):
		return foregroundSpan(token, "#8aadf4", baseFg, false)
	case logHTTPMethodPattern.MatchString(trimmed) && (mode == logColorFull || mode == logColorHTTP):
		return foregroundSpan(token, "#7dcfff", baseFg, true)
	case logHTTPStatusPattern.MatchString(trimmed) && (mode == logColorFull || mode == logColorHTTP):
		return foregroundSpan(token, httpStatusColor(trimmed), baseFg, true)
	case logSeverityPattern.MatchString(token) && (mode == logColorFull || mode == logColorSeverity):
		return foregroundSpan(token, logSeverityColor(token), baseFg, true)
	default:
		return token
	}
}

// renderLogMatch highlights just the matching characters within a
// filter-matched token — not the whole token — via backgroundSpan instead
// of a self-contained Style.Render() call: same fix, same reasoning as
// renderLogToken's own doc comment, just for a span that genuinely needs
// to change background (the match highlight), not only foreground:
// backgroundSpan explicitly restores to the surrounding line's own
// background/foreground afterward instead of emitting an absolute reset.
// Whole-token highlighting was the original (and, reported live, wrong)
// behavior: typing a single common letter like "f" highlighted every word
// containing an "f" anywhere, in full, instead of just the "f" — the
// opposite of what live, character-by-character highlighting is supposed
// to show as you type. The non-matching prefix/suffix render plain (any
// per-token color renderLogToken already carried is dropped for the whole
// token, same as before this fix, just no longer forcibly extended across
// characters that don't actually match).
//
// The purple/white pair is fixed, not theme-derived — the same choice
// renderLogToken already makes for timestamps/HTTP methods/severity
// keywords. Theme.Selected is used elsewhere for ordinary row selection,
// so reusing it here made a live search match look like just another
// selected row instead of standing out against arbitrary log text.
func renderLogMatch(renderer tideui.Renderer, query, token, rendered string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return rendered
	}
	tokenRunes, queryRunes := []rune(token), []rune(query)
	start := runeIndexFold(tokenRunes, queryRunes)
	if start < 0 {
		return rendered
	}
	end := start + len(queryRunes)
	const matchBg = lipgloss.Color("#a855f7")
	const matchFg = lipgloss.Color("#ffffff")
	baseFg := styleForeground(renderer.Styles.DetailBody, renderer.Styles.Theme.Fg)
	match := backgroundSpan(string(tokenRunes[start:end]), matchBg, matchFg, renderer.Styles.Theme.Bg, baseFg, true)
	return string(tokenRunes[:start]) + match + string(tokenRunes[end:])
}

// runeIndexFold returns the index of needle's first case-insensitive
// occurrence in haystack, both already decoded to runes — rune-based
// rather than strings.Index on strings.ToLower'd text, so a match
// position is never computed from a byte offset that could land inside a
// multi-byte character if lowercasing ever changed a string's byte
// length (rare, but real, for some non-ASCII casing). Returns -1 if
// needle is empty or not found.
func runeIndexFold(haystack, needle []rune) int {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return -1
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j, r := range needle {
			if unicode.ToLower(haystack[i+j]) != unicode.ToLower(r) {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func isLogSpace(char byte) bool {
	return char == ' ' || char == '\t'
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
	// The gauge style's own bar is already a proportional fill — pairing
	// it with the old 5-segment meter would show the same information
	// twice, so gauge skips the meter and lets the bar fill the space
	// the meter would otherwise have taken.
	meter := ""
	sparkWidth := width
	if settings.GraphStyle != graphStyleGauge {
		meter = renderMeter(renderer, settings, graphLevel(settings, graph), color)
		sparkWidth = width - lipgloss.Width(meter) - 2
	}
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
	parts := []string{spark}
	if meter != "" {
		parts = []string{meter, spark}
	}
	if delta != "" {
		parts = append(parts, renderer.Styles.DetailMeta.Render(delta))
	}
	// The gap between parts is otherwise a bare, unstyled string — same
	// falls-through-to-default issue as the suffix above.
	gap := lipgloss.NewStyle().Background(renderer.Styles.Theme.Bg).Render("  ")
	return strings.Join(parts, gap)
}

// renderGaugeBar draws the graphStyleGauge look: a single thin progress
// bar for the latest value, rather than a glyph-per-sample history —
// value/maxValue as a fraction of width columns of "━", one "╸" leading-edge
// cap at the fill boundary, and "─" for the remainder.
func renderGaugeBar(renderer tideui.Renderer, settings appSettings, value, maxValue float64, color lipgloss.Color, width int) string {
	width = max(1, width)
	frac := 0.0
	if maxValue > 0 {
		frac = value / maxValue
		if frac < 0 {
			frac = 0
		} else if frac > 1 {
			frac = 1
		}
	}
	filled := clamp(int(frac*float64(width)+0.5), 0, width)
	level := clamp(int(frac*7)+1, 1, 7)
	hotColor := statHeatColor(settings, level, color, renderer)
	bg := renderer.Styles.Theme.Bg
	dimmed := renderer.Styles.Theme.Dimmed

	switch {
	case filled <= 0:
		return lipgloss.NewStyle().Background(bg).Foreground(dimmed).Render(strings.Repeat("─", width))
	case filled >= width:
		return lipgloss.NewStyle().Background(bg).Foreground(hotColor).Bold(true).Render(strings.Repeat("━", width))
	default:
		fill := lipgloss.NewStyle().Background(bg).Foreground(hotColor).Bold(true).Render(strings.Repeat("━", filled-1) + "╸")
		rest := lipgloss.NewStyle().Background(bg).Foreground(dimmed).Render(strings.Repeat("─", width-filled))
		return fill + rest
	}
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
		if settings.GraphStyle == graphStyleGauge {
			return renderGaugeBar(renderer, settings, 0, 1, color, width)
		}
		level := clamp(graph.fallbackLevel, 1, len(glyphs))
		return styleGraphGlyphs(renderer, settings, strings.Join(glyphs[:level], ""), level, color)
	}
	if settings.GraphStyle == graphStyleGauge {
		return renderGaugeBar(renderer, settings, graph.values[len(graph.values)-1], graph.maxValue, color, width)
	}
	// Each bar takes 1+spacing columns (bars' spaced style takes 2: the
	// glyph plus its trailing blank), so fewer values fit in the same
	// width budget than a style with no spacing — callers size width in
	// total columns, not glyph count, so this keeps that contract intact
	// rather than letting a spaced sparkline overflow it.
	unit := 1 + graphGlyphSpacing(settings)
	maxValues := max(1, width/unit)
	values := graph.values
	if len(values) > maxValues {
		values = values[len(values)-maxValues:]
	}
	bg := renderer.Styles.Theme.Bg
	spacing := graphGlyphSpacing(settings)
	out := make([]string, 0, len(values))
	for _, value := range values {
		level := graphValueLevel(settings, value, graph.maxValue)
		glyph := glyphs[level-1]
		cell := lipgloss.NewStyle().
			Background(bg).
			Foreground(statGlyphColor(settings, glyph, color, renderer)).
			Bold(statGlyphBold(glyph)).
			Render(glyph)
		if spacing > 0 {
			cell += lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", spacing))
		}
		out = append(out, cell)
	}
	return strings.Join(out, "")
}

func graphGlyphs(settings appSettings) []string {
	switch settings.GraphStyle {
	case graphStyleBlocks, graphStyleBars, graphStyleGauge:
		return []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}
	case graphStyleBraille:
		return []string{"⣀", "⣤", "⣶", "⣿"}
	default:
		return []string{"▁", "▂", "▂", "▃", "▄", "▅", "▇", "█", "▆", "▄", "▃", "▅", "▆", "▇"}
	}
}

// graphGlyphSpacing is how many blank, background-colored columns follow
// every glyph a graph style draws — 0 for every style except bars, which
// is otherwise identical to blocks (same 8-level glyph set) but reads as
// a spaced-out bar chart instead of an unbroken sparkline.
func graphGlyphSpacing(settings appSettings) int {
	if settings.GraphStyle == graphStyleBars {
		return 1
	}
	return 0
}

func staticGraphGlyph(settings appSettings, level int) string {
	glyphs := graphGlyphs(settings)
	return glyphs[clamp(level, 1, len(glyphs))-1]
}

func styleGraphGlyphs(renderer tideui.Renderer, settings appSettings, graph string, _ int, color lipgloss.Color) string {
	bg := renderer.Styles.Theme.Bg
	spacing := graphGlyphSpacing(settings)
	var out []string
	for _, glyph := range graph {
		glyph := string(glyph)
		cell := lipgloss.NewStyle().
			Background(bg).
			Foreground(statGlyphColor(settings, glyph, color, renderer)).
			Bold(statGlyphBold(glyph)).
			Render(glyph)
		if spacing > 0 {
			cell += lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", spacing))
		}
		out = append(out, cell)
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

	// The summary line must be padded to width itself (not left to whatever
	// composites this pane's content): every problem row below it goes
	// through RenderRow, which fills the full width internally, but this
	// line previously didn't — leaving it short. Composited against a full-
	// width neighbor (namely the first/selected row's own ItemSelected
	// background), the short line's unpadded tail picked up that
	// neighboring row's background color instead of its own, showing up as
	// a stray colored box sitting half over the header line. Width(width)
	// makes this line self-contained the same way every RenderRow-built
	// line already is.
	listLines := make([]string, 0, len(problems)+1)
	listLines = append(listLines, padPlainLine(renderer.Styles.DetailMeta, fmt.Sprintf("%d problem(s) found", len(problems)), width))
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
// Every return path below wraps its final joined lines in
// DetailBody.Width(width).Render(...) rather than a bare .Render(...) —
// the same fix renderProblemsSplit's summary line needed (see its own
// doc comment). heading here is built via foregroundSpan, which embeds
// its own short-lived ANSI codes and is *not* itself padded to width;
// joined with wrapInsightText's already-width-padded body lines and
// handed to lipgloss.JoinVertical (in renderProblemsSplit) alongside the
// full-width list/divider blocks, an unpadded heading line got stretched
// to match by JoinVertical's own plain, unstyled space-padding — the
// exact same stray-background-box bug, just on the "Analyzing with AI…"/
// "AI Analysis"/"AI analysis failed" heading instead of the problem
// count. Width(width) makes every line this function returns
// self-contained instead of depending on a caller to pad it correctly.
func (m Model) renderProblemInsight(renderer tideui.Renderer, row problemRow, width int) string {
	if m.aiAnalysisFor != row.id {
		lines := wrapInsightText(problemInsight(row), width, m.problemsInsightRows())
		return renderer.Styles.DetailBody.Width(width).Render(strings.Join(lines, "\n"))
	}

	baseFg := styleForeground(renderer.Styles.DetailBody, renderer.Styles.Theme.Fg)
	switch {
	case m.aiAnalyzing:
		heading := foregroundSpan("Analyzing with AI…", renderer.Styles.Theme.Dimmed, baseFg, false)
		return renderer.Styles.DetailBody.Width(width).Render(heading)
	case m.aiAnalysisErr != nil:
		heading := foregroundSpan("AI analysis failed", renderer.Styles.Theme.Error, baseFg, true)
		body := wrapInsightText(m.aiAnalysisErr.Error(), width, max(1, m.problemsInsightRows()-1))
		lines := append([]string{heading}, body...)
		return renderer.Styles.DetailBody.Width(width).Render(strings.Join(lines, "\n"))
	case m.aiAnalysis != "":
		heading := foregroundSpan("AI Analysis", renderer.Styles.Theme.BorderFocus, baseFg, true)
		body := wrapInsightText(m.aiAnalysis, width, max(1, m.problemsInsightRows()-1))
		lines := append([]string{heading}, body...)
		return renderer.Styles.DetailBody.Width(width).Render(strings.Join(lines, "\n"))
	default:
		lines := wrapInsightText(problemInsight(row), width, m.problemsInsightRows())
		return renderer.Styles.DetailBody.Width(width).Render(strings.Join(lines, "\n"))
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

// dashboardSummary is the Dashboard overlay's fleet-wide rollup — a
// "fleet summary" per the user's own framing, not real host-OS metrics
// (Docker's API doesn't expose those, and they wouldn't work uniformly
// across a local vs. SSH-tunneled remote system anyway). counts is keyed
// by statusGlyph(ctr), the exact same classification (and glyph/color set,
// via statusLegendEntries) already used everywhere else in this app —
// deliberately reused rather than reinvented, so the Dashboard's summary
// line never disagrees with the tree/Problems pane about what a container
// counts as.
type dashboardSummary struct {
	counts               map[string]int
	totalCPU             float64
	memUsed, memLimit    uint64
	netRxRate, netTxRate uint64
}

// fleetSummary computes dashboardSummary from whatever containers/history
// whatthedock already has in memory — no new Docker API call. CPU/memory/
// network aggregates only include containers whose history has actually
// received at least one sample yet (history.lastStats != nil); a
// container the Dashboard hasn't polled yet (this render, right after
// opening) simply doesn't contribute rather than counting as zero.
func (m Model) fleetSummary() dashboardSummary {
	summary := dashboardSummary{counts: map[string]int{}}
	for _, ctr := range m.snapshotContainers() {
		summary.counts[statusGlyph(ctr)]++
		if !ctr.IsRunning() {
			continue
		}
		history := m.statsHistory[ctr.ID]
		if history.lastStats == nil {
			continue
		}
		summary.totalCPU += history.lastStats.CPUPercent
		summary.memUsed += history.lastStats.MemoryUsage
		summary.memLimit += history.lastStats.MemoryLimit
		if len(history.NetworkRx) > 0 {
			summary.netRxRate += history.NetworkRx[len(history.NetworkRx)-1]
		}
		if len(history.NetworkTx) > 0 {
			summary.netTxRate += history.NetworkTx[len(history.NetworkTx)-1]
		}
	}
	return summary
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

// backgroundSpan is foregroundSpan for a span that needs to change
// background too (a selection/match highlight, not just colored text) —
// same non-resetting-restore shape: an absolute "\x1b[0m" reset would
// wipe out whatever background an outer, already-open Style.Render() call
// established before this span started, not just this span's own. bg/fg
// are the highlight's own colors; restoreBg/restoreFg are what the
// surrounding text's background/foreground should read as immediately
// after the span ends.
func backgroundSpan(text string, bg, fg, restoreBg, restoreFg lipgloss.Color, bold bool) string {
	style := ansi.NewStyle().BackgroundColor(bg).ForegroundColor(fg)
	if bold {
		style = style.Bold()
	}
	restoreStyle := ansi.NewStyle().BackgroundColor(restoreBg).ForegroundColor(restoreFg).Normal().Italic(false)
	return style.String() + text + restoreStyle.String()
}

// foregroundSpanDefault is foregroundSpan for text whose surrounding
// context has no specific foreground color to restore to (e.g. adjacent
// plain punctuation/whitespace that was never given an explicit
// foreground of its own) — it resets foreground/bold/italic back to the
// terminal's own default (SGR 39) instead of a specific color, using the
// same non-resetting-restore shape foregroundSpan/backgroundSpan use:
// never an absolute reset that would also wipe out whatever background an
// outer, already-open Style.Render() call established before this span
// started.
func foregroundSpanDefault(text string, color lipgloss.Color, bold, italic bool) string {
	style := ansi.NewStyle().ForegroundColor(color)
	if bold {
		style = style.Bold()
	}
	if italic {
		style = style.Italic(true)
	}
	restoreStyle := ansi.NewStyle().DefaultForegroundColor().Normal().Italic(false)
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
	// raw skips the label's usual lowercasing — for live-typed content
	// (the log filter chip's input echo), which must show exactly what
	// the user typed, not a static hint.
	raw bool
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
		label := action.label
		if !action.raw {
			label = strings.ToLower(label)
		}
		chip := keyStyle.Render(" "+action.key+" ") + labelStyle.Render(label+" ")
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

// logFilterFieldAction is the Logs pane's live filter field, rendered as
// a pane-action chip right next to the live/pause one rather than a
// separate modal (see handleOverlayKey's overlayLogFilter case, which
// applies logDraft as the active filter on every keystroke, not just on
// enter). Idle, it's a "/" hint; once open (overlay == overlayLogFilter)
// it echoes exactly what's been typed so far with a trailing caret — raw:
// true so renderPaneActionStrip doesn't lowercase the user's own input;
// once closed with something typed, it keeps showing the active filter
// text instead of falling back to the generic hint, so it's still
// visible what's currently filtering.
func (m Model) logFilterFieldAction() paneAction {
	const maxFieldWidth = 40
	switch {
	case m.overlay == overlayLogFilter:
		return paneAction{key: "/", label: short(m.logDraft, maxFieldWidth) + "|", raw: true}
	case strings.TrimSpace(m.logFilter) != "":
		return paneAction{key: "/", label: short(m.logFilter, maxFieldWidth), raw: true}
	default:
		return paneAction{key: "/", label: "filter"}
	}
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
			follow := paneAction{key: "space", label: "live"}
			if m.logFollow {
				follow = paneAction{key: "space", label: "pause"}
			}
			return []paneAction{follow, m.logFilterFieldAction()}
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
	case overlayDashboard:
		return m.dashboardOverlay(renderer)
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
		// No modal: the Logs pane's own action strip shows the live input
		// in place (see logFilterFieldAction) — editing happens right next
		// to the log lines it's filtering, not in a separate overlay.
		return nil
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
	case overlayEditScope:
		return m.editScopeOverlay(renderer)
	default:
		return nil
	}
}

// editScopeOverlay is the warn-and-offer prompt shown when "m" targets an
// individual container that's part of a multi-service project — see the
// "m" key handler and openEditWholeStackOverlay's doc comment.
func (m Model) editScopeOverlay(renderer tideui.Renderer) *tideui.Overlay {
	width := min(72, max(40, m.width-8))
	contentWidth := width - 4
	prompt := "No container selected."
	if selected := m.selectedContainer(); selected != nil {
		project := selected.Compose.Project
		count := siblingServiceCount(m.snapshot, project)
		prompt = fmt.Sprintf("Project %q has %d services. Edit just %s, or the whole stack?", project, count, selected.Compose.Service)
	}
	content := renderer.RenderSoftBody(width, strings.Join([]string{
		renderer.Styles.DetailMeta.Width(contentWidth).Render(prompt),
		"",
		renderer.RenderSoftHints(contentWidth,
			tideui.SoftHint{Key: "enter/e", Label: "this service"},
			tideui.SoftHint{Key: "s", Label: "whole stack"},
			tideui.SoftHint{Key: "esc", Label: "cancel"},
		),
	}, "\n"))
	overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "edit scope", Content: content, Width: width})
	return &overlay
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
// Once installing, the same overlay switches to a progress bar (see
// updateProgressBar) instead of the y/n hints — visible confirmation that
// something is actually happening, not just a status-bar line the user
// might not be looking at. Once installed, it switches again to a success
// message with an "enter restart" hint (see updateInstallSucceeded).
func (m Model) updateOverlay(renderer tideui.Renderer) *tideui.Overlay {
	width := min(72, max(40, m.width-8))
	contentWidth := width - 4
	title := "update available"
	var lines []string
	switch {
	case m.updateInstallSucceeded:
		title = "update installed"
		lines = []string{
			renderer.Styles.DetailMeta.Width(contentWidth).Render("Updated whatthedock to " + m.updateAvailableVersion + "."),
			"",
			renderer.RenderSoftHints(contentWidth,
				tideui.SoftHint{Key: "enter", Label: "restart"},
			),
		}
	case m.updateInstalling:
		title = "installing update"
		label := fmt.Sprintf("Installing whatthedock %s… %d%%", m.updateAvailableVersion, m.updateInstallProgress)
		lines = []string{
			renderer.Styles.DetailMeta.Width(contentWidth).Render(label),
			"",
			updateProgressBar(renderer, m.updateInstallProgress, contentWidth),
		}
	default:
		prompt := fmt.Sprintf("whatthedock %s is available (you have %s). Download and install it now?", m.updateAvailableVersion, m.appVersion)
		lines = []string{
			renderer.Styles.DetailMeta.Width(contentWidth).Render(prompt),
			"",
			renderer.RenderSoftHints(contentWidth,
				tideui.SoftHint{Key: "y", Label: "update"},
				tideui.SoftHint{Key: "n/esc", Label: "ignore"},
			),
		}
	}
	content := renderer.RenderSoftBody(width, strings.Join(lines, "\n"))
	overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: title, Content: content, Width: width})
	return &overlay
}

// updateProgressBar renders a plain linear 0-100% fill for the update
// overlay's install progress. Deliberately linear rather than the
// sqrt-scaled perceptual fill dashboardMemMeter uses — this is a literal
// percentage of a fixed-duration fake animation, not an area meter. Every
// cell sets its own background explicitly (matching dashboardMemMeter's own
// pattern), so concatenating the filled/empty spans on one line is safe —
// neither depends on an ambient background surviving the other's reset.
func updateProgressBar(renderer tideui.Renderer, pct int, width int) string {
	width = max(1, width)
	pct = clamp(pct, 0, 100)
	bg, ok := renderer.Styles.DetailMeta.GetBackground().(lipgloss.Color)
	if !ok {
		bg = renderer.Styles.Theme.Bg
	}
	filled := clamp(width*pct/100, 0, width)
	full := lipgloss.NewStyle().Background(bg).Foreground(renderer.Styles.Theme.BorderFocus).Render(strings.Repeat("█", filled))
	empty := lipgloss.NewStyle().Background(bg).Foreground(renderer.Styles.Theme.Dimmed).Render(strings.Repeat("░", width-filled))
	return full + empty
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
	case systemFieldSSHPassword:
		return "Password", m.systemPasswordFieldDisplay()
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
	"L              expand logs: shrink tree/inspector to icons, fill width",
	"/              live-filter logs as you type, hiding non-matches",
	"e / w / i / a  log errors, warnings, info, all",
	"Space          toggle live/paused log tail",
	"f / End        jump to end (live)",
	"Esc            clear active log filter",
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
	"d              dashboard: fleet summary and every container's stats",
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

// dashboardHeaderRows is the fleet summary line, divider, and column
// header row dashboardOverlay always renders above the container list —
// subtracted from softOverlayBodyBudget's allowance to get the list's own
// budget, same shape as helpBodyBudget subtracting the status legend.
const dashboardHeaderRows = 3

func (m Model) dashboardListBudget() int {
	return max(3, m.softOverlayBodyBudget()-dashboardHeaderRows)
}

// dashboardWarnPct/dashboardCritPct are the absolute utilization
// thresholds the whole Dashboard uses to decide when a CPU/memory number
// stops being "just information" and starts being worth a warning color.
// This is the fix for the redesign's core complaint: the pre-redesign
// version colored every graph via statHeatColor's *relative* gradient —
// each sample's level relative to that one container's own historical
// max — so a container quietly parked at 0.4% memory but sitting at its
// own all-time peak lit up the same hot orange/red as a container
// actually exhausting its limit. Comparing against fixed, absolute
// percentages instead means low utilization reliably looks low no matter
// how flat that container's own history happens to be.
const (
	dashboardWarnPct = 70.0
	dashboardCritPct = 90.0
)

// dashboardThresholdColor is neutral below dashboardWarnPct, amber from
// there to dashboardCritPct, and red at/above it — reusing the exact
// colors statusLegendEntries already assigns to "restarting" and
// "dead/unhealthy" so a hot CPU/memory number reads as the same kind of
// trouble as a stopped container, not a different color language.
func dashboardThresholdColor(pct float64, neutral lipgloss.Color) lipgloss.Color {
	switch {
	case pct >= dashboardCritPct:
		return "#e06c75"
	case pct >= dashboardWarnPct:
		return "#e5c07b"
	default:
		return neutral
	}
}

// dashboardStatusWords labels dashboardSummaryLine's per-status counts —
// mirrors statusLegendEntries' glyph set exactly (see its own doc
// comment) so the header never drifts out of sync with what the tree/
// inspector panes and the Problems pane already call each state.
var dashboardStatusWords = map[string]string{
	"●": "RUNNING",
	"▲": "RESTARTING",
	"○": "STOPPED",
	"✖": "DEAD",
	"!": "UNHEALTHY",
}

// dashboardRunningContainers returns every running container sorted by
// display name (dashboardOverlay's order today; Compose-project grouping
// is a natural next step once it can be designed properly — see the
// package's goal #8 notes — but the sort lives in exactly one place so
// swapping it for a grouped order later only touches this function) plus
// how many non-running containers exist, matching fleetSummary/
// statusGlyph's own running/not-running split so the Dashboard and
// Problems pane never disagree about what counts as "stopped."
func (m Model) dashboardRunningContainers() (running []domain.Container, stopped int) {
	containers := m.snapshotContainers()
	sort.Slice(containers, func(i, j int) bool {
		return containers[i].DisplayName() < containers[j].DisplayName()
	})
	for _, ctr := range containers {
		if ctr.IsRunning() {
			running = append(running, ctr)
		} else {
			stopped++
		}
	}
	return running, stopped
}

// dashboardVisiblePlan trims running down to what budget rows can show,
// reserving exactly one of those rows for a "N more" indicator the moment
// it doesn't all fit.
func dashboardVisiblePlan(running []domain.Container, budget int) (shown []domain.Container, more int) {
	if len(running) <= budget {
		return running, 0
	}
	cut := max(0, budget-1)
	return running[:cut], len(running) - cut
}

// dashboardBodyPlan is the single source of truth for which running
// containers dashboardOverlay shows, how many are hidden behind "N more",
// and which body-content line (0-based, before the blank+hints lines
// RenderSoftBody appends) ends up holding the bottom status row — shared
// by dashboardOverlay's render and dashboardHitTest's mouse mapping so
// the two can never disagree about which line is which.
func (m Model) dashboardBodyPlan() (shown []domain.Container, more, stopped, statusLineIdx, totalLines int) {
	running, stopped := m.dashboardRunningContainers()
	budget := m.dashboardListBudget()
	shown, more = dashboardVisiblePlan(running, budget)
	statusLineIdx = dashboardHeaderRows + len(shown)
	if more > 0 || len(shown) == 0 {
		statusLineIdx++
	}
	totalLines = statusLineIdx + 1
	return shown, more, stopped, statusLineIdx, totalLines
}

// dashboardOverlay renders the full fleet dashboard: a header line
// (status counts plus aggregate CPU/memory/network, see fleetSummary), a
// column header, one row per running container, and a bottom status row
// that only turns into a warning when something actually needs attention
// (see dashboardProblemsRow) — deliberately sized far wider than every
// other overlay (m.width-4 instead of the usual m.width-8-ish cap) since
// the point of this screen is showing as much of the fleet at once as the
// terminal actually allows, not a small centered dialog.
func (m Model) dashboardOverlay(renderer tideui.Renderer) *tideui.Overlay {
	width := max(70, m.width-4)
	contentWidth := width - 4
	summary := m.fleetSummary()

	lines := []string{
		dashboardPadLine(renderer, m.dashboardSummaryLine(renderer, summary, contentWidth), contentWidth),
		renderer.Styles.DetailMeta.Render(strings.Repeat("─", contentWidth)),
		dashboardPadLine(renderer, m.dashboardHeaderRow(renderer, contentWidth), contentWidth),
	}

	shown, more, stopped, _, _ := m.dashboardBodyPlan()
	cursor := clamp(m.dashboardCursor, 0, max(0, len(shown)-1))
	for i, ctr := range shown {
		selected := len(shown) > 0 && i == cursor
		lines = append(lines, dashboardPadLine(renderer, m.dashboardRow(renderer, ctr, contentWidth, selected), contentWidth))
	}
	switch {
	case more > 0:
		lines = append(lines, dashboardPadLine(renderer, renderer.Styles.DetailMeta.Render(fmt.Sprintf("▼ %d more running container(s)", more)), contentWidth))
	case len(shown) == 0:
		lines = append(lines, dashboardPadLine(renderer, renderer.Styles.DetailMeta.Render("No running containers."), contentWidth))
	}
	lines = append(lines, dashboardPadLine(renderer, m.dashboardProblemsRow(renderer, stopped, contentWidth), contentWidth))

	content := renderer.RenderSoftBody(width, strings.Join(lines, "\n")+"\n\n"+
		renderer.RenderSoftHints(contentWidth,
			tideui.SoftHint{Key: "esc/d/q", Label: "close"},
			tideui.SoftHint{Key: "↑↓/jk", Label: "select"},
			tideui.SoftHint{Key: "enter", Label: "open"},
		))
	overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "dashboard", Content: content, Width: width})
	return &overlay
}

// dashboardHitTest maps a mouse click's screen coordinates back to a
// container row index (or the bottom status row). Rather than
// hand-deriving where tideui centers the overlay's box on screen (the
// app's own topbar can wrap onto a second line depending on host name/
// project-count text length, which shifts everything below it by a row
// in a way that isn't knowable without actually measuring it), this
// re-renders the real view and locates the box's own top border by its
// title text — the exact same chrome RenderSoftPanel/RenderSoftBody (see
// that module's soft_panel.go) build around dashboardBodyPlan's lines, so
// the offsets below only need to account for what comes *after* that
// anchor, not everything above it.
func (m Model) dashboardHitTest(msg tea.MouseMsg) (rowIndex int, isStatusRow, ok bool) {
	shown, _, _, statusLineIdx, _ := m.dashboardBodyPlan()
	panelWidth := max(70, m.width-4)
	contentWidth := panelWidth - 4

	boxTop, boxLeft := -1, -1
	for y, line := range strings.Split(m.View(), "\n") {
		plain := ansi.Strip(line)
		if idx := strings.Index(plain, "whatthedock · dashboard"); idx >= 0 {
			boxTop = y
			boxLeft = strings.Index(plain, "╭")
			break
		}
	}
	if boxTop < 0 || boxLeft < 0 {
		return 0, false, false
	}
	bodyTop := boxTop + 2   // top border line + RenderSoftBody's blank top pad
	bodyLeft := boxLeft + 3 // border(1) + RenderSoftBody's own left pad(2)

	if msg.X < bodyLeft || msg.X >= bodyLeft+contentWidth {
		return 0, false, false
	}
	y := msg.Y - bodyTop
	switch {
	case y >= dashboardHeaderRows && y < dashboardHeaderRows+len(shown):
		return y - dashboardHeaderRows, false, true
	case y == statusLineIdx:
		return 0, true, true
	}
	return 0, false, false
}

// dashboardPadLine is the belt-and-suspenders fix for a bug reported live
// twice: every line dashboardOverlay builds is supposed to end up exactly
// contentWidth visible columns wide (dashboardColumnsFor's own width math
// is meant to guarantee that), but any drift between that math and what a
// row/line actually renders — even a column or two — leaves a gap on the
// right with no explicit background, which showed up as a stray,
// wrong-colored strip on a real terminal (most visibly a persistent
// vertical band down the right edge of the whole container list). Rather
// than keep chasing exact-width arithmetic across several column-budget
// functions, every line gets measured (ansi.Strip, so this works whether
// the line is pure plain text or already carries other segments' own
// embedded ANSI) and explicitly padded with the panel's own background to
// reach exactly width — so even if the width math is ever off again, the
// gap is never left unstyled.
func dashboardPadLine(renderer tideui.Renderer, line string, width int) string {
	if pad := width - len([]rune(ansi.Strip(line))); pad > 0 {
		bg := renderer.Styles.Theme.Bg
		fg := styleForeground(renderer.Styles.DetailBody, renderer.Styles.Theme.Fg)
		line += lipgloss.NewStyle().Background(bg).Foreground(fg).Render(strings.Repeat(" ", pad))
	}
	return line
}

// dashboardSummaryLine is the Dashboard's header: status counts (running
// always shown; stopped/dead/restarting/unhealthy only when non-zero, and
// colored via the exact same statusLegendEntries palette as the tree/
// Problems pane) followed by aggregate CPU/RAM/network. CPU and RAM stay
// neutral text unless the fleet-wide aggregate actually crosses
// dashboardWarnPct/dashboardCritPct (see that constant's doc comment).
// Status counts are never dropped for space — the panel floors at 70
// columns wide (66 of content), which is always enough room for them —
// but CPU/RAM/network are appended only while they still fit, in that
// priority order, so a narrow terminal loses the least-important
// information first instead of overflowing or wrapping mid-panel.
func (m Model) dashboardSummaryLine(renderer tideui.Renderer, summary dashboardSummary, width int) string {
	baseFg := styleForeground(renderer.Styles.DetailBody, renderer.Styles.Theme.Fg)
	bg := renderer.Styles.Theme.Bg
	plain := lipgloss.NewStyle().Background(bg).Foreground(baseFg)
	sep := plain.Render("   ")

	type segment struct {
		rendered string
		plain    string
	}

	var counts []segment
	for _, entry := range statusLegendEntries {
		count := summary.counts[entry.glyph]
		if entry.glyph != "●" && count == 0 {
			continue
		}
		text := fmt.Sprintf("%s %d %s", entry.glyph, count, dashboardStatusWords[entry.glyph])
		// See dashboardRow's doc comment on its own glyph: foregroundSpan
		// never sets a background, so each count+glyph pair needs its own
		// explicit outer Background wrap here too, or it falls through to
		// the terminal's default the moment the preceding separator's own
		// Render call resets — reported live as the summary counts having
		// the wrong background.
		counts = append(counts, segment{
			rendered: lipgloss.NewStyle().Background(bg).Render(foregroundSpan(text, entry.color, baseFg, false)),
			plain:    text,
		})
	}

	// totalCPU is a *sum* of each running container's own CPUPercent (see
	// fleetSummary), not a value bounded to 0-100 the way a single
	// container's CPU% or RAM's used/limit fraction is — a fleet of a
	// dozen ordinary containers can trivially sum past 70-90 without any
	// of them individually being hot. Applying dashboardThresholdColor to
	// it would flag amber/red for a perfectly healthy fleet just because
	// it has several containers, which is exactly the "alarming a normal
	// metric" failure mode this redesign is trying to avoid — so the
	// aggregate stays neutral here even though the per-row CPU number
	// (a real single-container percentage) does use the threshold.
	cpuText := fmt.Sprintf("CPU %.1f%%", summary.totalCPU)
	cpuSeg := segment{rendered: plain.Render(cpuText), plain: cpuText}

	ramColor := baseFg
	if summary.memLimit > 0 {
		ramColor = dashboardThresholdColor(float64(summary.memUsed)/float64(summary.memLimit)*100, baseFg)
	}
	ramText := "RAM " + formatMemFraction(summary.memUsed, summary.memLimit)
	ramSeg := segment{rendered: lipgloss.NewStyle().Background(bg).Foreground(ramColor).Render(ramText), plain: ramText}

	netText := fmt.Sprintf("↓%s ↑%s", formatCompactBytes(summary.netRxRate), formatCompactBytes(summary.netTxRate))
	netSeg := segment{rendered: plain.Render(netText), plain: netText}

	var parts []string
	lineWidth := 0
	for _, s := range counts {
		if lineWidth > 0 {
			parts = append(parts, sep)
			lineWidth += 3
		}
		parts = append(parts, s.rendered)
		lineWidth += lipgloss.Width(s.plain)
	}
	for _, s := range []segment{cpuSeg, ramSeg, netSeg} {
		add := lipgloss.Width(s.plain) + 3
		if lineWidth+add > width {
			continue
		}
		parts = append(parts, sep, s.rendered)
		lineWidth += add
	}
	return ansi.Truncate(strings.Join(parts, ""), width, "")
}

func formatMemFraction(used, limit uint64) string {
	if limit == 0 {
		return formatBytes(used)
	}
	return formatBytes(used) + " / " + formatBytes(limit)
}

// dashboardTier picks how much visual detail a container row can afford:
// narrow terminals get bare numbers, medium adds a CPU sparkline and a
// memory meter, wide splits network into separate down/up columns with
// their own trends. The breakpoints were chosen (and tested) against
// roughly the 90/110/140/180-column terminals this redesign's test matrix
// calls out.
type dashboardTier int

const (
	dashboardTierNarrow dashboardTier = iota
	dashboardTierMedium
	dashboardTierWide
)

const (
	dashboardNarrowBreak = 90
	dashboardMediumBreak = 130
)

func dashboardTierFor(width int) dashboardTier {
	switch {
	case width < dashboardNarrowBreak:
		return dashboardTierNarrow
	case width < dashboardMediumBreak:
		return dashboardTierMedium
	default:
		return dashboardTierWide
	}
}

// dashboardColumns computes the widths dashboardHeaderRow and dashboardRow
// both share, so header labels can never drift out of alignment with the
// numbers/graphics they label (see TestDashboardHeaderRowAlignsWithRowNumbers).
// A zero-value *Width field means "that visualization is dropped, number
// only" — dashboardTierNarrow zeroes all three; medium adds cpuSparkW/
// memMeterW; wide adds netSparkW and splits net into two columns.
type dashboardColumns struct {
	tier        dashboardTier
	nameWidth   int
	pctWidth    int
	cpuSparkW   int
	memMeterW   int
	netColWidth int // combined "↓X ↑Y" width, used when !splitNet
	netSparkW   int // per-direction trend width, used when splitNet
	splitNet    bool
}

const (
	dashboardRailWidth   = 2 // selection marker (tideui.Renderer.SoftRail)
	dashboardPctWidth    = 6 // "100.0%"
	dashboardMeterWidth  = 10
	dashboardNetNumWidth = 6 // e.g. "46.1K", "348B" — no "/s", the ↓/↑ glyph already implies "rate"
	dashboardMinSparkW   = 6
	dashboardColGap      = 2
)

// dashboardNarrowBaseline/dashboardMediumBaseline/dashboardWideBaseline
// are each tier's fixed (non-sparkline) row width, hand-derived to match
// dashboardRow's actual construction exactly — see that function's own
// doc comment for the full column-by-column accounting. Any drift between
// these constants and dashboardRow's real layout can only make a row
// *narrower* than its budget (dashboardPadLine then pads the gap), never
// wider — dashboardRow's own trailing ansi.Truncate is the hard backstop
// against ever exceeding width — and TestDashboardRowNeverExceedsWidth
// checks the real, rendered output directly rather than trusting this
// arithmetic in isolation.
const (
	dashboardNarrowBaseline = dashboardPctWidth + dashboardColGap + dashboardPctWidth + dashboardColGap
	dashboardMediumBaseline = dashboardPctWidth + dashboardColGap + dashboardColGap + dashboardPctWidth + dashboardColGap + dashboardMeterWidth + dashboardColGap + (2*(1+dashboardNetNumWidth) + 1)
	dashboardWideBaseline   = dashboardPctWidth + dashboardColGap + dashboardColGap + dashboardPctWidth + dashboardColGap + dashboardMeterWidth + dashboardColGap + 2*(1+1+dashboardNetNumWidth) + 1 + 3 + 1
)

// dashboardColumnsFor's fixed prefix must equal exactly what dashboardRow
// spends before its three metric columns start — the selection rail, the
// name column, a gap, the glyph itself, and a gap. A mismatch here
// previously left every row a few columns short of the panel's actual
// width; combined with dashboardPadLine (the belt-and-suspenders fix for
// that class of bug generally) this keeps the columns as wide as the
// terminal actually allows instead of leaving width on the table.
func (m Model) dashboardColumnsFor(width int) dashboardColumns {
	tier := dashboardTierFor(width)
	nameWidth := 22
	fixed := dashboardRailWidth + nameWidth + dashboardColGap + 1 /*glyph*/ + dashboardColGap
	remaining := max(0, width-fixed)

	cols := dashboardColumns{tier: tier, nameWidth: nameWidth, pctWidth: dashboardPctWidth}
	switch tier {
	case dashboardTierNarrow:
		cols.netColWidth = max(8, remaining-dashboardNarrowBaseline)
	case dashboardTierMedium:
		cols.memMeterW = dashboardMeterWidth
		cols.netColWidth = 2*(1+dashboardNetNumWidth) + 1
		if spark := remaining - dashboardMediumBaseline; spark >= dashboardMinSparkW {
			cols.cpuSparkW = min(spark, 16)
		}
	case dashboardTierWide:
		cols.memMeterW = dashboardMeterWidth
		cols.splitNet = true
		if leftover := max(0, remaining-dashboardWideBaseline); leftover/3 >= dashboardMinSparkW {
			spark := min(leftover/3, 24)
			cols.cpuSparkW = spark
			cols.netSparkW = spark
		}
	}
	return cols
}

func padRunes(s string, n int) string {
	r := []rune(s)
	if len(r) >= n {
		return string(r[:n])
	}
	return s + strings.Repeat(" ", n-len(r))
}

func rightAlignRunes(s string, n int) string {
	r := []rune(s)
	if len(r) >= n {
		return string(r[:n])
	}
	return strings.Repeat(" ", n-len(r)) + s
}

// truncateEllipsis shortens name to at most width runes, replacing the
// last one with "…" once it doesn't fit — short() elsewhere in this file
// truncates silently, which is fine for the compact IDs/hashes it's used
// for, but a container name cut off mid-word with no indicator reads as a
// rendering bug rather than "there's more here."
func truncateEllipsis(name string, width int) string {
	r := []rune(name)
	if len(r) <= width {
		return name
	}
	if width <= 1 {
		if width <= 0 {
			return ""
		}
		return string(r[:width])
	}
	return string(r[:width-1]) + "…"
}

// dashboardHeaderRow builds "CPU"/"MEM"/"NET" labels positioned to match
// dashboardRow's real column layout exactly: CPU/MEM are right-aligned
// within just pctWidth (where dashboardRow right-aligns its own numbers
// too), leaving any sparkline/meter portion of that column blank —
// labeling the number's own slot, not the whole column. NET is a group
// label over the whole network area (one or two sub-columns) rather than
// per-direction, matching the "Network" column header in the redesign's
// own visual target.
func (m Model) dashboardHeaderRow(renderer tideui.Renderer, width int) string {
	cols := m.dashboardColumnsFor(width)
	bg := renderer.Styles.Theme.Bg
	style := renderer.Styles.DetailMeta.Copy().Background(bg)

	prefix := strings.Repeat(" ", dashboardRailWidth+cols.nameWidth+dashboardColGap+1+dashboardColGap)
	cpu := rightAlignRunes("CPU", cols.pctWidth)
	if cols.cpuSparkW > 0 {
		cpu += strings.Repeat(" ", dashboardColGap+cols.cpuSparkW)
	}
	mem := rightAlignRunes("MEM", cols.pctWidth)
	if cols.memMeterW > 0 {
		mem += strings.Repeat(" ", dashboardColGap+cols.memMeterW)
	}
	netWidth := cols.netColWidth
	if cols.splitNet {
		netWidth = 2*(2+dashboardNetNumWidth+1+cols.netSparkW) + 3
	}
	net := padRunes("NET", netWidth)

	header := prefix + cpu + strings.Repeat(" ", dashboardColGap) + mem + strings.Repeat(" ", dashboardColGap) + net
	return ansi.Truncate(style.Render(header), width, "")
}

// dashboardRow renders one running container's line: a selection rail,
// name (ellipsis-truncated if it doesn't fit), state glyph, then CPU/Mem/
// Net built from the same m.statsHistory the single-container Stats pane
// already populates — Dashboard's own polling loop (dashboardRefreshCmd)
// just makes sure history exists for every running container, not only
// the selected one.
//
// CPU and memory deliberately share the same "number, then a small
// visualization" shape (a sparkline for CPU, a meter for memory) instead
// of the old mismatched "near-empty line vs. huge heatmap" look, and
// every color here comes from dashboardThresholdColor's absolute
// percentage cutoffs rather than any per-container relative scale — see
// dashboardWarnPct's doc comment for why that distinction is the point of
// this redesign. Column layout: rail(2) name(nameWidth) gap(2) glyph(1)
// gap(2) [cpuNum(pctWidth) [gap(2) cpuSpark]] gap(2) [memNum(pctWidth)
// [gap(2) memMeter]] gap(2) net — see dashboardNarrowBaseline/
// dashboardMediumBaseline/dashboardWideBaseline for the exact per-tier
// accounting that must stay in sync with this.
//
// Every plain-text segment is rendered through plain (an explicit
// Background+Foreground style), and the whole row is returned with NO
// further Width()-based wrapping applied on top — calling an outer
// Style.Width().Render() on a string that already contains other
// segments' own embedded ANSI resets (glyph, sparklines, the meter)
// mis-measures/reflows it, leaving stretches of the row on whatever
// background the terminal happens to default to instead of the theme's
// own — this was reported live as random-colored boxes in the Mem/Net
// columns on a real terminal. ansi.Truncate at the end is a hard backstop
// against ever exceeding width, independent of the column math above.
func (m Model) dashboardRow(renderer tideui.Renderer, ctr domain.Container, width int, selected bool) string {
	cols := m.dashboardColumnsFor(width)
	history := m.statsHistory[ctr.ID]
	stats := history.lastStats

	rowBg := renderer.Styles.Theme.Bg
	if selected {
		if c, ok := renderer.Styles.ItemSelected.GetBackground().(lipgloss.Color); ok {
			rowBg = c
		}
	}
	baseFg := styleForeground(renderer.Styles.DetailBody, renderer.Styles.Theme.Fg)
	plain := lipgloss.NewStyle().Background(rowBg).Foreground(baseFg)

	rail := renderer.SoftRail(selected, rowBg)
	// foregroundSpan only ever sets foreground (see its own doc comment) —
	// it's meant to sit inside one continuous outer-styled Render call,
	// not standalone between independently-`.Render()`ed segments the way
	// this row composes everything. Each of those independent Render
	// calls emits its own trailing reset, so without this explicit outer
	// Background wrap the glyph's cell falls through to the terminal's
	// own default background the moment the segment before it resets —
	// reported live as the status glyphs having the wrong background
	// color.
	glyph := lipgloss.NewStyle().Background(rowBg).Render(foregroundSpan(statusGlyph(ctr), inspectorStatusColor(ctr), baseFg, false))
	name := plain.Render(padRunes(truncateEllipsis(ctr.DisplayName(), cols.nameWidth), cols.nameWidth))

	cpuPct := statsCPU(stats)
	cpuNum := plain.Render(rightAlignRunes(fmt.Sprintf("%.1f%%", cpuPct), cols.pctWidth))
	cpuPart := cpuNum
	if cols.cpuSparkW > 0 {
		color := dashboardThresholdColor(cpuPct, "#7dcfff")
		cpuPart += plain.Render("  ") + dashboardSpark(renderer, m.settings, cpuStatGraph(stats, history), color, cols.cpuSparkW, rowBg)
	}

	memPct, hasLimit, memText := 0.0, false, "n/a"
	switch {
	case stats != nil && stats.MemoryLimit > 0:
		memPct = float64(stats.MemoryUsage) / float64(stats.MemoryLimit) * 100
		hasLimit = true
		memText = fmt.Sprintf("%.1f%%", memPct)
	case stats != nil:
		memText = formatCompactBytes(stats.MemoryUsage)
	}
	memPart := plain.Render(rightAlignRunes(memText, cols.pctWidth))
	if cols.memMeterW > 0 {
		memPart += plain.Render("  ") + dashboardMemMeter(renderer, m.settings, memPct, hasLimit, cols.memMeterW, rowBg)
	}

	var rx, tx uint64
	if len(history.NetworkRx) > 0 {
		rx = history.NetworkRx[len(history.NetworkRx)-1]
	}
	if len(history.NetworkTx) > 0 {
		tx = history.NetworkTx[len(history.NetworkTx)-1]
	}

	var netPart string
	if cols.splitNet {
		rxGraph := uintStatGraph(history.NetworkRx, history.maxNetwork, byteLevel(rx), formatByteDelta)
		txGraph := uintStatGraph(history.NetworkTx, history.maxNetwork, byteLevel(tx), formatByteDelta)
		down := plain.Render("↓ " + rightAlignRunes(formatCompactBytes(rx), dashboardNetNumWidth))
		up := plain.Render("↑ " + rightAlignRunes(formatCompactBytes(tx), dashboardNetNumWidth))
		netPart = down + plain.Render(" ") + dashboardNetSpark(renderer, m.settings, rxGraph, cols.netSparkW, rowBg) +
			plain.Render("   ") + up + plain.Render(" ") + dashboardNetSpark(renderer, m.settings, txGraph, cols.netSparkW, rowBg)
	} else {
		combined := fmt.Sprintf("↓%s ↑%s", formatCompactBytes(rx), formatCompactBytes(tx))
		netPart = plain.Render(padRunes(combined, cols.netColWidth))
	}

	row := rail + name + plain.Render("  ") + glyph + plain.Render("  ") +
		cpuPart + plain.Render("  ") + memPart + plain.Render("  ") + netPart
	return ansi.Truncate(row, width, "")
}

// dashboardGaugeBar mirrors renderGaugeBar's graphStyleGauge look — a
// single thin proportional bar: "━" filled, one "╸" leading-edge cap at
// the fill boundary, "─" for the remainder — so picking that Graph style
// in Settings gives the Dashboard the same shape it gives the
// single-container Stats pane. Unlike renderGaugeBar, color is a plain
// caller-supplied value rather than being derived from
// settings.GraphColor/statHeatColor's relative gradient: see
// dashboardWarnPct's doc comment for why the Dashboard's colors stay on
// the absolute-threshold scale regardless of that setting.
func dashboardGaugeBar(renderer tideui.Renderer, frac float64, color lipgloss.Color, width int, bg lipgloss.Color) string {
	width = max(1, width)
	if frac < 0 {
		frac = 0
	} else if frac > 1 {
		frac = 1
	}
	filled := clamp(int(frac*float64(width)+0.5), 0, width)
	dim := renderer.Styles.Theme.Dimmed
	switch {
	case filled <= 0:
		return lipgloss.NewStyle().Background(bg).Foreground(dim).Render(strings.Repeat("─", width))
	case filled >= width:
		return lipgloss.NewStyle().Background(bg).Foreground(color).Bold(true).Render(strings.Repeat("━", width))
	default:
		fill := lipgloss.NewStyle().Background(bg).Foreground(color).Bold(true).Render(strings.Repeat("━", filled-1) + "╸")
		rest := lipgloss.NewStyle().Background(bg).Foreground(dim).Render(strings.Repeat("─", width-filled))
		return fill + rest
	}
}

// dashboardMemMeter draws the memory column's visualization. With
// settings.GraphStyle == graphStyleGauge it's a single proportional
// dashboardGaugeBar (matching what that style does everywhere else in the
// app); otherwise it's a restrained "█"/"░" meter filled proportional to
// sqrt(pct/100) rather than pct/100 directly — real container memory
// usage is very often single-digit percentages, and a linear 0-100% meter
// would render nearly every healthy container as entirely empty,
// indistinguishable from 0% and useless for at-a-glance comparison across
// a fleet. The square-root curve keeps low values visually distinct (0.4%
// and 2.7% land on 1 and 2 filled cells of 10 respectively, matching this
// redesign's own worked examples) while still saturating sensibly as
// usage climbs toward its limit. Either shape colors via
// dashboardThresholdColor, never settings.GraphColor's relative gradient
// (see dashboardWarnPct's doc comment). hasLimit false (no memory limit
// reported) falls back to a dim dashed line — the same "unknown, not
// necessarily bad" treatment dashboardNetSpark gives zero traffic —
// rather than guessing a color.
func dashboardMemMeter(renderer tideui.Renderer, settings appSettings, pct float64, hasLimit bool, width int, bg lipgloss.Color) string {
	width = max(0, width)
	if width == 0 {
		return ""
	}
	dim := renderer.Styles.Theme.Dimmed
	if !hasLimit {
		return lipgloss.NewStyle().Background(bg).Foreground(dim).Render(strings.Repeat("─", width))
	}
	color := dashboardThresholdColor(pct, "#80c990")
	if settings.GraphStyle == graphStyleGauge {
		return dashboardGaugeBar(renderer, pct/100, color, width, bg)
	}
	frac := pct / 100
	if frac < 0 {
		frac = 0
	} else if frac > 1 {
		frac = 1
	}
	filled := clamp(int(math.Sqrt(frac)*float64(width)+0.5), 0, width)
	full := lipgloss.NewStyle().Background(bg).Foreground(color).Render(strings.Repeat("█", filled))
	empty := lipgloss.NewStyle().Background(bg).Foreground(dim).Render(strings.Repeat("░", width-filled))
	return full + empty
}

// dashboardSpark renders graph's values as a compact, single-color
// sparkline using the caller's Graph style (settings.GraphStyle) for
// glyph shape/spacing — graphGlyphs/graphGlyphSpacing are the exact same
// functions the single-container Stats pane's own sparkline uses, so
// Blocks/Braille/Bars/(the default) Wave all look the same style the user
// picked in Settings. graphStyleGauge instead collapses to a single
// dashboardGaugeBar for the latest value, matching that style's meaning
// everywhere else in the app.
//
// What deliberately does NOT carry over from the shared renderSparkline
// path is its coloring: statHeatColor scales each glyph's color by that
// sample's level *relative to the container's own historical max* — a
// container sitting near its own usual plateau glows the same hot orange/
// red as one actually maxed out, regardless of how small that plateau is
// in absolute terms. Every glyph here gets the one caller-supplied color
// instead (see dashboardWarnPct's doc comment for why), so only the glyph
// shapes vary with the data — activity reads as movement, not a color
// show — and settings.GraphColor is never consulted.
func dashboardSpark(renderer tideui.Renderer, settings appSettings, graph statGraph, color lipgloss.Color, width int, bg lipgloss.Color) string {
	width = max(1, width)
	if settings.GraphStyle == graphStyleGauge {
		value, maxValue := 0.0, graph.maxValue
		if len(graph.values) > 0 {
			value = graph.values[len(graph.values)-1]
		}
		if maxValue <= 0 {
			maxValue = 1
		}
		return dashboardGaugeBar(renderer, value/maxValue, color, width, bg)
	}

	glyphs := graphGlyphs(settings)
	spacing := graphGlyphSpacing(settings)
	unit := 1 + spacing
	slots := max(1, width/unit)
	gap := ""
	if spacing > 0 {
		gap = lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", spacing))
	}
	flatCell := lipgloss.NewStyle().Background(bg).Foreground(renderer.Styles.Theme.Dimmed).Render(glyphs[0]) + gap

	if len(graph.values) == 0 || graph.maxValue <= 0 {
		return strings.Repeat(flatCell, slots)
	}
	values := graph.values
	if len(values) > slots {
		values = values[len(values)-slots:]
	}
	var b strings.Builder
	for _, v := range values {
		level := clamp(int(v/graph.maxValue*float64(len(glyphs)-1)+0.5), 0, len(glyphs)-1)
		b.WriteString(lipgloss.NewStyle().Background(bg).Foreground(color).Render(glyphs[level]) + gap)
	}
	if pad := slots - len(values); pad > 0 {
		b.WriteString(strings.Repeat(flatCell, pad))
	}
	return b.String()
}

// dashboardNetSpark is dashboardSpark for network traffic specifically:
// when the latest sample is zero (or there's no history yet), it renders
// a flat dashed line instead of the style's own lowest glyph — "zero
// traffic" and "very low but nonzero traffic" need to look different, and
// a dim dash reads unambiguously as "quiet" rather than "chart with tiny
// bars," regardless of which Graph style is selected (this also happens
// to be exactly graphStyleGauge's own empty-bar look, so gauge style
// needs no special-casing here at all).
func dashboardNetSpark(renderer tideui.Renderer, settings appSettings, graph statGraph, width int, bg lipgloss.Color) string {
	width = max(1, width)
	quiet := len(graph.values) == 0 || graph.maxValue <= 0 || graph.values[len(graph.values)-1] == 0
	if quiet {
		return lipgloss.NewStyle().Background(bg).Foreground(renderer.Styles.Theme.Dimmed).Render(strings.Repeat("─", width))
	}
	return dashboardSpark(renderer, settings, graph, "#8aadf4", width, bg)
}

// dashboardProblemsRow is the Dashboard's bottom status/action row: a
// warning naming how many containers need attention plus a clickable "p
// View problems" hint when any exist (replacing the old easy-to-miss
// inline "+N stopped/dead" text), or a quiet all-clear line when the
// fleet is healthy — deliberately muted even then, so a healthy fleet
// doesn't compete for attention with the rows above it.
func (m Model) dashboardProblemsRow(renderer tideui.Renderer, stopped int, width int) string {
	bg := renderer.Styles.Theme.Bg
	if stopped == 0 {
		return lipgloss.NewStyle().Background(bg).Foreground(renderer.Styles.Theme.Dimmed).Render("✓ All monitored containers healthy")
	}
	noun, verb := "containers", "need"
	if stopped == 1 {
		noun, verb = "container", "needs"
	}
	left := lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color("#e5c07b")).Bold(true).
		Render(fmt.Sprintf("⚠ %d %s %s attention", stopped, noun, verb))
	action := lipgloss.NewStyle().Background(bg).Foreground(renderer.Styles.Theme.Fg).Bold(true).Render("p") +
		lipgloss.NewStyle().Background(bg).Foreground(renderer.Styles.Theme.Dimmed).Render("  View problems")
	gap := max(1, width-lipgloss.Width(left)-lipgloss.Width(action))
	line := left + lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", gap)) + action
	return ansi.Truncate(line, width, "")
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

// aboutShipStatusText is the version line a swimmer writes into their wake
// once the ship animation settles (see overlayAboutSwimmerCells in
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

// formatCompactBytes is formatBytes without the space and with single-
// letter units ("14.0M" not "14.0 MiB") — for the Dashboard's per-row
// columns, where dashboardNumWidth is sized for this shorter form, not
// formatBytes' own (that's still used for the wider fleet-summary line
// and the single-container Stats pane, where the extra width is fine).
func formatCompactBytes(value uint64) string {
	units := []string{"B", "K", "M", "G", "T"}
	size := float64(value)
	unit := 0
	for size >= 1024 && unit < len(units)-1 {
		size /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d%s", value, units[unit])
	}
	return fmt.Sprintf("%.1f%s", size, units[unit])
}

func formatRateCompact(value uint64) string {
	return formatCompactBytes(value) + "/s"
}

func short(value string, n int) string {
	if len(value) <= n {
		return value
	}
	return value[:n]
}

// columnRatios is the single source of truth for the 3-column layout's
// proportions — used both by View()'s tideui.Layout{ColumnRatios: ...}
// and by leftPaneWidth/centerPaneWidth/rightPaneWidth below, so mouse
// hit-testing and the Tree pane's content-wrap width can never drift out
// of sync with what's actually rendered. They used to each hardcode
// their own copy of the same {3,5,4} fractions independently; expanding
// logs needed a second set of proportions, and duplicating the ratio a
// third time was exactly the kind of drift this collapses instead.
func (m Model) columnRatios() [3]float64 {
	if m.logsExpanded {
		return [3]float64{1, 18, 1} // Tree/Inspector reduced to icon-only slivers
	}
	return [3]float64{3, 5, 4}
}

func (m Model) leftPaneWidth() int {
	r := m.columnRatios()
	return max(1, int(float64(m.width)*r[0]/(r[0]+r[1]+r[2])))
}

func (m Model) centerPaneWidth() int {
	r := m.columnRatios()
	return max(1, int(float64(m.width)*r[1]/(r[0]+r[1]+r[2])))
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
