package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/allisonhere/whatthedock/internal/domain"
)

var allGraphStyles = []graphStyle{graphStyleWave, graphStyleBlocks, graphStyleBraille, graphStyleBars, graphStyleGauge}

// busyHistory is a container with a varied CPU, memory and network history
// (and a memory limit), so every style has something to draw.
func busyHistory(t *testing.T) (*domain.ContainerStats, statsHistory) {
	t.Helper()
	model := testModel()
	id := domain.ResourceID{Host: "local", ID: "busy"}
	cpu := []float64{2, 5, 5, 40, 90, 30, 8, 8, 60, 20, 3, 3}
	for i, c := range cpu {
		model.appendStats(domain.ContainerStats{
			ID: id, CPUPercent: c,
			MemoryUsage: uint64(100+i*40) << 20, MemoryLimit: 1 << 30,
			NetworkRx: uint64(i*i) * 4000, NetworkTx: uint64(i) * 1500,
		})
	}
	history := model.statsHistory[id]
	return history.lastStats, history
}

func TestEveryGraphStyleDrawsEachColumnDifferently(t *testing.T) {
	withTrueColorProfile(t) // wave and blocks can share glyphs; wave also fades older samples
	renderer := dashRenderer()
	stats, history := busyHistory(t)
	columns := map[string]dashboardSeries{
		"cpu": dashboardCPUSeries(stats, history),
		"mem": dashboardMemSeries(stats, history),
		"net": dashboardNetSeries(history.NetworkRx, history.maxNetwork),
	}
	for name, series := range columns {
		seen := map[string]graphStyle{}
		for _, style := range allGraphStyles {
			out := dashboardGraph(renderer, style, series, 12, "#000000")
			if got := lipgloss.Width(out); got != 12 {
				t.Errorf("%s/%v: width = %d, want 12: %q", name, style, got, ansi.Strip(out))
			}
			if other, dup := seen[out]; dup {
				t.Errorf("%s: styles %v and %v draw the same thing (%q); changing the Graph style should change this column", name, other, style, ansi.Strip(out))
			}
			seen[out] = style
		}
	}
}

// The five styles are two families: fills (the latest value as a bar) and
// histories (the recent past as a sparkline). Each uses its own glyphs.
func TestGraphStylesUseTheirOwnGlyphFamilies(t *testing.T) {
	renderer := dashRenderer()
	stats, history := busyHistory(t)
	live := *stats
	live.CPUPercent = 45 // a gauge at the busy fixture's own 3% would be empty
	series := dashboardCPUSeries(&live, history)
	draw := func(style graphStyle, width int) string {
		return ansi.Strip(dashboardGraph(renderer, style, series, width, "#000000"))
	}
	only := func(s, allowed string) bool { return strings.Trim(s, allowed) == "" }

	if out := draw(graphStyleGauge, 12); !only(out, "━╸─") || !strings.ContainsAny(out, "━╸") {
		t.Errorf("gauge = %q, want only the thin ━╸─ bar", out)
	}
	if out := draw(graphStyleBars, 12); !only(out, "█▏▎▍▌▋▊▉░") || !strings.ContainsAny(out, "█▏▎▍▌▋▊▉") {
		t.Errorf("bars = %q, want only the chunky █▌░ bar", out)
	}
	for _, style := range []graphStyle{graphStyleWave, graphStyleBlocks} {
		if out := draw(style, 12); !only(out, "▁▂▃▄▅▆▇█") {
			t.Errorf("%v = %q, want only block sparkline glyphs", style, out)
		}
	}
	isBraille := func(r rune) bool { return r >= 0x2800 && r <= 0x28FF }
	out := draw(graphStyleBraille, 12)
	if strings.TrimFunc(out, isBraille) != "" {
		t.Errorf("braille = %q, want only braille patterns", out)
	}
}

func TestDashboardFillBarsHaveSubCellPrecision(t *testing.T) {
	renderer := dashRenderer()
	series := func(current float64) dashboardSeries {
		return dashboardSeries{current: current, fillMax: 100, color: func(float64, float64) lipgloss.Color { return "#ffffff" }}
	}
	draw := func(current float64) string {
		return ansi.Strip(dashboardGraph(renderer, graphStyleBars, series(current), 10, "#000000"))
	}
	if got := draw(50); got != "█████░░░░░" {
		t.Errorf("50%% = %q, want 5 full cells then track", got)
	}
	if got := draw(55); got != "█████▌░░░░" {
		t.Errorf("55%% = %q, want a half-block partial cell at the edge", got)
	}
	if got := draw(100); got != "██████████" {
		t.Errorf("100%% = %q, want completely full", got)
	}
	if got := draw(0); got != "░░░░░░░░░░" {
		t.Errorf("0%% = %q, want an empty track", got)
	}
	// Above capacity saturates rather than overflowing.
	if got := lipgloss.Width(draw(400)); got != 10 {
		t.Errorf("400%% drew %d cells, want 10", got)
	}
}

