package ui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// aboutShipPhase names where in the storm/sinking sequence a frame belongs —
// descriptive only; frame selection is driven by cumulative duration, not
// phase (see aboutShipFrameAt).
type aboutShipPhase int

const (
	aboutShipSailing aboutShipPhase = iota
	aboutShipStorm
	aboutShipLightning
	aboutShipWaveImpact // not used by the current 14 hand-authored frames — the
	// wave-impact geometry is fused into the aboutShipLightning keyframe instead;
	// declared for the full spec'd phase order and any future frame added here.
	aboutShipPitching
	aboutShipSinking
	aboutShipMast
	aboutShipBubbles // not used by the current 14 frames — mast sinking goes
	// straight to open ocean; declared for the same reason as aboutShipWaveImpact.
	aboutShipFinished
)

// aboutShipSegment is one colored run of literal text within a ship-frame
// row — concatenated in order to build the row. A blank/no-color segment
// (leading padding) just carries text with color == "", rendered as plain
// background-colored space the same way renderCells already treats any
// space rune, regardless of its cell's color.
type aboutShipSegment struct {
	text  string
	color string // hex, e.g. aboutShipSail; ignored for blank/space-only text
}

// aboutShipFrame is one 3-row animation keyframe — the [3] array (not a
// slice) makes "exactly 3 rows" a compile-time property of every literal
// below rather than something to remember while hand-authoring. duration is
// per-frame so pacing can be tuned without touching the fixed 55ms Update
// tick that drives it (aboutTickMsg in model.go — untouched by this file).
type aboutShipFrame struct {
	phase    aboutShipPhase
	duration time.Duration
	rows     [3][]aboutShipSegment
}

// Color palette, hand-picked per frame from the user's own raw ANSI RGB
// values (converted to hex) — never computed/interpolated, matching the
// "hand-author every frame, water included" decision for this animation.
const (
	aboutShipSail         = "#ebebdc" // 235,235,220 — ivory sail/mast-top
	aboutShipWood         = "#d2aa64" // 210,170,100 — golden-brown hull/mast
	aboutShipHullFaded    = "#b48c50" // 180,140,80  — muted brown, mast once sinking
	aboutShipOceanBase    = "#145aaa" // 20,90,170   — base ocean blue (most rows)
	aboutShipOceanBright  = "#1eb4dc" // 30,180,220  — bright cyan surge (wave impact)
	aboutShipFoam         = "#f5f5f5" // 245,245,245 — whitecap glyph "^"
	aboutShipGlint        = "#ffedb4" // 255,245,180 — pre-strike spark
	aboutShipBolt         = "#fff55a" // 255,245,90  — lightning bolt "⚡"
	aboutShipOceanDeep    = "#0f468c" // 15,70,140   — final-frame top row, darker
	aboutShipOceanMid     = "#146ebe" // 20,110,190  — final-frame middle row
	aboutShipOceanDarkest = "#0a4187" // 10,65,135   — final-frame bottom row
	aboutShipOceanRipple  = "#1982c8" // 25,130,200  — lighter ripple, mast-submerging frame

	aboutShipStatusDim   = aboutShipOceanMid // fade-in start: same shade as the water it sits on
	aboutShipStatusBrite = aboutShipSail     // fade-in end: ivory, matching the sail
)

// aboutShipStatusFadeDelay is how many ticks the settled ocean holds, ship
// gone, before the version/update-status line starts fading in — a beat of
// "just water" so the text reads as materializing out of the scene rather
// than popping in the instant the ship sinks.
const aboutShipStatusFadeDelay = 12

// aboutShipStatusFadeTicks is how many ticks the fade-in itself takes, once
// it starts.
const aboutShipStatusFadeTicks = 24

// aboutShipStageWidth is the ship's fixed "stage" — within the 50-70 column
// range asked for — centered inside the panel's (wider, variable) content
// width via aboutShipRows, so the scene reads as a fixed-size vignette
// regardless of terminal size. Matches the widest hand-authored row.
const aboutShipStageWidth = 45

