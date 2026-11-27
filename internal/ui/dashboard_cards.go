package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/allisonhere/tideui"

	"github.com/allisonhere/whatthedock/internal/domain"
)

// ── Layout mode ──────────────────────────────────────────────────────────

const (
	// dashboardCardsMinWidth/dashboardCardsMinBudget are the smallest
	// content width and overlay body budget at which the Dashboard swaps its
	// one-line summary for the four stat cards. Below either, the compact
	// summary/gauge/ribbon header is used instead, so a small terminal keeps
	// its list rows.
	dashboardCardsMinWidth  = 92
	dashboardCardsMinBudget = 16
	dashboardCardRows       = 4
	dashboardCardGap        = 1
)

// dashboardUsesCards reports whether the Dashboard currently renders its
// header as stat cards (see dashboardCards) rather than the compact trio of
// summary line, gauge row and mood ribbon.
func (m Model) dashboardUsesCards() bool {
	return dashboardPanelWidth(m.width)-4 >= dashboardCardsMinWidth && m.softOverlayBodyBudget() >= dashboardCardsMinBudget
}

// dashboardHeaderLineCount is the number of lines dashboardOverlay renders
// above the container list: the cards (or the compact summary, gauge row and
// ribbon) plus the column header row. It must equal what is actually
// rendered — dashboardBodyPlan and dashboardHitTest use it to map list rows
// to screen lines.
func (m Model) dashboardHeaderLineCount() int {
	if m.dashboardUsesCards() {
		return dashboardCardRows + 1
	}
	return 4
}

// ── Stat cards ───────────────────────────────────────────────────────────

// dashboardCards renders the four header cards — CPU, MEM, NET, FLEET — as
// dashboardCardRows lines, each exactly width columns wide. Each card's
// border is tinted by how hot its metric is (the same absolute green→red
// scales the rows use), and every border lifts briefly toward white after a
// poll lands (age counts pulse frames since it kicked off), so a refresh
// reads as a heartbeat.
func (m Model) dashboardCards(renderer tideui.Renderer, summary dashboardSummary, attention []attentionItem, age, width int) []string {
	bg := renderer.Styles.Theme.Bg
	baseFg := styleForeground(renderer.Styles.DetailBody, renderer.Styles.Theme.Fg)
	dim := lipgloss.NewStyle().Background(bg).Foreground(renderer.Styles.Theme.Dimmed)
	bold := func(color lipgloss.Color) lipgloss.Style {
		return lipgloss.NewStyle().Background(bg).Foreground(color).Bold(true)
	}

	cpuPct := summary.totalCPU
	if summary.cpuCores > 0 {
		cpuPct = summary.totalCPU / summary.cpuCores
	}
	memPct := 0.0
	if summary.memLimit > 0 {
		memPct = float64(summary.memUsed) / float64(summary.memLimit) * 100
	}
	netRate := summary.netRxRate + summary.netTxRate

	fleetTint := activePalette.OK
	for _, item := range attention {
		if item.rank <= 1 {
			fleetTint = activePalette.Bad
			break
		}
		fleetTint = activePalette.Warn
	}

	specs := []struct {
		title string
		tint  lipgloss.Color
		body  func(cw int) (string, string)
	}{
		{"CPU", dashboardGraphColor(cpuPct), func(cw int) (string, string) {
			line1 := bold(dashboardGraphColor(cpuPct)).Render(fmt.Sprintf("%.1f%%", cpuPct))
			if summary.cpuCores > 0 {
				line1 += dim.Render(fmt.Sprintf(" of %.0f cores", summary.cpuCores))
			}
			return line1, renderCPUGauge(renderer, statGraph{values: []float64{summary.totalCPU}}, summary.cpuCores*100, cw, bg)
		}},
		{"MEM", dashboardMemColor(memPct), func(cw int) (string, string) {
			if summary.memLimit == 0 {
				return dim.Render("n/a"), renderCPUGauge(renderer, statGraph{}, 0, cw, bg)
			}
			line1 := bold(dashboardMemColor(memPct)).Render(formatBytes(summary.memUsed)) +
				dim.Render(" / "+formatBytes(summary.memLimit)+fmt.Sprintf("  %.0f%%", memPct))
			return line1, renderCPUGauge(renderer, statGraph{values: []float64{float64(summary.memUsed)}}, float64(summary.memLimit), cw, bg)
		}},
		{"NET", dashboardNetGraphColor(netRate), func(cw int) (string, string) {
			line1 := lipgloss.NewStyle().Background(bg).Foreground(dashboardNetGraphColor(summary.netRxRate)).Render("↓"+formatCompactBytes(summary.netRxRate)) +
				lipgloss.NewStyle().Background(bg).Render(" ") +
				lipgloss.NewStyle().Background(bg).Foreground(dashboardNetGraphColor(summary.netTxRate)).Render("↑"+formatCompactBytes(summary.netTxRate))
			return line1, m.fleetNetSpark(renderer, cw, bg)
		}},
		{"FLEET", fleetTint, func(cw int) (string, string) {
			var parts []string
			for _, entry := range statusLegendEntries() {
				count := summary.counts[entry.glyph]
				if entry.glyph != "●" && count == 0 {
					continue
				}
				parts = append(parts, lipgloss.NewStyle().Background(bg).Foreground(entry.color).Render(fmt.Sprintf("%s %d", entry.glyph, count)))
			}
			sep := lipgloss.NewStyle().Background(bg).Foreground(baseFg).Render("  ")
			return strings.Join(parts, sep), dashboardStatusBar(renderer, summary, cw, bg)
		}},
	}

	const n = 4
	avail := width - (n-1)*dashboardCardGap
	base, extra := avail/n, avail%n
	flash := 0.0
	if age >= 0 && age < dashboardMoodFlashFrames {
		flash = (1 - float64(age)/float64(dashboardMoodFlashFrames)) * 0.4
	}

	rendered := make([][]string, n)
	for i, spec := range specs {
		w := base
		if i < extra {
			w++
		}
		line1, line2 := spec.body(max(1, w-4))
		rendered[i] = renderDashboardCard(renderer, spec.title, spec.tint, line1, line2, w, flash)
	}
	gap := lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", dashboardCardGap))
	lines := make([]string, dashboardCardRows)
	for row := range lines {
		parts := make([]string, n)
		for i := range rendered {
			parts[i] = rendered[i][row]
		}
		lines[row] = strings.Join(parts, gap)
	}
	return lines
}

