package ui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/allisonhere/tideui"
	"github.com/charmbracelet/x/ansi"

	"github.com/allisonhere/whatthedock/internal/domain"
)

func TestFleetSummaryCountsByStatus(t *testing.T) {
	model := testModel()
	summary := model.fleetSummary()

	// testModel's fixture (see newFakeProvider): radarr-1 running+unhealthy
	// ("!"), jellyfin-1 stopped ("○").
	if summary.counts["!"] != 1 {
		t.Fatalf("counts[!] = %d, want 1 (radarr-1, unhealthy)", summary.counts["!"])
	}
	if summary.counts["○"] != 1 {
		t.Fatalf("counts[○] = %d, want 1 (jellyfin-1, stopped)", summary.counts["○"])
	}
}

func TestFleetSummaryAggregatesOnlyContainersWithStats(t *testing.T) {
	model := testModel()
	// No stats polled yet — history is empty, so aggregates must stay zero
	// rather than counting a not-yet-polled container as zero usage.
	summary := model.fleetSummary()
	if summary.totalCPU != 0 || summary.memUsed != 0 || summary.memLimit != 0 {
		t.Fatalf("summary = %#v, want all-zero aggregates before any stats arrive", summary)
	}

	radarrID := domain.ResourceID{Host: "local", ID: "1"}
	model.appendStats(domain.ContainerStats{ID: radarrID, CPUPercent: 41.5, MemoryUsage: 384 * 1024 * 1024, MemoryLimit: 2 * 1024 * 1024 * 1024})

	summary = model.fleetSummary()
	if summary.totalCPU != 41.5 {
		t.Fatalf("totalCPU = %v, want 41.5", summary.totalCPU)
	}
	if summary.memUsed != 384*1024*1024 || summary.memLimit != 2*1024*1024*1024 {
		t.Fatalf("mem = %d/%d, want 384MiB/2GiB", summary.memUsed, summary.memLimit)
	}
}

func TestFleetSummarySumsAcrossMultipleRunningContainers(t *testing.T) {
	model := testModel()
	idA := domain.ResourceID{Host: "local", ID: "1"}
	idB := domain.ResourceID{Host: "local", ID: "b"}
	model.appendStats(domain.ContainerStats{ID: idA, CPUPercent: 10, MemoryUsage: 100})
	model.appendStats(domain.ContainerStats{ID: idB, CPUPercent: 20, MemoryUsage: 200})

	summary := model.fleetSummary()
	// idB isn't a real container in the snapshot, so fleetSummary (which
	// iterates snapshotContainers, not statsHistory) only picks up idA's
	// contribution — this is intentional: a stats entry for a container
	// that's since disappeared from the snapshot shouldn't keep inflating
	// the fleet total forever.
	if summary.totalCPU != 10 {
		t.Fatalf("totalCPU = %v, want 10 (only idA is a real snapshot container)", summary.totalCPU)
	}
}

func TestDashboardRefreshCmdNilWhenOverlayClosed(t *testing.T) {
	model := testModel()
	model.overlay = overlayNone
	if cmd := model.dashboardRefreshCmd(); cmd != nil {
		t.Fatal("dashboardRefreshCmd() != nil with overlay closed, want nil")
	}
}

func TestDashboardRefreshCmdOnlyDispatchesRunningContainers(t *testing.T) {
	model := testModel()
	model.overlay = overlayDashboard
	model.settings.StatsRefresh = time.Millisecond // keep the re-arm tick's own timer out of this test's runtime

	cmd := model.dashboardRefreshCmd()
	if cmd == nil {
		t.Fatal("dashboardRefreshCmd() = nil, want a batch command")
	}
	msg := runCmd(t, cmd)
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("msg = %#v, want tea.BatchMsg", msg)
	}
	// testModel's fixture has exactly one running container (radarr-1) plus
	// the re-arm tick — so exactly 2 commands in the batch.
	if len(batch) != 2 {
		t.Fatalf("batch has %d commands, want 2 (one running container + the re-arm tick)", len(batch))
	}

	var sawStats, sawTick bool
	for _, c := range batch {
		switch c().(type) {
		case dashboardStatsMsg:
			sawStats = true
		case dashboardTickMsg:
			sawTick = true
		}
	}
	if !sawStats || !sawTick {
		t.Fatalf("sawStats=%v sawTick=%v, want both true", sawStats, sawTick)
	}
}

func TestDashboardStatsMsgAppendsIntoHistory(t *testing.T) {
	model := testModel()
	model.overlay = overlayDashboard
	id := domain.ResourceID{Host: "local", ID: "1"}

	updated, _ := model.Update(dashboardStatsMsg{stats: domain.ContainerStats{ID: id, CPUPercent: 55}})
	next := updated.(Model)
	if len(next.statsHistory[id].CPU) == 0 || next.statsHistory[id].CPU[len(next.statsHistory[id].CPU)-1] != 55 {
		t.Fatalf("statsHistory[id].CPU = %v, want it to end with 55", next.statsHistory[id].CPU)
	}
}

// TestDashboardStatsMsgIgnoredAfterOverlayClosed guards against a response
// landing after the user already left the Dashboard — must not append into
// history for a screen nobody's looking at (mirrors the aiAnalysisDoneMsg
// stale-response guard from earlier this session).
func TestDashboardStatsMsgIgnoredAfterOverlayClosed(t *testing.T) {
	model := testModel()
	model.overlay = overlayNone
	id := domain.ResourceID{Host: "local", ID: "1"}

	updated, _ := model.Update(dashboardStatsMsg{stats: domain.ContainerStats{ID: id, CPUPercent: 55}})
	next := updated.(Model)
	if len(next.statsHistory[id].CPU) != 0 {
		t.Fatalf("statsHistory[id].CPU = %v, want untouched while the overlay is closed", next.statsHistory[id].CPU)
	}
}

func TestDashboardStatsMsgErrorDoesNotAppend(t *testing.T) {
	model := testModel()
	model.overlay = overlayDashboard
	id := domain.ResourceID{Host: "local", ID: "1"}

	updated, _ := model.Update(dashboardStatsMsg{stats: domain.ContainerStats{ID: id}, err: errBoom})
	next := updated.(Model)
	if len(next.statsHistory[id].CPU) != 0 {
		t.Fatalf("statsHistory[id].CPU = %v, want untouched when the fetch errored", next.statsHistory[id].CPU)
	}
}

func TestDashboardTickMsgReArmsWhileOpenNoopWhenClosed(t *testing.T) {
	model := testModel()
	model.overlay = overlayDashboard
	if _, cmd := model.Update(dashboardTickMsg{}); cmd == nil {
		t.Fatal("dashboardTickMsg with overlay open returned nil cmd, want a re-armed refresh")
	}

	model.overlay = overlayNone
	if _, cmd := model.Update(dashboardTickMsg{}); cmd != nil {
		t.Fatal("dashboardTickMsg with overlay closed returned a non-nil cmd, want nil")
	}
}

