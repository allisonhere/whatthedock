package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The legend used to be built once at init, so a theme change left its colors
// (the FLEET card, the help legend) on the default theme's.
func TestStatusLegendFollowsTheActivePalette(t *testing.T) {
	original := activePalette
	defer func() { activePalette = original }()
	activePalette.OK = "#123456"
	activePalette.Bad = "#654321"

	entries := statusLegendEntries()
	if entries[0].color != "#123456" || entries[3].color != "#654321" {
		t.Fatalf("legend colors = %v / %v, want the changed palette's", entries[0].color, entries[3].color)
	}
}

// [, ], * and ? are legal in a path but make base+".whatthedock-*.bak" a
// different pattern, so restore and prune found no backups.
func TestComposeBackupsHandleGlobMetacharactersInThePath(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "stack [prod]*?.yml")
	os.WriteFile(base, []byte("current"), 0o644)
	old1 := composeBackupPath(base, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	old2 := composeBackupPath(base, time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC))
	os.WriteFile(old1, []byte("one"), 0o644)
	os.WriteFile(old2, []byte("two"), 0o644)
	os.WriteFile(filepath.Join(dir, "other.yml.whatthedock-20240101-000000.bak"), []byte("x"), 0o644) // someone else's

	got, err := composeBackups(base)
	if err != nil || len(got) != 2 || got[0] != old1 || got[1] != old2 {
		t.Fatalf("composeBackups = %v, %v; want [%s %s]", got, err, old1, old2)
	}

	backup, err := restoreComposeBackupLocal(base)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if backup != old2 {
		t.Fatalf("restored %q, want the newest backup %q", backup, old2)
	}
	if data, _ := os.ReadFile(base); string(data) != "two" {
		t.Fatalf("base = %q after restore, want the newest backup's content", data)
	}
}

func TestPruneComposeBackupsHandlesGlobMetacharactersInThePath(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "a[1].yml")
	os.WriteFile(base, []byte("current"), 0o644)
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < composeBackupKeep+3; i++ {
		os.WriteFile(composeBackupPath(base, start.Add(time.Duration(i)*time.Hour)), []byte("b"), 0o644)
	}
	if err := backupComposeBase(base); err != nil { // adds one more, then prunes
		t.Fatal(err)
	}
	got, _ := composeBackups(base)
	if len(got) != composeBackupKeep {
		t.Fatalf("%d backups left, want %d", len(got), composeBackupKeep)
	}
}