// aboutShipLoop is false by default (non-looping): once the animation falls
// off the end of aboutShipFrames it holds on the last frame forever. Flip to
// true to loop instead — the only code this changes is aboutShipFrameAt's
// wraparound branch.
const aboutShipLoop = false

// aboutShipTickInterval mirrors tickAbout's 55ms cadence (model.go) as a
// local constant, purely for aboutShipFrameAt's duration-to-tick-count math.
// model.go and the logo/spotlight animation it also drives are not touched
// by this file at all.
const aboutShipTickInterval = 55 * time.Millisecond

// aboutShipFrames is the ship's full 14-frame storyboard, transcribed
// verbatim from the user's own hand-authored ANSI/truecolor frames. Water
// rows are padded to aboutShipStageWidth with extra '~' so every frame
// renders the same width; the ship silhouette rows are left their natural
// (short) length — aboutShipSegmentsToCells pads the remainder with plain
// background space, which is the correct look around a small hull/mast.
var aboutShipFrames = []aboutShipFrame{
	{ // 0: empty water, ship not yet visible
		phase:    aboutShipSailing,
		duration: 500 * time.Millisecond,
		rows: [3][]aboutShipSegment{
			nil,
			nil,
			{{text: strings.Repeat("~", 45), color: aboutShipOceanBase}},
		},
	},
	{ // 1: ship enters at the left edge
		phase:    aboutShipSailing,
		duration: 450 * time.Millisecond,
		rows: [3][]aboutShipSegment{
			{{text: "   |\\_", color: aboutShipSail}},
			{{text: "__", color: aboutShipWood}, {text: "/|  \\", color: aboutShipSail}},
			{{text: "\\_____", color: aboutShipWood}, {text: strings.Repeat("~", 39), color: aboutShipOceanBase}},
		},
	},
	{ // 2
		phase:    aboutShipSailing,
		duration: 450 * time.Millisecond,
		rows: [3][]aboutShipSegment{
			{{text: "      |\\_", color: aboutShipSail}},
			{{text: " "}, {text: "____", color: aboutShipWood}, {text: "/|  \\__", color: aboutShipSail}},
			{{text: "\\__________/", color: aboutShipWood}, {text: strings.Repeat("~", 33), color: aboutShipOceanBase}},
		},
	},
	{ // 3
		phase:    aboutShipSailing,
		duration: 450 * time.Millisecond,
		rows: [3][]aboutShipSegment{
			{{text: "         |\\_", color: aboutShipSail}},
			{{text: "    "}, {text: "____", color: aboutShipWood}, {text: "/|  \\____", color: aboutShipSail}},
			{{text: strings.Repeat("~", 3), color: aboutShipOceanBase}, {text: "\\_____________/", color: aboutShipWood}, {text: strings.Repeat("~", 27), color: aboutShipOceanBase}},
		},
	},
	{ // 4
		phase:    aboutShipSailing,
		duration: 450 * time.Millisecond,
		rows: [3][]aboutShipSegment{
			{{text: "            |\\_", color: aboutShipSail}},
			{{text: "       "}, {text: "____", color: aboutShipWood}, {text: "/|  \\____", color: aboutShipSail}},
			{{text: strings.Repeat("~", 6), color: aboutShipOceanBase}, {text: "\\_____________/", color: aboutShipWood}, {text: strings.Repeat("~", 24), color: aboutShipOceanBase}},
		},
	},
	{ // 5
		phase:    aboutShipSailing,
		duration: 450 * time.Millisecond,
		rows: [3][]aboutShipSegment{
			{{text: "               |\\_", color: aboutShipSail}},
			{{text: "          "}, {text: "____", color: aboutShipWood}, {text: "/|  \\____", color: aboutShipSail}},
			{{text: strings.Repeat("~", 9), color: aboutShipOceanBase}, {text: "\\_____________/", color: aboutShipWood}, {text: strings.Repeat("~", 21), color: aboutShipOceanBase}},
		},
	},
	{ // 6: storm begins — ship still crossing, water prefix keeps growing
		phase:    aboutShipStorm,
		duration: 450 * time.Millisecond,
		rows: [3][]aboutShipSegment{
			{{text: "                  |\\_", color: aboutShipSail}},
			{{text: "             "}, {text: "____", color: aboutShipWood}, {text: "/|  \\____", color: aboutShipSail}},
			{{text: strings.Repeat("~", 12), color: aboutShipOceanBase}, {text: "\\_____________/", color: aboutShipWood}, {text: strings.Repeat("~", 18), color: aboutShipOceanBase}},
		},
	},
	{ // 7: pre-strike glint
		phase:    aboutShipStorm,
		duration: 300 * time.Millisecond,
		rows: [3][]aboutShipSegment{
			{{text: "     *", color: aboutShipGlint}, {text: strings.Repeat(" ", 13)}, {text: "|\\_", color: aboutShipSail}},
			{{text: strings.Repeat(" ", 7)}, {text: "\\", color: aboutShipGlint}, {text: strings.Repeat(" ", 6)}, {text: "____", color: aboutShipWood}, {text: "/|  \\____", color: aboutShipSail}},
			{{text: strings.Repeat("~", 13), color: aboutShipOceanBase}, {text: "\\_____________/", color: aboutShipWood}, {text: strings.Repeat("~", 17), color: aboutShipOceanBase}},
		},
	},
	{ // 8: lightning strike + wave impact fused into one keyframe
		phase:    aboutShipLightning,
		duration: 220 * time.Millisecond,
		rows: [3][]aboutShipSegment{
			{{text: strings.Repeat(" ", 13)}, {text: "⚡", color: aboutShipBolt}, {text: strings.Repeat(" ", 6)}, {text: "|\\", color: aboutShipSail}},
			{{text: strings.Repeat(" ", 15)}, {text: "_____", color: aboutShipWood}, {text: "/| \\", color: aboutShipSail}},
			{{text: strings.Repeat("~", 14), color: aboutShipOceanBase}, {text: "/_______|__\\", color: aboutShipWood}, {text: strings.Repeat("~", 4), color: aboutShipOceanBright}, {text: "^", color: aboutShipFoam}, {text: strings.Repeat("~", 14), color: aboutShipOceanBase}},
		},
	},
	{ // 9: pitching, bow rising
		phase:    aboutShipPitching,
		duration: 380 * time.Millisecond,
		rows: [3][]aboutShipSegment{
			{{text: strings.Repeat(" ", 21)}, {text: "/|", color: aboutShipSail}},
			{{text: strings.Repeat(" ", 19)}, {text: "_/", color: aboutShipWood}, {text: " |", color: aboutShipSail}},
			{{text: strings.Repeat("~", 18), color: aboutShipOceanBase}, {text: "/___|", color: aboutShipWood}, {text: strings.Repeat("~", 22), color: aboutShipOceanBase}},
		},
	},
	{ // 10: near-vertical sinking (the user's own keyframe)
		phase:    aboutShipSinking,
		duration: 420 * time.Millisecond,
		rows: [3][]aboutShipSegment{
			{{text: strings.Repeat(" ", 22)}, {text: "/|", color: aboutShipSail}},
			{{text: strings.Repeat(" ", 21)}, {text: "/ |", color: aboutShipSail}},
			{{text: strings.Repeat("~", 20), color: aboutShipOceanBase}, {text: "/__/", color: aboutShipWood}, {text: strings.Repeat("~", 21), color: aboutShipOceanBase}},
		},
	},
	{ // 11: mast only, full height
		phase:    aboutShipMast,
		duration: 500 * time.Millisecond,
		rows: [3][]aboutShipSegment{
			{{text: strings.Repeat(" ", 22)}, {text: "|", color: aboutShipHullFaded}},
			{{text: strings.Repeat(" ", 22)}, {text: "|", color: aboutShipHullFaded}},
			{{text: strings.Repeat("~", 22), color: aboutShipOceanBase}, {text: "|", color: aboutShipHullFaded}, {text: strings.Repeat("~", 22), color: aboutShipOceanBase}},
		},
	},
	{ // 12: mast submerging further, flanked by two water rows
		phase:    aboutShipMast,
		duration: 500 * time.Millisecond,
		rows: [3][]aboutShipSegment{
			{{text: strings.Repeat(" ", 22)}, {text: "|", color: aboutShipHullFaded}},
			{{text: strings.Repeat("~", 22), color: aboutShipOceanBase}, {text: "|", color: aboutShipHullFaded}, {text: strings.Repeat("~", 22), color: aboutShipOceanBase}},
			{{text: strings.Repeat("~", 22), color: aboutShipOceanRipple}, {text: "|", color: aboutShipHullFaded}, {text: strings.Repeat("~", 22), color: aboutShipOceanBase}},
		},
	},
	{ // 13: final settled ocean, no ship — held forever (aboutShipLoop is false)
		phase:    aboutShipFinished,
		duration: 0,
		rows: [3][]aboutShipSegment{
			{{text: strings.Repeat("~", 45), color: aboutShipOceanDeep}},
			{{text: strings.Repeat("~", 45), color: aboutShipOceanMid}},
			{{text: strings.Repeat("~", 45), color: aboutShipOceanDarkest}},
		},
	},
}

