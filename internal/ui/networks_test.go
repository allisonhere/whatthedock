package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/allisonhere/whatthedock/internal/actions"
	"github.com/allisonhere/whatthedock/internal/domain"
)

func TestNetworkColumnsFillModalWidth(t *testing.T) {
	name, subnet, state, id := networkColumnWidths(80)
	if got := name + subnet + state + id + 12; got != 80 {
		t.Fatalf("column width = %d, want 80 (name=%d subnet=%d state=%d id=%d)", got, name, subnet, state, id)
	}
}

func TestNetworkCurationListsAndSelectsOnlyUnusedNetworks(t *testing.T) {
	model := testModel()
	model.width, model.height = 120, 34
	fake := model.provider.(*fakeProvider)
	fake.networks = []domain.Network{
		{ID: "bridge-id", Name: "bridge", Driver: "bridge", Containers: 0},
		{ID: "used-id", Name: "media_default", Driver: "bridge", Containers: 2, Subnet: "172.18.0.0/16"},
		{ID: "unused-id", Name: "old_default", Driver: "bridge", Containers: 0, Subnet: "172.20.0.0/16"},
	}

	updated, cmd := model.executeCommand(actions.CurateNetworks)
	model = updated.(Model)
	if model.overlay != overlayNetworkCuration || cmd == nil {
		t.Fatalf("overlay/cmd = %v/%v, want network curator and load command", model.overlay, cmd != nil)
	}
	updated, _ = model.Update(runCmd(t, cmd).(networkListMsg))
	model = updated.(Model)

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	model = updated.(Model)
	if model.selectedNetworkCount() != 0 {
		t.Fatalf("used/built-in network became selectable; selected count = %d", model.selectedNetworkCount())
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	model = updated.(Model)
	if model.selectedNetworkCount() != 1 {
		t.Fatalf("selection = %d networks, want 1", model.selectedNetworkCount())
	}
	if model.networkCursor != 2 {
		t.Fatalf("cursor = %d after selecting, want 2", model.networkCursor)
	}
	view := ansi.Strip(model.View())
	if !strings.Contains(view, "network curator") || !strings.Contains(view, "media_default") || !strings.Contains(view, "BUILT-IN") || !strings.Contains(view, "USED") || !strings.Contains(view, "UNUSED") || !strings.Contains(view, "selected: 1 network(s)") {
		t.Fatalf("network curator view missing inventory:\n%s", view)
	}
}

func TestNetworkCurationConfirmsAndRemovesSelectedNetworks(t *testing.T) {
	model := testModel()
	model.width, model.height = 120, 34
	fake := model.provider.(*fakeProvider)
	fake.networks = []domain.Network{{ID: "unused-id", Name: "old_default", Driver: "bridge"}}
	updated, cmd := model.executeCommand(actions.CurateNetworks)
	model = updated.(Model)
	updated, _ = model.Update(runCmd(t, cmd).(networkListMsg))
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(Model)
	if !model.networkConfirming {
		t.Fatal("d did not open network cleanup confirmation")
	}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "DELETE 1 NETWORK(S)?") || !strings.Contains(view, "y/enter continue") {
		t.Fatalf("delete command strip is not prominent or actionable:\n%s", view)
	}
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	if !model.networkRemoving || cmd == nil {
		t.Fatalf("removing/cmd = %v/%v, want removal in progress", model.networkRemoving, cmd != nil)
	}
	updated, _ = model.Update(runCmd(t, cmd).(networkRemoveDoneMsg))
	model = updated.(Model)
	if len(fake.removedNetworks) != 1 || fake.removedNetworks[0] != "unused-id" {
		t.Fatalf("removed networks = %#v, want unused-id", fake.removedNetworks)
	}
	if model.networkRemoving || model.networkConfirming {
		t.Fatalf("network cleanup still active: removing=%v confirming=%v", model.networkRemoving, model.networkConfirming)
	}
	if model.overlay != overlayNetworkCuration || len(model.networkItems) != 0 {
		t.Fatalf("overlay/network count = %v/%d, want curator open with removed network gone", model.overlay, len(model.networkItems))
	}
}

func TestNetworkCurationCancelDoesNotRemove(t *testing.T) {
	model := testModel()
	fake := model.provider.(*fakeProvider)
	fake.networks = []domain.Network{{ID: "unused-id", Name: "old_default", Driver: "bridge"}}
	updated, cmd := model.executeCommand(actions.CurateNetworks)
	model = updated.(Model)
	updated, _ = model.Update(runCmd(t, cmd).(networkListMsg))
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEscape})
	model = updated.(Model)
	if model.networkConfirming || len(fake.removedNetworks) != 0 {
		t.Fatalf("cancel changed cleanup state: confirming=%v removed=%#v", model.networkConfirming, fake.removedNetworks)
	}
}
