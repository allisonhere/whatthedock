package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/allisonhere/whatthedock/internal/actions"
	"github.com/allisonhere/whatthedock/internal/config"
)

func writeBackup(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreComposeBackupLocalRestoresNewestAndSnapshotsCurrent(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yaml")
	writeBackup(t, base, "services:\n  web:\n    image: broken\n")

	older := base + ".whatthedock-20260101-000000.bak"
	newer := base + ".whatthedock-20260202-000000.bak"
	writeBackup(t, older, "services:\n  web:\n    image: old\n")
	writeBackup(t, newer, "services:\n  web:\n    image: newest\n")

	used, err := restoreComposeBackupLocal(base)
	if err != nil {
		t.Fatalf("restoreComposeBackupLocal() error = %v", err)
	}
	if used != newer {
		t.Fatalf("restored from %q, want the newest backup %q", used, newer)
	}
	got, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "image: newest") {
		t.Fatalf("base = %q, want the newest backup's content", got)
	}

	// The broken current content must have been snapshotted first, so the
	// restore itself is reversible.
	matches, _ := filepath.Glob(composeBackupGlob(base))
	foundSnapshotOfBroken := false
	for _, m := range matches {
		if m == older || m == newer {
			continue
		}
		data, _ := os.ReadFile(m)
		if strings.Contains(string(data), "image: broken") {
			foundSnapshotOfBroken = true
		}
	}
	if !foundSnapshotOfBroken {
		t.Fatalf("no pre-restore snapshot of the current file was written; backups = %#v", matches)
	}
}

func TestRestoreComposeBackupLocalErrorsWithoutBackup(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yaml")
	writeBackup(t, base, "services: {}\n")
	if _, err := restoreComposeBackupLocal(base); err == nil {
		t.Fatal("restoreComposeBackupLocal() error = nil, want a no-backup error")
	}
}

func TestRestoreComposeBackupThroughModel(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yaml")
	writeBackup(t, base, "services:\n  web:\n    image: broken\n")
	writeBackup(t, base+".whatthedock-20260101-000000.bak", "services:\n  web:\n    image: good\n")

	model := modelSelecting("media", "web", base)
	updated, _ := model.executeCommand(actions.RestoreBackup)
	model = updated.(Model)
	if model.overlay != overlayRestoreComposeConfirm {
		t.Fatalf("overlay = %v, want overlayRestoreComposeConfirm", model.overlay)
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("no restore command after confirming")
	}
	msg := runCmd(t, cmd)
	updated, _ = model.Update(msg)
	model = updated.(Model)

	got, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "image: good") {
		t.Fatalf("base = %q, want the backup's content restored", got)
	}
	if !strings.Contains(model.status, "Replicate") {
		t.Fatalf("status = %q, want it to tell the user to run Replicate to apply", model.status)
	}
}

func TestRestoreComposeBackupRemoteUsesNewestAndPromotesAtomically(t *testing.T) {
	fake := withFakeSSHRun(t)
	base := "/srv/web/compose.yaml"
	older := base + ".whatthedock-20260101-000000.bak"
	newer := base + ".whatthedock-20260202-000000.bak"
	fake.respond("ls -1 '"+base+"'.whatthedock-*.bak", older+"\n"+newer+"\n", nil)
	fake.respond("cat '"+newer+"'", "services:\n  web:\n    image: good\n", nil)

	system := config.System{Kind: "ssh", SSHHost: "h", Name: "h"}
	used, err := restoreComposeBackupRemote(context.Background(), system, base)
	if err != nil {
		t.Fatalf("restoreComposeBackupRemote() error = %v", err)
	}
	if used != newer {
		t.Fatalf("restored from %q, want the newest backup %q", used, newer)
	}

	var staged string
	sawBackup, sawPromote := false, false
	for _, call := range fake.calls {
		switch {
		case strings.HasPrefix(call, "cat > '"+base+".tmp'"):
			staged = call
		case strings.HasPrefix(call, "cp -p '"+base+"'"):
			sawBackup = true
		case call == "mv '"+base+".tmp' '"+base+"'":
			sawPromote = true
		}
	}
	if !strings.Contains(staged, "image: good") {
		t.Fatalf("staged remote write = %q, want the backup's content", staged)
	}
	if !sawBackup {
		t.Fatalf("current remote file was not snapshotted before restore; calls = %#v", fake.calls)
	}
	if !sawPromote {
		t.Fatalf("restored remote file was not promoted atomically; calls = %#v", fake.calls)
	}
}

func TestRestoreBackupPaletteActionEnabledForComposeService(t *testing.T) {
	model := modelSelecting("media", "web", "/srv/web/compose.yaml")
	found := false
	for _, cmd := range model.filteredCommands() {
		if cmd.ID == actions.RestoreBackup {
			found = true
			if !cmd.Enabled {
				t.Fatal("RestoreBackup is disabled for a selected Compose service")
			}
		}
	}
	if !found {
		t.Fatal("RestoreBackup missing from the command palette")
	}
}