// renderDashboardCard draws one rounded card exactly width columns wide:
// a top border carrying title, two content lines (each truncated, then
// padded with the panel background, to the card's inner width), and a
// bottom border. The border is the metric's tint blended toward the muted
// text color, plus flash toward white for the post-refresh pulse.
func renderDashboardCard(renderer tideui.Renderer, title string, tint lipgloss.Color, line1, line2 string, width int, flash float64) []string {
	bg := renderer.Styles.Theme.Bg
	border := lerpHexColor(string(renderer.Styles.Theme.Dimmed), string(tint), 0.55)
	border = lerpHexColor(border, "#ffffff", flash)
	bs := lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color(border))
	titleStyle := lipgloss.NewStyle().Background(bg).Foreground(tint).Bold(true)
	spaces := func(n int) string { return lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", max(0, n))) }

	fill := max(0, width-lipgloss.Width(title)-5)
	top := bs.Render("╭─ ") + titleStyle.Render(title) + bs.Render(" "+strings.Repeat("─", fill)+"╮")
	row := func(content string) string {
		inner := ansi.Truncate(content, width-4, "")
		return bs.Render("│") + spaces(1) + inner + spaces(width-4-lipgloss.Width(inner)+1) + bs.Render("│")
	}
	bottom := bs.Render("╰" + strings.Repeat("─", max(0, width-2)) + "╯")
	return []string{top, row(line1), row(line2), bottom}
}