// aboutShipRows renders the ship's 3-row scene for the given animation
// frame — the direct replacement for the old aboutBoatRows, same signature,
// same off-screen-during-logo-reveal behavior.
//
// Centering the stage within the wider panel width is done entirely at the
// aboutCell level (blank cells prepended/appended before rendering), never
// by post-processing the already-rendered ANSI string. An earlier version
// centered by running the *rendered* string (already full of embedded
// escape codes) through positionAt, which rune-slices/pads plain text —
// against ANSI content that inflates the rune count far past width, it
// truncated mid-escape-sequence almost every frame, dropping the closing
// reset and everything after it. That's what turned the whole ship area
// black in a real terminal despite every isolated check on the byte stream
// looking fine — the corruption only exists post-render, one layer up from
// where the earlier checks were looking.
// statusText, when non-empty, is composited onto the middle row (centered)
// once the animation has settled on its final held frame — see
// overlayAboutShipStatusCells. It fades in at the aboutCell level, same as
// the ship/water art itself, so it's subject to the same never-truncate-an-
// already-rendered-ANSI-string rule and can't reintroduce this file's own
// bug.
func aboutShipRows(width, frame int, bg lipgloss.TerminalColor, statusText string) []string {
	if width <= 0 {
		return nil
	}
	startFrame := aboutSearchFrames + aboutConvergeFrames + aboutExpandFrames
	elapsed := frame - startFrame
	if elapsed < 0 {
		return blankAboutShipRows(width, bg)
	}
	stage := aboutShipFrames[aboutShipFrameAt(elapsed)]
	stageWidth := min(aboutShipStageWidth, width)
	col := max(0, (width-stageWidth)/2)
	rows := make([]string, 3)
	for i, segs := range stage.rows {
		cells := make([]aboutCell, 0, width)
		cells = append(cells, blankAboutCells(col)...)
		cells = append(cells, aboutShipSegmentsToCells(segs, stageWidth)...)
		cells = append(cells, blankAboutCells(width-len(cells))...)
		if i == 1 && statusText != "" {
			cells = overlayAboutShipStatusCells(cells, width, elapsed, statusText)
		}
		rows[i] = lipgloss.NewStyle().Width(width).Background(bg).Render(renderCells(cells, bg))
	}
	return rows
}

