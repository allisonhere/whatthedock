package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/allisonhere/ripple"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/allisonhere/whatthedock/internal/actions"
	"github.com/allisonhere/whatthedock/internal/config"
	"github.com/allisonhere/whatthedock/internal/domain"
)

func TestCreateOverlayOpensFromShortcutAndRendersPreview(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.width, model.height = 120, 34

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	model = updated.(Model)

	if model.overlay != overlayCreate {
		t.Fatalf("overlay = %v, want create", model.overlay)
	}
	view := ansi.Strip(model.View())
	for _, want := range []string{"whatthedock · create", "Compose service", "Standalone container", "services:", "radarr", "ctrl+s validate", "Draft looks good"} {
		if !strings.Contains(view, want) {
			t.Fatalf("create overlay missing %q:\n%s", want, view)
		}
	}
}

func TestCreateCommandPaletteActionOpensOverlay(t *testing.T) {
	model := testModel()

	updated, cmd := model.executeCommand(actions.Create)
	model = updated.(Model)

	if cmd != nil {
		t.Fatalf("create command returned cmd, want nil")
	}
	if model.overlay != overlayCreate {
		t.Fatalf("overlay = %v, want create", model.overlay)
	}
}

func TestCreateOverlayTextFieldsAcceptNavigationLetters(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.moveCreateField(1) // off the Mode tab pseudo-field, onto Project

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	model = updated.(Model)

	if model.createDraft.Project != "l" {
		t.Fatalf("project = %q, want typed l", model.createDraft.Project)
	}
}

// TestCreateValidationAllowsRemoteComposeEditing guards against a
// regression of the "compose editing is local-only" limitation: Compose
// drafts against an SSH system must validate the same as local ones now
// that browsing/writing/applying all have SSH-aware paths.
func TestCreateValidationAllowsRemoteComposeEditing(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.systems = []config.System{{ID: "remote", Name: "remote", Kind: "ssh", SSHHost: "dock.example", RemoteSocket: "/var/run/docker.sock", LocalSocket: "/tmp/whatthedock.sock"}}
	model.activeSystem = "remote"
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)

	if model.statusErr || !strings.Contains(model.status, "validated") {
		t.Fatalf("status/statusErr = %q/%v, want a successful validation", model.status, model.statusErr)
	}
}

func TestCreateStandaloneConfirmsBeforeProviderCreate(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.width, model.height = 120, 34
	model.openCreateOverlay()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	model = updated.(Model)

	if model.createDraft.Mode != createModeStandalone {
		t.Fatalf("mode = %v, want standalone", model.createDraft.Mode)
	}
	if !model.createDraft.Confirming {
		t.Fatalf("confirming = false, want true")
	}
	if len(model.provider.(*fakeProvider).creates) != 0 {
		t.Fatalf("provider create ran before confirmation")
	}
	view := ansi.Strip(model.View())
	if !strings.Contains(view, "whatthedock · confirm create") || !strings.Contains(view, "y create") {
		t.Fatalf("confirm overlay missing expected copy:\n%s", view)
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("confirm returned nil cmd, want create command")
	}
	msg := runCmd(t, cmd).(createDoneMsg)
	updated, cmd = model.Update(msg)
	model = updated.(Model)

	if len(model.provider.(*fakeProvider).creates) != 1 {
		t.Fatalf("creates = %d, want 1", len(model.provider.(*fakeProvider).creates))
	}
	if model.overlay != overlayNone || model.statusErr || !strings.Contains(model.status, "created") {
		t.Fatalf("overlay/status/statusErr = %v/%q/%v, want created and closed", model.overlay, model.status, model.statusErr)
	}
	if cmd == nil {
		t.Fatal("create completion returned nil cmd, want refresh")
	}
}

func TestCreateStandaloneSpecParsesFields(t *testing.T) {
	draft := createDraft{
		Mode:          createModeStandalone,
		ContainerName: "cache",
		Image:         "redis:7",
		Command:       "redis-server --appendonly yes",
		Ports:         "127.0.0.1:6379:6379/tcp",
		Mounts:        "redis-data:/data:ro",
		Env:           "REDIS_APPENDONLY=yes",
		Restart:       "always",
	}

	spec, err := draft.ContainerSpec()
	if err != nil {
		t.Fatalf("ContainerSpec() error = %v", err)
	}
	if spec.Name != "cache" || spec.Image != "redis:7" || spec.RestartPolicy != "always" || !spec.Start {
		t.Fatalf("spec basics = %#v", spec)
	}
	if got := strings.Join(spec.Command, " "); got != "redis-server --appendonly yes" {
		t.Fatalf("command = %q", got)
	}
	if len(spec.Ports) != 1 || spec.Ports[0].HostIP != "127.0.0.1" || spec.Ports[0].HostPort != 6379 || spec.Ports[0].ContainerPort != 6379 {
		t.Fatalf("ports = %#v", spec.Ports)
	}
	if len(spec.Mounts) != 1 || spec.Mounts[0].Source != "redis-data" || spec.Mounts[0].Destination != "/data" || !spec.Mounts[0].ReadOnly {
		t.Fatalf("mounts = %#v", spec.Mounts)
	}
	if len(spec.Env) != 1 || spec.Env[0] != "REDIS_APPENDONLY=yes" {
		t.Fatalf("env = %#v", spec.Env)
	}
}