// dashboardStatusBar draws the FLEET card's second line: one bar split into
// a segment per container status (running, restarting, stopped, dead,
// unhealthy), each in the colour the rest of the app uses for that status
// and sized by its share of the fleet. Every status that exists gets at
// least one cell, so a lone unhealthy container among fifty is still
// visible.
func dashboardStatusBar(renderer tideui.Renderer, summary dashboardSummary, width int, bg lipgloss.Color) string {
	width = max(1, width)
	counts := make([]int, len(statusLegendEntries()))
	total, present := 0, 0
	for i, entry := range statusLegendEntries() {
		counts[i] = summary.counts[entry.glyph]
		total += counts[i]
		if counts[i] > 0 {
			present++
		}
	}
	if total == 0 || present > width {
		return lipgloss.NewStyle().Background(bg).Foreground(renderer.Styles.Theme.Dimmed).Render(strings.Repeat("─", width))
	}
	cells := make([]int, len(counts))
	assigned, largest := 0, 0
	for i, count := range counts {
		if count == 0 {
			continue
		}
		cells[i] = max(1, count*width/total)
		assigned += cells[i]
		if count > counts[largest] {
			largest = i
		}
	}
	// Rounding (and the one-cell floor) leaves the widths off by a few
	// cells; the largest segment absorbs the difference.
	cells[largest] = max(1, cells[largest]+width-assigned)

	var b strings.Builder
	for i, entry := range statusLegendEntries() {
		if cells[i] > 0 {
			b.WriteString(lipgloss.NewStyle().Background(bg).Foreground(entry.color).Bold(true).Render(strings.Repeat("━", cells[i])))
		}
	}
	return b.String()
}

// fleetNetSpark is the fleet-wide network sparkline, shared by the compact
// gauge row and the NET card.
func (m Model) fleetNetSpark(renderer tideui.Renderer, width int, bg lipgloss.Color) string {
	var netMax, latestNet uint64
	for _, v := range m.fleetNetHistory {
		netMax = maxUint(netMax, v)
	}
	if len(m.fleetNetHistory) > 0 {
		latestNet = m.fleetNetHistory[len(m.fleetNetHistory)-1]
	}
	return dashboardNetSpark(renderer, m.settings, uintStatGraph(m.fleetNetHistory, netMax, byteLevel(latestNet), formatByteDelta, formatBytes), width, bg)
}

// ── Grouping by Compose stack ────────────────────────────────────────────

// dashboardGroupOf is the stack a container belongs to for grouping: its
// Compose project, or "" for a standalone container.
func dashboardGroupOf(ctr domain.Container) string {
	return ctr.Compose.Project
}

// dashboardGroupLess orders stacks alphabetically with standalone
// containers ("") last.
func dashboardGroupLess(a, b string) bool {
	if a == b {
		return false
	}
	if a == "" {
		return false
	}
	if b == "" {
		return true
	}
	return a < b
}

// dashboardLine is one line of the Dashboard's list: either a container row
// (index is its position in the shown slice) or, when grouping is on, the
// header that introduces a stack (index is -1, group names the stack).
type dashboardLine struct {
	index int
	group string
}

func (l dashboardLine) isHeader() bool { return l.index < 0 }

// dashboardListLines expands shown (already in display order) into the
// lines that render it — one per container, plus a header line wherever the
// stack changes when grouping is on. It is the single mapping between
// containers and screen lines, shared by the renderer, the budget planner
// and the mouse hit test.
func (m Model) dashboardListLines(shown []domain.Container) []dashboardLine {
	lines := make([]dashboardLine, 0, len(shown)+4)
	for i, ctr := range shown {
		if m.dashboardGrouped && (i == 0 || dashboardGroupOf(shown[i-1]) != dashboardGroupOf(ctr)) {
			lines = append(lines, dashboardLine{index: -1, group: dashboardGroupOf(ctr)})
		}
		lines = append(lines, dashboardLine{index: i})
	}
	return lines
}