// aboutShipFinalFrameStart is the elapsed-tick value at which the animation
// reaches its last frame (the settled, ship-free ocean) — the sum of every
// earlier frame's duration in ticks. aboutShipFrameAt already clamps to this
// same last frame once elapsed runs past it; this just names that boundary
// so the status-line fade knows when the ocean has actually finished
// settling, not just "some frame or other" is being held.
func aboutShipFinalFrameStart() int {
	total := 0
	for i := 0; i < len(aboutShipFrames)-1; i++ {
		total += ticksForAboutShipDuration(aboutShipFrames[i].duration)
	}
	return total
}

// overlayAboutShipStatusCells fades text into the center of a row's cells
// once the ship has finished sinking and the ocean has settled — before
// that it's a no-op, returning cells untouched, so it never affects the
// storm/sinking sequence itself. The fade goes from the same hex as the
// water it sits on up to full ivory brightness over aboutShipStatusFadeTicks,
// so the text reads as materializing out of the ocean rather than popping in.
func overlayAboutShipStatusCells(cells []aboutCell, width, elapsed int, text string) []aboutCell {
	fadeStart := aboutShipFinalFrameStart() + aboutShipStatusFadeDelay
	if elapsed < fadeStart {
		return cells
	}
	progress := float64(elapsed-fadeStart) / float64(aboutShipStatusFadeTicks)
	if progress > 1 {
		progress = 1
	}
	color := lerpHexColor(aboutShipStatusDim, aboutShipStatusBrite, progress)
	runes := []rune(text)
	if len(runes) > width {
		runes = runes[:width]
	}
	start := max(0, (width-len(runes))/2)
	for i, r := range runes {
		pos := start + i
		if pos < 0 || pos >= len(cells) {
			continue
		}
		cells[pos] = aboutCell{r: r, color: color}
	}
	return cells
}