func TestDKeyOpensDashboardOverlay(t *testing.T) {
	model := testModel()
	model.width, model.height = 120, 34

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	next := updated.(Model)
	if next.overlay != overlayDashboard {
		t.Fatalf("overlay = %v after \"d\", want overlayDashboard", next.overlay)
	}
	if cmd == nil {
		t.Fatal("opening the dashboard returned nil cmd, want the initial refresh command")
	}
}

func TestDashboardOverlayCloseKeys(t *testing.T) {
	keys := []tea.KeyMsg{
		{Type: tea.KeyEsc},
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
		{Type: tea.KeyRunes, Runes: []rune{'d'}},
	}
	for _, key := range keys {
		model := testModel()
		model.overlay = overlayDashboard
		updated, _ := model.Update(key)
		next := updated.(Model)
		if next.overlay != overlayNone {
			t.Fatalf("key %v: overlay = %v, want overlayNone", key, next.overlay)
		}
	}
}

// TestDashboardLeftRightCyclesGraphStyle checks that left/right cycle
// m.settings.GraphStyle directly while the Dashboard overlay is open
// (cycleDashboardGraphStyle), wrapping via modIndex the same way the
// Settings screen's own left/right graph-style cycling does, and without
// closing the overlay.
func TestDashboardLeftRightCyclesGraphStyle(t *testing.T) {
	model := testModel()
	model.overlay = overlayDashboard
	model.settings.GraphStyle = graphStyleWave

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	next := updated.(Model)
	if next.settings.GraphStyle != graphStyleBlocks {
		t.Fatalf("after right: GraphStyle = %v, want graphStyleBlocks", next.settings.GraphStyle)
	}
	if next.overlay != overlayDashboard {
		t.Fatalf("after right: overlay = %v, want overlayDashboard (still open)", next.overlay)
	}

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyLeft})
	next = updated.(Model)
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyLeft})
	next = updated.(Model)
	if next.settings.GraphStyle != graphStyleGauge {
		t.Fatalf("after right,left,left: GraphStyle = %v, want graphStyleGauge (wrapped)", next.settings.GraphStyle)
	}
}

func TestDashboardOverlayShowsSummaryAndRunningContainer(t *testing.T) {
	model := testModel()
	model.width, model.height = 120, 34
	model.overlay = overlayDashboard
	model.appendStats(domain.ContainerStats{ID: domain.ResourceID{Host: "local", ID: "1"}, CPUPercent: 41.5})

	view := ansi.Strip(model.View())
	if !strings.Contains(view, "whatthedock · dashboard") {
		t.Fatalf("view missing the dashboard overlay:\n%s", view)
	}
	if !strings.Contains(view, "radarr-1") {
		t.Fatalf("view missing the running container's row:\n%s", view)
	}
	if !strings.Contains(view, "! 1 UNHEALTHY") {
		t.Fatalf("view missing the unhealthy-count summary:\n%s", view)
	}
}

// TestDashboardOverlayDoesNotPanicOnShortTerminal mirrors
// TestRenderProblemsSplitDoesNotPanicOnShortTerminal — the Dashboard's
// column-width math (dashboardColumnsFor) must degrade gracefully instead
// of panicking on a terminal too narrow/short for the full layout.
func TestDashboardOverlayDoesNotPanicOnShortTerminal(t *testing.T) {
	model := testModel()
	model.width, model.height = 40, 8
	model.overlay = overlayDashboard

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("View() panicked on a short terminal: %v", r)
		}
	}()
	_ = model.View()
}

// TestDashboardOverlayShowsMoreIndicatorWhenListOverflows checks a fleet
// larger than the overlay's budget gets a "N more" indicator instead of
// silently dropping rows — the same never-truncate-silently standard this
// session already established for the Problems-pane insight text.
func TestDashboardOverlayShowsMoreIndicatorWhenListOverflows(t *testing.T) {
	model := testModel()
	model.width, model.height = 120, 12 // short enough that not everything fits
	model.overlay = overlayDashboard

	// Add a pile of extra running standalone containers so the list
	// definitely overflows a 12-row-tall terminal's budget.
	for i := 0; i < 20; i++ {
		id := "extra-" + string(rune('a'+i))
		model.snapshot.Standalone = append(model.snapshot.Standalone, domain.Container{
			ID:    domain.ResourceID{Host: "local", ID: id},
			Name:  "extra-container-" + string(rune('a'+i)),
			State: domain.StateRunning,
		})
	}

	view := ansi.Strip(model.View())
	if !strings.Contains(view, "more running container") {
		t.Fatalf("view missing a \"N more\" indicator despite the list overflowing:\n%s", view)
	}
}

func TestFormatCompactBytesAndRate(t *testing.T) {
	cases := map[uint64]string{
		0:                      "0B",
		512:                    "512B",
		14 * 1024 * 1024:       "14.0M",
		2 * 1024 * 1024 * 1024: "2.0G",
	}
	for value, want := range cases {
		if got := formatCompactBytes(value); got != want {
			t.Fatalf("formatCompactBytes(%d) = %q, want %q", value, got, want)
		}
	}
	if got := formatRateCompact(1024); got != "1.0K/s" {
		t.Fatalf("formatRateCompact(1024) = %q, want 1.0K/s", got)
	}
}

// TestDashboardHeaderRowAlignsWithRowNumbers is the regression test for a
// layout bug caught live: header labels were built as a full-width
// left-aligned column, while row data right-aligns its numbers within a
// much narrower numWidth slot inside that same column — the two need to
// share their right edge for the header to actually sit over the numbers
// it labels.
func TestDashboardHeaderRowAlignsWithRowNumbers(t *testing.T) {
	model := testModel()
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	model.appendStats(domain.ContainerStats{ID: domain.ResourceID{Host: "local", ID: "1"}, CPUPercent: 41.5})

	width := 120
	header := ansi.Strip(model.dashboardHeaderRow(renderer, width))
	var row string
	for _, ctr := range model.snapshotContainers() {
		if ctr.IsRunning() {
			row = ansi.Strip(model.dashboardRow(renderer, ctr, width, false))
			break
		}
	}
	if row == "" {
		t.Fatal("no running container found to render a row for")
	}

	cpuHeaderEnd := strings.Index(header, "CPU") + len("CPU")
	cpuDataIdx := strings.Index(row, "41.5%")
	if cpuDataIdx < 0 {
		t.Fatalf("row missing the expected CPU number:\n%s", row)
	}
	cpuDataEnd := cpuDataIdx + len("41.5%")
	if cpuHeaderEnd != cpuDataEnd {
		t.Fatalf("CPU header right edge = %d, data right edge = %d, want equal:\nheader: %q\nrow:    %q", cpuHeaderEnd, cpuDataEnd, header, row)
	}
}

