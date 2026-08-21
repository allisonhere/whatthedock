package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingCatalogReturnsEmpty(t *testing.T) {
	entries, err := Load(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %#v, want empty", entries)
	}
}

func TestSaveReadRenameDeleteCatalogEntry(t *testing.T) {
	dir := t.TempDir()
	content := "services:\n  dash:\n    image: ghcr.io/allisonhere/dash:latest\n"
	entry, err := Save(dir, "Dash", content)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if entry.ID != "dash" || entry.Name != "Dash" {
		t.Fatalf("entry = %#v, want id dash/name Dash", entry)
	}
	got, err := Read(dir, entry.ID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got != content {
		t.Fatalf("content = %q, want %q", got, content)
	}
	if err := Rename(dir, entry.ID, "Dash prod"); err != nil {
		t.Fatalf("Rename() error = %v", err)
	}
	entries, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "Dash prod" {
		t.Fatalf("entries = %#v, want renamed entry", entries)
	}
	if err := Delete(dir, entry.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, templatesDir, entry.ID+".yml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("template still exists or stat failed unexpectedly: %v", err)
	}
	entries, err = Load(dir)
	if err != nil {
		t.Fatalf("Load() after delete error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries after delete = %#v, want empty", entries)
	}
}

func TestSaveCatalogEntryUsesUniqueIDs(t *testing.T) {
	dir := t.TempDir()
	first, err := Save(dir, "Dash", "services:\n  dash:\n    image: one\n")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Save(dir, "Dash", "services:\n  dash:\n    image: two\n")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "dash" || second.ID != "dash-2" {
		t.Fatalf("ids = %q/%q, want dash/dash-2", first.ID, second.ID)
	}
}