// Memory fills on a square-root curve so single-digit percentages stay
// visible (0.4% and 2.7% of a 10-cell meter are 1 and 2 cells), in the thin
// gauge as in the chunky bar.
func TestDashboardMemoryFillUsesSqrtCurve(t *testing.T) {
	renderer := dashRenderer()
	cells := func(style graphStyle, pct float64) int {
		series := dashboardSeries{current: pct, fillMax: 100, fillCurve: dashboardMemSeries(&domain.ContainerStats{MemoryLimit: 1}, statsHistory{}).fillCurve,
			color: func(float64, float64) lipgloss.Color { return "#ffffff" }}
		out := ansi.Strip(dashboardGraph(renderer, style, series, 10, "#000000"))
		return lipgloss.Width(strings.TrimRight(strings.TrimRight(out, "─"), "░"))
	}
	for _, c := range []struct {
		pct  float64
		want int
	}{{0, 0}, {0.4, 1}, {2.7, 2}, {100, 10}} {
		if got := cells(graphStyleGauge, c.pct); got != c.want {
			t.Errorf("gauge %v%% filled %d cells, want %d", c.pct, got, c.want)
		}
	}
}

func TestDashboardQuietTrackInEveryStyle(t *testing.T) {
	renderer := dashRenderer()
	noLimit := dashboardMemSeries(&domain.ContainerStats{MemoryUsage: 5}, statsHistory{})
	allZeroNet := dashboardNetSeries([]uint64{0, 0, 0}, 0)
	for _, style := range allGraphStyles {
		for name, series := range map[string]dashboardSeries{"no memory limit": noLimit, "zero traffic": allZeroNet} {
			if got := ansi.Strip(dashboardGraph(renderer, style, series, 6, "#000000")); got != strings.Repeat("─", 6) {
				t.Errorf("%v / %s = %q, want the quiet dashed track", style, name, got)
			}
		}
	}
	// A lone zero among real traffic is a low glyph, not a vanished graph.
	live := dashboardNetSeries([]uint64{4000, 0}, 4000)
	if got := ansi.Strip(dashboardGraph(renderer, graphStyleBlocks, live, 6, "#000000")); got == strings.Repeat("─", 6) {
		t.Errorf("a momentary zero blanked a line with real recent traffic: %q", got)
	}
}

// Wave smooths the series into a trend; blocks draws each sample raw. A
// single-poll spike is a tower in blocks and a hill in wave.
func TestWaveSmoothsWhatBlocksDrawsRaw(t *testing.T) {
	renderer := dashRenderer()
	series := dashboardSeries{
		values: []float64{0, 0, 100, 0, 0}, current: 0, fillMax: 100, floor: 10,
		color: func(float64, float64) lipgloss.Color { return "#ffffff" },
	}
	raw := ansi.Strip(dashboardGraph(renderer, graphStyleBlocks, series, 5, "#000000"))
	smooth := ansi.Strip(dashboardGraph(renderer, graphStyleWave, series, 5, "#000000"))
	if raw != "▁▁█▁▁" {
		t.Errorf("blocks = %q, want the raw spike ▁▁█▁▁", raw)
	}
	if smooth != "▁▃▃▃▁" {
		t.Errorf("wave = %q, want the spike rounded into its neighbours", smooth)
	}
	runes := []rune(smooth)
	if runes[1] <= runes[0] || runes[3] <= runes[4] {
		t.Errorf("wave = %q, want the spike's neighbours raised above the baseline", smooth)
	}
}

// The old wave glyph ladder was non-monotonic, so a larger value could draw
// shorter. Every style's ladder must now rise with the value.
func TestSparklineGlyphsRiseWithTheValue(t *testing.T) {
	renderer := dashRenderer()
	for _, style := range []graphStyle{graphStyleWave, graphStyleBlocks} {
		var settings appSettings
		settings.GraphStyle = style
		graph := statGraph{values: []float64{5, 15, 30, 45, 60, 75, 90, 100, 100, 100}, maxValue: 100}
		runes := []rune(ansi.Strip(renderSparkline(renderer, settings, graph, "#7dcfff", 10)))
		for i := 1; i < len(runes); i++ {
			if runes[i] < runes[i-1] {
				t.Errorf("%v: glyph %d (%q) is shorter than glyph %d (%q) for a larger value", style, i, runes[i], i-1, runes[i-1])
			}
		}
	}
}

