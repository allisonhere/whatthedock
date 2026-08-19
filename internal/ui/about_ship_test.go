package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func aboutShipStartFrame() int {
	return aboutSearchFrames + aboutConvergeFrames + aboutExpandFrames
}

// TestAboutShipRowsStaysOffScreenDuringLogoReveal checks the ship doesn't
// appear until the logo's own search+converge+expand animation has
// finished — it shouldn't compete with the logo for attention.
func TestAboutShipRowsStaysOffScreenDuringLogoReveal(t *testing.T) {
	width := 60
	bg := lipgloss.Color("#1e1e2e")
	for _, frame := range []int{0, aboutShipStartFrame() - 1} {
		rows := aboutShipRows(width, frame, bg, "")
		for i, row := range rows {
			if strings.TrimSpace(ansi.Strip(row)) != "" {
				t.Fatalf("frame %d row %d = %q, want blank (still in logo reveal)", frame, i, row)
			}
		}
	}
}

// TestAboutShipRowsStartsAtLogoRevealBoundary checks the ship visibly
// begins the instant the logo reveal ends, matching the old boat's start
// behavior.
func TestAboutShipRowsStartsAtLogoRevealBoundary(t *testing.T) {
	width := 60
	bg := lipgloss.Color("#1e1e2e")
	rows := aboutShipRows(width, aboutShipStartFrame(), bg, "")
	hasContent := false
	for _, row := range rows {
		if strings.TrimSpace(ansi.Strip(row)) != "" {
			hasContent = true
			break
		}
	}
	if !hasContent {
		t.Fatal("all rows blank at the logo-reveal boundary frame, want the ship/water visible")
	}
}

// TestAboutShipRowsAlwaysThreeRows sweeps a wide range of frame indices and
// checks aboutShipRows always returns exactly 3 rows — belt-and-suspenders
// on top of aboutShipFrame.rows' [3]-array compile-time guarantee, since
// aboutShipRows itself still builds a []string.
func TestAboutShipRowsAlwaysThreeRows(t *testing.T) {
	width := 60
	bg := lipgloss.Color("#1e1e2e")
	start := aboutShipStartFrame()
	for frame := 0; frame < start+400; frame += 7 {
		if rows := aboutShipRows(width, frame, bg, ""); len(rows) != 3 {
			t.Fatalf("frame %d: aboutShipRows returned %d rows, want 3", frame, len(rows))
		}
	}
}

// TestAboutShipRowsNeverExceedsWidth checks every row is padded/clamped to
// exactly the requested width, for both a narrow terminal (narrower than
// aboutShipStageWidth) and wider ones.
func TestAboutShipRowsNeverExceedsWidth(t *testing.T) {
	bg := lipgloss.Color("#1e1e2e")
	start := aboutShipStartFrame()
	for _, width := range []int{20, 45, 62, 100} {
		for frame := 0; frame < start+400; frame += 11 {
			rows := aboutShipRows(width, frame, bg, "")
			for i, row := range rows {
				if got := len([]rune(ansi.Strip(row))); got != width {
					t.Fatalf("width=%d frame=%d row %d rendered width = %d, want %d", width, frame, i, got, width)
				}
			}
		}
	}
}

// TestAboutShipPhasesOccurInOrder checks aboutShipFrameAt's phase sequence
// never jumps backward as elapsed increases, starts at aboutShipSailing,
// and ends at aboutShipFinished.
func TestAboutShipPhasesOccurInOrder(t *testing.T) {
	if aboutShipFrames[0].phase != aboutShipSailing {
		t.Fatalf("first frame phase = %v, want aboutShipSailing", aboutShipFrames[0].phase)
	}
	if aboutShipFrames[len(aboutShipFrames)-1].phase != aboutShipFinished {
		t.Fatalf("last frame phase = %v, want aboutShipFinished", aboutShipFrames[len(aboutShipFrames)-1].phase)
	}
	last := aboutShipFrames[0].phase
	for elapsed := 0; elapsed < 400; elapsed++ {
		phase := aboutShipFrames[aboutShipFrameAt(elapsed)].phase
		if phase < last {
			t.Fatalf("elapsed=%d phase=%v went backward from %v", elapsed, phase, last)
		}
		last = phase
	}
}

// TestAboutShipRowsDoesNotLoopByDefault checks the animation holds on the
// final frame indefinitely rather than looping once aboutShipLoop is false.
func TestAboutShipRowsDoesNotLoopByDefault(t *testing.T) {
	if aboutShipLoop {
		t.Skip("aboutShipLoop is true; non-looping behavior doesn't apply")
	}
	width := 60
	bg := lipgloss.Color("#1e1e2e")
	start := aboutShipStartFrame()
	soon := aboutShipRows(width, start+400, bg, "")
	muchLater := aboutShipRows(width, start+5000, bg, "")
	for i := range soon {
		if soon[i] != muchLater[i] {
			t.Fatalf("row %d differs between elapsed+400 and elapsed+5000, want identical (holding on the last frame)\nsoon:  %q\nlater: %q", i, soon[i], muchLater[i])
		}
	}
}