func TestCreateComposeSpecWritesOverrideBesideBaseFile(t *testing.T) {
	draft := createDraft{
		Mode:        createModeCompose,
		Project:     "media",
		Service:     "Redis Cache",
		Image:       "redis:7",
		Ports:       "6379:6379",
		Mounts:      "redis-data:/data",
		Env:         "REDIS_APPENDONLY=yes",
		Restart:     "unless-stopped",
		ComposeFile: "/srv/media/compose.yml",
	}

	spec, err := draft.ComposeSpec(config.DefaultSystem())
	if err != nil {
		t.Fatalf("ComposeSpec() error = %v", err)
	}
	if spec.Project != "media" || spec.Service != "Redis Cache" || spec.BaseFile != "/srv/media/compose.yml" {
		t.Fatalf("spec basics = %#v", spec)
	}
	if spec.OverrideFile != "/srv/media/compose.whatthedock.redis-cache.yml" {
		t.Fatalf("override = %q", spec.OverrideFile)
	}
	for _, want := range []string{`"Redis Cache":`, `image: "redis:7"`, `- "6379:6379"`, `- "redis-data:/data"`, `- "REDIS_APPENDONLY=yes"`} {
		if !strings.Contains(spec.Content, want) {
			t.Fatalf("content missing %q:\n%s", want, spec.Content)
		}
	}
}

func TestCreateComposeConfirmsBeforeApply(t *testing.T) {
	original := applyComposeCreate
	defer func() { applyComposeCreate = original }()
	var applied []composeCreateSpec
	applyComposeCreate = func(_ context.Context, spec composeCreateSpec) error {
		applied = append(applied, spec)
		return nil
	}

	model := testModelWithSelectedContainer()
	model.width, model.height = 120, 34
	model.openCreateOverlay()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	model = updated.(Model)
	if !model.createDraft.Confirming {
		t.Fatalf("confirming = false, want true")
	}
	if len(applied) != 0 {
		t.Fatalf("compose apply ran before confirmation")
	}
	view := ansi.Strip(model.View())
	if !strings.Contains(view, "whatthedock · confirm create") || !strings.Contains(view, "compose.whatthedock") {
		t.Fatalf("confirm overlay missing compose apply copy:\n%s", view)
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("confirm returned nil cmd, want compose apply command")
	}
	msg := runCmd(t, cmd).(createDoneMsg)
	updated, cmd = model.Update(msg)
	model = updated.(Model)

	if len(applied) != 1 {
		t.Fatalf("applied = %d, want 1", len(applied))
	}
	if model.overlay != overlayNone || model.statusErr || !strings.Contains(model.status, "created") {
		t.Fatalf("overlay/status/statusErr = %v/%q/%v, want created and closed", model.overlay, model.status, model.statusErr)
	}
	if cmd == nil {
		t.Fatal("compose completion returned nil cmd, want refresh")
	}
}

func TestCreateFileEntriesShowsDirsAndComposeFilesOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "stack"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"compose.yml", "docker-compose.yaml", "compose.prod.yml", "notes.txt", "values.yaml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := createFileEntries(dir, filepath.Join(dir, "compose.yml"))
	if err != nil {
		t.Fatalf("createFileEntries() error = %v", err)
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	for _, want := range []string{"..", "stack", "compose.prod.yml", "compose.yml", "docker-compose.yaml"} {
		if !containsString(names, want) {
			t.Fatalf("entries = %#v, missing %q", names, want)
		}
	}
	for _, notWant := range []string{"notes.txt", "values.yaml"} {
		if containsString(names, notWant) {
			t.Fatalf("entries = %#v, unexpectedly included %q", names, notWant)
		}
	}
}

func TestCreateComposeFileBrowserSelectsFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(target, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	model := testModelWithSelectedContainer()
	model.width, model.height = 120, 34
	model.openCreateOverlay()
	model.createDraft.ComposeFile = filepath.Join(dir, "compose.yml")
	model.createField = createFieldComposeFile

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if !model.createBrowsing {
		t.Fatal("createBrowsing = false, want true")
	}
	for i, entry := range model.createFiles {
		if entry.Path == target {
			model.createFileCursor = i
			break
		}
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)

	if model.createBrowsing {
		t.Fatal("createBrowsing = true after selecting file")
	}
	if model.createDraft.ComposeFile != target {
		t.Fatalf("ComposeFile = %q, want %q", model.createDraft.ComposeFile, target)
	}
}

func TestCreateComposeFileBrowserOpensWithCtrlOFromAnyCreateField(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.ComposeFile = filepath.Join(dir, "compose.yml")
	model.createField = createFieldProject

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model = updated.(Model)

	if !model.createBrowsing {
		t.Fatal("createBrowsing = false after ctrl+o, want true")
	}
}

// TestCreateComposeFileBrowserOpensWithOFromChoiceField checks where bare
// "o" is still a browse shortcut: choice fields (Mode/Restart) that have no
// other use for a plain letter anyway. See
// TestCreateOTypesNormallyOnTextFields, including the Compose file row
// itself, for the fields where it must NOT hijack typing.
func TestCreateComposeFileBrowserOpensWithOFromChoiceField(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, field := range []createField{createFieldMode, createFieldRestart} {
		t.Run(createFieldLabel(field), func(t *testing.T) {
			model := testModelWithSelectedContainer()
			model.openCreateOverlay()
			model.createDraft.ComposeFile = filepath.Join(dir, "compose.yml")
			model.createField = field

			updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
			model = updated.(Model)

			if !model.createBrowsing {
				t.Fatalf("createBrowsing = false after o on %v, want true", field)
			}
		})
	}
}

