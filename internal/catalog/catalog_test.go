package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestSaveStackReadFilesAndUpdateNote(t *testing.T) {
	dir := t.TempDir()
	entry, err := SaveStack(dir, "Media stack", "runs media services", "remote", []string{"/srv/media/compose.yml", "/srv/media/override.yml"}, []FileContent{
		{Name: "compose.yml", SourcePath: "/srv/media/compose.yml", Content: "services:\n  jellyfin:\n    image: jellyfin/jellyfin\n", Primary: true},
		{Name: "override.yml", SourcePath: "/srv/media/override.yml", Content: "services:\n  jellyfin:\n    ports:\n      - 8096:8096\n"},
	})
	if err != nil {
		t.Fatalf("SaveStack() error = %v", err)
	}
	if entry.ID != "media-stack" || entry.PrimaryFile != "compose.yml" || len(entry.Files) != 2 {
		t.Fatalf("entry = %#v, want multi-file media-stack", entry)
	}

	files, err := ReadFiles(dir, entry.ID)
	if err != nil {
		t.Fatalf("ReadFiles() error = %v", err)
	}
	if len(files) != 2 || !files[0].Primary {
		t.Fatalf("files = %#v, want primary then override", files)
	}
	if !contains(files[0].Content, NoteHeaderStart) || !contains(files[0].Content, "# runs media services") || contains(files[1].Content, NoteHeaderStart) {
		t.Fatalf("note header applied incorrectly:\nprimary=%s\noverride=%s", files[0].Content, files[1].Content)
	}

	if err := UpdateNote(dir, entry.ID, "updated note"); err != nil {
		t.Fatalf("UpdateNote() error = %v", err)
	}
	content, err := Read(dir, entry.ID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !contains(content, "# updated note") || contains(content, "# runs media services") || !contains(content, "services:") {
		t.Fatalf("updated primary content = %q", content)
	}
}

func TestArchiveStackEntry(t *testing.T) {
	dir := t.TempDir()
	entry, err := SaveStack(dir, "Archive me", "", "local", nil, []FileContent{{Name: "compose.yml", Content: "services:\n  app:\n    image: nginx\n", Primary: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := SetArchived(dir, entry.ID, true); err != nil {
		t.Fatalf("SetArchived() error = %v", err)
	}
	entries, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !entries[0].Archived {
		t.Fatalf("entries = %#v, want archived entry", entries)
	}
}

func TestSetTagsNormalizesAndPersists(t *testing.T) {
	dir := t.TempDir()
	entry, err := SaveStack(dir, "Tagged", "", "local", nil, []FileContent{{Name: "compose.yml", Content: "services:\n  app:\n    image: nginx\n", Primary: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := SetTags(dir, entry.ID, []string{"Prod, #Media", "prod", "  backup  "}); err != nil {
		t.Fatalf("SetTags() error = %v", err)
	}
	entries, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(entries[0].Tags, ","); got != "backup,media,prod" {
		t.Fatalf("tags = %q, want normalized backup,media,prod", got)
	}
}

func TestUpdatePrimaryFilePreservesManagedNoteHeader(t *testing.T) {
	dir := t.TempDir()
	entry, err := SaveStack(dir, "Editable", "keep this note", "local", nil, []FileContent{{Name: "compose.yml", Content: "services:\n  app:\n    image: nginx\n", Primary: true}})
	if err != nil {
		t.Fatal(err)
	}
	replacement := NoteHeaderStart + "\n# stale note\n" + NoteHeaderEnd + "\n\nservices:\n  app:\n    image: caddy\n"
	if err := UpdatePrimaryFile(dir, entry.ID, replacement); err != nil {
		t.Fatalf("UpdatePrimaryFile() error = %v", err)
	}
	content, err := Read(dir, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(content, "# keep this note") || contains(content, "# stale note") || !contains(content, "image: caddy") {
		t.Fatalf("updated content = %q", content)
	}
	entries, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].Status != StatusDraft {
		t.Fatalf("status = %q, want draft after edit", entries[0].Status)
	}
}

func TestDuplicateAsDraftCopiesEntryContentAndMetadata(t *testing.T) {
	dir := t.TempDir()
	entry, err := SaveStack(dir, "Media", "keep note", "local", []string{"/srv/media/compose.yml"}, []FileContent{{
		Name: "compose.yml", SourcePath: "/srv/media/compose.yml", Content: "services:\n  app:\n    image: nginx\n", Primary: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := SetTags(dir, entry.ID, []string{"prod, media"}); err != nil {
		t.Fatal(err)
	}
	draft, err := DuplicateAsDraft(dir, entry.ID)
	if err != nil {
		t.Fatalf("DuplicateAsDraft() error = %v", err)
	}
	if draft.ID == entry.ID || draft.Status != StatusDraft || draft.Note != "keep note" || strings.Join(draft.Tags, ",") != "media,prod" {
		t.Fatalf("draft = %#v, want copied metadata with draft status", draft)
	}
	content, err := Read(dir, draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(content, "image: nginx") || !contains(content, "# keep note") {
		t.Fatalf("draft content = %q, want copied compose and note", content)
	}
}

func TestReplaceStackRefreshesExistingEntryWithoutChangingID(t *testing.T) {
	dir := t.TempDir()
	entry, err := SaveStack(dir, "Media", "keep note", "local", []string{"/srv/media/compose.yml"}, []FileContent{{
		Name: "compose.yml", Content: "services:\n  app:\n    image: nginx\n", Primary: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdatePrimaryFile(dir, entry.ID, "services:\n  app:\n    image: caddy\n"); err != nil {
		t.Fatal(err)
	}
	refreshed, err := ReplaceStack(dir, entry.ID, "", "local", []string{"/srv/media/compose.yml"}, []FileContent{{
		Name: "compose.yml", Content: "services:\n  app:\n    image: redis\n", Primary: true,
	}})
	if err != nil {
		t.Fatalf("ReplaceStack() error = %v", err)
	}
	if refreshed.ID != entry.ID || refreshed.Status != StatusSaved || refreshed.Note != "keep note" {
		t.Fatalf("refreshed = %#v, want same id/status saved/note kept", refreshed)
	}
	content, err := Read(dir, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(content, "image: redis") || !contains(content, "# keep note") {
		t.Fatalf("refreshed content = %q", content)
	}
}

func contains(value, substr string) bool {
	return strings.Contains(value, substr)
}