// TestDashboardPadLineReachesExactWidth is the regression test for the bug
// reported live twice: dashboard rows/lines that ended up a few columns
// short of the panel's real width left an unstyled gap on the right that
// rendered as a stray, wrong-colored strip on a real terminal. Every line
// dashboardOverlay builds must come out to exactly the requested width
// after padding, never short (and dashboardPadLine itself must never
// truncate a line that's already at or past the target width).
func TestDashboardPadLineReachesExactWidth(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	cases := []string{"", "short", strings.Repeat("x", 50), strings.Repeat("x", 60)}
	for _, s := range cases {
		got := dashboardPadLine(renderer, s, 50)
		if visible := len([]rune(ansi.Strip(got))); visible < 50 {
			t.Fatalf("dashboardPadLine(%q, 50) visible width = %d, want >= 50", s, visible)
		}
	}
}

func TestDashboardMoodStripCapsRuleOnWideDisplays(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	model := testModel()
	got := ansi.Strip(model.dashboardMoodStrip(renderer, dashboardSummary{}, -1, 220))
	if visible := len([]rune(got)); visible != 220 {
		t.Fatalf("dashboardMoodStrip visible width = %d, want 220", visible)
	}
	if dashCount := strings.Count(got, "─"); dashCount != dashboardDividerMaxWidth {
		t.Fatalf("dashboardMoodStrip dash count = %d, want capped at %d", dashCount, dashboardDividerMaxWidth)
	}
	if !strings.HasPrefix(got, strings.Repeat(" ", 50)) || !strings.HasSuffix(got, strings.Repeat(" ", 50)) {
		t.Fatalf("dashboardMoodStrip should center the capped rule in a wide line, got %q", got)
	}
}

// TestDashboardMoodStripHueTracksPressure checks goal #2/#3: the ribbon's
// colour is fleet-green when nothing is under load and shifts to the
// amber/red end once the hottest container crosses into the warn band,
// and the post-refresh flash makes the whole ribbon render differently
// for a few pulse frames.
func TestDashboardMoodStripHueTracksPressure(t *testing.T) {
	original := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(original)

	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	model := testModel()

	calm := model.dashboardMoodStrip(renderer, dashboardSummary{peakPressure: 3}, -1, 80)
	mid := model.dashboardMoodStrip(renderer, dashboardSummary{peakPressure: 60}, -1, 80)
	hot := model.dashboardMoodStrip(renderer, dashboardSummary{peakPressure: 95}, -1, 80)
	if calm == mid || mid == hot || calm == hot {
		t.Fatalf("dashboardMoodStrip hue did not track peak pressure across 3/60/95:\ncalm %q\nmid  %q\nhot  %q", calm, mid, hot)
	}
	// The calm ribbon is unmistakably green (g channel well above r and b);
	// the hot one is not.
	if !strings.Contains(calm, ";201;14") {
		t.Fatalf("calm mood strip should be fleet-green, got %q", calm)
	}
	if strings.Contains(hot, ";201;14") {
		t.Fatalf("maxed-out mood strip should not still be fleet-green, got %q", hot)
	}

	steady := model.dashboardMoodStrip(renderer, dashboardSummary{peakPressure: 3}, 99, 80)
	flashing := model.dashboardMoodStrip(renderer, dashboardSummary{peakPressure: 3}, 0, 80)
	if steady == flashing {
		t.Fatal("dashboardMoodStrip post-refresh flash produced no visible change at age 0")
	}
}

// TestDashboardRowTintsHotContainers checks goal #4: a container whose CPU
// or memory-of-limit is in the warn band renders its name in the shared
// amber/red dashboardThresholdColor and washes its row background, while a
// container comfortably below the threshold gets neither.
func TestDashboardRowTintsHotContainers(t *testing.T) {
	original := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(original)

	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	id := domain.ResourceID{Host: "local", ID: "1"}

	runningRow := func(m Model) string {
		for _, ctr := range m.snapshotContainers() {
			if ctr.IsRunning() {
				return m.dashboardRow(renderer, ctr, 140, false)
			}
		}
		t.Fatal("no running container in fixture")
		return ""
	}

	// The fixture's running container is named radarr-1; the tint recolors
	// its name run (not just the status glyph, which is already red for an
	// unhealthy container).
	tintedName := regexp.MustCompile(`38;2;224;108;117;48;2;\d+;\d+;\d+mradarr-1`)

	calm := testModel()
	calm.appendStats(domain.ContainerStats{ID: id, CPUPercent: 12, MemoryUsage: 10, MemoryLimit: 1000})
	calmRow := runningRow(calm)
	if tintedName.MatchString(calmRow) {
		t.Fatalf("a quiet container's row should not tint its name:\n%q", calmRow)
	}

	hot := testModel()
	hot.appendStats(domain.ContainerStats{ID: id, CPUPercent: 96, MemoryUsage: 10, MemoryLimit: 1000})
	hotRow := runningRow(hot)
	if !tintedName.MatchString(hotRow) {
		t.Fatalf("a container pegged at 96%% CPU should tint its name red:\n%q", hotRow)
	}
	if hotRow == calmRow {
		t.Fatal("hot and calm rows rendered identically")
	}
}

// TestDashboardFleetSparkRowRendersAggregateHistory checks goal #1: the
// header's aggregate CPU/NET sparklines are built from the fleet history
// rings, carry both labels at a normal width, and collapse to CPU-only
// when the panel is narrow.
func TestDashboardFleetSparkRowRendersAggregateHistory(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	model := testModel()
	model.fleetCPUHistory = []float64{5, 20, 60, 120, 90, 140}
	model.fleetNetHistory = []uint64{0, 1 << 10, 1 << 20, 8 << 20, 2 << 20, 32 << 20}

	wide := ansi.Strip(model.dashboardFleetSparkRow(renderer, 120))
	if !strings.Contains(wide, "CPU ") || !strings.Contains(wide, "NET ") {
		t.Fatalf("wide fleet spark row missing CPU/NET labels: %q", wide)
	}
	if strings.TrimSpace(strings.NewReplacer("CPU", "", "NET", "", "─", "").Replace(wide)) == "" {
		t.Fatalf("wide fleet spark row drew no sparkline glyphs: %q", wide)
	}

	narrow := ansi.Strip(model.dashboardFleetSparkRow(renderer, 18))
	if !strings.Contains(narrow, "CPU ") || strings.Contains(narrow, "NET ") {
		t.Fatalf("narrow fleet spark row should collapse to CPU only: %q", narrow)
	}
}