// TestCreateOTypesNormallyOnTextFields guards against the bug found while
// testing remote browsing: bare "o" used to open the file browser from ANY
// field in Compose mode, including plain text fields and the Compose file
// row itself — meaning you could never type a value containing the letter
// "o" (like "postgres", "sonarr", or "compose.yml" itself, which contains
// two) without the browser popping open mid-keystroke. Only choice fields
// (Mode/Restart) treat it as a shortcut now; Enter or Ctrl+O still open the
// browser from the Compose file field.
func TestCreateOTypesNormallyOnTextFields(t *testing.T) {
	for _, field := range []createField{createFieldProject, createFieldService, createFieldImage, createFieldPorts, createFieldMounts, createFieldEnv, createFieldComposeFile} {
		t.Run(createFieldLabel(field), func(t *testing.T) {
			model := testModelWithSelectedContainer()
			model.openCreateOverlay()
			model.createDraft.Mode = createModeCompose
			model.createField = field

			updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
			model = updated.(Model)

			if model.createBrowsing {
				t.Fatalf("createBrowsing = true after typing o into %v, want the letter typed instead", field)
			}
		})
	}
}

func TestCreateStandaloneTextFieldsStillAcceptO(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeStandalone
	model.createField = createFieldContainerName
	model.createDraft.ContainerName = ""

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	model = updated.(Model)

	if model.createBrowsing {
		t.Fatal("createBrowsing = true in standalone mode, want typed text")
	}
	if model.createDraft.ContainerName != "o" {
		t.Fatalf("ContainerName = %q, want typed o", model.createDraft.ContainerName)
	}
}

func TestCreateFileBrowserOpensFromStandaloneModeViaCtrlO(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeStandalone
	model.createDraft.ComposeFile = filepath.Join(dir, "compose.yml")
	model.createField = createFieldContainerName

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model = updated.(Model)

	if !model.createBrowsing {
		t.Fatal("createBrowsing = false after ctrl+o from standalone mode, want true")
	}
	if model.createDraft.Mode != createModeCompose || model.createField != createFieldComposeFile {
		t.Fatalf("mode/field = %v/%v, want compose/compose-file", model.createDraft.Mode, model.createField)
	}
}

