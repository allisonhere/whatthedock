package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/allisonhere/whatthedock/internal/actions"
	"github.com/allisonhere/whatthedock/internal/domain"
)

func TestVolumeColumnsFillModalWidth(t *testing.T) {
	name, mount, state := volumeColumnWidths(80)
	if got := name + mount + state + 12; got != 80 {
		t.Fatalf("column width = %d, want 80 (name=%d mount=%d state=%d)", got, name, mount, state)
	}
}

func TestVolumeCurationListsAndSelectsOnlyUnusedVolumes(t *testing.T) {
	model := testModel()
	model.width, model.height = 120, 34
	fake := model.provider.(*fakeProvider)
	fake.volumes = []domain.Volume{
		{Name: "media-data", Driver: "local", Mountpoint: "/var/lib/docker/volumes/media-data/_data", InUse: true},
		{Name: "old-cache", Driver: "local", Mountpoint: "/var/lib/docker/volumes/old-cache/_data"},
	}

	updated, cmd := model.executeCommand(actions.CurateVolumes)
	model = updated.(Model)
	if model.overlay != overlayVolumeCuration || cmd == nil {
		t.Fatalf("overlay/cmd = %v/%v, want volume curator and load command", model.overlay, cmd != nil)
	}
	updated, _ = model.Update(runCmd(t, cmd).(volumeListMsg))
	model = updated.(Model)

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	model = updated.(Model)
	if model.selectedVolumeCount() != 0 {
		t.Fatalf("used volume became selectable; selected count = %d", model.selectedVolumeCount())
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	model = updated.(Model)
	if model.selectedVolumeCount() != 1 {
		t.Fatalf("selection = %d volumes, want 1", model.selectedVolumeCount())
	}
	if model.volumeCursor != 1 {
		t.Fatalf("cursor = %d after selecting, want 1", model.volumeCursor)
	}
	view := ansi.Strip(model.View())
	if !strings.Contains(view, "volume curator") || !strings.Contains(view, "media-data") || !strings.Contains(view, "USED") || !strings.Contains(view, "UNUSED") || !strings.Contains(view, "selected: 1 volume(s)") {
		t.Fatalf("volume curator view missing inventory:\n%s", view)
	}
}

func TestVolumeCurationConfirmsAndRemovesSelectedVolumes(t *testing.T) {
	model := testModel()
	model.width, model.height = 120, 34
	fake := model.provider.(*fakeProvider)
	fake.volumes = []domain.Volume{{Name: "old-cache", Driver: "local", Mountpoint: "/var/lib/docker/volumes/old-cache/_data"}}
	updated, cmd := model.executeCommand(actions.CurateVolumes)
	model = updated.(Model)
	updated, _ = model.Update(runCmd(t, cmd).(volumeListMsg))
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(Model)
	if !model.volumeConfirming {
		t.Fatal("d did not open volume cleanup confirmation")
	}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "DELETE 1 VOLUME(S)?") || !strings.Contains(view, "y/enter continue") {
		t.Fatalf("delete command strip is not prominent or actionable:\n%s", view)
	}
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	if !model.volumeRemoving || cmd == nil {
		t.Fatalf("removing/cmd = %v/%v, want removal in progress", model.volumeRemoving, cmd != nil)
	}
	updated, _ = model.Update(runCmd(t, cmd).(volumeRemoveDoneMsg))
	model = updated.(Model)
	if len(fake.removedVolumes) != 1 || fake.removedVolumes[0] != "old-cache" {
		t.Fatalf("removed volumes = %#v, want old-cache", fake.removedVolumes)
	}
	if model.volumeRemoving || model.volumeConfirming {
		t.Fatalf("volume cleanup still active: removing=%v confirming=%v", model.volumeRemoving, model.volumeConfirming)
	}
	if model.overlay != overlayVolumeCuration || len(model.volumeItems) != 0 {
		t.Fatalf("overlay/volume count = %v/%d, want curator open with removed volume gone", model.overlay, len(model.volumeItems))
	}
}

func TestVolumeCurationCancelDoesNotRemove(t *testing.T) {
	model := testModel()
	fake := model.provider.(*fakeProvider)
	fake.volumes = []domain.Volume{{Name: "old-cache", Driver: "local", Mountpoint: "/var/lib/docker/volumes/old-cache/_data"}}
	updated, cmd := model.executeCommand(actions.CurateVolumes)
	model = updated.(Model)
	updated, _ = model.Update(runCmd(t, cmd).(volumeListMsg))
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEscape})
	model = updated.(Model)
	if model.volumeConfirming || len(fake.removedVolumes) != 0 {
		t.Fatalf("cancel changed cleanup state: confirming=%v removed=%#v", model.volumeConfirming, fake.removedVolumes)
	}
}