func TestDashboardPanelWidthIsConstrainedOnWideDisplays(t *testing.T) {
	if got := dashboardPanelWidth(240); got != dashboardMaxPanelWidth {
		t.Fatalf("dashboardPanelWidth(240) = %d, want cap %d", got, dashboardMaxPanelWidth)
	}
	if got := dashboardPanelWidth(120); got != 116 {
		t.Fatalf("dashboardPanelWidth(120) = %d, want terminal width minus margin", got)
	}
}

func TestDashboardOverlayIsConstrainedOnWideDisplays(t *testing.T) {
	model := testModel()
	model.width, model.height = 240, 34
	model.overlay = overlayDashboard

	for _, line := range strings.Split(model.View(), "\n") {
		plain := ansi.Strip(line)
		if !strings.Contains(plain, "whatthedock · dashboard") {
			continue
		}
		left := strings.Index(plain, "╭")
		right := strings.LastIndex(plain, "╮")
		if left < 0 || right < left {
			t.Fatalf("dashboard title row missing overlay corners:\n%q", plain)
		}
		if got := lipgloss.Width(plain[left : right+len("╮")]); got != dashboardMaxOverlayWidth {
			t.Fatalf("dashboard overlay box width = %d, want constrained width %d:\n%q", got, dashboardMaxOverlayWidth, plain)
		}
		return
	}
	t.Fatal("rendered wide dashboard missing title row")
}

// TestDashboardRowAndSummaryLineNeverFallShortOfContentWidth is a direct
// regression test against the real failure mode: dashboardColumnsFor's
// width math (nameWidth + fixed prefix + 3 metric columns) previously
// left a 2-6 column gap versus the actual contentWidth passed to
// RenderSoftBody, at a range of realistic terminal widths — this checks
// the padded, panel-ready line (as dashboardOverlay actually builds it)
// always reaches exactly contentWidth, not just that the unpadded row
// happens to be close.
func TestDashboardRowAndSummaryLineNeverFallShortOfContentWidth(t *testing.T) {
	model := testModel()
	model.appendStats(domain.ContainerStats{ID: domain.ResourceID{Host: "local", ID: "1"}, CPUPercent: 41.5, MemoryUsage: 100, MemoryLimit: 200, NetworkRx: 10, NetworkTx: 10})
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})

	for _, width := range []int{66, 90, 100, 120, 140, 200} {
		for _, ctr := range model.snapshotContainers() {
			if !ctr.IsRunning() {
				continue
			}
			row := dashboardPadLine(renderer, model.dashboardRow(renderer, ctr, width, false), width)
			if got := len([]rune(ansi.Strip(row))); got != width {
				t.Fatalf("width=%d: padded row visible width = %d, want exactly %d\nrow: %q", width, got, width, ansi.Strip(row))
			}
		}
		summary := dashboardPadLine(renderer, model.dashboardSummaryLine(renderer, model.fleetSummary(), width), width)
		if got := len([]rune(ansi.Strip(summary))); got != width {
			t.Fatalf("width=%d: padded summary line visible width = %d, want exactly %d", width, got, width)
		}
	}
}

// dashboardBgThenFgSGR matches an explicit truecolor background SGR
// immediately followed by a foreground SGR — the exact shape
// dashboardRow's glyph and dashboardSummaryLine's count+glyph produce now
// that each is wrapped in its own outer Background(...).Render(...). Its
// absence is the regression this test guards: foregroundSpan only ever
// sets foreground (by design — see its own doc comment), so a glyph
// composed via foregroundSpan alone, sitting next to other independently-
// `.Render()`ed segments, relies on background "carrying over" from a
// neighboring segment — which it doesn't, because each of those Render
// calls emits its own trailing full reset. That gap was reported live
// twice as the status glyphs (and the summary line's count+glyph pairs)
// showing the wrong background color.
var dashboardBgThenFgSGR = regexp.MustCompile(`\x1b\[48;2;\d+;\d+;\d+m\x1b\[38;2;\d+;\d+;\d+m`)

func TestDashboardGlyphsCarryExplicitBackground(t *testing.T) {
	original := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(original)

	model := testModel()
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})

	var row string
	for _, ctr := range model.snapshotContainers() {
		if ctr.IsRunning() {
			row = model.dashboardRow(renderer, ctr, 120, false)
			break
		}
	}
	if row == "" {
		t.Fatal("no running container found to render a row for")
	}
	if !dashboardBgThenFgSGR.MatchString(row) {
		t.Fatalf("dashboardRow's status glyph has no explicit background SGR immediately preceding its foreground SGR — it's relying on a neighboring segment's background to carry over, which it doesn't:\n%q", row)
	}

	summary := model.dashboardSummaryLine(renderer, dashboardSummary{counts: map[string]int{"●": 3}}, 80)
	if !dashboardBgThenFgSGR.MatchString(summary) {
		t.Fatalf("dashboardSummaryLine's count+glyph has no explicit background SGR immediately preceding its foreground SGR:\n%q", summary)
	}
}

// TestDashboardThresholdColorBoundaries checks dashboardThresholdColor's
// three bands land exactly on dashboardWarnPct/dashboardCritPct — the
// whole redesign hinges on these being absolute cutoffs rather than a
// per-container relative scale (see dashboardWarnPct's own doc comment).
func TestDashboardThresholdColorBoundaries(t *testing.T) {
	neutral := lipgloss.Color("#7dcfff")
	cases := []struct {
		pct  float64
		want lipgloss.Color
	}{
		{0, neutral},
		{0.4, neutral},
		{69.9, neutral},
		{70, "#e5c07b"},
		{89.9, "#e5c07b"},
		{90, "#e06c75"},
		{100, "#e06c75"},
	}
	for _, c := range cases {
		if got := dashboardThresholdColor(c.pct, neutral); got != c.want {
			t.Fatalf("dashboardThresholdColor(%v) = %v, want %v", c.pct, got, c.want)
		}
	}
}

// TestDashboardGraphColorBoundaries checks dashboardGraphColor's anchor
// points land exactly on CPU's own cyan identity color at 0%, and on the
// shared yellow/orange/red anchors at dashboardCautionPct/dashboardWarnPct/
// dashboardCritPct — the Dashboard's CPU sparkline always has a resting
// cyan baseline (not dashboardThresholdColor's per-caller neutral) that
// climbs continuously rather than jumping between flat bands.
func TestDashboardGraphColorBoundaries(t *testing.T) {
	cases := []struct {
		pct  float64
		want lipgloss.Color
	}{
		{0, "#7dcfff"},
		{50, "#e8c170"},
		{70, "#edad75"},
		{90, "#e06c75"},
		{100, "#e06c75"},
	}
	for _, c := range cases {
		if got := dashboardGraphColor(c.pct); got != c.want {
			t.Fatalf("dashboardGraphColor(%v) = %v, want %v", c.pct, got, c.want)
		}
	}
	// Between anchors the color is continuously interpolated, not a flat
	// step — two points either side of the old 50% band boundary must
	// differ, unlike the pre-redesign flat-band behavior.
	if dashboardGraphColor(49) == dashboardGraphColor(51) {
		t.Fatalf("dashboardGraphColor(49) and (51) are identical, want a continuous gradient either side of 50%%")
	}
}