func TestCreateModeTabsSwitchWithBracketKeysFromAnyField(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeStandalone
	model.createField = createFieldImage
	model.createDraft.Image = "custom:tag"

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	model = updated.(Model)

	if model.createDraft.Mode != createModeCompose {
		t.Fatalf("mode = %v after ], want compose", model.createDraft.Mode)
	}
	if model.createField != createFieldImage {
		t.Fatalf("field = %v after mode switch, want createFieldImage to carry over (shared by both modes)", model.createField)
	}
	if model.createDraft.Image != "custom:tag" {
		t.Fatalf("Image = %q after mode switch, want draft preserved", model.createDraft.Image)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	model = updated.(Model)
	if model.createDraft.Mode != createModeStandalone {
		t.Fatalf("mode = %v after [, want standalone", model.createDraft.Mode)
	}
}

func TestCreateModeTabsResetFieldWhenNotSharedBetweenModes(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createField = createFieldService

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	model = updated.(Model)

	if model.createDraft.Mode != createModeStandalone {
		t.Fatalf("mode = %v after ], want standalone", model.createDraft.Mode)
	}
	standaloneFields := model.visibleCreateFields()
	if model.createField != standaloneFields[0] {
		t.Fatalf("field = %v after switching away from a compose-only field, want reset to first standalone field %v", model.createField, standaloneFields[0])
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestApplyComposeCreateValidatesTempBeforePromote(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	var calls []string
	composeCommand = func(_ context.Context, spec composeCreateSpec, args ...string) error {
		calls = append(calls, spec.OverrideFile+" "+strings.Join(args, " "))
		return nil
	}
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(base, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := composeCreateSpec{
		Project:      "media",
		Service:      "cache",
		BaseFile:     base,
		OverrideFile: filepath.Join(dir, "compose.whatthedock.cache.yml"),
		Content:      "services:\n  cache:\n    image: redis:7\n",
	}

	if err := defaultApplyComposeCreate(context.Background(), spec); err != nil {
		t.Fatalf("defaultApplyComposeCreate() error = %v", err)
	}
	if _, err := os.Stat(spec.OverrideFile); err != nil {
		t.Fatalf("override was not promoted: %v", err)
	}
	if _, err := os.Stat(spec.OverrideFile + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp override still exists or stat failed unexpectedly: %v", err)
	}
	if len(calls) != 2 || !strings.Contains(calls[0], ".tmp config") || !strings.Contains(calls[1], "up -d cache") || strings.Contains(calls[1], ".tmp") {
		t.Fatalf("compose calls = %#v", calls)
	}
}

func TestApplyComposeCreateRemovesTempOnValidationFailure(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	composeCommand = func(_ context.Context, _ composeCreateSpec, args ...string) error {
		if len(args) == 1 && args[0] == "config" {
			return errors.New("invalid compose")
		}
		return nil
	}
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(base, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := composeCreateSpec{
		Project:      "media",
		Service:      "cache",
		BaseFile:     base,
		OverrideFile: filepath.Join(dir, "compose.whatthedock.cache.yml"),
		Content:      "services:\n  cache:\n    image: redis:7\n",
	}

	err := defaultApplyComposeCreate(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "invalid compose") {
		t.Fatalf("error = %v, want invalid compose", err)
	}
	if _, err := os.Stat(spec.OverrideFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("final override exists after failed validation or stat failed unexpectedly: %v", err)
	}
	if _, err := os.Stat(spec.OverrideFile + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp override exists after failed validation or stat failed unexpectedly: %v", err)
	}
}

// fakeSSHRun swaps in for sshRun so remote-Compose tests never shell out to a
// real ssh binary. It records every script (and stdin) it was called with, in
// order, and dispatches canned responses by exact script match.
type fakeSSHRun struct {
	calls     []string
	responses map[string]struct {
		output []byte
		err    error
	}
}

func (f *fakeSSHRun) run(_ context.Context, _ config.System, script string, stdin string) ([]byte, error) {
	call := script
	if stdin != "" {
		call += "\x00stdin=" + stdin
	}
	f.calls = append(f.calls, call)
	if resp, ok := f.responses[script]; ok {
		return resp.output, resp.err
	}
	return nil, nil
}

func (f *fakeSSHRun) respond(script string, output string, err error) {
	if f.responses == nil {
		f.responses = map[string]struct {
			output []byte
			err    error
		}{}
	}
	f.responses[script] = struct {
		output []byte
		err    error
	}{[]byte(output), err}
}

func withFakeSSHRun(t *testing.T) *fakeSSHRun {
	t.Helper()
	fake := &fakeSSHRun{}
	original := sshRun
	sshRun = fake.run
	t.Cleanup(func() { sshRun = original })
	return fake
}

func TestRemoteFileEntriesParsesListing(t *testing.T) {
	fake := withFakeSSHRun(t)
	system := config.System{Kind: "ssh", SSHHost: "jarvis", Name: "jarvis"}
	fake.respond("cd '/srv/media-stack' 2>&1 && pwd && ls -1Ap",
		"/srv/media-stack\ncompose.yml\ncompose.override.yml\nREADME.md\nsubdir/\n", nil)

	entries, resolvedDir, err := remoteFileEntries(context.Background(), system, "/srv/media-stack", "/srv/media-stack/compose.override.yml")
	if err != nil {
		t.Fatalf("remoteFileEntries() error = %v", err)
	}
	if resolvedDir != "/srv/media-stack" {
		t.Fatalf("resolvedDir = %q, want /srv/media-stack", resolvedDir)
	}
	if len(entries) != 4 { // .. , subdir/, compose.yml, compose.override.yml (README.md is filtered out)
		t.Fatalf("entries = %#v, want 4 (parent, one dir, two compose files)", entries)
	}
	if entries[0].Name != ".." || !entries[0].Parent {
		t.Fatalf("entries[0] = %#v, want parent entry", entries[0])
	}
	if entries[1].Name != "subdir" || !entries[1].Dir {
		t.Fatalf("entries[1] = %#v, want subdir directory", entries[1])
	}
	names := []string{entries[2].Name, entries[3].Name}
	if names[0] != "compose.override.yml" && names[1] != "compose.override.yml" {
		t.Fatalf("compose files = %v, want compose.override.yml among them", names)
	}
	for _, e := range entries[2:] {
		if e.Name == "compose.override.yml" && !e.Selected {
			t.Fatalf("compose.override.yml not marked selected: %#v", e)
		}
	}
}

func TestRemoteFileEntriesReturnsErrorFromSSH(t *testing.T) {
	fake := withFakeSSHRun(t)
	system := config.System{Kind: "ssh", SSHHost: "jarvis", Name: "jarvis"}
	fake.respond("cd '/no/such/dir' 2>&1 && pwd && ls -1Ap", "", errors.New("no such file or directory"))

	_, _, err := remoteFileEntries(context.Background(), system, "/no/such/dir", "")
	if err == nil || !strings.Contains(err.Error(), "no such file or directory") {
		t.Fatalf("err = %v, want the remote error surfaced", err)
	}
}

func TestBrowseCreateDirIsAsyncForSSHSystems(t *testing.T) {
	fake := withFakeSSHRun(t)
	fake.respond("cd '/srv/media-stack' 2>&1 && pwd && ls -1Ap", "/srv/media-stack\ncompose.yml\n", nil)

	model := testModelWithSelectedContainer()
	model.systems = []config.System{{ID: "remote", Name: "jarvis", Kind: "ssh", SSHHost: "jarvis", RemoteSocket: "/var/run/docker.sock", LocalSocket: "/tmp/whatthedock.sock"}}
	model.activeSystem = "remote"

	cmd := model.browseCreateDir("/srv/media-stack")
	if cmd == nil {
		t.Fatal("browseCreateDir() returned nil cmd for an SSH system, want an async listing command")
	}
	if !model.createFileLoading {
		t.Fatal("createFileLoading = false immediately after starting an SSH browse, want true")
	}
	if len(fake.calls) != 0 {
		t.Fatalf("sshRun invoked synchronously (%d calls) — browsing must not block Update", len(fake.calls))
	}

	msg := runCmd(t, cmd).(createFileBrowseMsg)
	updated, _ := model.Update(msg)
	model = updated.(Model)

	if model.createFileLoading {
		t.Fatal("createFileLoading = true after the browse message landed, want false")
	}
	if model.createBrowseDir != "/srv/media-stack" || len(model.createFiles) != 2 { // ".." parent + compose.yml
		t.Fatalf("browseDir/files = %q/%#v after remote listing", model.createBrowseDir, model.createFiles)
	}
}

func TestApplyComposeCreateRemoteWritesAndAppliesOverSSH(t *testing.T) {
	fake := withFakeSSHRun(t)
	system := config.System{Kind: "ssh", SSHHost: "jarvis", Name: "jarvis"}
	spec := composeCreateSpec{
		Project:      "media-stack",
		Service:      "cache",
		BaseFile:     "/srv/media-stack/compose.yml",
		OverrideFile: "/srv/media-stack/compose.whatthedock.cache.yml",
		Content:      "services:\n  cache:\n    image: redis:7\n",
		System:       system,
	}

	if err := applyComposeCreateRemote(context.Background(), spec); err != nil {
		t.Fatalf("applyComposeCreateRemote() error = %v", err)
	}

	wantScripts := []string{
		"test -f '/srv/media-stack/compose.yml'",
		"mkdir -p '/srv/media-stack'",
		"cat > '/srv/media-stack/compose.whatthedock.cache.yml.tmp'\x00stdin=" + spec.Content,
		"docker 'compose' '-p' 'media-stack' '-f' '/srv/media-stack/compose.yml' '-f' '/srv/media-stack/compose.whatthedock.cache.yml.tmp' 'config'",
		"mv '/srv/media-stack/compose.whatthedock.cache.yml.tmp' '/srv/media-stack/compose.whatthedock.cache.yml'",
		"docker 'compose' '-p' 'media-stack' '-f' '/srv/media-stack/compose.yml' '-f' '/srv/media-stack/compose.whatthedock.cache.yml' 'up' '-d' 'cache'",
	}
	if len(fake.calls) != len(wantScripts) {
		t.Fatalf("calls = %#v, want %d calls matching %#v", fake.calls, len(wantScripts), wantScripts)
	}
	for i, want := range wantScripts {
		if fake.calls[i] != want {
			t.Fatalf("call %d = %q, want %q", i, fake.calls[i], want)
		}
	}
}

func TestApplyComposeCreateRemoteCleansUpTempOnValidationFailure(t *testing.T) {
	fake := withFakeSSHRun(t)
	system := config.System{Kind: "ssh", SSHHost: "jarvis", Name: "jarvis"}
	spec := composeCreateSpec{
		Project:      "media-stack",
		Service:      "cache",
		BaseFile:     "/srv/media-stack/compose.yml",
		OverrideFile: "/srv/media-stack/compose.whatthedock.cache.yml",
		Content:      "services:\n  cache:\n    image: redis:7\n",
		System:       system,
	}
	fake.respond(
		"docker 'compose' '-p' 'media-stack' '-f' '/srv/media-stack/compose.yml' '-f' '/srv/media-stack/compose.whatthedock.cache.yml.tmp' 'config'",
		"", errors.New("invalid compose"),
	)

	err := applyComposeCreateRemote(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "invalid compose") {
		t.Fatalf("error = %v, want invalid compose", err)
	}
	last := fake.calls[len(fake.calls)-1]
	if last != "rm -f '/srv/media-stack/compose.whatthedock.cache.yml.tmp'" {
		t.Fatalf("last call = %q, want the temp override cleaned up", last)
	}
	for _, call := range fake.calls {
		if strings.HasPrefix(call, "mv ") {
			t.Fatalf("promoted the override despite failed validation: %#v", fake.calls)
		}
	}
}

func TestCreateEditorOpensPrefilledWithGeneratedYAML(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)

	if !model.createEditingCompose {
		t.Fatal("createEditingCompose = false after ctrl+y, want true")
	}
	generated := model.createDraft.composeOverrideContent()
	if model.createEditor.Value() != generated {
		t.Fatalf("editor value = %q, want generated override %q", model.createEditor.Value(), generated)
	}
}

func TestCreateEditorIgnoredInStandaloneMode(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeStandalone

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)

	if model.createEditingCompose {
		t.Fatal("createEditingCompose = true after ctrl+y in standalone mode, want false")
	}
}

func TestCreateEditorSaveSetsRawOverrideUsedByComposeSpec(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createDraft.Project = "media"
	model.createDraft.Service = "custom"
	model.createDraft.ComposeFile = filepath.Join(t.TempDir(), "compose.yml")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)
	model.createEditor.SetValue("services:\n  custom:\n    image: custom:tag\n")

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)

	if model.createEditingCompose {
		t.Fatal("createEditingCompose = true after ctrl+s, want false")
	}
	if !model.createDraft.OverrideRawSet {
		t.Fatal("OverrideRawSet = false after save, want true")
	}
	want := "services:\n  custom:\n    image: custom:tag"
	if model.createDraft.OverrideRaw != want {
		t.Fatalf("OverrideRaw = %q, want %q", model.createDraft.OverrideRaw, want)
	}

	spec, err := model.createDraft.ComposeSpec(model.activeSystemConfig())
	if err != nil {
		t.Fatal(err)
	}
	if spec.Content != want {
		t.Fatalf("ComposeSpec content = %q, want raw override %q", spec.Content, want)
	}
	if !strings.Contains(model.createDraft.Preview(), "custom:tag") {
		t.Fatalf("Preview() = %q, want it to reflect the hand-edited override", model.createDraft.Preview())
	}
}

func TestCreateEditorEscCancelsWithoutSettingOverride(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)
	model.createEditor.SetValue("garbage")

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)

	if model.createEditingCompose {
		t.Fatal("createEditingCompose = true after esc, want false")
	}
	if model.createDraft.OverrideRawSet {
		t.Fatal("OverrideRawSet = true after esc-cancel, want false")
	}
}

