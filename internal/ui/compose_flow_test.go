package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/allisonhere/whatthedock/internal/config"
)

// These tests drive the *whole* create/edit flow through the Model — open,
// async load, edit, confirm, actual apply — and then assert on the bytes left
// on disk. The unit tests cover the pure helpers; these cover the seams
// (state transitions + message ordering + real file writes) where every
// dangerous regression so far has actually lived.

// applyComposeThroughModel presses ctrl+enter to open the confirm screen and
// "y" to run the apply command the model builds, returning the resulting
// createDoneMsg (or failing if the model produced no apply command).
func applyComposeThroughModel(t *testing.T, model Model) (Model, createDoneMsg) {
	t.Helper()
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	model = updated.(Model)
	if !model.createDraft.Confirming {
		t.Fatal("Confirming = false after alt+enter, want the confirm screen")
	}
	updated, applyCmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	if applyCmd == nil {
		t.Fatal("no apply command after confirming with y")
	}
	msg := runCmd(t, applyCmd)
	done, ok := msg.(createDoneMsg)
	if !ok {
		t.Fatalf("apply command returned %#v, want createDoneMsg", msg)
	}
	return model, done
}

// TestComposeEditFlowThroughModelPreservesUnmanagedKeys is the end-to-end
// golden path for the live incident this work started from: open Edit on a
// base-defined Compose service, change one field, confirm, and apply. The
// resulting base file must carry the edit while every key the form doesn't
// manage (container_name, network_mode, build) and every comment survive.
func TestComposeEditFlowThroughModelPreservesUnmanagedKeys(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	composeCommand = func(context.Context, composeCreateSpec, ...string) error { return nil }

	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yaml")
	content := `services:
  dash:
    # a comment the form knows nothing about
    build: .
    image: ghcr.io/allisonhere/dash:latest
    container_name: dash
    restart: unless-stopped
    network_mode: host
    ports:
      - "3939:3939"
    volumes:
      - ./data:/config
    environment:
      - PORT=3939
`
	if err := os.WriteFile(base, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	model := modelSelecting("dash", "dash", base)
	loadCmd := model.openEditOverlay()
	if loadCmd == nil {
		t.Fatal("openEditOverlay() returned nil, want a base compose load command")
	}
	updated, _ := model.Update(runCmd(t, loadCmd))
	model = updated.(Model)

	model.createField = createFieldRestart
	model.syncCreateFieldEditor()
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)

	model, done := applyComposeThroughModel(t, model)
	if done.err != nil {
		t.Fatalf("apply error = %v", done.err)
	}

	got, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)
	for _, want := range []string{
		"restart: always",
		"container_name: dash",
		"network_mode: host",
		"build: .",
		"a comment the form knows nothing about",
		`"3939:3939"`,
		"./data:/config",
		"PORT=3939",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("base after the full edit flow = %q, missing %q", out, want)
		}
	}
	if strings.Contains(out, "unless-stopped") {
		t.Fatalf("base after the full edit flow = %q, still has the stale restart value", out)
	}
}

// TestComposeEditFlowValidationFailureLeavesBaseUntouched is the failure-path
// seam: when `docker compose config` rejects the staged file, the original
// base must be byte-for-byte unchanged and no temp/backup left behind. A
// regression here is exactly how a merge could corrupt a live stack.
func TestComposeEditFlowValidationFailureLeavesBaseUntouched(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	composeCommand = func(_ context.Context, _ composeCreateSpec, args ...string) error {
		if len(args) > 0 && args[0] == "config" {
			return errors.New("invalid compose file")
		}
		return nil
	}

	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yaml")
	content := "services:\n  dash:\n    image: dash:latest\n    restart: unless-stopped\n"
	if err := os.WriteFile(base, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	model := modelSelecting("dash", "dash", base)
	loadCmd := model.openEditOverlay()
	if loadCmd == nil {
		t.Fatal("openEditOverlay() returned nil, want a base compose load command")
	}
	updated, _ := model.Update(runCmd(t, loadCmd))
	model = updated.(Model)

	model.createField = createFieldRestart
	model.syncCreateFieldEditor()
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)

	_, done := applyComposeThroughModel(t, model)
	if done.err == nil {
		t.Fatal("apply error = nil, want the validation failure surfaced")
	}

	got, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Fatalf("base changed after a failed validation:\n got %q\nwant %q", got, content)
	}
	if temps, _ := filepath.Glob(base + ".tmp"); len(temps) != 0 {
		t.Fatalf("staged temp file left behind after validation failure: %#v", temps)
	}
	if backups, _ := filepath.Glob(base + ".whatthedock-*.bak"); len(backups) != 0 {
		t.Fatalf("backup created despite validation failure: %#v", backups)
	}
}