// TestDashboardMemColorHasItsOwnIdentity checks dashboardMemColor starts
// from Memory's own green identity color, distinct from CPU's cyan
// dashboardGraphColor, at the same percentage — the point of splitting
// them was to give each metric its own baseline hue.
func TestDashboardMemColorHasItsOwnIdentity(t *testing.T) {
	if got := dashboardMemColor(0); got != "#80c990" {
		t.Fatalf("dashboardMemColor(0) = %v, want #80c990 (Memory's green identity)", got)
	}
	if dashboardMemColor(10) == dashboardGraphColor(10) {
		t.Fatalf("dashboardMemColor(10) and dashboardGraphColor(10) are identical, want distinct per-metric baseline colors")
	}
	// Both still converge on the same red danger color once critical.
	if got := dashboardMemColor(90); got != "#e06c75" {
		t.Fatalf("dashboardMemColor(90) = %v, want #e06c75 (shared red danger color)", got)
	}
}

// TestDashboardNetGraphColorBoundaries checks dashboardNetGraphColor's
// anchor points land on Net's own blue identity color and the same
// byteLevel-matching landmarks (32MB/128MB/512MB) the old flat-band
// version used, so a Net sparkline lands in the same green→red gradient
// family as CPU/Memory rather than a scale unique to network traffic —
// but interpolated continuously (in log-byte space) between them.
func TestDashboardNetGraphColorBoundaries(t *testing.T) {
	cases := []struct {
		value uint64
		want  lipgloss.Color
	}{
		{1 << 20, "#8aadf4"},
		{32 << 20, "#e8c170"},
		{128 << 20, "#edad75"},
		{512 << 20, "#e06c75"},
		{1 << 30, "#e06c75"},
	}
	for _, c := range cases {
		if got := dashboardNetGraphColor(c.value); got != c.want {
			t.Fatalf("dashboardNetGraphColor(%v) = %v, want %v", c.value, got, c.want)
		}
	}
	if dashboardNetGraphColor(2<<20) == dashboardNetGraphColor(16<<20) {
		t.Fatalf("dashboardNetGraphColor(2MB) and (16MB) are identical, want a continuous gradient between the 1MB and 32MB landmarks")
	}
}

// TestDashboardMemMeterSqrtScaling is the regression test for the
// redesign's own worked examples: 0.4% and 2.7% utilization must land on
// 1 and 2 filled cells of a 10-wide meter respectively — a plain linear
// pct/100 scale would round both down to 0, indistinguishable from true
// zero usage and useless for comparing healthy containers against each
// other (see dashboardMemMeter's doc comment).
func TestDashboardMemMeterSqrtScaling(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	cases := []struct {
		pct    float64
		filled int
	}{
		{0, 0},
		{0.4, 1},
		{2.7, 2},
		{100, 10},
	}
	for _, c := range cases {
		out := ansi.Strip(dashboardMemMeter(renderer, defaultSettings(), c.pct, true, 10, "#000000"))
		if got := strings.Count(out, "█"); got != c.filled {
			t.Fatalf("dashboardMemMeter(%v%%) filled = %d (%q), want %d", c.pct, got, out, c.filled)
		}
		if got := len([]rune(out)); got != 10 {
			t.Fatalf("dashboardMemMeter(%v%%) width = %d, want 10", c.pct, got)
		}
	}
}

// TestDashboardMemMeterNoLimitIsDashedNotRed checks the "unknown, not
// necessarily bad" fallback: a container with no reported memory limit
// gets a dim dashed meter, never a threshold color guessed from nothing.
func TestDashboardMemMeterNoLimitIsDashedNotRed(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	out := ansi.Strip(dashboardMemMeter(renderer, defaultSettings(), 0, false, 10, "#000000"))
	if out != strings.Repeat("─", 10) {
		t.Fatalf("dashboardMemMeter(hasLimit=false) = %q, want 10 dashes", out)
	}
}

// TestDashboardMemMeterGaugeStyleUsesGaugeBar checks that choosing the
// Gauge Graph style in Settings actually changes the Dashboard's memory
// meter shape — a single proportional "━"/"╸"/"─" bar, the same look
// graphStyleGauge gives every stat row in the single-container Stats pane
// — rather than the block meter's "█"/"░" cells.
func TestDashboardMemMeterGaugeStyleUsesGaugeBar(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	settings := defaultSettings()
	settings.GraphStyle = graphStyleGauge

	out := ansi.Strip(dashboardMemMeter(renderer, settings, 50, true, 10, "#000000"))
	if strings.ContainsAny(out, "█░") {
		t.Fatalf("dashboardMemMeter(gauge style) = %q, want the gauge bar's ━/╸/─ glyphs, not the block meter's █/░", out)
	}
	if !strings.ContainsAny(out, "━╸") {
		t.Fatalf("dashboardMemMeter(gauge style) = %q, want at least one ━ or ╸ gauge glyph", out)
	}
}

// TestDashboardSparkGaugeStyleUsesGaugeBar is TestDashboardMemMeterGaugeStyleUsesGaugeBar's
// counterpart for the CPU/network sparkline shape.
func TestDashboardSparkGaugeStyleUsesGaugeBar(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	settings := defaultSettings()
	settings.GraphStyle = graphStyleGauge

	graph := statGraph{values: []float64{10, 20, 50}, maxValue: 100}
	flatColor := func(float64) lipgloss.Color { return "#7dcfff" }
	out := ansi.Strip(dashboardSpark(renderer, settings, graph, flatColor, 10, "#000000"))
	if strings.ContainsAny(out, "▁▂▃▄▅▆▇█") {
		t.Fatalf("dashboardSpark(gauge style) = %q, want the gauge bar's ━/╸/─ glyphs, not per-sample block glyphs", out)
	}
	if !strings.ContainsAny(out, "━╸") {
		t.Fatalf("dashboardSpark(gauge style) = %q, want at least one ━ or ╸ gauge glyph for a nonzero value", out)
	}
}