func TestCreateEditorSaveWhitespaceOnlyResetsToGenerated(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createDraft.OverrideRaw = "services: {}"
	model.createDraft.OverrideRawSet = true

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)
	model.createEditor.SetValue("   \n  \n")

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)

	if model.createDraft.OverrideRawSet {
		t.Fatal("OverrideRawSet = true after saving a whitespace-only edit, want false (reset to generated)")
	}
}

func TestCreateEditorRippleSubmitAndCancelMsgsRouteToSaveAndCancel(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)
	model.createEditor.SetValue("services:\n  x:\n    image: x\n")

	updated, _ = model.Update(ripple.SubmitMsg{})
	model = updated.(Model)
	if !model.createDraft.OverrideRawSet || model.createEditingCompose {
		t.Fatalf("after SubmitMsg: OverrideRawSet=%v editing=%v, want true/false", model.createDraft.OverrideRawSet, model.createEditingCompose)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)
	model.createEditor.SetValue("garbage")
	updated, _ = model.Update(ripple.CancelMsg{})
	model = updated.(Model)
	if model.createEditingCompose {
		t.Fatal("createEditingCompose = true after CancelMsg, want false")
	}
	if model.createDraft.OverrideRaw != "services:\n  x:\n    image: x" {
		t.Fatalf("OverrideRaw changed after a cancelled re-edit: %q", model.createDraft.OverrideRaw)
	}
}

