package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// openInspectorDetailOnField opens overlayInspectorDetail on the field row
// labeled label, failing the test if no such row exists.
func openInspectorDetailOnField(t *testing.T, model Model, label string) Model {
	t.Helper()
	rows := model.inspectorRows()
	idx := -1
	for i, row := range rows {
		if row.kind == inspectorRowField && row.label == label {
			idx = i
			break
		}
	}
	if idx == -1 {
		t.Fatalf("no inspector field row labeled %q", label)
	}
	model.inspectorCursor = idx
	updated, _ := model.openInspectorDetail()
	model = updated.(Model)
	if model.overlay != overlayInspectorDetail {
		t.Fatalf("overlay = %v, want overlayInspectorDetail", model.overlay)
	}
	return model
}

// A pending vim operator ("d" awaiting a motion) reports the same Mode(),
// CursorIndex(), and SelectedText() as an idle Normal-mode editor — Esc
// clearing that pending state used to be indistinguishable, by those three,
// from an Esc with nothing to cancel at all, so it closed the overlay
// immediately instead of just clearing the operator like real vim does.
func TestInspectorDetailVimPendingOperatorEscDoesNotCloseOverlay(t *testing.T) {
	model := testModelWithSelectedContainer()
	setEditorVimMode(true) // NewModel resets this to the (false) default settings value; set it after construction.
	t.Cleanup(func() { setEditorVimMode(false) })
	model = openInspectorDetailOnField(t, model, "Image")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(Model)
	if model.overlay != overlayInspectorDetail {
		t.Fatal("overlay closed after 'd', want it to stay open awaiting a motion")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.overlay != overlayInspectorDetail {
		t.Fatal("esc after a pending 'd' closed the overlay, want it to only clear the pending operator")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.overlay != overlayNone {
		t.Fatal("esc with nothing pending did not close the overlay")
	}
}

func TestInspectorDetailVimIdleEscClosesOverlayImmediately(t *testing.T) {
	model := testModelWithSelectedContainer()
	setEditorVimMode(true) // NewModel resets this to the (false) default settings value; set it after construction.
	t.Cleanup(func() { setEditorVimMode(false) })
	model = openInspectorDetailOnField(t, model, "Image")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.overlay != overlayNone {
		t.Fatal("esc with nothing pending did not close the overlay")
	}
}

func TestInspectorDetailVimVisualEscClearsSelectionBeforeClosing(t *testing.T) {
	model := testModelWithSelectedContainer()
	setEditorVimMode(true) // NewModel resets this to the (false) default settings value; set it after construction.
	t.Cleanup(func() { setEditorVimMode(false) })
	model = openInspectorDetailOnField(t, model, "Image")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	model = updated.(Model)
	if model.inspectorDetailEditor.Mode() != "VISUAL" {
		t.Fatalf("mode = %q, want VISUAL after 'v'", model.inspectorDetailEditor.Mode())
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.overlay != overlayInspectorDetail {
		t.Fatal("esc leaving Visual mode closed the overlay, want it to only clear the selection")
	}
	if model.inspectorDetailEditor.Mode() != "NORMAL" {
		t.Fatalf("mode = %q, want NORMAL after leaving Visual", model.inspectorDetailEditor.Mode())
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.overlay != overlayNone {
		t.Fatal("esc with nothing pending did not close the overlay")
	}
}

func TestInspectorDetailPlainModeEscClosesOverlayImmediately(t *testing.T) {
	model := testModelWithSelectedContainer()
	model = openInspectorDetailOnField(t, model, "Image")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.overlay != overlayNone {
		t.Fatal("plain-mode esc did not close the overlay")
	}
}