// TestDashboardSparkHonorsGraphStyleGlyphSet checks that non-gauge Graph
// styles change the sparkline's glyph set the same way they do for the
// single-container Stats pane's own sparkline (graphGlyphs is the exact
// function both paths call) — Braille's glyphs are a completely disjoint
// Unicode block from Blocks/Bars/the default Wave, so this is an
// unambiguous signal the setting is actually being consulted.
func TestDashboardSparkHonorsGraphStyleGlyphSet(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	graph := statGraph{values: []float64{10, 90}, maxValue: 100}

	blocks := defaultSettings()
	blocks.GraphStyle = graphStyleBlocks
	braille := defaultSettings()
	braille.GraphStyle = graphStyleBraille

	flatColor := func(float64) lipgloss.Color { return "#7dcfff" }
	blocksOut := ansi.Strip(dashboardSpark(renderer, blocks, graph, flatColor, 10, "#000000"))
	brailleOut := ansi.Strip(dashboardSpark(renderer, braille, graph, flatColor, 10, "#000000"))
	if strings.ContainsAny(blocksOut, "⣀⣤⣶⣿") {
		t.Fatalf("dashboardSpark(blocks style) = %q, want block glyphs (▁-█), not Braille", blocksOut)
	}
	if !strings.ContainsAny(brailleOut, "⣀⣤⣶⣿") {
		t.Fatalf("dashboardSpark(braille style) = %q, want at least one Braille glyph (⣀⣤⣶⣿)", brailleOut)
	}
}

// TestDashboardSparkColorsEachGlyphByItsOwnValue is the regression test
// for a spike getting flattened to whatever color the *latest* sample
// grades to: a historical spike sitting among quiet neighbors must render
// its own glyph in its own (hotter) color, not the line's single most
// recent value's color — see dashboardSpark's doc comment for why that's
// safe now that colorFor grades on absolute thresholds.
func TestDashboardSparkColorsEachGlyphByItsOwnValue(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	graph := statGraph{values: []float64{2, 2, 95, 2}, maxValue: 100}

	out := dashboardSpark(renderer, defaultSettings(), graph, dashboardGraphColor, 10, "#000000")

	sgrFor := func(color lipgloss.Color) string {
		rendered := lipgloss.NewStyle().Foreground(color).Render("x")
		return rendered[:strings.Index(rendered, "x")]
	}
	redSGR := sgrFor("#e06c75")
	greenSGR := sgrFor("#80c990")

	if !strings.Contains(out, redSGR) {
		t.Fatalf("dashboardSpark output = %q, want the spike sample's own red foreground SGR (%q)", out, redSGR)
	}
	if !strings.Contains(out, greenSGR) {
		t.Fatalf("dashboardSpark output = %q, want a quiet neighbor sample's own green foreground SGR (%q)", out, greenSGR)
	}
}

// TestDashboardGraphColorSettingDoesNotAffectDashboardColors is the
// regression test for the whole point of dashboardThresholdColor:
// changing settings.GraphColor (which switches the single-container Stats
// pane between its relative heat gradient, a flat per-metric color, and
// monochrome) must not change what color the Dashboard's memory meter
// picks for a given percentage — that color comes from the absolute
// dashboardWarnPct/dashboardCritPct cutoffs only.
func TestDashboardGraphColorSettingDoesNotAffectDashboardColors(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})

	var outputs []string
	for _, mode := range []graphColorMode{graphColorGradient, graphColorMetric, graphColorMono} {
		settings := defaultSettings()
		settings.GraphColor = mode
		outputs = append(outputs, dashboardMemMeter(renderer, settings, 2.7, true, 10, "#000000"))
	}
	for i := 1; i < len(outputs); i++ {
		if outputs[i] != outputs[0] {
			t.Fatalf("dashboardMemMeter output changed across GraphColor modes: %q vs %q — want it independent of that setting", outputs[0], outputs[i])
		}
	}
}

// TestDashboardNetSparkQuietWhenZero is the regression test for goal #4's
// "zero traffic should appear visually quiet": a container with no
// network history, or whose *entire* visible history is zero, must render
// a flat dashed line rather than a row of "▁" glyphs indistinguishable
// from genuinely tiny (but nonzero) traffic.
func TestDashboardNetSparkQuietWhenZero(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})

	empty := ansi.Strip(dashboardNetSpark(renderer, defaultSettings(), statGraph{}, 6, "#000000"))
	if empty != strings.Repeat("─", 6) {
		t.Fatalf("dashboardNetSpark(no history) = %q, want 6 dashes", empty)
	}

	allZero := ansi.Strip(dashboardNetSpark(renderer, defaultSettings(), statGraph{values: []float64{0, 0}, maxValue: 100}, 6, "#000000"))
	if allZero != strings.Repeat("─", 6) {
		t.Fatalf("dashboardNetSpark(all zero) = %q, want 6 dashes", allZero)
	}

	active := ansi.Strip(dashboardNetSpark(renderer, defaultSettings(), statGraph{values: []float64{10, 40}, maxValue: 100}, 6, "#000000"))
	if active == strings.Repeat("─", 6) {
		t.Fatalf("dashboardNetSpark(latest=40) = %q, want real sparkline glyphs, not dashes", active)
	}
}

// TestDashboardNetSparkStaysDrawnThroughAMomentaryZeroSample is the
// regression test for the sparkline vanishing and redrawing every time a
// single poll landed on exactly zero bytes: a latest sample of zero with
// real nonzero traffic earlier in the same window must keep rendering the
// scrolling sparkline, not fall back to the flat dashed placeholder —
// that fallback is reserved for a window that's genuinely all zero (see
// TestDashboardNetSparkQuietWhenZero and dashboardNetSpark's own doc
// comment).
func TestDashboardNetSparkStaysDrawnThroughAMomentaryZeroSample(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})

	out := ansi.Strip(dashboardNetSpark(renderer, defaultSettings(), statGraph{values: []float64{40, 0}, maxValue: 100}, 6, "#000000"))
	if out == strings.Repeat("─", 6) {
		t.Fatalf("dashboardNetSpark(latest=0, earlier=40) = %q, want real sparkline glyphs (a momentary zero shouldn't blank a line with real recent activity)", out)
	}
}

// TestTruncateEllipsisShortensLongNames checks the ellipsis truncation
// dashboardRow uses for container names — short() elsewhere in this file
// truncates silently, which reads as a rendering bug when applied to a
// human-readable name rather than a compact ID.
func TestTruncateEllipsisShortensLongNames(t *testing.T) {
	if got := truncateEllipsis("short", 10); got != "short" {
		t.Fatalf("truncateEllipsis(short name) = %q, want unchanged", got)
	}
	if got := truncateEllipsis("a-very-long-container-name", 10); got != "a-very-lo…" {
		t.Fatalf("truncateEllipsis(long name) = %q, want \"a-very-lo…\"", got)
	}
	if got := len([]rune(truncateEllipsis("a-very-long-container-name", 10))); got != 10 {
		t.Fatalf("truncateEllipsis width = %d, want 10", got)
	}
}