func TestCreateEditorOverlayRendersLargeAndVimStatus(t *testing.T) {
	t.Cleanup(func() { setEditorVimMode(false) })

	model := testModelWithSelectedContainer()
	setEditorVimMode(true) // NewModel resets this to the (false) default settings value; set it after construction.
	model.width, model.height = 140, 44
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)

	view := ansi.Strip(model.View())
	if !strings.Contains(view, "whatthedock · edit override yaml") {
		t.Fatalf("editor overlay missing title:\n%s", view)
	}
	if !strings.Contains(view, "vim") {
		t.Fatalf("editor overlay missing vim status indicator:\n%s", view)
	}
	lines := strings.Split(view, "\n")
	widest := 0
	for _, line := range lines {
		if w := len(line); w > widest {
			widest = w
		}
	}
	if widest < model.width-20 {
		t.Fatalf("editor overlay looks too narrow: widest line %d cols in a %d-col terminal", widest, model.width)
	}
}

func TestApplyOverrideFieldsFromYAMLSyncsFields(t *testing.T) {
	d := createDraft{Service: "radarr", Image: "stale:old", Ports: "stale"}
	content := "services:\n" +
		"  radarr:\n" +
		"    image: \"radarr:custom\"\n" +
		"    restart: \"always\"\n" +
		"    command: \"run --flag\"\n" +
		"    ports:\n" +
		"      - \"7878:7878\"\n" +
		"      - \"7879:7879\"\n" +
		"    volumes:\n" +
		"      - \"/data:/data\"\n" +
		"    environment:\n" +
		"      - \"PUID=1000\"\n"
	d.applyOverrideFieldsFromYAML(content)

	if d.Image != "radarr:custom" {
		t.Fatalf("Image = %q, want radarr:custom", d.Image)
	}
	if d.Restart != "always" {
		t.Fatalf("Restart = %q, want always", d.Restart)
	}
	if d.Command != "run --flag" {
		t.Fatalf("Command = %q, want %q", d.Command, "run --flag")
	}
	if d.Ports != "7878:7878, 7879:7879" {
		t.Fatalf("Ports = %q, want %q", d.Ports, "7878:7878, 7879:7879")
	}
	if d.Mounts != "/data:/data" {
		t.Fatalf("Mounts = %q, want /data:/data", d.Mounts)
	}
	if d.Env != "PUID=1000" {
		t.Fatalf("Env = %q, want PUID=1000", d.Env)
	}
}

func TestApplyOverrideFieldsFromYAMLHandlesMapEnvironment(t *testing.T) {
	d := createDraft{Service: "radarr"}
	content := "services:\n  radarr:\n    image: radarr:custom\n    environment:\n      PUID: 1000\n      TZ: UTC\n"
	d.applyOverrideFieldsFromYAML(content)

	if d.Env != "PUID=1000, TZ=UTC" {
		t.Fatalf("Env = %q, want sorted key=value pairs from map form", d.Env)
	}
}

func TestApplyOverrideFieldsFromYAMLUsesSoleServiceWhenDraftServiceIsBlank(t *testing.T) {
	d := createDraft{Service: ""}
	content := "services:\n  radarr:\n    image: radarr:custom\n"
	d.applyOverrideFieldsFromYAML(content)

	if d.Service != "radarr" || d.Image != "radarr:custom" {
		t.Fatalf("Service/Image = %q/%q, want radarr/radarr:custom", d.Service, d.Image)
	}
}

