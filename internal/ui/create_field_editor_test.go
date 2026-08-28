package ui

import (
	"strings"
	"testing"

	"github.com/allisonhere/tideui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// openStandaloneCreateOn opens a fresh standalone create draft and
// navigates (via real Tab presses, exactly like a user would, so
// createFieldEditor gets synced the same way production navigation
// always does) until field is focused.
func openStandaloneCreateOn(t *testing.T, field createField) Model {
	t.Helper()
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeStandalone
	model.createField = createFieldMode
	model.syncCreateFieldEditor()
	for i := 0; i < 20; i++ {
		if model.createField == field {
			return model
		}
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
		model = updated.(Model)
	}
	t.Fatalf("field %v not reached via Tab from standalone create draft", field)
	return model
}

func TestCreateFieldEditorTypingInsertsAtCursor(t *testing.T) {
	model := openStandaloneCreateOn(t, createFieldContainerName)
	// defaultCreateDraft prefills ContainerName with "new-container" —
	// clear it first so typing below starts from a known empty field.
	model.createDraft.ContainerName = ""
	model.syncCreateFieldEditor()

	for _, r := range "web" {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(Model)
	}
	if model.createDraft.ContainerName != "web" {
		t.Fatalf("ContainerName = %q, want web", model.createDraft.ContainerName)
	}

	// Left twice, then insert — proves the cursor is a real position, not
	// always "append at the end".
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	model = updated.(Model)
	if model.createDraft.ContainerName != "wXeb" {
		t.Fatalf("ContainerName = %q, want wXeb (inserted before the last two characters)", model.createDraft.ContainerName)
	}
}

func TestCreateFieldEditorBackspaceAndDelete(t *testing.T) {
	model := openStandaloneCreateOn(t, createFieldContainerName)
	model.createDraft.ContainerName = "webapp"
	model.syncCreateFieldEditor()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	model = updated.(Model)
	if model.createDraft.ContainerName != "webap" {
		t.Fatalf("ContainerName = %q after backspace, want webap", model.createDraft.ContainerName)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyHome})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDelete})
	model = updated.(Model)
	if model.createDraft.ContainerName != "ebap" {
		t.Fatalf("ContainerName = %q after home+delete, want ebap", model.createDraft.ContainerName)
	}
}

func TestCreateFieldEditorShiftSelectionThenTypeReplaces(t *testing.T) {
	model := openStandaloneCreateOn(t, createFieldContainerName)
	model.createDraft.ContainerName = "webapp"
	model.syncCreateFieldEditor()

	// Cursor starts at the end (SetValue's own behavior). Shift+Left three
	// times selects "app"; typing then replaces the selection — real
	// selection behavior this field never had before.
	for i := 0; i < 3; i++ {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyShiftLeft})
		model = updated.(Model)
	}
	if model.createFieldEditor.SelectedText() != "app" {
		t.Fatalf("SelectedText() = %q, want app", model.createFieldEditor.SelectedText())
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	model = updated.(Model)
	if model.createDraft.ContainerName != "webX" {
		t.Fatalf("ContainerName = %q, want webX (selection replaced by typing)", model.createDraft.ContainerName)
	}
}

func TestCreateFieldEditorHomeEndCtrlAliases(t *testing.T) {
	model := openStandaloneCreateOn(t, createFieldContainerName)
	model.createDraft.ContainerName = "webapp"
	model.syncCreateFieldEditor()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	model = updated.(Model)
	if model.createDraft.ContainerName != "Xwebapp" {
		t.Fatalf("ContainerName = %q, want Xwebapp (ctrl+a acts as Home)", model.createDraft.ContainerName)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
	model = updated.(Model)
	if model.createDraft.ContainerName != "XwebappY" {
		t.Fatalf("ContainerName = %q, want XwebappY (ctrl+e acts as End)", model.createDraft.ContainerName)
	}
}

func TestCreateFieldEditorCtrlUClearsField(t *testing.T) {
	model := openStandaloneCreateOn(t, createFieldContainerName)
	model.createDraft.ContainerName = "webapp"
	model.syncCreateFieldEditor()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	model = updated.(Model)
	if model.createDraft.ContainerName != "" {
		t.Fatalf("ContainerName = %q after ctrl+u, want empty", model.createDraft.ContainerName)
	}
}

func TestCreateFieldEditorEnterAdvancesFieldNeverInsertsNewline(t *testing.T) {
	model := openStandaloneCreateOn(t, createFieldContainerName)
	model.createDraft.ContainerName = "web"
	model.syncCreateFieldEditor()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)

	if model.createField == createFieldContainerName {
		t.Fatal("Enter did not advance to the next field")
	}
	if strings.Contains(model.createDraft.ContainerName, "\n") {
		t.Fatalf("ContainerName = %q, Enter must never insert a newline into the value", model.createDraft.ContainerName)
	}
	if model.createDraft.ContainerName != "web" {
		t.Fatalf("ContainerName = %q, want unchanged by Enter", model.createDraft.ContainerName)
	}
}

