package ui

import (
	"testing"

	"github.com/allisonhere/whatthedock/internal/config"
)

// After deleting remote-2 from [local, remote-2, remote-3], len(systems)+1 is
// 3 — remote-3 again, with the same tunnel socket and keychain entry.
func TestAddSystemPicksAnIDNoSystemUses(t *testing.T) {
	model := testModel()
	model.systems = []config.System{
		{ID: "local", Name: "local", Kind: "local"},
		{ID: "remote-3", Name: "nope", Kind: "ssh", SSHHost: "nope"},
	}
	model.startAddSystem()
	if model.systemDraft.ID != "remote-1" {
		t.Fatalf("new system ID = %q, want the free remote-1", model.systemDraft.ID)
	}

	model.systems = append(model.systems, config.System{ID: "remote-1", Name: "a", Kind: "ssh", SSHHost: "a"})
	model.startAddSystem()
	if model.systemDraft.ID != "remote-2" {
		t.Fatalf("new system ID = %q, want remote-2 (remote-1 and remote-3 are taken)", model.systemDraft.ID)
	}
}

// A socket owned by another system counts as taken even under a different ID.
func TestAddSystemAvoidsASocketAnotherSystemOwns(t *testing.T) {
	model := testModel()
	model.startAddSystem()
	first := model.systemDraft
	model.systems = append(model.systems, config.System{ID: "renamed", Name: "r", Kind: "ssh", SSHHost: "h", LocalSocket: first.LocalSocket})
	model.startAddSystem()
	if model.systemDraft.LocalSocket == first.LocalSocket || model.systemDraft.ID == first.ID {
		t.Fatalf("new system %q reuses socket %q owned by another system", model.systemDraft.ID, first.LocalSocket)
	}
}

// A new system whose ID exists must not save, and must not touch the other
// system's keychain entry.
func TestSaveNewSystemRejectsDuplicateIDWithoutStoringPassword(t *testing.T) {
	model := testModel()
	store := stubPasswordStore(&model)
	store["remote-1"] = "original"
	model.systems = append(model.systems, config.System{ID: "remote-1", Name: "a", Kind: "ssh", SSHHost: "a", SSHAuth: "keychain"})
	model.systemDraft = config.System{ID: "remote-1", Name: "b", Kind: "ssh", SSHHost: "b", SSHAuth: "keychain"}
	model.systemDraftNew = true
	model.systemDraftPassword = "intruder"

	model.saveSystemDraft()

	if !model.statusErr || store["remote-1"] != "original" || len(model.systems) != 2 {
		t.Fatalf("err=%v store=%v systems=%d; a duplicate ID must be rejected before the keychain is written", model.statusErr, store, len(model.systems))
	}
}

func TestSwitchingAwayFromKeychainForgetsTheStoredPassword(t *testing.T) {
	model := testModel()
	store := stubPasswordStore(&model)
	store["remote-1"] = "secret"
	model.systems = append(model.systems, config.System{ID: "remote-1", Name: "a", Kind: "ssh", SSHHost: "a", SSHUser: "u", SSHAuth: "keychain"})
	model.systemsCursor = 1
	model.systemDraft = config.System{ID: "remote-1", Name: "a", Kind: "ssh", SSHHost: "a", SSHUser: "u", SSHAuth: "config"}

	model.saveSystemDraft()

	if _, still := store["remote-1"]; still {
		t.Fatalf("password for remote-1 survived a switch to config auth: %v", store)
	}
}

func TestDeletingASystemForgetsItsPasswordWhateverItsCurrentAuth(t *testing.T) {
	model := testModel()
	store := stubPasswordStore(&model)
	store["remote-1"] = "stale"
	model.systems = append(model.systems, config.System{ID: "remote-1", Name: "a", Kind: "ssh", SSHHost: "a", SSHAuth: "config"})
	model.systemsCursor = 1

	model.deleteCurrentSystem()

	if _, still := store["remote-1"]; still || len(model.systems) != 1 {
		t.Fatalf("store=%v systems=%d; delete should drop the system and any leftover secret", store, len(model.systems))
	}
}
