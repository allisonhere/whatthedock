package ui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/allisonhere/tideui"

	"github.com/allisonhere/whatthedock/internal/domain"
)

// The Dashboard's container rows draw three metrics — CPU, memory and
// network — and every Graph style changes how all three look. The styles
// fall into two families:
//
//	fills     gauge  thin "━╸─" bar          bars  chunky "██▌░░" bar with
//	                                                sub-cell precision
//	          (the latest value, against a fixed capacity)
//
//	history   wave   smoothed block sparkline   blocks   raw block sparkline
//	          braille  2×4-dot sparkline, two samples per cell
//	          (the last few minutes, newest at the right)
//
// A dashboardSeries describes one metric in a style-neutral way, and
// dashboardGraph turns it into a width-column string in the chosen style.

// dashboardNetScaleFloor is the smallest byte rate treated as "full scale"
// for a network graph. Graphs are relative to a container's own peak, so a
// quiet container's shape still shows — but a peak of a few bytes a second
// is noise, not a pattern, and below this it draws flat.
const dashboardNetScaleFloor = 512

// dashboardSeries is one metric's data and how to scale and colour it.
type dashboardSeries struct {
	values  []float64 // history, oldest first; may be empty
	current float64   // the latest value (what fills draw)
	fillMax float64   // the value that fills a gauge/bar completely
	floor   float64   // the smallest value a history graph treats as full height
	// fillCurve reshapes a 0..1 fraction before it becomes a bar length —
	// memory uses a square root so single-digit percentages stay visible.
	// nil is linear. History graphs are always linear: a flat 5% line drawn
	// at sqrt height would read as high usage.
	fillCurve func(float64) float64
	// color grades value; scale is what the graph treats as full (the
	// history window's ceiling, or fillMax for fills). CPU and memory grade
	// on absolute thresholds and ignore it; network grades relative to it.
	color func(value, scale float64) lipgloss.Color
	// quiet draws the dim "nothing to show" track instead of a graph: no
	// data yet, no memory limit, or a network window that is all zero.
	quiet bool
}

// fraction is value's share of scale, 0..1, linear (history graphs).
func (s dashboardSeries) fraction(value, scale float64) float64 {
	if scale <= 0 {
		return 0
	}
	return math.Min(1, math.Max(0, value/scale))
}

// fillFraction is fraction after the series' fill curve (gauge and bars).
func (s dashboardSeries) fillFraction(value, scale float64) float64 {
	f := s.fraction(value, scale)
	if s.fillCurve != nil {
		f = s.fillCurve(f)
	}
	return f
}

// historyScale is the value a history graph's tallest glyph stands for: the
// window's own peak, but never below the series' floor.
func (s dashboardSeries) historyScale(window []float64) float64 {
	scale := s.floor
	for _, v := range window {
		scale = math.Max(scale, v)
	}
	if scale <= 0 {
		return 1
	}
	return scale
}

// dashboardCPUSeries: percent of one core, filled against one core and
// graded on the Dashboard's absolute thresholds. History graphs use the
// window's peak but never less than 10%, so an idle container's noise
// doesn't draw as a mountain range.
func dashboardCPUSeries(stats *domain.ContainerStats, history statsHistory) dashboardSeries {
	return dashboardSeries{
		values:  history.CPU,
		current: statsCPU(stats),
		fillMax: cpuGaugeOneCore,
		floor:   10,
		color:   func(v, _ float64) lipgloss.Color { return dashboardGraphColor(v) },
		quiet:   stats == nil && len(history.CPU) == 0,
	}
}

// dashboardMemSeries: percent of the container's memory limit. A container
// with no limit has nothing to measure against, so it gets the quiet track
// rather than a colour guessed from nothing.
func dashboardMemSeries(stats *domain.ContainerStats, history statsHistory) dashboardSeries {
	if stats == nil || stats.MemoryLimit == 0 {
		return dashboardSeries{quiet: true}
	}
	limit := float64(stats.MemoryLimit)
	values := make([]float64, len(history.Memory))
	for i, used := range history.Memory {
		values[i] = float64(used) / limit * 100
	}
	return dashboardSeries{
		values:    values,
		current:   float64(stats.MemoryUsage) / limit * 100,
		fillMax:   100,
		floor:     25,
		fillCurve: math.Sqrt,
		color:     func(v, _ float64) lipgloss.Color { return dashboardMemColor(v) },
	}
}

