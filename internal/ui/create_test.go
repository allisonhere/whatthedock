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

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	model = updated.(Model)

	if model.createDraft.Project != "l" {
		t.Fatalf("project = %q, want typed l", model.createDraft.Project)
	}
}

func TestCreateValidationRejectsRemoteComposeEditing(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.systems = []config.System{{ID: "remote", Name: "remote", Kind: "ssh", SSHHost: "dock.example", RemoteSocket: "/var/run/docker.sock", LocalSocket: "/tmp/whatthedock.sock"}}
	model.activeSystem = "remote"
	model.openCreateOverlay()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)

	if !model.statusErr || !strings.Contains(model.status, "local-only") {
		t.Fatalf("status/statusErr = %q/%v, want local-only validation error", model.status, model.statusErr)
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

	spec, err := draft.ComposeSpec()
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

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
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

func TestCreateComposeFileBrowserOpensWithOFromAnyCreateField(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.ComposeFile = filepath.Join(dir, "compose.yml")
	model.createField = createFieldProject

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	model = updated.(Model)

	if !model.createBrowsing {
		t.Fatal("createBrowsing = false after o, want true")
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

	spec, err := model.createDraft.ComposeSpec()
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