func TestApplyOverrideFieldsFromYAMLLeavesDraftUnchangedOnParseError(t *testing.T) {
	d := createDraft{Service: "radarr", Image: "kept:as-is"}
	d.applyOverrideFieldsFromYAML("services: [unclosed")

	if d.Image != "kept:as-is" {
		t.Fatalf("Image = %q, want unchanged after a parse error", d.Image)
	}
}

func TestApplyOverrideFieldsFromYAMLLeavesDraftUnchangedWhenServiceNameIsAmbiguous(t *testing.T) {
	d := createDraft{Service: "radarr", Image: "kept:as-is"}
	content := "services:\n  sonarr:\n    image: sonarr:latest\n  lidarr:\n    image: lidarr:latest\n"
	d.applyOverrideFieldsFromYAML(content)

	if d.Image != "kept:as-is" || d.Service != "radarr" {
		t.Fatalf("Image/Service = %q/%q, want unchanged when no service matches and there's more than one", d.Image, d.Service)
	}
}

func TestOpenCreateOverlayLoadedOverrideSyncsFormFields(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(base, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	overrideContent := "services:\n  radarr:\n    image: \"radarr:custom\"\n    ports:\n      - \"7878:7878\"\n"
	if err := os.WriteFile(filepath.Join(dir, "compose.whatthedock.radarr.yml"), []byte(overrideContent), 0o644); err != nil {
		t.Fatal(err)
	}

	model := modelSelecting("media", "radarr", base)
	model.openCreateOverlay()

	if model.createDraft.Image != "radarr:custom" {
		t.Fatalf("Image = %q after loading an existing override, want radarr:custom", model.createDraft.Image)
	}
	if model.createDraft.Ports != "7878:7878" {
		t.Fatalf("Ports = %q after loading an existing override, want 7878:7878", model.createDraft.Ports)
	}
}

func TestSavingCreateEditorSyncsFormFieldsFromEditedYAML(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(base, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	model := modelSelecting("media", "radarr", base)
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)
	model.createEditor.SetValue("services:\n  radarr:\n    image: \"radarr:edited\"\n")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)

	if model.createDraft.Image != "radarr:edited" {
		t.Fatalf("Image = %q after hand-editing and saving, want radarr:edited", model.createDraft.Image)
	}
}

func TestCreateOverrideCheckMsgSyncsFormFields(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createDraft.Service = "radarr"

	overrideContent := "services:\n  radarr:\n    image: \"radarr:custom\"\n"
	updated, _ := model.Update(createOverrideCheckMsg{service: "radarr", content: overrideContent, found: true})
	model = updated.(Model)

	if model.createDraft.Image != "radarr:custom" {
		t.Fatalf("Image = %q after an SSH override-check result, want radarr:custom", model.createDraft.Image)
	}
}

func TestLintComposeYAML(t *testing.T) {
	if err := lintComposeYAML("services:\n  demo:\n    image: demo:latest\n"); err != nil {
		t.Fatalf("lintComposeYAML(valid) = %v, want nil", err)
	}
	if err := lintComposeYAML(""); err != nil {
		t.Fatalf("lintComposeYAML(empty) = %v, want nil", err)
	}
	if err := lintComposeYAML("services:\n  demo:\n\timage: bad tab indent\n"); err == nil {
		t.Fatal("lintComposeYAML(tab-indented) = nil, want a syntax error")
	}
	if err := lintComposeYAML("services: [unclosed"); err == nil {
		t.Fatal("lintComposeYAML(unclosed bracket) = nil, want a syntax error")
	}
}

func TestCreateEditorOverlayShowsLiveLintStatus(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.width, model.height = 140, 44
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)

	view := ansi.Strip(model.View())
	if !strings.Contains(view, "valid YAML") {
		t.Fatalf("editor overlay missing valid-YAML lint status for freshly generated content:\n%s", view)
	}

	model.createEditor.SetValue("services: [unclosed")
	view = ansi.Strip(model.View())
	if strings.Contains(view, "valid YAML") {
		t.Fatalf("editor overlay still shows valid YAML for broken content:\n%s", view)
	}
	if !strings.Contains(view, "yaml:") {
		t.Fatalf("editor overlay missing yaml error text for broken content:\n%s", view)
	}
}

