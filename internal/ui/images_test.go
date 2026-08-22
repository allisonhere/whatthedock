package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/allisonhere/whatthedock/internal/actions"
	"github.com/allisonhere/whatthedock/internal/domain"
)

func TestImageColumnsFillModalWidth(t *testing.T) {
	name, size, state, id := imageColumnWidths(80)
	if got := name + 38; got != 80 {
		t.Fatalf("column width = %d, want 80 (name=%d size=%d state=%d id=%d)", got, name, size, state, id)
	}
}

func TestImageCurationListsAndSelectsOnlyUnusedImages(t *testing.T) {
	model := testModel()
	model.width, model.height = 120, 34
	fake := model.provider.(*fakeProvider)
	fake.images = []domain.Image{
		{ID: domain.ResourceID{Host: fake.host.ID, ID: "used-id"}, RepoTags: []string{"used:latest"}, Containers: 1, Size: 100},
		{ID: domain.ResourceID{Host: fake.host.ID, ID: "unused-id"}, RepoTags: []string{"unused:latest"}, Size: 200},
		{ID: domain.ResourceID{Host: fake.host.ID, ID: "dangling-id"}, Dangling: true, Size: 300},
	}

	updated, cmd := model.executeCommand(actions.CurateImages)
	model = updated.(Model)
	if model.overlay != overlayImageCuration || cmd == nil {
		t.Fatalf("overlay/cmd = %v/%v, want image curator and load command", model.overlay, cmd != nil)
	}
	updated, _ = model.Update(runCmd(t, cmd).(imageListMsg))
	model = updated.(Model)

	if got := model.imageItems[0].DisplayName(); got != "<none>" {
		t.Fatalf("first image = %q, want dangling image sorted first", got)
	}
	// Move to the tagged unused image; the used image remains visible but is
	// never selectable because its container reference count is non-zero.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	model = updated.(Model)
	if model.selectedImageCount() != 1 || model.selectedImageBytes() != 200 {
		t.Fatalf("selection = %d images/%d bytes, want 1/200", model.selectedImageCount(), model.selectedImageBytes())
	}
	if model.imageCursor != 2 {
		t.Fatalf("cursor = %d after selecting, want 2", model.imageCursor)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	model = updated.(Model)
	if model.selectedImageCount() != 1 {
		t.Fatalf("used image became selectable; selected count = %d", model.selectedImageCount())
	}
	view := ansi.Strip(model.View())
	if !strings.Contains(view, "image curator") || !strings.Contains(view, "used:latest") || !strings.Contains(view, "USED") || !strings.Contains(view, "UNUSED") || !strings.Contains(view, "selected: 1 image(s)") {
		t.Fatalf("image curator view missing inventory:\n%s", view)
	}
}

func TestImageCurationReservesSelectionSummaryRow(t *testing.T) {
	model := testModel()
	model.width, model.height = 120, 34
	fake := model.provider.(*fakeProvider)
	fake.images = []domain.Image{{ID: domain.ResourceID{Host: fake.host.ID, ID: "unused-id"}, RepoTags: []string{"unused:latest"}}}
	updated, cmd := model.executeCommand(actions.CurateImages)
	model = updated.(Model)
	updated, _ = model.Update(runCmd(t, cmd).(imageListMsg))
	model = updated.(Model)
	view := ansi.Strip(model.View())
	if !strings.Contains(view, "selected: 0 image(s)") {
		t.Fatalf("empty image selection did not reserve summary row:\n%s", view)
	}
}

func TestImageCurationReservesFeedbackRow(t *testing.T) {
	model := testModel()
	model.width, model.height = 120, 34
	fake := model.provider.(*fakeProvider)
	fake.images = []domain.Image{{ID: domain.ResourceID{Host: fake.host.ID, ID: "unused-id"}, RepoTags: []string{"unused:latest"}}}
	updated, cmd := model.executeCommand(actions.CurateImages)
	model = updated.(Model)
	updated, _ = model.Update(runCmd(t, cmd).(imageListMsg))
	model = updated.(Model)
	before := ansi.Strip(model.View())
	model.imageResult = "removed 1 image(s)"
	after := ansi.Strip(model.View())
	if strings.Count(before, "\n") != strings.Count(after, "\n") {
		t.Fatalf("feedback changed modal height: before=%d lines after=%d", strings.Count(before, "\n"), strings.Count(after, "\n"))
	}
}

func TestImageCurationConfirmsAndRemovesSelectedImages(t *testing.T) {
	model := testModel()
	model.width, model.height = 120, 34
	fake := model.provider.(*fakeProvider)
	fake.images = []domain.Image{{ID: domain.ResourceID{Host: fake.host.ID, ID: "unused-id"}, RepoTags: []string{"unused:latest"}, Size: 200}}
	updated, cmd := model.executeCommand(actions.CurateImages)
	model = updated.(Model)
	updated, _ = model.Update(runCmd(t, cmd).(imageListMsg))
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(Model)
	if !model.imageConfirming {
		t.Fatal("d did not open image cleanup confirmation")
	}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "DELETE 1 IMAGE(S)?") || !strings.Contains(view, "y/enter continue") {
		t.Fatalf("delete command strip is not prominent or actionable:\n%s", view)
	}
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	if !model.imageRemoving || cmd == nil {
		t.Fatalf("removing/cmd = %v/%v, want removal in progress", model.imageRemoving, cmd != nil)
	}
	updated, _ = model.Update(runCmd(t, cmd).(imageRemoveDoneMsg))
	model = updated.(Model)
	if len(fake.removedImages) != 1 || fake.removedImages[0] != "unused-id" {
		t.Fatalf("removed images = %#v, want unused-id", fake.removedImages)
	}
	if model.imageRemoving || model.imageConfirming {
		t.Fatalf("image cleanup still active: removing=%v confirming=%v", model.imageRemoving, model.imageConfirming)
	}
	if model.overlay != overlayImageCuration || len(model.imageItems) != 0 {
		t.Fatalf("overlay/image count = %v/%d, want curator open with removed image gone", model.overlay, len(model.imageItems))
	}
}

func TestImageCurationCancelDoesNotRemove(t *testing.T) {
	model := testModel()
	fake := model.provider.(*fakeProvider)
	fake.images = []domain.Image{{ID: domain.ResourceID{Host: fake.host.ID, ID: "unused-id"}, RepoTags: []string{"unused:latest"}}}
	updated, cmd := model.executeCommand(actions.CurateImages)
	model = updated.(Model)
	updated, _ = model.Update(runCmd(t, cmd).(imageListMsg))
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEscape})
	model = updated.(Model)
	if model.imageConfirming || len(fake.removedImages) != 0 {
		t.Fatalf("cancel changed cleanup state: confirming=%v removed=%#v", model.imageConfirming, fake.removedImages)
	}
}