// TestComposeEditFlowRemoteMergesChangedFieldsAndBacksUp drives the SSH apply
// seam end to end through the dispatcher: only the edited field may be
// rewritten remotely, the unrelated keys/ports/env must survive, and the
// remote base must be backed up before the atomic promote.
func TestComposeEditFlowRemoteMergesChangedFieldsAndBacksUp(t *testing.T) {
	fake := withFakeSSHRun(t)
	base := "/srv/dash/compose.yaml"
	content := `services:
  dash:
    image: dash:latest
    container_name: dash
    restart: unless-stopped
    network_mode: host
    ports:
      - "3939:3939"
    environment:
      - PORT=3939
`
	fake.respond("cat '"+base+"'", content, nil)

	model := testModelWithSelectedContainer()
	model.systems = []config.System{{
		ID: "remote", Name: "remote", Kind: "ssh", SSHHost: "dock.example",
		RemoteSocket: "/var/run/docker.sock", LocalSocket: "/tmp/whatthedock.sock",
	}}
	model.activeSystem = "remote"
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createDraft.Service = "dash"
	model.createDraft.ComposeFile = base
	model.createDraft.OverrideRaw = content
	model.createDraft.OverrideRawSet = true
	model.createDraft.OverrideRawBase = true
	model.createDraft.loadFields(content)
	model.createDraft.Restart = "always"

	spec, err := model.createDraft.ComposeSpec(model.activeSystemConfig())
	if err != nil {
		t.Fatalf("ComposeSpec() error = %v", err)
	}
	if spec.System.Kind != "ssh" {
		t.Fatalf("spec.System.Kind = %q, want ssh", spec.System.Kind)
	}
	if spec.ChangedFields != composeFieldRestart {
		t.Fatalf("ChangedFields = %b, want only restart", spec.ChangedFields)
	}

	if err := defaultApplyComposeCreate(context.Background(), spec); err != nil {
		t.Fatalf("defaultApplyComposeCreate() error = %v", err)
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
	if staged == "" {
		t.Fatalf("no staged remote base write; calls = %#v", fake.calls)
	}
	for _, want := range []string{"restart: always", "container_name: dash", "network_mode: host", "3939:3939", "PORT=3939"} {
		if !strings.Contains(staged, want) {
			t.Fatalf("staged remote base = %q, missing %q", staged, want)
		}
	}
	if strings.Contains(staged, "unless-stopped") {
		t.Fatalf("staged remote base = %q, still has the stale restart value", staged)
	}
	if !sawBackup {
		t.Fatalf("remote base was not backed up before overwrite; calls = %#v", fake.calls)
	}
	if !sawPromote {
		t.Fatalf("remote base was not promoted atomically; calls = %#v", fake.calls)
	}
}

// TestComposeNewServiceFlowWritesOverrideAndLeavesBaseUntouched covers the
// other apply branch: a brand-new service the base file doesn't define writes
// a generated override and must not modify the base file at all.
func TestComposeNewServiceFlowWritesOverrideAndLeavesBaseUntouched(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	composeCommand = func(context.Context, composeCreateSpec, ...string) error { return nil }

	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yaml")
	baseContent := "services:\n  web:\n    image: nginx:latest\n"
	if err := os.WriteFile(base, []byte(baseContent), 0o644); err != nil {
		t.Fatal(err)
	}

	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createDraft.Project = "media"
	model.createDraft.Service = "cache"
	model.createDraft.Image = "redis:7"
	model.createDraft.ComposeFile = base
	// Re-run the override check against the real path so BaseFileMissing
	// reflects it (openCreateOverlay checked the generic "compose.yml").
	model.checkComposeOverrideCmd()
	model.createDraft.baseline = model.createDraft.editableValues()

	_, done := applyComposeThroughModel(t, model)
	if done.err != nil {
		t.Fatalf("apply error = %v", done.err)
	}

	baseAfter, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	if string(baseAfter) != baseContent {
		t.Fatalf("base changed while creating a new service:\n got %q\nwant %q", baseAfter, baseContent)
	}

	overridePath := filepath.Join(dir, "compose.whatthedock.cache.yml")
	override, err := os.ReadFile(overridePath)
	if err != nil {
		t.Fatalf("ReadFile(override) error = %v, want the generated override written", err)
	}
	for _, want := range []string{"cache", "redis:7"} {
		if !strings.Contains(string(override), want) {
			t.Fatalf("override = %q, missing %q", override, want)
		}
	}
}