func TestCreateModeFieldIsFirstTabStopAndCyclesWithHL(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()

	if model.createField != createFieldMode {
		t.Fatalf("initial field = %v, want createFieldMode as the first tab stop", model.createField)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	model = updated.(Model)
	if model.createDraft.Mode != createModeStandalone {
		t.Fatalf("mode = %v after l on the Mode field, want standalone", model.createDraft.Mode)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	model = updated.(Model)
	if model.createDraft.Mode != createModeCompose {
		t.Fatalf("mode = %v after h on the Mode field, want compose", model.createDraft.Mode)
	}
}

// TestCreateStrayKeyOnModeFieldIsIgnoredNotTypedElsewhere guards against the
// bug being fixed here: landing on the wrong field (or the Mode field,
// before it was Tab-reachable) and pressing a key that isn't a recognized
// shortcut used to silently type that character into whatever field last
// held focus, corrupting it. A key that isn't h/l/enter/[/] while focused on
// Mode must now be a no-op, not a stray character typed anywhere.
func TestCreateStrayKeyOnModeFieldIsIgnoredNotTypedElsewhere(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	before := model.createDraft

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	model = updated.(Model)

	if model.createDraft != before {
		t.Fatalf("draft changed after a stray '/' on the Mode field: before=%#v after=%#v", before, model.createDraft)
	}
}

func TestDefaultCreateDraftPrefillsServiceFromSelectedContainer(t *testing.T) {
	model := testModelWithSelectedContainer() // container "1": Compose.Service = "radarr"
	draft := model.defaultCreateDraft()
	if draft.Service != "radarr" {
		t.Fatalf("Service = %q, want radarr prefilled from the selected container", draft.Service)
	}
}

// modelSelecting builds a model whose "selected container" is a synthetic
// fixture pointing at a real temp-dir Compose file, so override-detection
// tests can control the exact path instead of the shared fixture's
// hardcoded /srv/media/compose.yml.
func modelSelecting(project, service, composeFile string) Model {
	model := testModel()
	id := domain.ResourceID{Host: "local", ID: "override-test"}
	ctr := domain.Container{
		ID:    id,
		Name:  service + "-1",
		Image: "image:tag",
		Compose: domain.ComposeRef{
			Project:     project,
			Service:     service,
			ConfigFiles: composeFile,
		},
	}
	model.selected = &ctr
	model.selectedID = id
	return model
}

func TestOpenCreateOverlayLoadsExistingLocalOverride(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(base, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	overridePath := filepath.Join(dir, "compose.whatthedock.radarr.yml")
	overrideContent := "services:\n  radarr:\n    image: radarr:custom\n"
	if err := os.WriteFile(overridePath, []byte(overrideContent), 0o644); err != nil {
		t.Fatal(err)
	}

	model := modelSelecting("media", "radarr", base)
	cmd := model.openCreateOverlay()
	if cmd != nil {
		t.Fatal("openCreateOverlay() returned a Cmd for a local system, want synchronous detection (nil)")
	}

	if !model.createDraft.OverrideRawSet || !model.createDraft.OverrideLoaded {
		t.Fatalf("OverrideRawSet/OverrideLoaded = %v/%v, want both true", model.createDraft.OverrideRawSet, model.createDraft.OverrideLoaded)
	}
	if model.createDraft.OverrideRaw != overrideContent {
		t.Fatalf("OverrideRaw = %q, want the existing file's content %q", model.createDraft.OverrideRaw, overrideContent)
	}
	if model.statusErr || !strings.Contains(model.status, "loaded existing override") {
		t.Fatalf("status/statusErr = %q/%v, want a loaded-override confirmation", model.status, model.statusErr)
	}

	spec, err := model.createDraft.ComposeSpec(model.activeSystemConfig())
	if err != nil {
		t.Fatalf("ComposeSpec() error = %v", err)
	}
	if spec.Content != overrideContent {
		t.Fatalf("ComposeSpec content = %q, want the loaded override content, not a regenerated one", spec.Content)
	}
}

func TestOpenCreateOverlayLeavesDraftGeneratedWhenNoOverrideExists(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(base, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	model := modelSelecting("media", "radarr", base)
	model.openCreateOverlay()

	if model.createDraft.OverrideRawSet || model.createDraft.OverrideLoaded {
		t.Fatalf("OverrideRawSet/OverrideLoaded = %v/%v, want both false when no override file exists", model.createDraft.OverrideRawSet, model.createDraft.OverrideLoaded)
	}
}

func TestSavingCreateEditorClearsOverrideLoadedFlag(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(base, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.whatthedock.radarr.yml"), []byte("services:\n  radarr:\n    image: radarr:custom\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	model := modelSelecting("media", "radarr", base)
	model.openCreateOverlay()
	if !model.createDraft.OverrideLoaded {
		t.Fatal("OverrideLoaded = false after loading an existing override, want true")
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)
	model.createEditor.SetValue("services:\n  radarr:\n    image: radarr:edited\n")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)

	if model.createDraft.OverrideLoaded {
		t.Fatal("OverrideLoaded = true after hand-editing and saving, want false (it's edited now, not just loaded)")
	}
	if !model.createDraft.OverrideRawSet {
		t.Fatal("OverrideRawSet = false after saving an edit, want true")
	}
}

func TestCheckRemoteOverrideCmdFindsExistingOverride(t *testing.T) {
	fake := withFakeSSHRun(t)
	system := config.System{Kind: "ssh", SSHHost: "jarvis", Name: "jarvis"}
	overrideContent := "services:\n  radarr:\n    image: radarr:custom\n"
	fake.respond("cat '/srv/media-stack/compose.whatthedock.radarr.yml'", overrideContent, nil)

	cmd := checkRemoteOverrideCmd(system, "/srv/media-stack/compose.yml", "radarr")
	if cmd == nil {
		t.Fatal("checkRemoteOverrideCmd() = nil, want a Cmd")
	}
	msg := runCmd(t, cmd).(createOverrideCheckMsg)
	if !msg.found || msg.content != overrideContent || msg.service != "radarr" {
		t.Fatalf("msg = %#v, want found=true content=%q service=radarr", msg, overrideContent)
	}
}

func TestCreateOverrideCheckMsgIgnoredIfServiceChangedBeforeItArrived(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createDraft.Service = "a-different-service" // user changed it before the ssh round trip landed

	updated, _ := model.Update(createOverrideCheckMsg{service: "radarr", content: "stale content", found: true})
	model = updated.(Model)

	if model.createDraft.OverrideRawSet {
		t.Fatal("a stale override-check result was applied after the draft's service changed")
	}
}