func TestCreateFieldEditorResyncsOnFocusChange(t *testing.T) {
	model := openStandaloneCreateOn(t, createFieldContainerName)
	model.createDraft.ContainerName = "web"
	model.createDraft.Image = "nginx:alpine"
	model.syncCreateFieldEditor()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	if model.createField != createFieldImage {
		t.Fatalf("field = %v after Tab, want createFieldImage", model.createField)
	}
	if model.createFieldEditor.Value() != "nginx:alpine" {
		t.Fatalf("createFieldEditor.Value() = %q, want it reseeded to Image's own value nginx:alpine", model.createFieldEditor.Value())
	}
	// Typing here must edit Image, not leak into ContainerName.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	model = updated.(Model)
	if model.createDraft.ContainerName != "web" {
		t.Fatalf("ContainerName = %q, want untouched by typing on the Image field", model.createDraft.ContainerName)
	}
	if model.createDraft.Image != "nginx:alpine!" {
		t.Fatalf("Image = %q, want the typed ! appended", model.createDraft.Image)
	}
}

// TestCreateFieldEditorSurvivesComposeFileBrowseThenCancel is a regression
// test for a real bug found while wiring this up: ctrl+o (and the "o"
// choice-field browse shortcut) changed createField to createFieldComposeFile
// directly, without the syncCreateFieldEditor call every other
// focus-changing path makes — so canceling the file browser (Esc) left the
// ComposeFile row's embedded editor showing stale content from whichever
// field was focused before, instead of the actual ComposeFile value.
func TestCreateFieldEditorSurvivesComposeFileBrowseThenCancel(t *testing.T) {
	model := openStandaloneCreateOn(t, createFieldContainerName)
	model.createDraft.ContainerName = "web"
	model.createDraft.ComposeFile = "compose.yml"
	model.syncCreateFieldEditor()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model = updated.(Model)
	if !model.createBrowsing || model.createField != createFieldComposeFile {
		t.Fatalf("browsing/field = %v/%v after ctrl+o, want true/createFieldComposeFile", model.createBrowsing, model.createField)
	}
	if model.createFieldEditor.Value() != "compose.yml" {
		t.Fatalf("createFieldEditor.Value() = %q right after ctrl+o, want compose.yml (not stale ContainerName content)", model.createFieldEditor.Value())
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.createBrowsing {
		t.Fatal("createBrowsing = true after Esc, want false")
	}
	if model.createFieldEditor.Value() != "compose.yml" {
		t.Fatalf("createFieldEditor.Value() = %q after canceling the browser, want compose.yml preserved", model.createFieldEditor.Value())
	}
}

func TestCreateFieldEditorRendersRealCursorNotPipeCharacter(t *testing.T) {
	model := openStandaloneCreateOn(t, createFieldContainerName)
	model.createDraft.ContainerName = "web"
	model.syncCreateFieldEditor()
	model.width, model.height = 120, 34

	view := ansi.Strip(model.View())
	if strings.Contains(view, "web|") || strings.Contains(view, "|web") {
		t.Fatalf("view still shows the old spliced '|' caret:\n%s", view)
	}
	if !strings.Contains(view, "web") {
		t.Fatalf("view missing the field's own value:\n%s", view)
	}
}

// TestCreateFieldEditorVimModeStartsInInsert confirms these fields respect
// the app's global vim-mode setting (rather than always being plain,
// which would be inconsistent with the Compose YAML editor) but land in
// Insert immediately on focus — the same fix openCreateEditor already
// applies for the Compose editor, for the same reason: without it, the
// first keystroke (and first Esc) on a freshly-focused field would land
// in vim Normal mode and do nothing visible, which would be a confusing
// regression for a one-line Name/Image field.
func TestCreateFieldEditorVimModeStartsInInsert(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	setEditorVimMode(true) // NewModel resets this to the (false) default settings value; set it after construction.
	t.Cleanup(func() { setEditorVimMode(false) })
	model.createDraft.Mode = createModeStandalone
	model.createDraft.ContainerName = ""
	model.createField = createFieldContainerName
	model.syncCreateFieldEditor()

	if model.createFieldEditor.Mode() != "INSERT" {
		t.Fatalf("Mode() = %q right after focusing a field in vim mode, want INSERT", model.createFieldEditor.Mode())
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	model = updated.(Model)
	if model.createDraft.ContainerName != "w" {
		t.Fatalf("ContainerName = %q, want w typed immediately with no need to press i first", model.createDraft.ContainerName)
	}
}

// TestCreateFieldEditorRowKeepsBackgroundPastCursor is a regression test
// for a real bug found live: the focused row's selected-background
// highlight only ever extended up to the left of the cursor, because the
// cursor/selection rendering used raw lipgloss.Style.Render() calls, which
// end in an absolute reset ("\x1b[0m") — RenderSoftRow only wraps this
// field's whole Suffix string in its own Background(...) *after* it's
// already built, so that reset didn't just end its own span, it wiped the
// row's background for everything from that point rightward, for the
// rest of the row. Fixed by rendering the cursor/selection with
// foregroundSpanDefault (used elsewhere in this file for exactly this
// reason), which never emits a background-touching code at all. This
// checks the structural cause directly (no mid-row full reset), not just
// that some background exists, since a reset can still appear between two
// spans that each separately re-assert the *same* color and look right
// in a screenshot while still breaking the invariant for anything that
// doesn't immediately re-assert it (e.g. this row's own trailing padding).
func TestCreateFieldEditorRowKeepsBackgroundPastCursor(t *testing.T) {
	original := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(original) })

	model := openStandaloneCreateOn(t, createFieldContainerName)
	model.createDraft.ContainerName = "webapp"
	model.syncCreateFieldEditor()
	// Move the cursor into the middle so there's real trailing text after
	// it within the same row — the exact shape that exposed the bug.
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = updated.(Model)

	// Checked at the Suffix level, not the fully-composed row: RenderSoftRow
	// itself legitimately concatenates an independently-styled rail glyph
	// (its own full reset at the end, by tideui's own design) with a
	// separately, freshly re-styled content span — that reset is harmless
	// there because nothing after it relies on inheriting from before it.
	// This Suffix is different: it becomes *part of* that single content
	// span's own background wrap, applied by RenderSoftRow after this
	// whole string already exists — a bare reset anywhere inside it, with
	// real content following, is exactly the bug (see this function's own
	// doc comment).
	renderer := tideui.NewRenderer(tideui.LavenderFieldsForever, tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	suffix := model.createFieldEditorSuffix(renderer, 30)
	if hasMidRowReset(suffix) {
		t.Fatalf("field editor suffix contains a mid-row full reset that can drop the row's focus background once RenderSoftRow wraps it:\n%q", suffix)
	}
}

// TestCreateFieldEditorHandlesExactWidthMultipleWithoutGoingBlank is a
// regression test for a real bug found live ("long content in field
// doesn't scroll as expected"): a field's value would render as
// completely blank (just the cursor, no text at all) whenever its length
// happened to be an *exact multiple* of the row's render width while the
// cursor sat at the end — the normal resting place right after focusing a
// field. Root cause is upstream in ripple itself (see
// fieldEditorRenderWidth's own doc comment for the exact mechanism: its
// line-wrapping appends one extra, genuinely empty visual line whenever a
// logical line's length divides evenly by the configured width, and the
// end-of-text cursor lands on that phantom empty line instead of the
// previous one holding the actual last characters) — worked around here
// by nudging the width ripple wraps at by one column in exactly that
// situation, since ripple exposes no way to correct its viewport
// directly from outside the package.
func TestCreateFieldEditorHandlesExactWidthMultipleWithoutGoingBlank(t *testing.T) {
	model := openStandaloneCreateOn(t, createFieldPorts)
	// 70 runes — deliberately chosen so that 70 % 35 == 0, the exact
	// boundary that triggered the bug at a real, plausible field width.
	long := "8080:80/tcp, 8443:443/tcp, 5432:5432/tcp, 9090:9090/tcp, 3000:3000/tcp"
	if got := len([]rune(long)); got != 70 {
		t.Fatalf("test fixture length = %d, want 70 (the test depends on this exact length)", got)
	}
	model.createDraft.Ports = long
	model.syncCreateFieldEditor()

	renderer := tideui.NewRenderer(tideui.LavenderFieldsForever, tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	for _, width := range []int{35, 70, 10, 14, 7} {
		suffix := model.createFieldEditorSuffix(renderer, width)
		stripped := strings.TrimSpace(ansi.Strip(suffix))
		// The cursor glyph itself is always present; anything else means
		// there's real content alongside it.
		if stripped == "▊" || stripped == "▏" {
			t.Fatalf("width=%d (an exact multiple of the 70-rune value): field rendered as blank apart from the cursor:\n%q", width, suffix)
		}
	}
}

func TestCreateFieldEditorCopiesSelectionToClipboard(t *testing.T) {
	model := openStandaloneCreateOn(t, createFieldContainerName)
	model.createDraft.ContainerName = "webapp"
	model.syncCreateFieldEditor()

	for i := 0; i < 3; i++ {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyShiftLeft})
		model = updated.(Model)
	}
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("ctrl+c with a selection returned no cmd, want a clipboard-write cmd")
	}
}
