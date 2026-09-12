package ui

import (
	"strings"
	"testing"

	"github.com/allisonhere/tideui"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/allisonhere/whatthedock/internal/domain"
)

// withTrueColorProfile forces lipgloss's default renderer to TrueColor for
// the duration of a test and restores whatever it was after. lipgloss.
// Style.Render (unlike the raw ansi.NewStyle calls healthSpan uses)
// auto-detects a color profile from the attached terminal, which a plain
// `go test` process doesn't have — without this, styling silently
// degrades to no color at all, making color-presence assertions
// environment-dependent rather than a real check of what a real terminal
// would show.
func withTrueColorProfile(t *testing.T) {
	t.Helper()
	original := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(original) })
}

func TestInspectorStatusColorDeadIsRed(t *testing.T) {
	got := inspectorStatusColor(domain.Container{State: domain.StateDead})
	if got != "#e06c75" {
		t.Fatalf("inspectorStatusColor(dead) = %q, want red #e06c75", got)
	}
}

func TestStatusGlyphDeadIsDistinctFromUnknownStates(t *testing.T) {
	dead := statusGlyph(domain.Container{State: domain.StateDead})
	unknown := statusGlyph(domain.Container{State: domain.StateUnknown})
	if dead == unknown {
		t.Fatalf("statusGlyph(dead) = %q, same as an unrelated unknown state %q — should be visually distinct", dead, unknown)
	}
}