// A historical spike keeps its own hot colour among quiet neighbours, and
// CPU colour is absolute: a container idling at 2% never turns red just
// because 2% is its own peak.
func TestDashboardHistoryColoursEachSampleByItsOwnValue(t *testing.T) {
	withTrueColorProfile(t)
	renderer := dashRenderer()
	// lipgloss folds foreground and background into one SGR, so match the
	// foreground prefix without its closing "m".
	fgPrefix := func(color lipgloss.Color) string {
		rendered := lipgloss.NewStyle().Foreground(color).Render("x")
		return strings.TrimSuffix(rendered[:strings.Index(rendered, "x")], "m")
	}
	red := fgPrefix(activePalette.Bad)

	series := dashboardCPUSeries(&domain.ContainerStats{CPUPercent: 2}, statsHistory{CPU: []float64{2, 2, 95, 2}})
	out := dashboardGraph(renderer, graphStyleBlocks, series, 4, "#000000")
	cells := strings.Split(out, "\x1b[0m") // one styled cell each
	if len(cells) < 4 || !strings.Contains(cells[2], red) {
		t.Errorf("history = %q, want the 95%% spike (cell 2) in red", out)
	}
	for _, i := range []int{0, 1, 3} {
		if strings.Contains(cells[i], red) {
			t.Errorf("quiet 2%% sample %d drawn in red: %q", i, cells[i])
		}
	}

	idle := dashboardCPUSeries(&domain.ContainerStats{CPUPercent: 2}, statsHistory{CPU: []float64{2, 2, 2, 2}})
	if got := dashboardGraph(renderer, graphStyleBlocks, idle, 8, "#000000"); strings.Contains(got, red) {
		t.Errorf("an idle container's flat history drew a red sample: %q", got)
	}
}

// Quiet network traffic still has a shape: colour is relative to the
// container's own peak, so it spans the palette even at a few KB/s.
func TestDashboardNetHistoryShapeIsColouredRelativeToItsPeak(t *testing.T) {
	withTrueColorProfile(t)
	renderer := dashRenderer()
	series := dashboardNetSeries([]uint64{0, 200, 1500, 400, 1500, 0}, 1500)
	out := dashboardGraph(renderer, graphStyleBlocks, series, 12, "#000000")
	sgrFor := func(color lipgloss.Color) string {
		// lipgloss folds foreground and background into one SGR, so match
		// the foreground prefix without its closing "m".
		rendered := lipgloss.NewStyle().Foreground(color).Render("x")
		return strings.TrimSuffix(rendered[:strings.Index(rendered, "x")], "m")
	}
	for _, want := range []lipgloss.Color{"#80c990", "#e06c75"} {
		if !strings.Contains(out, sgrFor(want)) {
			t.Errorf("quiet-traffic graph = %q, want it to span the palette including %q", out, want)
		}
	}
}

// End to end: switching the Graph style changes the container rows — the
// whole row, not just one column of it.
func TestGraphStyleChangesTheWholeContainerRow(t *testing.T) {
	renderer := dashRenderer()
	model := testModel()
	id := domain.ResourceID{Host: "local", ID: "1"}
	for i := range 12 {
		model.appendStats(domain.ContainerStats{
			ID: id, CPUPercent: float64(i * 7 % 60), MemoryUsage: uint64(100+i*30) << 20, MemoryLimit: 1 << 30,
			NetworkRx: uint64(i*i) * 6000, NetworkTx: uint64(i) * 2000,
		})
	}
	var ctr domain.Container
	for _, c := range model.snapshotContainers() {
		if c.ID == id {
			ctr = c
		}
	}

	rows := map[string]graphStyle{}
	for _, style := range allGraphStyles {
		model.settings.GraphStyle = style
		row := ansi.Strip(model.dashboardRow(renderer, ctr, 150, false, false))
		if lipgloss.Width(row) != 150 {
			t.Errorf("%v: row width = %d, want 150", style, lipgloss.Width(row))
		}
		if other, dup := rows[row]; dup {
			t.Errorf("styles %v and %v render an identical row:\n%q", other, style, row)
		}
		rows[row] = style
	}
}