// dashboardNetSeries: bytes per second against the container's own peak
// (never less than dashboardNetScaleFloor). Colour is relative to that
// scale — absolute byte-rate landmarks are tens of megabytes a second, so a
// quiet fleet would otherwise be one flat colour. A window that is all zero
// is the quiet track, but a single zero among real traffic is just a low
// glyph, so a line doesn't vanish and reappear every time one poll lands on
// no traffic.
func dashboardNetSeries(values []uint64, peak uint64) dashboardSeries {
	floats := make([]float64, len(values))
	quiet := true
	for i, v := range values {
		floats[i] = float64(v)
		if v != 0 {
			quiet = false
		}
	}
	current := 0.0
	if len(floats) > 0 {
		current = floats[len(floats)-1]
	}
	return dashboardSeries{
		values:  floats,
		current: current,
		fillMax: math.Max(float64(peak), dashboardNetScaleFloor),
		floor:   dashboardNetScaleFloor,
		color:   func(v, scale float64) lipgloss.Color { return graphHeatColor(v / scale) },
		quiet:   quiet,
	}
}

// dashboardGraph draws s in the given Graph style, exactly width columns
// wide on background bg.
func dashboardGraph(renderer tideui.Renderer, style graphStyle, s dashboardSeries, width int, bg lipgloss.Color) string {
	width = max(1, width)
	if s.quiet {
		return lipgloss.NewStyle().Background(bg).Foreground(renderer.Styles.Theme.Dimmed).Render(strings.Repeat("─", width))
	}
	switch style {
	case graphStyleGauge:
		return dashboardFillGauge(renderer, s, width, bg)
	case graphStyleBars:
		return dashboardFillBars(renderer, s, width, bg)
	case graphStyleBraille:
		return dashboardHistoryBraille(renderer, s, width, bg)
	default: // wave, blocks
		return dashboardHistoryBlocks(renderer, s, style == graphStyleWave, width, bg)
	}
}

// fillColorAt is the colour of the fill cell at index i of width: the
// metric's own gradient swept across the bar, so where the leading edge sits
// in the gradient says how hot the value is.
func (s dashboardSeries) fillColorAt(i, width int) lipgloss.Color {
	pos := 0.0
	if width > 1 {
		pos = float64(i) / float64(width-1)
	}
	return s.color(pos*s.fillMax, s.fillMax)
}

// dashboardFillGauge is the thin look: "━" filled, a "╸" leading edge, "─"
// for the rest.
func dashboardFillGauge(renderer tideui.Renderer, s dashboardSeries, width int, bg lipgloss.Color) string {
	filled := clamp(int(s.fillFraction(s.current, s.fillMax)*float64(width)+0.5), 0, width)
	dim := lipgloss.NewStyle().Background(bg).Foreground(renderer.Styles.Theme.Dimmed)
	if filled == 0 {
		return dim.Render(strings.Repeat("─", width))
	}
	var b strings.Builder
	for i := range filled {
		glyph := "━"
		if i == filled-1 && filled < width {
			glyph = "╸"
		}
		b.WriteString(lipgloss.NewStyle().Background(bg).Foreground(s.fillColorAt(i, width)).Bold(true).Render(glyph))
	}
	if filled < width {
		b.WriteString(dim.Render(strings.Repeat("─", width-filled)))
	}
	return b.String()
}

// dashboardFillBars is the chunky look: solid "█" cells, one partial cell
// drawn in eighths ("▏" … "▉") so the length has eight times the precision
// of a cell count, and a "░" track for the rest.
func dashboardFillBars(renderer tideui.Renderer, s dashboardSeries, width int, bg lipgloss.Color) string {
	eighths := clamp(int(s.fillFraction(s.current, s.fillMax)*float64(width*8)+0.5), 0, width*8)
	full, part := eighths/8, eighths%8
	partial := [8]string{"", "▏", "▎", "▍", "▌", "▋", "▊", "▉"}

	var b strings.Builder
	for i := range full {
		b.WriteString(lipgloss.NewStyle().Background(bg).Foreground(s.fillColorAt(i, width)).Render("█"))
	}
	used := full
	if part > 0 {
		b.WriteString(lipgloss.NewStyle().Background(bg).Foreground(s.fillColorAt(full, width)).Render(partial[part]))
		used++
	}
	if used < width {
		b.WriteString(lipgloss.NewStyle().Background(bg).Foreground(renderer.Styles.Theme.Dimmed).Render(strings.Repeat("░", width-used)))
	}
	return b.String()
}