// TestAboutShipRowsReachesFinalOceanFrame checks the ship visually
// disappears once the animation settles — only water/whitecap characters
// remain, not just that some frame is being held.
func TestAboutShipRowsReachesFinalOceanFrame(t *testing.T) {
	width := 60
	bg := lipgloss.Color("#1e1e2e")
	rows := aboutShipRows(width, aboutShipStartFrame()+5000, bg, "")
	for i, row := range rows {
		plain := strings.TrimSpace(ansi.Strip(row))
		for _, r := range plain {
			if r != '~' && r != '^' {
				t.Fatalf("final row %d contains %q, want only water characters (~/^): %q", i, r, plain)
			}
		}
	}
}

// TestAboutShipFrameDurationsProduceAtLeastOneTick is a structural sanity
// check over the literal aboutShipFrames table: every authored frame must
// hold for at least one tick, catching an accidentally-zero duration that
// isn't the intentional final "hold forever" frame relying on the clamp.
func TestAboutShipFrameDurationsProduceAtLeastOneTick(t *testing.T) {
	for i, f := range aboutShipFrames {
		if ticks := ticksForAboutShipDuration(f.duration); ticks < 1 {
			t.Fatalf("frame %d duration=%v converts to %d ticks, want >= 1", i, f.duration, ticks)
		}
	}
}

// TestAboutShipFramesAllHaveThreeRows guards the hand-authored table
// directly, on top of the [3]-array compile-time guarantee — makes the
// invariant explicit and searchable from the test file.
func TestAboutShipFramesAllHaveThreeRows(t *testing.T) {
	for i, f := range aboutShipFrames {
		if len(f.rows) != 3 {
			t.Fatalf("frame %d has %d rows, want 3", i, len(f.rows))
		}
	}
}

// aboutShipElapsedForFrame sums ticksForAboutShipDuration for every frame
// before idx, returning the smallest elapsed value that lands on it — lets
// tests target a specific known frame by index instead of guessing an
// elapsed value.
func aboutShipElapsedForFrame(idx int) int {
	elapsed := 0
	for i := 0; i < idx; i++ {
		elapsed += ticksForAboutShipDuration(aboutShipFrames[i].duration)
	}
	return elapsed
}

// TestAboutShipRowsGlyphsSurviveWideCentering guards against the bug
// reported live: the ship's rows appeared entirely black in a real
// terminal at typical (wide) panel widths. Root cause was aboutShipRows
// centering the stage by running the *already-rendered* ANSI string
// through positionAt, a rune-slicing helper with no notion of escape
// sequences — the embedded ANSI codes inflate the rune count far past the
// panel width, so positionAt's truncation almost always cut a frame off
// mid-escape-sequence, dropping the closing reset and every visible glyph
// after it. Centering is now done at the aboutCell level before rendering
// (see blankAboutCells in aboutShipRows), so this must hold at any width
// wider than aboutShipStageWidth — not just the narrow ones where the old
// bug happened not to truncate anything away.
func TestAboutShipRowsGlyphsSurviveWideCentering(t *testing.T) {
	// go test's default color profile is Ascii (no TTY attached), which
	// makes renderCells emit plain, uncolored text with no embedded escape
	// sequences at all — the bug this test guards against only exists once
	// real ANSI codes are in the string, so the profile must be forced to
	// TrueColor (matching a real terminal) or this test passes vacuously
	// regardless of whether the underlying bug is present.
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	bg := lipgloss.Color("#101419")
	frame := aboutShipStartFrame() + aboutShipElapsedForFrame(10) // "Sinking": distinctive "/__/"
	for _, width := range []int{aboutShipStageWidth, 62, 90, 140} {
		rows := aboutShipRows(width, frame, bg, "")
		joined := strings.Join(rows, "\n")
		plain := ansi.Strip(joined)
		if !strings.Contains(plain, "/__/") {
			t.Fatalf("width=%d: sinking hull glyph missing after centering, want it intact:\n%s", width, plain)
		}
		for i, row := range rows {
			if got := len([]rune(ansi.Strip(row))); got != width {
				t.Fatalf("width=%d row=%d rendered width = %d, want %d", width, i, got, width)
			}
		}
	}
}

// aboutSwimStartElapsed mirrors overlayAboutSwimmerCells' own swimStart
// computation, so tests can target specific points in the swim by name
// instead of duplicating the formula inline everywhere.
func aboutSwimStartElapsed() int {
	return aboutShipFinalFrameStart() + aboutSwimStartDelay
}

// TestAboutShipRowsSwimmerHiddenBeforeSwimStarts checks neither the
// swimmer nor any of the version text appears before the swim's own start
// boundary — only calm water (and the ship/storm/sinking sequence earlier
// still) up to that point.
func TestAboutShipRowsSwimmerHiddenBeforeSwimStarts(t *testing.T) {
	width := 60
	bg := lipgloss.Color("#1e1e2e")
	start := aboutShipStartFrame()
	swimStart := aboutSwimStartElapsed()
	text := "Version 0.1.4"
	for elapsed := 0; elapsed < swimStart; elapsed += 3 {
		rows := aboutShipRows(width, start+elapsed, bg, text)
		joined := ansi.Strip(strings.Join(rows, "\n"))
		if strings.Contains(joined, "0.1.4") {
			t.Fatalf("elapsed=%d: version text visible before the swim starts:\n%s", elapsed, joined)
		}
	}
}