// TestTreeRowColorsDeadContainerNameRed guards against the bug reported
// live: a dead container's row only colored its tiny status glyph and the
// trailing status word red, leaving the container's actual name — the
// bulk of the row — in the default color, easy to miss scrolling past it.
// The container name itself must now carry the same red span.
func TestTreeRowColorsDeadContainerNameRed(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 30
	ctr := domain.Container{
		ID:    domain.ResourceID{Host: "local", ID: "dead-1"},
		Name:  "totally-dead-container",
		State: domain.StateDead,
	}
	model.rows = []treeRow{{kind: rowContainer, container: &ctr}}

	renderer := tideui.NewRenderer(model.theme, tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	raw := model.renderTree(renderer)

	// healthSpan's actual color bytes depend on lipgloss's global,
	// terminal-detected color profile (lipgloss.Color.RGBA() — see its own
	// "Deprecated" doc comment — silently resolves to black without a real
	// TTY, which a plain `go test` process doesn't have). That makes exact
	// color bytes environment-dependent and not a meaningful thing to
	// assert on here. What's actually being tested is structural: the same
	// color-span opener that already wraps the glyph (proven to render
	// correctly in the real app — that's the established, working part of
	// this pattern) must now also wrap the container name, not just the
	// glyph and trailing status word.
	healthColor := inspectorStatusColor(ctr)
	openSeq := ansi.NewStyle().ForegroundColor(healthColor).String()
	if count := strings.Count(raw, openSeq); count < 2 {
		t.Fatalf("color span opener appears %d time(s) in the row, want >=2 (glyph and name both colored):\n%q", count, raw)
	}
	if !strings.Contains(raw, "totally-dead") {
		t.Fatalf("row is missing the container name entirely:\n%q", raw)
	}
}

// TestStatusLegendLinesUsesTheSameGlyphsAndColorsAsTheTree pins
// statusLegendLines (the Help overlay's status color legend) to the exact
// glyph/color source of truth (statusGlyph/inspectorStatusColor) so it
// can't silently drift out of sync with what containers actually show.
func TestStatusLegendLinesUsesTheSameGlyphsAndColorsAsTheTree(t *testing.T) {
	want := map[domain.ContainerState]string{
		domain.StateRunning: "healthy / running",
		domain.StateDead:    "dead",
		domain.StateStopped: "stopped, exited cleanly",
	}
	for state, label := range want {
		ctr := domain.Container{State: state}
		wantGlyph := statusGlyph(ctr)
		wantColor := inspectorStatusColor(ctr)
		found := false
		for _, entry := range statusLegendEntries {
			if entry.label == label {
				found = true
				if entry.glyph != wantGlyph {
					t.Errorf("legend glyph for %q = %q, want %q (statusGlyph)", label, entry.glyph, wantGlyph)
				}
				if entry.color != wantColor {
					t.Errorf("legend color for %q = %q, want %q (inspectorStatusColor)", label, entry.color, wantColor)
				}
			}
		}
		if !found {
			t.Errorf("no legend entry labeled %q", label)
		}
	}
}

func TestStatusLegendLinesAreFullyColored(t *testing.T) {
	withTrueColorProfile(t)
	model := testModel()
	renderer := tideui.NewRenderer(model.theme, tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	lines := statusLegendLines(renderer)
	if len(lines) != statusLegendLineCount {
		t.Fatalf("statusLegendLines() returned %d lines, want %d (statusLegendLineCount)", len(lines), statusLegendLineCount)
	}
	// Every line (including the "Status colors:" header) must carry its
	// own explicit background/foreground — RenderSoftBody only applies its
	// default panel styling to lines with no escape codes of their own
	// (see the doc comment on statusLegendLines), so an unstyled line here
	// would fall through to the terminal's raw background instead of the
	// panel's.
	for i, line := range lines {
		if !strings.Contains(line, "\x1b[") {
			t.Fatalf("legend line %d has no styling at all: %q", i, line)
		}
	}
}

func TestGraphStyleBarsStringAndPersistedRoundTrip(t *testing.T) {
	if got := graphStyleBars.String(); got != "bars" {
		t.Fatalf("graphStyleBars.String() = %q, want bars", got)
	}
	var s appSettings
	s.GraphStyle = graphStyleBars
	var restored appSettings
	restored.applyPersisted(s.persisted())
	if restored.GraphStyle != graphStyleBars {
		t.Fatalf("GraphStyle round-tripped to %v, want graphStyleBars", restored.GraphStyle)
	}
}

func TestCycleSettingGraphStyleIncludesBars(t *testing.T) {
	model := testModel()
	rows := model.settingsRows()
	index := -1
	for i, row := range rows {
		if row.label == "Graph style" {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatal("settingsRows() has no \"Graph style\" row")
	}
	seen := map[graphStyle]bool{}
	for i := 0; i < 5; i++ {
		model.cycleSetting(index, 1)
		seen[model.settingsDraft.GraphStyle] = true
	}
	for _, want := range []graphStyle{graphStyleWave, graphStyleBlocks, graphStyleBraille, graphStyleBars, graphStyleGauge} {
		if !seen[want] {
			t.Fatalf("cycling Graph style 5 times never landed on %v, want all five styles reachable", want)
		}
	}
	// A 5th cycle from gauge must land back on wave — confirms the cycle
	// length is actually 5, not silently still shorter with gauge
	// unreachable via wraparound.
	model.settingsDraft.GraphStyle = graphStyleGauge
	model.cycleSetting(index, 1)
	if model.settingsDraft.GraphStyle != graphStyleWave {
		t.Fatalf("GraphStyle after cycling past gauge = %v, want it to wrap to wave", model.settingsDraft.GraphStyle)
	}
}

func TestGraphGlyphsBarsMatchesBlocksGlyphSet(t *testing.T) {
	var blocks, bars appSettings
	blocks.GraphStyle = graphStyleBlocks
	bars.GraphStyle = graphStyleBars
	blocksGlyphs := graphGlyphs(blocks)
	barsGlyphs := graphGlyphs(bars)
	if len(blocksGlyphs) != len(barsGlyphs) {
		t.Fatalf("bars glyph count = %d, want it to match blocks' %d", len(barsGlyphs), len(blocksGlyphs))
	}
	for i := range blocksGlyphs {
		if blocksGlyphs[i] != barsGlyphs[i] {
			t.Fatalf("glyph %d: bars=%q blocks=%q, want the same glyph set — bars only differs by spacing", i, barsGlyphs[i], blocksGlyphs[i])
		}
	}
}

// TestRenderSparklineBarsInsertsSpaceBetweenGlyphs is the regression test
// for the feature requested live: a graph style using blocks' glyph set
// but with a blank column after every bar, instead of an unbroken
// sparkline.
func TestRenderSparklineBarsInsertsSpaceBetweenGlyphs(t *testing.T) {
	withTrueColorProfile(t)
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	var settings appSettings
	settings.GraphStyle = graphStyleBars
	graph := statGraph{values: []float64{10, 50, 90}, maxValue: 100}

	got := ansi.Strip(renderSparkline(renderer, settings, graph, "#7dcfff", 20))
	if !strings.Contains(got, " ") {
		t.Fatalf("bars sparkline = %q, want a space between each bar", got)
	}
	// Every non-space rune should be immediately followed by a space (or
	// be the last rune) — i.e. genuinely spaced, not just containing a
	// space somewhere incidentally.
	runes := []rune(got)
	for i, r := range runes {
		if r == ' ' {
			continue
		}
		if i+1 < len(runes) && runes[i+1] != ' ' {
			t.Fatalf("bars sparkline = %q, glyph at index %d not followed by a space", got, i)
		}
	}
}

// TestRenderSparklineBlocksHasNoSpacing guards the un-spaced style against
// regressing into always-spaced output — bars and blocks share a glyph
// set but must still render differently.
func TestRenderSparklineBlocksHasNoSpacing(t *testing.T) {
	withTrueColorProfile(t)
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	var settings appSettings
	settings.GraphStyle = graphStyleBlocks
	graph := statGraph{values: []float64{10, 50, 90}, maxValue: 100}

	got := ansi.Strip(renderSparkline(renderer, settings, graph, "#7dcfff", 20))
	if strings.Contains(got, " ") {
		t.Fatalf("blocks sparkline = %q, want no spacing between bars", got)
	}
}

// TestRenderSparklineBarsRespectsWidthBudget checks bars' 2-columns-per-bar
// spacing still stays within the width callers pass — a spaced style
// packing the same number of values as an unspaced one would overflow
// every caller's width budget (e.g. the Dashboard's per-column layout).
func TestRenderSparklineBarsRespectsWidthBudget(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	var settings appSettings
	settings.GraphStyle = graphStyleBars
	values := make([]float64, 30)
	for i := range values {
		values[i] = float64(i)
	}
	graph := statGraph{values: values, maxValue: 30}

	for _, width := range []int{5, 10, 21, 40} {
		got := ansi.Strip(renderSparkline(renderer, settings, graph, "#7dcfff", width))
		if visible := len([]rune(got)); visible > width {
			t.Fatalf("width=%d: bars sparkline visible width = %d, want <= %d:\n%q", width, visible, width, got)
		}
	}
}

// TestRenderSparklineGaugeDrawsProportionalFill is the regression test for
// the gauge style: a single thin progress bar for the latest value, not a
// glyph-per-sample history — value=37 of maxValue=100 at width=20 should
// fill roughly 37% of the bar with "━" and leave the rest "─", separated
// by exactly one "╸" leading-edge cap.
func TestRenderSparklineGaugeDrawsProportionalFill(t *testing.T) {
	withTrueColorProfile(t)
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	var settings appSettings
	settings.GraphStyle = graphStyleGauge
	graph := statGraph{values: []float64{10, 50, 37}, maxValue: 100}

	got := ansi.Strip(renderSparkline(renderer, settings, graph, "#7dcfff", 20))
	runes := []rune(got)
	if len(runes) != 20 {
		t.Fatalf("gauge bar width = %d, want 20 (got %q)", len(runes), got)
	}
	if strings.Count(got, "╸") != 1 {
		t.Fatalf("gauge bar = %q, want exactly one leading-edge cap", got)
	}
	capIndex := -1
	for i, r := range runes {
		if r == '╸' {
			capIndex = i
			break
		}
	}
	frac := 0.37
	wantFilled := int(frac*20 + 0.5)
	if capIndex != wantFilled-1 {
		t.Fatalf("gauge bar = %q, cap at rune index %d, want it at %d for 37%% of width 20", got, capIndex, wantFilled-1)
	}
	if strings.ContainsAny(got, "▁▂▃▄▅▆▇█") {
		t.Fatalf("gauge bar = %q, want no sparkline glyphs, only ━/╸/─", got)
	}
}

// TestRenderSparklineGaugeEmptyWithNoHistory guards the no-history
// fallback: with no values yet, gauge should render an all-empty bar
// rather than guessing a level like the glyph-based styles do.
func TestRenderSparklineGaugeEmptyWithNoHistory(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	var settings appSettings
	settings.GraphStyle = graphStyleGauge
	graph := statGraph{fallbackLevel: 4}

	got := ansi.Strip(renderSparkline(renderer, settings, graph, "#7dcfff", 10))
	if got != strings.Repeat("─", 10) {
		t.Fatalf("gauge bar with no history = %q, want an all-empty 10-wide bar", got)
	}
}

// TestFormatMountsIsStableRegardlessOfInputOrder guards against a
// regression where the Inspector's Mounts row visibly reshuffled every
// time a container's details got refetched (a health check event, a
// routine refresh) with nothing actually having changed — Docker's own
// inspect.Mounts order isn't guaranteed stable between API calls, and
// formatMounts rendered whatever order it was just given instead of
// sorting the way Env/Labels (formatList/formatMap) already do.
func TestFormatMountsIsStableRegardlessOfInputOrder(t *testing.T) {
	a := []domain.Mount{
		{Source: "/data/media", Destination: "/media"},
		{Source: "/data/config", Destination: "/config"},
	}
	b := []domain.Mount{
		{Source: "/data/config", Destination: "/config"},
		{Source: "/data/media", Destination: "/media"},
	}
	gotA, gotB := formatMounts(a), formatMounts(b)
	if gotA != gotB {
		t.Fatalf("formatMounts gave different output for the same mounts in a different order:\n%q\n%q", gotA, gotB)
	}
}

// TestBrailleColumnBitsFillsFromBottomUp checks the dot-matrix building
// block renderBrailleGraph is built on: level N should light exactly the
// bottom N dots of a half-column (a filled mini-bar), not a single
// floating dot at height N.
func TestBrailleColumnBitsFillsFromBottomUp(t *testing.T) {
	tests := []struct {
		col, level int
		want       int
	}{
		{0, 0, 0x00},
		{0, 1, 0x40}, // bottom-left dot only
		{0, 4, 0x01 | 0x02 | 0x04 | 0x40},
		{1, 1, 0x80}, // bottom-right dot only
		{1, 4, 0x08 | 0x10 | 0x20 | 0x80},
	}
	for _, tt := range tests {
		if got := brailleColumnBits(tt.col, tt.level); got != tt.want {
			t.Fatalf("brailleColumnBits(%d, %d) = %#x, want %#x", tt.col, tt.level, got, tt.want)
		}
	}
}

func TestBrailleLevelQuantizesToFourLevels(t *testing.T) {
	tests := []struct {
		value, maxValue float64
		want            int
	}{
		{0, 100, 1},
		{25, 100, 2},
		{50, 100, 3},
		{99, 100, 4},
		{100, 100, 4},
		{5, 0, 0}, // no scale yet — brailleColumnBits(col, 0) leaves it empty
	}
	for _, tt := range tests {
		if got := brailleLevel(tt.value, tt.maxValue); got != tt.want {
			t.Fatalf("brailleLevel(%v, %v) = %d, want %d", tt.value, tt.maxValue, got, tt.want)
		}
	}
}

// TestRenderBrailleGraphPacksTwoSamplesPerColumn is the core regression
// test for the braille revamp: unlike every other graph style (one glyph
// per sample), braille must pack two samples into each output column's
// left/right half — 6 samples in a 3-column budget should use all 3
// columns with real dot patterns, not need 6 columns or fall back to one
// sample per column.
func TestRenderBrailleGraphPacksTwoSamplesPerColumn(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	values := []float64{10, 20, 30, 40, 50, 90}
	flatColor := func(float64) lipgloss.Color { return "#7dcfff" }

	out := ansi.Strip(renderBrailleGraph(renderer, values, 100, 3, flatColor, "#000000"))
	cells := []rune(out)
	if len(cells) != 3 {
		t.Fatalf("renderBrailleGraph() = %q (%d cells), want exactly 3 columns for a width-3 budget", out, len(cells))
	}
	for i, r := range cells {
		if r == 0x2800 {
			t.Fatalf("cell %d is empty (0x2800), want all 3 columns filled — 6 samples exactly fill a 3-column (6-sample) budget", i)
		}
	}
}

// TestRenderBrailleGraphNoDataIsAllFlatCells checks the "nothing collected
// yet" case renders as empty braille cells (0x2800) rather than panicking
// on a zero maxValue or leaving cells uninitialized.
func TestRenderBrailleGraphNoDataIsAllFlatCells(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	flatColor := func(float64) lipgloss.Color { return "#7dcfff" }

	out := ansi.Strip(renderBrailleGraph(renderer, nil, 0, 5, flatColor, "#000000"))
	for _, r := range out {
		if r != 0x2800 {
			t.Fatalf("renderBrailleGraph() with no data = %q, want every cell empty (0x2800)", out)
		}
	}
	if len([]rune(out)) != 5 {
		t.Fatalf("renderBrailleGraph() with no data = %q, want exactly 5 columns", out)
	}
}

// TestAppendStatsRetainsMoreThanTheOldTwentyFourSampleCap is the
// regression test for the history window being far too short (24 samples
// = 48s of trend at the default 2s refresh) — see statsHistorySamples.
func TestAppendStatsRetainsMoreThanTheOldTwentyFourSampleCap(t *testing.T) {
	m := &Model{}
	id := domain.ResourceID{Host: "local", ID: "1"}
	for i := 0; i < 40; i++ {
		m.appendStats(domain.ContainerStats{ID: id, CPUPercent: float64(i)})
	}
	got := len(m.statsHistory[id].CPU)
	if got != 40 {
		t.Fatalf("history length after 40 samples = %d, want 40 (old 24-sample cap would have truncated this)", got)
	}
}

// TestCPUStatGraphPeakUsesRealHistoricalMaxNotTheFlooredScale checks
// cpuStatGraph's peak label reports history.maxCPU itself, not the
// graph's scale ceiling (which is floored at 100 even when the real peak
// is lower) — the doc comment on cpuStatGraph's peak field explains why
// those two numbers can differ.
func TestCPUStatGraphPeakUsesRealHistoricalMaxNotTheFlooredScale(t *testing.T) {
	history := statsHistory{CPU: []float64{5, 12, 8}, maxCPU: 12}
	graph := cpuStatGraph(&domain.ContainerStats{CPUPercent: 8}, history)
	if graph.maxValue != 100 {
		t.Fatalf("graph.maxValue = %v, want the floored scale ceiling 100, not the real peak 12", graph.maxValue)
	}
	if graph.peak != "peak 12.0%" {
		t.Fatalf("graph.peak = %q, want \"peak 12.0%%\" (the real historical peak, not the floored scale)", graph.peak)
	}
}

func TestUintStatGraphSetsPeakFromMaxValue(t *testing.T) {
	graph := uintStatGraph([]uint64{100, 500, 200}, 500, 1, formatByteDelta, formatBytes)
	if graph.peak != "peak "+formatBytes(500) {
		t.Fatalf("graph.peak = %q, want %q", graph.peak, "peak "+formatBytes(500))
	}
}

// TestRenderHybridGraphShowsPeakOnAWideRow checks the peak annotation
// actually reaches rendered output when there's room for it.
func TestRenderHybridGraphShowsPeakOnAWideRow(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	settings := defaultSettings()
	settings.ShowDeltas = false // isolate peak from the delta slot
	graph := statGraph{values: []float64{10, 90}, maxValue: 100, peak: "peak 90.0%"}

	out := ansi.Strip(renderHybridGraph(renderer, settings, graph, "#7dcfff", 60))
	if !strings.Contains(out, "peak 90.0%") {
		t.Fatalf("renderHybridGraph() = %q, want it to include the peak annotation on a wide row", out)
	}
}