// TestDashboardColumnsForTiers checks dashboardTierFor's breakpoints and
// that each tier drops exactly the visualizations the redesign's own
// narrow/medium/wide fallbacks describe: narrow shows bare numbers only,
// medium adds a CPU sparkline and memory meter but keeps network
// combined, wide additionally splits network into two trended columns.
func TestDashboardColumnsForTiers(t *testing.T) {
	model := testModel()
	cases := []struct {
		width        int
		wantTier     dashboardTier
		wantSpark    bool
		wantMeter    bool
		wantSplitNet bool
	}{
		{66, dashboardTierNarrow, false, false, false},
		{82, dashboardTierNarrow, false, false, false},
		{102, dashboardTierMedium, true, true, false},
		{132, dashboardTierWide, true, true, true},
		{172, dashboardTierWide, true, true, true},
	}
	for _, c := range cases {
		cols := model.dashboardColumnsFor(c.width)
		if cols.tier != c.wantTier {
			t.Fatalf("width=%d: tier = %v, want %v", c.width, cols.tier, c.wantTier)
		}
		if (cols.cpuSparkW > 0) != c.wantSpark {
			t.Fatalf("width=%d: cpuSparkW=%d, want spark=%v", c.width, cols.cpuSparkW, c.wantSpark)
		}
		if (cols.memMeterW > 0) != c.wantMeter {
			t.Fatalf("width=%d: memMeterW=%d, want meter=%v", c.width, cols.memMeterW, c.wantMeter)
		}
		if cols.splitNet != c.wantSplitNet {
			t.Fatalf("width=%d: splitNet=%v, want %v", c.width, cols.splitNet, c.wantSplitNet)
		}
	}
}

// TestDashboardRowNeverExceedsWidth renders real rows (selected and not)
// across the tier breakpoints and a range of realistic terminal widths
// and checks the unpadded row never exceeds its budget — dashboardPadLine
// only ever adds columns, so an overrun here would corrupt the panel's
// right border on a real terminal. Complements
// TestDashboardRowAndSummaryLineNeverFallShortOfContentWidth, which only
// checks the padded (never-shrinks) side of the same invariant.
func TestDashboardRowNeverExceedsWidth(t *testing.T) {
	model := testModel()
	model.appendStats(domain.ContainerStats{ID: domain.ResourceID{Host: "local", ID: "1"}, CPUPercent: 100, MemoryUsage: 999, MemoryLimit: 1000, NetworkRx: 999999999, NetworkTx: 999999999})
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})

	for _, width := range []int{66, 82, 90, 102, 110, 132, 140, 172, 180, 300} {
		for _, selected := range []bool{false, true} {
			for _, ctr := range model.snapshotContainers() {
				if !ctr.IsRunning() {
					continue
				}
				row := model.dashboardRow(renderer, ctr, width, selected)
				if got := len([]rune(ansi.Strip(row))); got > width {
					t.Fatalf("width=%d selected=%v: row visible width = %d, want <= %d\nrow: %q", width, selected, got, width, ansi.Strip(row))
				}
			}
			header := model.dashboardHeaderRow(renderer, width)
			if got := len([]rune(ansi.Strip(header))); got > width {
				t.Fatalf("width=%d: header visible width = %d, want <= %d", width, got, width)
			}
		}
	}
}

// TestDashboardSummaryLineNeverExceedsWidthWhenNarrow checks the header's
// graceful-degradation path: on a very narrow panel it drops CPU/RAM/
// network before overflowing, and status counts (goal #1's top priority)
// are always present.
func TestDashboardSummaryLineNeverExceedsWidthWhenNarrow(t *testing.T) {
	model := testModel()
	model.appendStats(domain.ContainerStats{ID: domain.ResourceID{Host: "local", ID: "1"}, CPUPercent: 41.5, MemoryUsage: 100, MemoryLimit: 200})
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	summary := model.fleetSummary()

	for _, width := range []int{20, 40, 66} {
		line := ansi.Strip(model.dashboardSummaryLine(renderer, summary, width))
		if got := len([]rune(line)); got > width {
			t.Fatalf("width=%d: summary line width = %d, want <= %d\nline: %q", width, got, width, line)
		}
	}

	full := ansi.Strip(model.dashboardSummaryLine(renderer, summary, 66))
	if !strings.Contains(full, "RUNNING") {
		t.Fatalf("summary at width=66 missing the always-shown running count:\n%s", full)
	}
}

// TestDashboardProblemsRowWarnsOnlyWhenStopped checks goal #7: a warning
// row with the "p" action only appears when something's actually stopped,
// and a healthy fleet gets a quiet all-clear line instead.
func TestDashboardProblemsRowWarnsOnlyWhenStopped(t *testing.T) {
	original := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(original)

	model := testModel()
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})

	warn := ansi.Strip(model.dashboardProblemsRow(renderer, 3, 0, 80))
	if !strings.Contains(warn, "⚠ 3 containers need attention") || !strings.Contains(warn, "View problems") {
		t.Fatalf("dashboardProblemsRow(3) = %q, want a warning naming the count plus the View problems action", warn)
	}

	singular := ansi.Strip(model.dashboardProblemsRow(renderer, 1, 0, 80))
	if !strings.Contains(singular, "1 container needs attention") {
		t.Fatalf("dashboardProblemsRow(1) = %q, want singular \"container needs\"", singular)
	}

	healthy := ansi.Strip(model.dashboardProblemsRow(renderer, 0, 0, 80))
	if !strings.Contains(healthy, "All monitored containers healthy") || strings.Contains(healthy, "⚠") {
		t.Fatalf("dashboardProblemsRow(0) = %q, want a quiet all-clear line with no warning glyph", healthy)
	}

	// The warning line breathes on the pulse clock: two different frames
	// (a sine trough vs. peak) must pick visibly different amber shades,
	// while the healthy all-clear line stays byte-identical frame to frame.
	warnLo := model.dashboardProblemsRow(renderer, 3, 18, 80) // sine trough → dim amber
	warnHi := model.dashboardProblemsRow(renderer, 3, 6, 80)  // sine peak → bright amber
	if warnLo == warnHi {
		t.Fatalf("dashboardProblemsRow warning did not change between pulse trough and peak:\n%q", warnLo)
	}
	if model.dashboardProblemsRow(renderer, 0, 6, 80) != model.dashboardProblemsRow(renderer, 0, 18, 80) {
		t.Fatal("dashboardProblemsRow all-clear line must not animate")
	}
}