// TestAboutShipRowsSwimmerRevealsTextProgressively is the regression test
// for this design's central idea: the version text is written into the
// swimmer's wake as they cross the stage, not faded in as one block —
// partway through the swim, only a prefix should be visible, not the full
// string. The text's own resting position is centered on the stage (see
// overlayAboutSwimmerCells' doc comment), which sits left of
// aboutSwimStartColumn — so a handful of leading characters are already
// visible the instant the swimmer appears, and the reveal window that's
// left genuinely "progressive" (as opposed to "already complete") is
// narrower than it would be if the text started exactly where the swimmer
// does; this checks a point inside that narrower window, not an arbitrary
// fixed offset.
func TestAboutShipRowsSwimmerRevealsTextProgressively(t *testing.T) {
	width := 60
	bg := lipgloss.Color("#1e1e2e")
	start := aboutShipStartFrame()
	text := "Version 0.1.4"
	elapsed := aboutSwimStartElapsed() + 5 // partway through aboutSwimDuration
	rows := aboutShipRows(width, start+elapsed, bg, text)
	joined := ansi.Strip(strings.Join(rows, "\n"))
	if !strings.Contains(joined, "Version 0") {
		t.Fatalf("elapsed=swimStart+5: wake text prefix missing, want at least \"Version 0\":\n%s", joined)
	}
	if strings.Contains(joined, text) {
		t.Fatalf("elapsed=swimStart+5: full text %q already visible, want only a partial reveal this early:\n%s", text, joined)
	}
}

// TestAboutShipRowsSwimmerTextEndsUpCentered is the regression test for a
// bug reported live: the wake text's resting column used to be
// aboutSwimStartColumn (matching the mast's own sink column, close to but
// not exactly the stage's center), so it landed slightly off-center —
// noticeably so for a longer status string. The text's own start column
// must always be exactly centered on the stage, independent of the
// swimmer's own path.
func TestAboutShipRowsSwimmerTextEndsUpCentered(t *testing.T) {
	bg := lipgloss.Color("#1e1e2e")
	start := aboutShipStartFrame()
	elapsed := aboutSwimStartElapsed() + aboutSwimDuration + 5
	for _, width := range []int{aboutShipStageWidth, 60, 90} {
		for _, text := range []string{"Version 0.1.4", "v0.1.4 — up to date"} {
			rows := aboutShipRows(width, start+elapsed, bg, text)
			row := ansi.Strip(rows[1])
			idx := strings.Index(row, text)
			if idx < 0 {
				t.Fatalf("width=%d text=%q: not found in row:\n%q", width, text, row)
			}
			stageWidth := min(aboutShipStageWidth, width)
			stageStart := max(0, (width-stageWidth)/2)
			wantStart := stageStart + max(0, (stageWidth-len([]rune(text)))/2)
			if gotStart := len([]rune(row[:idx])); gotStart != wantStart {
				t.Fatalf("width=%d text=%q: text starts at column %d, want %d (centered on the stage):\n%q", width, text, gotStart, wantStart, row)
			}
		}
	}
}

// TestAboutShipRowsSwimmerTextHoldsAfterSwim checks the full version text
// is present and stable long after the swim finishes (matching the
// non-looping "hold forever" shape every other phase in this file uses).
func TestAboutShipRowsSwimmerTextHoldsAfterSwim(t *testing.T) {
	width := 60
	bg := lipgloss.Color("#1e1e2e")
	start := aboutShipStartFrame()
	text := "Version 0.1.4"
	swimStart := aboutSwimStartElapsed()
	for _, extra := range []int{aboutSwimDuration + 5, 2000, 20000} {
		rows := aboutShipRows(width, start+swimStart+extra, bg, text)
		joined := ansi.Strip(strings.Join(rows, "\n"))
		if !strings.Contains(joined, text) {
			t.Fatalf("elapsed=swimStart+%d: wake text not intact:\n%s", extra, joined)
		}
	}
}

// TestAboutShipRowsSwimmerNeverExceedsWidth checks the swimmer/wake overlay
// never blows out a row's fixed width, including when the text is wider
// than the panel itself.
func TestAboutShipRowsSwimmerNeverExceedsWidth(t *testing.T) {
	bg := lipgloss.Color("#1e1e2e")
	start := aboutShipStartFrame()
	elapsed := aboutSwimStartElapsed() + aboutSwimDuration + 5
	longText := strings.Repeat("Version 0.1.4 — up to date ", 5)
	for _, width := range []int{10, 20, 45, 60, 100} {
		rows := aboutShipRows(width, start+elapsed, bg, longText)
		for i, row := range rows {
			if got := len([]rune(ansi.Strip(row))); got != width {
				t.Fatalf("width=%d row=%d rendered width = %d, want %d", width, i, got, width)
			}
		}
	}
}