// dashboardWindow is the part of values a graph with slots samples of room
// can show — the most recent ones.
func dashboardWindow(values []float64, slots int) []float64 {
	if len(values) > slots {
		return values[len(values)-slots:]
	}
	return values
}

// smoothSeries is a three-point moving average, which is what makes the
// wave style a wave: single-poll spikes are rounded into the trend instead
// of drawn as isolated towers. The ends average over the two samples they
// have.
func smoothSeries(values []float64) []float64 {
	if len(values) < 3 {
		return values
	}
	out := make([]float64, len(values))
	for i := range values {
		lo, hi := max(0, i-1), min(len(values)-1, i+1)
		sum := 0.0
		for _, v := range values[lo : hi+1] {
			sum += v
		}
		out[i] = sum / float64(hi-lo+1)
	}
	return out
}

// dashboardHistoryBlocks draws one block glyph (▁ … █) per sample, oldest at
// the left, each coloured by its own value. Columns the history hasn't
// reached yet are dim flat glyphs.
//
// blocks (smooth false) draws each sample as it is. wave rounds the series
// into a trend and fades older samples toward the muted text colour, so the
// newest data glows and the past trails off — a flowing line rather than a
// bar chart.
func dashboardHistoryBlocks(renderer tideui.Renderer, s dashboardSeries, smooth bool, width int, bg lipgloss.Color) string {
	// The scale comes from the raw window even when the series is smoothed,
	// so smoothing visibly rounds a spike down instead of being rescaled
	// back up to a full-height tower.
	scale := s.historyScale(dashboardWindow(s.values, width))
	values := s.values
	if smooth {
		values = smoothSeries(values)
	}
	values = dashboardWindow(values, width)
	glyphs := []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}
	dim := renderer.Styles.Theme.Dimmed

	var b strings.Builder
	for i, v := range values {
		level := clamp(int(s.fraction(v, scale)*7+0.5), 0, 7)
		color := s.color(v, scale)
		if smooth && len(values) > 1 {
			fade := 0.6 * (1 - float64(i)/float64(len(values)-1)) // oldest 60% faded, newest not at all
			color = lipgloss.Color(lerpHexColor(string(color), string(dim), fade))
		}
		b.WriteString(lipgloss.NewStyle().Background(bg).Foreground(color).Render(glyphs[level]))
	}
	if pad := width - len(values); pad > 0 {
		b.WriteString(lipgloss.NewStyle().Background(bg).Foreground(renderer.Styles.Theme.Dimmed).Render(strings.Repeat("▁", pad)))
	}
	return b.String()
}

// dashboardHistoryBraille packs two samples into each braille cell, each
// half-column filled bottom-up to one of four dot heights — twice the
// history in the same width, at finer vertical steps than a block glyph. A
// cell takes the colour of the hotter of its two samples.
func dashboardHistoryBraille(renderer tideui.Renderer, s dashboardSeries, width int, bg lipgloss.Color) string {
	values := dashboardWindow(s.values, width*2)
	scale := s.historyScale(values)
	level := func(v float64) int { return clamp(int(s.fraction(v, scale)*4)+1, 1, 4) }

	var b strings.Builder
	cells := (len(values) + 1) / 2
	for c := range cells {
		left := values[c*2]
		bits := brailleColumnBits(0, level(left))
		hottest := left
		if c*2+1 < len(values) {
			right := values[c*2+1]
			bits |= brailleColumnBits(1, level(right))
			hottest = math.Max(left, right)
		}
		b.WriteString(lipgloss.NewStyle().Background(bg).Foreground(s.color(hottest, scale)).Render(string(rune(0x2800 + bits))))
	}
	if pad := width - cells; pad > 0 {
		b.WriteString(lipgloss.NewStyle().Background(bg).Foreground(renderer.Styles.Theme.Dimmed).Render(strings.Repeat(string(rune(0x2800)), pad)))
	}
	return b.String()
}