// TestDashboardKeyboardSelectionMovesAndOpens checks goal #6's keyboard
// navigation end to end: j/k move the highlighted row within the
// Dashboard's own visible list, and enter closes the overlay and focuses
// the inspector on that row's container via the same moveTreeCursorTo/
// loadSelectedCmd path selectProblem already uses.
func TestDashboardKeyboardSelectionMovesAndOpens(t *testing.T) {
	model := testModel()
	model.width, model.height = 120, 34
	model.rows = model.buildRows()
	model.overlay = overlayDashboard
	model.dashboardCursor = 0

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	next := updated.(Model)
	// testModel's fixture has exactly one running container (radarr-1), so
	// moving down must clamp at 0 rather than going out of bounds.
	if next.dashboardCursor != 0 {
		t.Fatalf("dashboardCursor after j with 1 running container = %d, want clamped to 0", next.dashboardCursor)
	}

	updated, cmd := next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(Model)
	if next.overlay != overlayNone {
		t.Fatalf("overlay after enter = %v, want overlayNone", next.overlay)
	}
	if next.focus != paneInspector {
		t.Fatalf("focus after enter = %v, want paneInspector", next.focus)
	}
	if next.selectedID.ID != "1" {
		t.Fatalf("selectedID after enter = %+v, want container 1 (radarr-1)", next.selectedID)
	}
	if cmd == nil {
		t.Fatal("enter on a dashboard row returned a nil cmd, want loadSelectedCmd's fetch")
	}
}

// TestDashboardPKeyOpensProblems checks goal #7: "p" continues to open
// Problems even while the Dashboard overlay has focus.
func TestDashboardPKeyOpensProblems(t *testing.T) {
	model := testModel()
	model.overlay = overlayDashboard

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	next := updated.(Model)
	if next.overlay != overlayNone {
		t.Fatalf("overlay after p = %v, want overlayNone", next.overlay)
	}
	if next.focus != paneActivity || next.mode != activityProblems {
		t.Fatalf("focus/mode after p = %v/%v, want paneActivity/activityProblems", next.focus, next.mode)
	}
}

// dashboardFindRowLine returns the screen line index (0-based, matching
// tea.MouseMsg.Y) of the first line containing needle *inside the
// Dashboard overlay's own box* — the underlying Problems/tree panes the
// overlay is drawn on top of can contain the same text (e.g. a container
// name also appears in the Problems list behind the overlay), so the
// search starts at the box's own top border rather than the top of the
// whole screen.
func dashboardFindRowLine(t *testing.T, view, needle string) int {
	t.Helper()
	lines := strings.Split(view, "\n")
	boxTop := -1
	for i, line := range lines {
		if strings.Contains(ansi.Strip(line), "whatthedock · dashboard") {
			boxTop = i
			break
		}
	}
	if boxTop < 0 {
		t.Fatal("rendered view missing the dashboard overlay's own box")
	}
	for i := boxTop; i < len(lines); i++ {
		if strings.Contains(ansi.Strip(lines[i]), needle) {
			return i
		}
	}
	t.Fatalf("rendered view missing %q inside the dashboard overlay", needle)
	return -1
}

// TestDashboardResizeDoesNotPanicWithStaleCursor checks goal #12's
// "terminal resizing does not panic": a cursor pointing well past what a
// shrunken terminal can show must not index out of range — dashboardRow
// selection highlighting and the mouse hit-test both key off the
// overlay's own currently-visible list (dashboardBodyPlan), not a fixed
// index, so a resize that shrinks the visible budget just changes what
// "visible" means rather than leaving a dangling reference.
func TestDashboardResizeDoesNotPanicWithStaleCursor(t *testing.T) {
	model := testModel()
	model.width, model.height = 180, 50
	model.overlay = overlayDashboard
	for i := 0; i < 30; i++ {
		id := "extra-" + string(rune('a'+i))
		model.snapshot.Standalone = append(model.snapshot.Standalone, domain.Container{
			ID: domain.ResourceID{Host: "local", ID: id}, Name: "extra-" + id, State: domain.StateRunning,
		})
	}
	model.dashboardCursor = 29

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("resize with a stale dashboardCursor panicked: %v", r)
		}
	}()
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 90, Height: 10})
	next := updated.(Model)
	_ = next.View()
	next.dashboardMoveCursor(1)
}

// TestDashboardHitTestMatchesRenderedRow renders a real Dashboard overlay
// and clicks the screen coordinates its own View() output puts a known
// container's row at, rather than hand-deriving expected coordinates —
// see dashboardHitTest's doc comment for why this is the regression test
// that actually matters if tideui's soft-panel chrome ever changes shape.
func TestDashboardHitTestMatchesRenderedRow(t *testing.T) {
	model := testModel()
	model.width, model.height = 120, 34
	model.overlay = overlayDashboard
	model.appendStats(domain.ContainerStats{ID: domain.ResourceID{Host: "local", ID: "1"}, CPUPercent: 41.5})

	targetY := dashboardFindRowLine(t, model.View(), "radarr-1")

	row, isStatusRow, ok := model.dashboardHitTest(tea.MouseMsg{X: model.width / 2, Y: targetY, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if !ok || isStatusRow || row != 0 {
		t.Fatalf("dashboardHitTest(Y=%d) = (row=%d, isStatusRow=%v, ok=%v), want (0, false, true)", targetY, row, isStatusRow, ok)
	}
}

// TestDashboardMouseClickRowSelectsAndOpens is the mouse counterpart to
// TestDashboardKeyboardSelectionMovesAndOpens: a left click on a
// container's rendered row selects and opens it in one action, the same
// as pressing enter after navigating there with the keyboard.
func TestDashboardMouseClickRowSelectsAndOpens(t *testing.T) {
	model := testModel()
	model.width, model.height = 120, 34
	model.rows = model.buildRows()
	model.overlay = overlayDashboard
	model.appendStats(domain.ContainerStats{ID: domain.ResourceID{Host: "local", ID: "1"}, CPUPercent: 41.5})

	targetY := dashboardFindRowLine(t, model.View(), "radarr-1")

	updated, cmd := model.Update(tea.MouseMsg{X: model.width / 2, Y: targetY, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	next := updated.(Model)
	if next.overlay != overlayNone {
		t.Fatalf("overlay after clicking a row = %v, want overlayNone", next.overlay)
	}
	if next.selectedID.ID != "1" {
		t.Fatalf("selectedID after clicking radarr-1's row = %+v, want container 1", next.selectedID)
	}
	if cmd == nil {
		t.Fatal("clicking a dashboard row returned a nil cmd, want loadSelectedCmd's fetch")
	}
}

// TestDashboardMouseClickStatusRowOpensProblems checks the bottom status
// row is clickable when it's a warning (goal #7) — clicking it takes the
// same path as pressing "p".
func TestDashboardMouseClickStatusRowOpensProblems(t *testing.T) {
	model := testModel()
	model.width, model.height = 120, 34
	model.overlay = overlayDashboard

	targetY := dashboardFindRowLine(t, model.View(), "View problems")

	updated, _ := model.Update(tea.MouseMsg{X: model.width / 2, Y: targetY, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	next := updated.(Model)
	if next.overlay != overlayNone {
		t.Fatalf("overlay after clicking the status row = %v, want overlayNone", next.overlay)
	}
	if next.focus != paneActivity || next.mode != activityProblems {
		t.Fatalf("focus/mode after clicking status row = %v/%v, want paneActivity/activityProblems", next.focus, next.mode)
	}
}