// blankAboutCells returns n plain, colorless (space) cells — n<=0 yields an
// empty slice rather than panicking, so callers can pass a possibly-negative
// remainder straight through.
func blankAboutCells(n int) []aboutCell {
	if n <= 0 {
		return nil
	}
	cells := make([]aboutCell, n)
	for i := range cells {
		cells[i] = aboutCell{r: ' '}
	}
	return cells
}

// aboutShipFrameAt walks aboutShipFrames' cumulative duration (converted to
// tickAbout's 55ms unit via aboutShipTickInterval) to find which frame index
// elapsed ticks falls into. Falling off the end clamps to the last frame —
// that clamp, not any special-cased "duration" value, is what makes the
// animation hold forever once aboutShipLoop is false. aboutShipLoop=true
// instead wraps elapsed via modulo the total tick count.
func aboutShipFrameAt(elapsed int) int {
	total := 0
	for i, f := range aboutShipFrames {
		ticks := ticksForAboutShipDuration(f.duration)
		if elapsed < total+ticks {
			return i
		}
		total += ticks
	}
	if aboutShipLoop && total > 0 {
		return aboutShipFrameAt(elapsed % total)
	}
	return len(aboutShipFrames) - 1
}

// ticksForAboutShipDuration converts a frame's duration into a whole number
// of aboutShipTickInterval-sized ticks, minimum 1 — every authored frame
// holds for at least one tick even if its duration rounds down to zero.
func ticksForAboutShipDuration(d time.Duration) int {
	ticks := int(d / aboutShipTickInterval)
	if ticks < 1 {
		ticks = 1
	}
	return ticks
}

// blankAboutShipRows is 3 plain width-wide, bg-only blank lines — the ship's
// off-screen state before the logo reveal finishes, matching the shape the
// old boat animation used before it started sliding in.
func blankAboutShipRows(width int, bg lipgloss.TerminalColor) []string {
	blank := lipgloss.NewStyle().Width(width).Background(bg).Render("")
	return []string{blank, blank, blank}
}

// aboutShipSegmentsToCells expands one row's hand-authored segments into a
// width-long cell grid for renderCells: space runes within a segment's text
// always render as a plain background space regardless of that segment's
// color (renderCells' own existing rule), and any remaining width past the
// segments' natural length is padded with blank cells rather than
// stretching the artwork.
func aboutShipSegmentsToCells(segs []aboutShipSegment, width int) []aboutCell {
	cells := make([]aboutCell, 0, width)
	for _, seg := range segs {
		for _, r := range seg.text {
			if len(cells) >= width {
				break
			}
			cells = append(cells, aboutCell{r: r, color: seg.color})
		}
	}
	for len(cells) < width {
		cells = append(cells, aboutCell{r: ' '})
	}
	if len(cells) > width {
		cells = cells[:width]
	}
	return cells
}
