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
	if !strings.Contains(view, "1 !") {
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
			row = ansi.Strip(model.dashboardRow(renderer, ctr, width))
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
			row := dashboardPadLine(renderer, model.dashboardRow(renderer, ctr, width), width)
			if got := len([]rune(ansi.Strip(row))); got != width {
				t.Fatalf("width=%d: padded row visible width = %d, want exactly %d\nrow: %q", width, got, width, ansi.Strip(row))
			}
		}
		summary := dashboardPadLine(renderer, model.dashboardSummaryLine(renderer, model.fleetSummary()), width)
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
			row = model.dashboardRow(renderer, ctr, 120)
			break
		}
	}
	if row == "" {
		t.Fatal("no running container found to render a row for")
	}
	if !dashboardBgThenFgSGR.MatchString(row) {
		t.Fatalf("dashboardRow's status glyph has no explicit background SGR immediately preceding its foreground SGR — it's relying on a neighboring segment's background to carry over, which it doesn't:\n%q", row)
	}

	summary := model.dashboardSummaryLine(renderer, dashboardSummary{counts: map[string]int{"●": 3}})
	if !dashboardBgThenFgSGR.MatchString(summary) {
		t.Fatalf("dashboardSummaryLine's count+glyph has no explicit background SGR immediately preceding its foreground SGR:\n%q", summary)
	}
}