// dashboardGroupRow renders a stack's header line: its name, a rule, and a
// one-line rollup (running/stopped counts, summed CPU and memory, and how
// many of its containers need attention). The rollup counts every container
// in the stack, not just the visible ones.
func (m Model) dashboardGroupRow(renderer tideui.Renderer, group string, width int) string {
	bg := renderer.Styles.Theme.Bg
	accent := renderer.Styles.Theme.Selected
	if accent == "" {
		accent = renderer.Styles.Theme.BorderFocus
	}
	dim := lipgloss.NewStyle().Background(bg).Foreground(renderer.Styles.Theme.Dimmed)

	name := group
	if name == "" {
		name = "standalone"
	}
	running, stopped, attention := 0, 0, 0
	var cpu float64
	var mem uint64
	for _, ctr := range m.snapshotContainers() {
		if dashboardGroupOf(ctr) != group {
			continue
		}
		if ctr.IsRunning() {
			running++
			if stats := m.statsHistory[ctr.ID].lastStats; stats != nil {
				cpu += stats.CPUPercent
				mem += stats.MemoryUsage
			}
		} else {
			stopped++
		}
		if statusGlyph(ctr) != "●" {
			attention++
		}
	}

	left := lipgloss.NewStyle().Background(bg).Foreground(accent).Bold(true).Render("▾ " + name)
	text := fmt.Sprintf("%d running", running)
	if stopped > 0 {
		text += fmt.Sprintf(" · %d stopped", stopped)
	}
	text += fmt.Sprintf(" · CPU %.1f%% · MEM %s", cpu, formatBytes(mem))
	right := dim.Render(text)
	if attention > 0 {
		right += lipgloss.NewStyle().Background(bg).Render("  ") +
			lipgloss.NewStyle().Background(bg).Foreground(activePalette.Warn).Bold(true).Render(fmt.Sprintf("⚠ %d", attention))
	}

	spaces := func(n int) string { return lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", max(0, n))) }
	// rail(2) + name + gap + rule + gap + rollup fills the row exactly.
	fill := width - 2 - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if fill < 1 {
		return ansi.Truncate(spaces(2)+left, width, "")
	}
	return ansi.Truncate(spaces(2)+left+spaces(1)+dim.Render(strings.Repeat("─", fill))+spaces(1)+right, width, "")
}

// ── Uptime and restarts ──────────────────────────────────────────────────

// dashboardMeta is what the Dashboard learns from a full inspect that the
// list summary doesn't carry — currently the restart count. It's refreshed
// slowly (dashboardMetaTTL) because inspecting every container is far more
// expensive than the stats poll.
type dashboardMeta struct {
	restarts int
	fetched  time.Time
}

const dashboardMetaTTL = 30 * time.Second

// compactUptime shortens Docker's human-readable "Up 3 days (healthy)"
// status to at most four columns ("3d", "36h", "11mo"). It returns "" for a
// container that isn't up or a status it doesn't recognize.
func compactUptime(status string) string {
	rest, ok := strings.CutPrefix(status, "Up ")
	if !ok {
		return ""
	}
	if i := strings.Index(rest, " ("); i >= 0 {
		rest = rest[:i]
	}
	words := strings.Fields(strings.ToLower(rest))
	if len(words) >= 2 && words[0] == "less" {
		return "<1s"
	}
	if len(words) > 0 && words[0] == "about" {
		words = words[1:]
	}
	if len(words) < 2 {
		return ""
	}
	n := 1
	if words[0] != "a" && words[0] != "an" {
		v, err := strconv.Atoi(words[0])
		if err != nil {
			return ""
		}
		n = v
	}
	var unit string
	switch strings.TrimSuffix(words[1], "s") {
	case "second":
		unit = "s"
	case "minute":
		unit = "m"
	case "hour":
		unit = "h"
	case "day":
		unit = "d"
	case "week":
		unit = "w"
	case "month":
		unit = "mo"
	case "year":
		unit = "y"
	default:
		return ""
	}
	return strconv.Itoa(n) + unit
}

// dashboardRestartCount is how many times ctr has restarted, and whether
// that's known: the inspect-derived value when the Dashboard has fetched
// one, else whatever the container already carries (the demo provider
// fills it in; a real list summary leaves it zero, which isn't knowledge).
func (m Model) dashboardRestartCount(ctr domain.Container) (count int, known bool) {
	if meta, ok := m.dashboardMeta[ctr.ID]; ok {
		return meta.restarts, true
	}
	return ctr.RestartCount, ctr.RestartCount > 0
}

// dashboardToggleGrouping flips between a flat list and one grouped under a
// header per Compose stack, pinning the cursor to its current container
// first so the highlight follows it to its new row.
func (m *Model) dashboardToggleGrouping() {
	if shown, _, _, _, _ := m.dashboardBodyPlan(); m.dashboardCursorActive && len(shown) > 0 {
		m.dashboardCursorID = shown[m.dashboardSelectedIndex(shown)].ID
	}
	m.dashboardGrouped = !m.dashboardGrouped
}
