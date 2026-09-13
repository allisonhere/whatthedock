package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/allisonhere/whatthedock/internal/config"
)

// TestApplyComposeEditBacksUpBaseBeforeOverwrite guards the safety net: any
// apply that rewrites the user's base compose file must snapshot the previous
// contents first, so a bad merge stays recoverable.
func TestApplyComposeEditBacksUpBaseBeforeOverwrite(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	composeCommand = func(context.Context, composeCreateSpec, ...string) error { return nil }

	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yaml")
	originalContent := "services:\n  web:\n    image: nginx\n    restart: unless-stopped\n"
	if err := os.WriteFile(base, []byte(originalContent), 0o644); err != nil {
		t.Fatal(err)
	}

	draft := createDraft{
		Mode:            createModeCompose,
		Editing:         true,
		Project:         "web",
		Service:         "web",
		Image:           "nginx",
		Restart:         "always",
		ComposeFile:     base,
		OverrideRaw:     originalContent,
		OverrideRawSet:  true,
		OverrideRawBase: true,
		FieldsDirty:     true,
	}
	spec, err := draft.ComposeSpec(config.DefaultSystem())
	if err != nil {
		t.Fatalf("ComposeSpec() error = %v", err)
	}
	if err := defaultApplyComposeCreate(context.Background(), spec); err != nil {
		t.Fatalf("defaultApplyComposeCreate() error = %v", err)
	}

	backups, err := filepath.Glob(base + ".whatthedock-*.bak")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("backups = %#v, want exactly one snapshot of the pre-apply base", backups)
	}
	got, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != originalContent {
		t.Fatalf("backup content = %q, want the original base %q", got, originalContent)
	}
}

func TestBackupComposeBasePrunesOldSnapshots(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(base, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < composeBackupKeep+3; i++ {
		if err := os.WriteFile(composeBackupPath(base, time.Unix(int64(i), 0)), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := backupComposeBase(base); err != nil {
		t.Fatalf("backupComposeBase() error = %v", err)
	}
	matches, err := filepath.Glob(base + ".whatthedock-*.bak")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != composeBackupKeep {
		t.Fatalf("backups = %d, want %d after pruning", len(matches), composeBackupKeep)
	}
}

func TestDiffLinesMarksAddRemoveAndUnchanged(t *testing.T) {
	got := diffLines([]string{"a", "b", "c"}, []string{"a", "x", "c"})
	want := []string{"  a", "- b", "+ x", "  c"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("diffLines() = %#v, want %#v", got, want)
	}
}

// TestComposeChangePreviewMirrorsMerge is the core of the confirm-screen fix:
// the shown after-image must be the actual merged base file (unmanaged keys
// preserved, edited field applied), not the sparse regenerated document that
// used to be previewed while something else got written.
func TestComposeChangePreviewMirrorsMerge(t *testing.T) {
	content := `services:
  dash:
    build: .
    image: ghcr.io/allisonhere/dash:latest
    container_name: dash
    restart: unless-stopped
    network_mode: host
`
	draft := createDraft{
		Mode:            createModeCompose,
		Project:         "dash",
		Service:         "dash",
		Image:           "ghcr.io/allisonhere/dash:latest",
		Restart:         "always",
		ComposeFile:     "/srv/dash/compose.yaml",
		OverrideRaw:     content,
		OverrideRawSet:  true,
		OverrideRawBase: true,
		FieldsDirty:     true,
	}

	label, before, after, ok := draft.composeChangePreview(config.DefaultSystem())
	if !ok {
		t.Fatal("composeChangePreview() ok = false, want a preview for a compose edit")
	}
	if label != "/srv/dash/compose.yaml" {
		t.Fatalf("target = %q, want the base compose file path", label)
	}
	if before != content {
		t.Fatalf("before = %q, want the loaded base content", before)
	}
	for _, want := range []string{"container_name: dash", "network_mode: host", "build: .", "restart: always"} {
		if !strings.Contains(after, want) {
			t.Fatalf("after = %q, missing %q", after, want)
		}
	}
	if strings.Contains(after, "unless-stopped") {
		t.Fatalf("after = %q, still has the stale restart value", after)
	}
}

func TestRenderConfirmDiffOmitsContextAndCaps(t *testing.T) {
	lines := []string{"  unchanged", "- old", "+ new", "  also unchanged"}
	out := renderConfirmDiff("compose.yaml", lines, 20)
	if !strings.Contains(out, "Changes to compose.yaml") || !strings.Contains(out, "1 added, 1 removed") {
		t.Fatalf("renderConfirmDiff() = %q, want header and summary", out)
	}
	if !strings.Contains(out, "- old") || !strings.Contains(out, "+ new") {
		t.Fatalf("renderConfirmDiff() = %q, want the changed lines", out)
	}
	if strings.Contains(out, "unchanged") {
		t.Fatalf("renderConfirmDiff() = %q, want unchanged context omitted", out)
	}

	if noChange := renderConfirmDiff("compose.yaml", []string{"  same"}, 20); !strings.Contains(noChange, "no changes") {
		t.Fatalf("renderConfirmDiff() with no diff = %q, want a no-changes note", noChange)
	}

	many := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		many = append(many, "+ line")
	}
	capped := renderConfirmDiff("compose.yaml", many, 5)
	if !strings.Contains(capped, "more changed lines") {
		t.Fatalf("renderConfirmDiff() = %q, want a truncation note", capped)
	}
}

// TestComposeConfirmScreenShowsWhatWillChange is the end-to-end guard for the
// confirm-screen fix: editing a field on a base-defined service and asking to
// confirm must show the diff of the base file that will actually be written,
// not the sparse regenerated preview.
func TestComposeConfirmScreenShowsWhatWillChange(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yaml")
	content := "services:\n  web:\n    image: nginx\n    restart: unless-stopped\n"
	if err := os.WriteFile(base, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	model := modelSelecting("web", "web", base)
	cmd := model.openEditOverlay()
	if cmd == nil {
		t.Fatal("openEditOverlay() returned nil, want a base compose load command")
	}
	msg := runCmd(t, cmd).(createSelectedComposeFileMsg)
	updated, _ := model.Update(msg)
	model = updated.(Model)

	model.createField = createFieldRestart
	model.syncCreateFieldEditor()
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	model = updated.(Model)
	if !model.createDraft.Confirming {
		t.Fatal("Confirming = false after alt+enter, want the confirm screen")
	}

	model.width, model.height = 120, 40
	view := ansi.Strip(model.View())
	if !strings.Contains(view, "Changes to compose.yaml") {
		t.Fatalf("confirm view missing the diff header:\n%s", view)
	}
	if !strings.Contains(view, "restart: always") {
		t.Fatalf("confirm view missing the proposed restart change:\n%s", view)
	}
}
