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
