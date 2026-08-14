package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/config"
	"github.com/allisonhere/whatthedock/internal/systems"
)

type createMode int

const (
	createModeCompose createMode = iota
	createModeStandalone
)

type createField int

const (
	createFieldMode createField = iota
	createFieldProject
	createFieldService
	createFieldContainerName
	createFieldImage
	createFieldCommand
	createFieldPorts
	createFieldMounts
	createFieldEnv
	createFieldRestart
	createFieldComposeFile
)

type createDraft struct {
	Mode          createMode
	Confirming    bool
	Project       string
	Service       string
	ContainerName string
	Image         string
	Command       string
	Ports         string
	Mounts        string
	Env           string
	Restart       string
	ComposeFile   string

	// OverrideRaw, when OverrideRawSet, is override YAML that takes
	// precedence over the generated composeOverrideContent for this draft —
	// either loaded from an existing compose.whatthedock.<service>.yml (see
	// openCreateOverlay/checkRemoteOverrideCmd) or hand-edited via the
	// Ripple editor opened with ctrl+y. OverrideLoaded distinguishes the
	// two for the form's label; saving an edit in the editor clears it.
	OverrideRaw    string
	OverrideRawSet bool
	OverrideLoaded bool
}

type composeCreateSpec struct {
	Project      string
	Service      string
	BaseFile     string
	OverrideFile string
	Content      string
	System       config.System
}

type createFileEntry struct {
	Name     string
	Path     string
	Dir      bool
	Parent   bool
	Selected bool
}

// openCreateOverlay opens the create form. When the draft already targets a
// Compose service that has a WhatTheDock-generated override on disk (the
// common case: re-opening create for an already-selected, already-managed
// service), it loads that override into the draft instead of silently
// offering to regenerate — and overwrite — it on confirm. Local systems are
// checked synchronously (a fast stat+read); SSH systems return a Cmd since
// that's a network round trip (see checkRemoteOverrideCmd).
func (m *Model) openCreateOverlay() tea.Cmd {
	m.createDraft = m.defaultCreateDraft()
	m.overlay = overlayCreate
	m.createField = m.visibleCreateFields()[0]
	m.createCursor = len([]rune(m.createFieldValue()))
	m.createEditingCompose = false
	m.status, m.statusErr = "create draft ready", false

	if m.createDraft.Mode != createModeCompose {
		return nil
	}
	system := m.activeSystemConfig()
	if system.Kind == "ssh" {
		return checkRemoteOverrideCmd(system, m.createDraft.ComposeFile, m.createDraft.Service)
	}
	if content, ok := existingOverrideContent(m.createDraft.ComposeFile, m.createDraft.Service); ok {
		m.createDraft.OverrideRaw = content
		m.createDraft.OverrideRawSet = true
		m.createDraft.OverrideLoaded = true
		m.createDraft.applyOverrideFieldsFromYAML(content)
		m.status, m.statusErr = "loaded existing override for "+m.createDraft.Service, false
	}
	return nil
}

// existingOverrideContent reads a local compose.whatthedock.<service>.yml
// beside base, if one exists.
func existingOverrideContent(base, service string) (string, bool) {
	base = strings.TrimSpace(base)
	service = strings.TrimSpace(service)
	if base == "" || service == "" {
		return "", false
	}
	overridePath := filepath.Join(filepath.Dir(base), "compose.whatthedock."+safeComposeFilename(service)+".yml")
	data, err := os.ReadFile(overridePath)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// createOverrideCheckMsg carries the result of checkRemoteOverrideCmd back
// into Update. service guards against applying a stale result if the
// draft's service changed (or the overlay closed) before the ssh round
// trip finished.
type createOverrideCheckMsg struct {
	service string
	content string
	found   bool
}

func checkRemoteOverrideCmd(system config.System, base, service string) tea.Cmd {
	base = strings.TrimSpace(base)
	service = strings.TrimSpace(service)
	if base == "" || service == "" {
		return nil
	}
	overridePath := path.Join(path.Dir(base), "compose.whatthedock."+safeComposeFilename(service)+".yml")
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		output, err := sshRun(ctx, system, "cat "+systems.ShellQuote(overridePath), "")
		if err != nil {
			return createOverrideCheckMsg{service: service, found: false}
		}
		return createOverrideCheckMsg{service: service, content: string(output), found: true}
	}
}

func (m Model) defaultCreateDraft() createDraft {
	draft := createDraft{
		Mode:          createModeCompose,
		Project:       "default",
		Service:       "new-service",
		ContainerName: "new-container",
		Image:         "image:tag",
		Restart:       "unless-stopped",
		ComposeFile:   "compose.yml",
	}
	if selected := m.selectedContainer(); selected != nil {
		if selected.Image != "" {
			draft.Image = selected.Image
		}
		if selected.Compose.Project != "" {
			draft.Project = selected.Compose.Project
		} else {
			draft.Mode = createModeStandalone
		}
		if selected.Compose.Service != "" {
			draft.Service = selected.Compose.Service
		}
		if selected.Compose.ConfigFiles != "" {
			files := splitComposeConfigFiles(selected.Compose.ConfigFiles)
			if len(files) > 0 {
				draft.ComposeFile = files[0]
			}
		}
	}
	return draft
}

func (m Model) handleCreateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.createEditingCompose {
		return m.handleCreateEditorKey(msg)
	}
	if m.createBrowsing {
		return m.handleCreateFileBrowserKey(msg)
	}
	if m.createDraft.Confirming {
		switch msg.String() {
		case "esc", "n", "q":
			m.createDraft.Confirming = false
			m.status, m.statusErr = "create cancelled", false
		case "y":
			if m.createDraft.Mode == createModeCompose {
				spec, err := m.createDraft.ComposeSpec(m.activeSystemConfig())
				if err != nil {
					m.createDraft.Confirming = false
					m.status, m.statusErr = "create: "+err.Error(), true
					return m, nil
				}
				m.status, m.statusErr = "applying compose service "+spec.Service, false
				return m, m.createComposeCmd(spec)
			}
			spec, err := m.createDraft.ContainerSpec()
			if err != nil {
				m.createDraft.Confirming = false
				m.status, m.statusErr = "create: "+err.Error(), true
				return m, nil
			}
			m.status, m.statusErr = "creating "+spec.Name, false
			return m, m.createContainerCmd(spec)
		}
		return m, nil
	}
	switch msg.String() {
	case "esc", "q":
		m.overlay = overlayNone
	case "up":
		m.moveCreateField(-1)
	case "down", "tab":
		m.moveCreateField(1)
	case "shift+tab":
		m.moveCreateField(-1)
	case "k":
		if m.isCreateChoiceField() {
			m.moveCreateField(-1)
			return m, nil
		}
		m.editCreateFieldString("k")
	case "j":
		if m.isCreateChoiceField() {
			m.moveCreateField(1)
			return m, nil
		}
		m.editCreateFieldString("j")
	case "left":
		if m.isCreateChoiceField() {
			m.cycleCreateChoice(-1)
		} else {
			m.moveCreateCursor(-1)
		}
	case "right":
		if m.isCreateChoiceField() {
			m.cycleCreateChoice(1)
		} else {
			m.moveCreateCursor(1)
		}
	case "h":
		if m.isCreateChoiceField() {
			m.cycleCreateChoice(-1)
			return m, nil
		}
		m.editCreateFieldString("h")
	case "l":
		if m.isCreateChoiceField() {
			m.cycleCreateChoice(1)
			return m, nil
		}
		m.editCreateFieldString("l")
	case "enter":
		if m.isCreateChoiceField() {
			m.cycleCreateChoice(1)
			return m, nil
		}
		if m.createField == createFieldComposeFile {
			return m, m.openCreateFileBrowser()
		}
		m.moveCreateField(1)
	case "ctrl+o":
		m.createDraft.Mode = createModeCompose
		m.createField = createFieldComposeFile
		return m, m.openCreateFileBrowser()
	case "o":
		// Bare "o" is a browse shortcut only on a choice field (Mode/
		// Restart), which ignores letters anyway. Every text field — the
		// Compose file row included — must accept "o" as ordinary input:
		// plenty of real values contain it ("postgres", "sonarr", and
		// "compose.yml" itself), and the Compose file field is exactly
		// where someone would want to type a path by hand. Enter or
		// Ctrl+O still open the browser from the Compose file field.
		if m.createDraft.Mode == createModeCompose && m.isCreateChoiceField() {
			m.createField = createFieldComposeFile
			return m, m.openCreateFileBrowser()
		}
		m.editCreateFieldString("o")
	case "[", "]":
		m.cycleCreateMode()
	case "ctrl+y":
		if m.createDraft.Mode == createModeCompose {
			m.openCreateEditor()
			return m, nil
		}
	case "ctrl+s":
		m.validateCreateDraft()
	case "ctrl+enter", "alt+enter":
		if m.validateCreateDraft() {
			m.createDraft.Confirming = true
			m.status, m.statusErr = "confirm create "+m.createDraft.TargetName(), false
		}
	case "backspace":
		m.editCreateFieldBackspace()
	case "delete":
		m.editCreateFieldDelete()
	case "home", "ctrl+a":
		m.createCursor = 0
	case "end", "ctrl+e":
		m.createCursor = len([]rune(m.createFieldValue()))
	case "ctrl+u":
		if !m.isCreateChoiceField() {
			m.setCreateFieldValue("")
			m.createCursor = 0
		}
	default:
		if len(msg.Runes) > 0 {
			m.editCreateFieldString(string(msg.Runes))
		}
	}
	return m, nil
}

func (m Model) createContainerCmd(spec app.ContainerCreateSpec) tea.Cmd {
	provider := m.provider
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		id, err := provider.CreateContainer(ctx, spec)
		return createDoneMsg{name: spec.Name, id: id, err: err}
	}
}

func (m Model) createComposeCmd(spec composeCreateSpec) tea.Cmd {
	apply := applyComposeCreate
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		err := apply(ctx, spec)
		return createDoneMsg{name: spec.Service, err: err}
	}
}

func (m Model) handleCreateFileBrowserKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.createBrowsing = false
	case "up", "k":
		m.moveCreateFileCursor(-1)
	case "down", "j", "tab":
		m.moveCreateFileCursor(1)
	case "home":
		m.createFileCursor = 0
	case "end":
		m.createFileCursor = max(0, len(m.createFiles)-1)
	case "backspace", "left", "h":
		parent := filepath.Dir(m.createBrowseDir)
		if m.activeSystemConfig().Kind == "ssh" {
			parent = path.Dir(m.createBrowseDir)
		}
		return m, m.browseCreateDir(parent)
	case "enter", "right", "l":
		if len(m.createFiles) == 0 {
			return m, nil
		}
		entry := m.createFiles[clamp(m.createFileCursor, 0, len(m.createFiles)-1)]
		if entry.Dir {
			return m, m.browseCreateDir(entry.Path)
		}
		m.createDraft.ComposeFile = entry.Path
		m.createCursor = len([]rune(m.createDraft.ComposeFile))
		m.createBrowsing = false
		m.status, m.statusErr = "compose file selected", false
	}
	return m, nil
}

// handleCreateEditorKey routes keys to the Ripple editor while the raw
// override-YAML editor (opened with ctrl+y) is active. ctrl+s and a plain-
// mode Esc save/cancel directly; in vim mode Esc is left to the editor
// itself (Normal-mode exit, or a second Esc emitting ripple.CancelMsg),
// and ":w"/":wq"/":x" / ":q" arrive the same way as ripple.SubmitMsg /
// ripple.CancelMsg, handled in Model.Update.
func (m Model) handleCreateEditorKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+s":
		m.saveCreateEditor()
		return m, nil
	case "esc":
		if !m.createEditor.vimMode() {
			m.cancelCreateEditor()
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.createEditor, cmd = m.createEditor.Update(msg)
	return m, cmd
}

// openCreateEditor opens the raw override-YAML editor, seeded with the
// draft's current hand-edited content if any, otherwise the generated
// override.
func (m *Model) openCreateEditor() {
	m.createEditor = newEditorArea()
	seed := m.createDraft.composeOverrideContent()
	if m.createDraft.OverrideRawSet {
		seed = m.createDraft.OverrideRaw
	}
	m.createEditor.SetValue(seed)
	m.createEditor.Focus()
	if m.createEditor.vimMode() {
		m.createEditor.EnterInsert()
	}
	m.createEditingCompose = true
}

// saveCreateEditor stores the editor's content back into the draft as the
// authoritative override YAML and returns to the form. Saving an
// all-whitespace edit clears OverrideRawSet instead of writing an empty
// file, falling back to the generated content again.
func (m *Model) saveCreateEditor() {
	value := strings.TrimSpace(m.createEditor.Value())
	m.createDraft.OverrideRaw = value
	m.createDraft.OverrideRawSet = value != ""
	m.createDraft.OverrideLoaded = false // now hand-edited this session, not just loaded
	m.createEditingCompose = false
	if m.createDraft.OverrideRawSet {
		m.createDraft.applyOverrideFieldsFromYAML(value)
		m.status, m.statusErr = "override YAML edited", false
	} else {
		m.status, m.statusErr = "override YAML reset to generated", false
	}
}

// cancelCreateEditor discards the in-progress edit and returns to the form.
func (m *Model) cancelCreateEditor() {
	m.createEditingCompose = false
	m.status, m.statusErr = "edit cancelled", false
}

func (m *Model) openCreateFileBrowser() tea.Cmd {
	m.createBrowsing = true
	return m.browseCreateDir(createBrowserStartDir(m.createDraft.ComposeFile, m.activeSystemConfig()))
}

func createBrowserStartDir(target string, system config.System) string {
	target = strings.TrimSpace(target)
	if system.Kind == "ssh" {
		// Existence is resolved remotely by the browse command itself (see
		// remoteFileEntries); just take the parent of a file-shaped path.
		if target == "" {
			return "."
		}
		if strings.HasSuffix(target, "/") {
			return target
		}
		return path.Dir(target)
	}
	if target == "" {
		if cwd, err := os.Getwd(); err == nil {
			return cwd
		}
		return "."
	}
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		return target
	}
	dir := filepath.Dir(target)
	if dir == "." {
		if cwd, err := os.Getwd(); err == nil {
			return cwd
		}
	}
	return dir
}

// browseCreateDir lists dir for the file browser. For local systems this is
// synchronous (fast filesystem access, matching the pre-existing behavior);
// for SSH systems it returns a tea.Cmd that lists the directory over the
// same ssh connection convention used for the Docker socket tunnel — a
// network round trip, so it must not block Update. Set m.createFileLoading
// so the browser overlay can show a loading state while that's in flight;
// the result arrives via createFileBrowseMsg.
func (m *Model) browseCreateDir(dir string) tea.Cmd {
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	system := m.activeSystemConfig()
	if system.Kind == "ssh" {
		m.createFileLoading = true
		m.createFileErr = ""
		selected := m.createDraft.ComposeFile
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			entries, resolvedDir, err := remoteFileEntries(ctx, system, dir, selected)
			return createFileBrowseMsg{dir: resolvedDir, entries: entries, err: err}
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = filepath.Clean(dir)
	}
	entries, err := createFileEntries(abs, m.createDraft.ComposeFile)
	m.createBrowseDir = abs
	m.createFileCursor = 0
	m.createFiles = entries
	m.createFileErr = ""
	m.createFileLoading = false
	if err != nil {
		m.createFileErr = err.Error()
	}
	return nil
}

// createFileBrowseMsg carries the result of an SSH directory listing
// (browseCreateDir) back into Update.
type createFileBrowseMsg struct {
	dir     string
	entries []createFileEntry
	err     error
}

// sshRun is the single seam all remote (SSH) Compose operations go through
// — listing directories, writing the override file, and running `docker
// compose` — so tests can substitute a fake instead of shelling out to a
// real ssh binary. stdin, if non-empty, is piped to the remote command
// (used for writing file content).
var sshRun = defaultSSHRun

func defaultSSHRun(ctx context.Context, system config.System, script string, stdin string) ([]byte, error) {
	cmd, err := systems.RemoteCommand(ctx, system, script)
	if err != nil {
		return nil, err
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		if text == "" {
			text = err.Error()
		}
		return nil, errors.New(text)
	}
	return output, nil
}

// remoteFileEntries lists dir on system's remote host in one round trip: cd
// into it (resolving "." and ".." the same way a local browse would) and ls
// it, so a single ssh invocation both validates the directory and lists it.
func remoteFileEntries(ctx context.Context, system config.System, dir string, selected string) ([]createFileEntry, string, error) {
	script := "cd " + systems.ShellQuote(dir) + " 2>&1 && pwd && ls -1Ap"
	output, err := sshRun(ctx, system, script, "")
	if err != nil {
		return nil, dir, err
	}
	lines := strings.Split(strings.TrimRight(string(output), "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return nil, dir, errors.New("empty response from remote host")
	}
	resolvedDir := strings.TrimSpace(lines[0])
	names := lines[1:]

	entries := []createFileEntry{}
	if resolvedDir != "/" {
		entries = append(entries, createFileEntry{Name: "..", Path: path.Dir(resolvedDir), Dir: true, Parent: true})
	}
	var dirs, files []createFileEntry
	for _, name := range names {
		if name == "" {
			continue
		}
		isDir := strings.HasSuffix(name, "/")
		cleanName := strings.TrimSuffix(name, "/")
		entryPath := path.Join(resolvedDir, cleanName)
		if isDir {
			dirs = append(dirs, createFileEntry{Name: cleanName, Path: entryPath, Dir: true})
			continue
		}
		if isComposeFileCandidate(cleanName) {
			files = append(files, createFileEntry{Name: cleanName, Path: entryPath, Selected: selected != "" && entryPath == selected})
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name) })
	sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name) })
	entries = append(entries, dirs...)
	entries = append(entries, files...)
	return entries, resolvedDir, nil
}

func createFileEntries(dir, selected string) ([]createFileEntry, error) {
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	entries := []createFileEntry{}
	parent := filepath.Dir(dir)
	if parent != dir {
		entries = append(entries, createFileEntry{Name: "..", Path: parent, Dir: true, Parent: true})
	}
	var dirs, files []createFileEntry
	selectedAbs, _ := filepath.Abs(selected)
	for _, item := range items {
		name := item.Name()
		path := filepath.Join(dir, name)
		if item.IsDir() {
			dirs = append(dirs, createFileEntry{Name: name, Path: path, Dir: true})
			continue
		}
		if isComposeFileCandidate(name) {
			abs, _ := filepath.Abs(path)
			files = append(files, createFileEntry{Name: name, Path: path, Selected: selectedAbs != "" && abs == selectedAbs})
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name) })
	sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name) })
	entries = append(entries, dirs...)
	entries = append(entries, files...)
	return entries, nil
}

func isComposeFileCandidate(name string) bool {
	lower := strings.ToLower(name)
	if !(strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".yaml")) {
		return false
	}
	base := strings.TrimSuffix(strings.TrimSuffix(lower, ".yaml"), ".yml")
	return base == "compose" || base == "docker-compose" || strings.HasPrefix(base, "compose.") || strings.HasPrefix(base, "docker-compose.")
}

func (m *Model) moveCreateFileCursor(delta int) {
	if len(m.createFiles) == 0 {
		m.createFileCursor = 0
		return
	}
	m.createFileCursor = clamp(m.createFileCursor+delta, 0, len(m.createFiles)-1)
}

func (m Model) activeSystemConfig() config.System {
	settings := config.NormalizeSystems(config.Settings{ActiveSystem: m.activeSystem, Systems: m.systems})
	if system := config.FindSystem(settings.Systems, settings.ActiveSystem); system != nil {
		return *system
	}
	return config.DefaultSystem()
}

func (m *Model) validateCreateDraft() bool {
	if err := m.createDraft.Validate(); err != nil {
		m.status, m.statusErr = "create: "+err.Error(), true
		return false
	}
	m.status, m.statusErr = "create draft validated", false
	return true
}

// lintComposeYAML reports a syntax error in content, or nil if it parses as
// valid YAML. It's a real-time editor aid (see createEditorOverlay), not a
// substitute for the authoritative `docker compose ... config` check that
// already runs at apply time in defaultApplyComposeCreate — a syntactically
// valid document can still be a semantically invalid Compose file, and only
// the real `docker compose config` call catches that.
func lintComposeYAML(content string) error {
	var doc any
	return yaml.Unmarshal([]byte(content), &doc)
}

func (d createDraft) Validate() error {
	if d.Mode == createModeCompose {
		if strings.TrimSpace(d.Project) == "" {
			return errors.New("project is required")
		}
		if strings.TrimSpace(d.Service) == "" {
			return errors.New("service name is required")
		}
		if strings.TrimSpace(d.Image) == "" {
			return errors.New("image is required")
		}
		if strings.TrimSpace(d.ComposeFile) == "" {
			return errors.New("compose file is required")
		}
		return nil
	}
	if strings.TrimSpace(d.ContainerName) == "" {
		return errors.New("container name is required")
	}
	if strings.TrimSpace(d.Image) == "" {
		return errors.New("image is required")
	}
	return nil
}

func (d createDraft) ContainerSpec() (app.ContainerCreateSpec, error) {
	if d.Mode != createModeStandalone {
		return app.ContainerCreateSpec{}, errors.New("compose create is not wired yet")
	}
	if err := d.Validate(); err != nil {
		return app.ContainerCreateSpec{}, err
	}
	ports, err := parseCreatePorts(d.Ports)
	if err != nil {
		return app.ContainerCreateSpec{}, err
	}
	mounts, err := parseCreateMounts(d.Mounts)
	if err != nil {
		return app.ContainerCreateSpec{}, err
	}
	env, err := parseCreateEnv(d.Env)
	if err != nil {
		return app.ContainerCreateSpec{}, err
	}
	return app.ContainerCreateSpec{
		Name:          strings.TrimSpace(d.ContainerName),
		Image:         strings.TrimSpace(d.Image),
		Command:       splitCommand(d.Command),
		Env:           env,
		Ports:         ports,
		Mounts:        mounts,
		RestartPolicy: normalizeRestartPolicy(d.Restart),
		Start:         true,
	}, nil
}

func (d createDraft) ComposeSpec(system config.System) (composeCreateSpec, error) {
	if d.Mode != createModeCompose {
		return composeCreateSpec{}, errors.New("compose spec requires compose mode")
	}
	if err := d.Validate(); err != nil {
		return composeCreateSpec{}, err
	}
	if _, err := parseCreatePorts(d.Ports); err != nil {
		return composeCreateSpec{}, err
	}
	if _, err := parseCreateMounts(d.Mounts); err != nil {
		return composeCreateSpec{}, err
	}
	if _, err := parseCreateEnv(d.Env); err != nil {
		return composeCreateSpec{}, err
	}
	base := strings.TrimSpace(d.ComposeFile)
	service := strings.TrimSpace(d.Service)
	overrideName := "compose.whatthedock." + safeComposeFilename(service) + ".yml"
	var override string
	if system.Kind == "ssh" {
		// The base file lives on the remote host, so its directory is a
		// POSIX remote path regardless of what OS whatthedock itself runs
		// on — use "path", not "filepath", for this join.
		override = path.Join(path.Dir(base), overrideName)
	} else {
		override = filepath.Join(filepath.Dir(base), overrideName)
	}
	content := d.composeOverrideContent()
	if d.OverrideRawSet {
		content = d.OverrideRaw
	}
	return composeCreateSpec{
		Project:      strings.TrimSpace(d.Project),
		Service:      service,
		BaseFile:     base,
		OverrideFile: override,
		Content:      content,
		System:       system,
	}, nil
}

func (d createDraft) composeOverrideContent() string {
	service := strings.TrimSpace(d.Service)
	image := strings.TrimSpace(d.Image)
	var lines []string
	lines = append(lines, "# Generated by WhatTheDock. Edit or remove this override file if needed.")
	lines = append(lines, "services:")
	lines = append(lines, "  "+strconv.Quote(service)+":")
	lines = append(lines, "    image: "+strconv.Quote(image))
	lines = append(lines, "    restart: "+strconv.Quote(normalizeRestartPolicy(d.Restart)))
	if command := strings.TrimSpace(d.Command); command != "" {
		lines = append(lines, "    command: "+strconv.Quote(command))
	}
	lines = appendQuotedYAMLList(lines, "    ports:", splitDraftList(d.Ports), "      - ")
	lines = appendQuotedYAMLList(lines, "    volumes:", splitDraftList(d.Mounts), "      - ")
	lines = appendQuotedYAMLList(lines, "    environment:", splitDraftList(d.Env), "      - ")
	return strings.Join(lines, "\n") + "\n"
}

func appendQuotedYAMLList(lines []string, title string, values []string, prefix string) []string {
	if len(values) == 0 {
		return lines
	}
	lines = append(lines, title)
	for _, value := range values {
		lines = append(lines, prefix+strconv.Quote(value))
	}
	return lines
}

// composeOverrideDoc/composeOverrideService are the subset of Compose YAML
// shape applyOverrideFieldsFromYAML reads back out of override content — the
// mirror image of composeOverrideContent's generation. Environment is typed
// as interface{} because Compose allows it as either a list ("KEY=value"
// entries) or a map (KEY: value); normalizeComposeEnvironment reconciles
// both into the list form the Env field stores.
type composeOverrideDoc struct {
	Services map[string]composeOverrideService `yaml:"services"`
}

type composeOverrideService struct {
	Image       string      `yaml:"image"`
	Restart     string      `yaml:"restart"`
	Command     string      `yaml:"command"`
	Ports       []string    `yaml:"ports"`
	Volumes     []string    `yaml:"volumes"`
	Environment interface{} `yaml:"environment"`
}

// applyOverrideFieldsFromYAML parses content as Compose override YAML and,
// if a service can be unambiguously identified (matching d.Service, or the
// sole service present), updates d's structured fields to match it — so the
// form reflects content that was loaded (override-detection) or hand-edited
// (the Ripple editor) outside the fields themselves. Content that fails to
// parse, or that names no service matching d.Service among several, leaves
// d unchanged rather than guessing.
func (d *createDraft) applyOverrideFieldsFromYAML(content string) {
	var doc composeOverrideDoc
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil || len(doc.Services) == 0 {
		return
	}
	name, svc, ok := selectOverrideService(doc.Services, d.Service)
	if !ok {
		return
	}
	d.Service = name
	d.Image = svc.Image
	if strings.TrimSpace(svc.Restart) != "" {
		d.Restart = svc.Restart
	}
	d.Command = svc.Command
	d.Ports = strings.Join(svc.Ports, ", ")
	d.Mounts = strings.Join(svc.Volumes, ", ")
	d.Env = strings.Join(normalizeComposeEnvironment(svc.Environment), ", ")
}

// selectOverrideService picks which parsed service to sync fields from: the
// one named preferredService if present, else the sole service when there's
// exactly one. Multiple services with no name match is ambiguous, so callers
// leave the draft untouched rather than guessing which one the user means.
func selectOverrideService(services map[string]composeOverrideService, preferredService string) (string, composeOverrideService, bool) {
	if svc, ok := services[strings.TrimSpace(preferredService)]; ok {
		return strings.TrimSpace(preferredService), svc, true
	}
	if len(services) == 1 {
		for name, svc := range services {
			return name, svc, true
		}
	}
	return "", composeOverrideService{}, false
}

// normalizeComposeEnvironment reconciles Compose's two allowed environment
// shapes — a list of "KEY=value" strings, or a KEY: value map — into the
// list form. Map form has no inherent order, so entries are sorted by key
// for a stable, predictable Env field value.
func normalizeComposeEnvironment(v interface{}) []string {
	switch val := v.(type) {
	case []interface{}:
		out := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	case map[string]interface{}:
		out := make([]string, 0, len(val))
		for k, v := range val {
			out = append(out, fmt.Sprintf("%s=%v", k, v))
		}
		sort.Strings(out)
		return out
	default:
		return nil
	}
}

func safeComposeFilename(value string) string {
	var out strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out.WriteRune(r)
			lastDash = false
		case out.Len() > 0:
			if !lastDash {
				out.WriteByte('-')
				lastDash = true
			}
		}
	}
	name := strings.Trim(out.String(), "-")
	if name == "" {
		return "service"
	}
	return name
}

func defaultApplyComposeCreate(ctx context.Context, spec composeCreateSpec) error {
	if strings.TrimSpace(spec.BaseFile) == "" {
		return errors.New("compose file is required")
	}
	if spec.System.Kind == "ssh" {
		return applyComposeCreateRemote(ctx, spec)
	}
	if _, err := os.Stat(spec.BaseFile); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(spec.OverrideFile), 0o755); err != nil {
		return err
	}
	tempSpec := spec
	tempSpec.OverrideFile = spec.OverrideFile + ".tmp"
	if err := os.WriteFile(tempSpec.OverrideFile, []byte(spec.Content), 0o644); err != nil {
		return err
	}
	if err := composeCommand(ctx, tempSpec, "config"); err != nil {
		_ = os.Remove(tempSpec.OverrideFile)
		return err
	}
	if err := os.Rename(tempSpec.OverrideFile, spec.OverrideFile); err != nil {
		_ = os.Remove(tempSpec.OverrideFile)
		return err
	}
	return composeCommand(ctx, spec, "up", "-d", spec.Service)
}

// applyComposeCreateRemote is defaultApplyComposeCreate's SSH-system
// counterpart: every filesystem operation runs on the remote host over the
// same ssh convention as the Docker socket tunnel (see sshRun), writing and
// validating a temp override before promoting it, exactly like the local
// path — just with `test`/`mkdir`/`cat >`/`mv` in place of the os package.
func applyComposeCreateRemote(ctx context.Context, spec composeCreateSpec) error {
	if _, err := sshRun(ctx, spec.System, "test -f "+systems.ShellQuote(spec.BaseFile), ""); err != nil {
		return fmt.Errorf("compose file not found on %s: %w", spec.System.Name, err)
	}
	if _, err := sshRun(ctx, spec.System, "mkdir -p "+systems.ShellQuote(path.Dir(spec.OverrideFile)), ""); err != nil {
		return err
	}
	tempSpec := spec
	tempSpec.OverrideFile = spec.OverrideFile + ".tmp"
	if _, err := sshRun(ctx, spec.System, "cat > "+systems.ShellQuote(tempSpec.OverrideFile), spec.Content); err != nil {
		return err
	}
	if err := composeCommand(ctx, tempSpec, "config"); err != nil {
		_, _ = sshRun(ctx, spec.System, "rm -f "+systems.ShellQuote(tempSpec.OverrideFile), "")
		return err
	}
	if _, err := sshRun(ctx, spec.System, "mv "+systems.ShellQuote(tempSpec.OverrideFile)+" "+systems.ShellQuote(spec.OverrideFile), ""); err != nil {
		_, _ = sshRun(ctx, spec.System, "rm -f "+systems.ShellQuote(tempSpec.OverrideFile), "")
		return err
	}
	return composeCommand(ctx, spec, "up", "-d", spec.Service)
}

func runDockerCompose(ctx context.Context, spec composeCreateSpec, args ...string) error {
	baseArgs := []string{"compose"}
	if strings.TrimSpace(spec.Project) != "" {
		baseArgs = append(baseArgs, "-p", spec.Project)
	}
	baseArgs = append(baseArgs, "-f", spec.BaseFile, "-f", spec.OverrideFile)
	baseArgs = append(baseArgs, args...)
	if spec.System.Kind == "ssh" {
		quoted := make([]string, len(baseArgs))
		for i, a := range baseArgs {
			quoted[i] = systems.ShellQuote(a)
		}
		_, err := sshRun(ctx, spec.System, "docker "+strings.Join(quoted, " "), "")
		return err
	}
	cmd := exec.CommandContext(ctx, "docker", baseArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		if text == "" {
			text = err.Error()
		}
		return errors.New(text)
	}
	return nil
}

func parseCreatePorts(value string) ([]app.PortBinding, error) {
	parts := splitDraftList(value)
	out := make([]app.PortBinding, 0, len(parts))
	for _, part := range parts {
		hostIP := ""
		if strings.Count(part, ":") >= 2 {
			before, after, _ := strings.Cut(part, ":")
			hostIP = strings.TrimSpace(before)
			part = after
		}
		hostRaw, containerRaw, ok := strings.Cut(part, ":")
		if !ok {
			return nil, fmt.Errorf("port %q must be host:container", part)
		}
		containerPortRaw := containerRaw
		protocol := "tcp"
		if portPart, protoPart, ok := strings.Cut(containerRaw, "/"); ok {
			containerPortRaw = portPart
			protocol = strings.TrimSpace(protoPart)
		}
		hostPort, err := parseCreatePort(hostRaw)
		if err != nil {
			return nil, fmt.Errorf("host port %q is invalid", hostRaw)
		}
		containerPort, err := parseCreatePort(containerPortRaw)
		if err != nil {
			return nil, fmt.Errorf("container port %q is invalid", containerPortRaw)
		}
		if protocol == "" {
			protocol = "tcp"
		}
		out = append(out, app.PortBinding{HostIP: hostIP, HostPort: hostPort, ContainerPort: containerPort, Protocol: protocol})
	}
	return out, nil
}

func parseCreatePort(value string) (uint16, error) {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || port < 1 || port > 65535 {
		return 0, errors.New("port must be 1-65535")
	}
	return uint16(port), nil
}

func parseCreateMounts(value string) ([]app.MountBinding, error) {
	parts := splitDraftList(value)
	out := make([]app.MountBinding, 0, len(parts))
	for _, part := range parts {
		source, rest, ok := strings.Cut(part, ":")
		if !ok {
			return nil, fmt.Errorf("mount %q must be source:target", part)
		}
		target := rest
		readOnly := false
		if destination, mode, ok := strings.Cut(rest, ":"); ok {
			target = destination
			readOnly = strings.Contains(strings.ToLower(mode), "ro")
		}
		source = strings.TrimSpace(source)
		target = strings.TrimSpace(target)
		if source == "" || target == "" {
			return nil, fmt.Errorf("mount %q must include source and target", part)
		}
		out = append(out, app.MountBinding{Source: source, Destination: target, ReadOnly: readOnly})
	}
	return out, nil
}

func parseCreateEnv(value string) ([]string, error) {
	parts := splitDraftList(value)
	for _, part := range parts {
		key, _, ok := strings.Cut(part, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("env %q must be KEY=value", part)
		}
	}
	return parts, nil
}

func splitCommand(value string) []string {
	return strings.Fields(strings.TrimSpace(value))
}

func normalizeRestartPolicy(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unless-stopped"
	}
	return value
}

func (m *Model) moveCreateField(delta int) {
	fields := m.visibleCreateFields()
	if len(fields) == 0 {
		m.createField = createFieldMode
		m.createCursor = 0
		return
	}
	current := 0
	for i, field := range fields {
		if field == m.createField {
			current = i
			break
		}
	}
	m.createField = fields[modIndex(current+delta, len(fields))]
	m.createCursor = len([]rune(m.createFieldValue()))
}

// visibleCreateFields lists the fields Tab/Shift+Tab cycle through.
// createFieldMode is always first: it isn't rendered as its own row (the
// tab pills at the top of the panel represent it visually — see
// renderCreateModeTabs), but it stays in the navigable field list so
// landing on the wrong field never types a stray character while someone's
// actually trying to reach mode-switching by mashing Tab; [/] also switches
// modes directly from any field (see cycleCreateMode).
func (m Model) visibleCreateFields() []createField {
	if m.createDraft.Mode == createModeStandalone {
		return []createField{createFieldMode, createFieldContainerName, createFieldImage, createFieldCommand, createFieldPorts, createFieldMounts, createFieldEnv, createFieldRestart}
	}
	return []createField{createFieldMode, createFieldProject, createFieldService, createFieldImage, createFieldPorts, createFieldMounts, createFieldEnv, createFieldRestart, createFieldComposeFile}
}

func (m Model) isCreateChoiceField() bool {
	return m.createField == createFieldMode || m.createField == createFieldRestart
}

func (m *Model) cycleCreateChoice(direction int) {
	if direction == 0 {
		direction = 1
	}
	switch m.createField {
	case createFieldMode:
		m.cycleCreateMode()
		return
	case createFieldRestart:
		options := []string{"unless-stopped", "always", "on-failure", "no"}
		current := 0
		for i, option := range options {
			if m.createDraft.Restart == option {
				current = i
				break
			}
		}
		m.createDraft.Restart = options[modIndex(current+direction, len(options))]
	}
	m.createCursor = len([]rune(m.createFieldValue()))
}

// cycleCreateMode toggles between Compose and standalone creation. Unlike
// the per-field choice cycling above, this is reachable from anywhere in
// the overlay (bound to "[" and "]") since Mode is rendered as its own tab
// row rather than a focusable field, so it can't collide with h/l/left/
// right editing text in whichever field the cursor is actually on. The
// currently focused field carries over across the switch when it exists in
// both modes (Image, Ports, Mounts, Env, Restart); otherwise focus resets
// to the new mode's first field.
func (m *Model) cycleCreateMode() {
	if m.createDraft.Mode == createModeCompose {
		m.createDraft.Mode = createModeStandalone
	} else {
		m.createDraft.Mode = createModeCompose
	}
	fields := m.visibleCreateFields()
	stillVisible := false
	for _, field := range fields {
		if field == m.createField {
			stillVisible = true
			break
		}
	}
	if !stillVisible && len(fields) > 0 {
		m.createField = fields[0]
	}
	m.createCursor = len([]rune(m.createFieldValue()))
}

func (m *Model) editCreateFieldBackspace() {
	if m.isCreateChoiceField() {
		return
	}
	value := m.createFieldValue()
	runes := []rune(value)
	m.createCursor = clamp(m.createCursor, 0, len(runes))
	if m.createCursor == 0 {
		return
	}
	runes = append(runes[:m.createCursor-1], runes[m.createCursor:]...)
	m.createCursor--
	m.setCreateFieldValue(string(runes))
}

func (m *Model) editCreateFieldDelete() {
	if m.isCreateChoiceField() {
		return
	}
	runes := []rune(m.createFieldValue())
	m.createCursor = clamp(m.createCursor, 0, len(runes))
	if m.createCursor >= len(runes) {
		return
	}
	runes = append(runes[:m.createCursor], runes[m.createCursor+1:]...)
	m.setCreateFieldValue(string(runes))
}

func (m *Model) editCreateFieldString(value string) {
	if m.isCreateChoiceField() {
		return
	}
	runes := []rune(m.createFieldValue())
	insert := []rune(value)
	m.createCursor = clamp(m.createCursor, 0, len(runes))
	updated := append([]rune{}, runes[:m.createCursor]...)
	updated = append(updated, insert...)
	updated = append(updated, runes[m.createCursor:]...)
	m.createCursor += len(insert)
	m.setCreateFieldValue(string(updated))
}

func (m *Model) moveCreateCursor(delta int) {
	if m.isCreateChoiceField() {
		return
	}
	m.createCursor = clamp(m.createCursor+delta, 0, len([]rune(m.createFieldValue())))
}

func (m Model) createFieldValue() string {
	switch m.createField {
	case createFieldMode:
		return m.createDraft.Mode.String()
	case createFieldProject:
		return m.createDraft.Project
	case createFieldService:
		return m.createDraft.Service
	case createFieldContainerName:
		return m.createDraft.ContainerName
	case createFieldImage:
		return m.createDraft.Image
	case createFieldCommand:
		return m.createDraft.Command
	case createFieldPorts:
		return m.createDraft.Ports
	case createFieldMounts:
		return m.createDraft.Mounts
	case createFieldEnv:
		return m.createDraft.Env
	case createFieldRestart:
		return m.createDraft.Restart
	case createFieldComposeFile:
		return m.createDraft.ComposeFile
	default:
		return ""
	}
}

func (m Model) createFieldValueWithCaret() string {
	runes := []rune(m.createFieldValue())
	cursor := clamp(m.createCursor, 0, len(runes))
	withCaret := append([]rune{}, runes[:cursor]...)
	withCaret = append(withCaret, '|')
	withCaret = append(withCaret, runes[cursor:]...)
	return string(withCaret)
}

func (m *Model) setCreateFieldValue(value string) {
	switch m.createField {
	case createFieldProject:
		m.createDraft.Project = value
	case createFieldService:
		m.createDraft.Service = value
	case createFieldContainerName:
		m.createDraft.ContainerName = value
	case createFieldImage:
		m.createDraft.Image = value
	case createFieldCommand:
		m.createDraft.Command = value
	case createFieldPorts:
		m.createDraft.Ports = value
	case createFieldMounts:
		m.createDraft.Mounts = value
	case createFieldEnv:
		m.createDraft.Env = value
	case createFieldComposeFile:
		m.createDraft.ComposeFile = value
	}
}

func (mode createMode) String() string {
	if mode == createModeStandalone {
		return "standalone container"
	}
	return "compose service"
}

func (d createDraft) TargetName() string {
	if d.Mode == createModeStandalone {
		return emptyAs(d.ContainerName, "new-container")
	}
	return emptyAs(d.Service, "new-service")
}

func createFieldLabel(field createField) string {
	switch field {
	case createFieldMode:
		return "Mode"
	case createFieldProject:
		return "Project"
	case createFieldService:
		return "Service"
	case createFieldContainerName:
		return "Name"
	case createFieldImage:
		return "Image"
	case createFieldCommand:
		return "Command"
	case createFieldPorts:
		return "Ports"
	case createFieldMounts:
		return "Mounts"
	case createFieldEnv:
		return "Env"
	case createFieldRestart:
		return "Restart"
	case createFieldComposeFile:
		return "Compose file"
	default:
		return ""
	}
}

func (d createDraft) Preview() string {
	if d.Mode == createModeStandalone {
		return d.standalonePreview()
	}
	if d.OverrideRawSet {
		return d.OverrideRaw
	}
	return d.composePreview()
}

func (d createDraft) composePreview() string {
	service := emptyAs(d.Service, "new-service")
	image := emptyAs(d.Image, "image:tag")
	var lines []string
	lines = append(lines, "services:", "  "+service+":", "    image: "+image, "    restart: "+emptyAs(d.Restart, "unless-stopped"))
	lines = appendPreviewList(lines, "    ports:", splitDraftList(d.Ports), "      - ")
	lines = appendPreviewList(lines, "    volumes:", splitDraftList(d.Mounts), "      - ")
	lines = appendPreviewList(lines, "    environment:", splitDraftList(d.Env), "      - ")
	return strings.Join(lines, "\n")
}

func (d createDraft) standalonePreview() string {
	args := []string{"docker run -d", "--name " + emptyAs(d.ContainerName, "new-container"), "--restart " + emptyAs(d.Restart, "unless-stopped")}
	for _, port := range splitDraftList(d.Ports) {
		args = append(args, "-p "+port)
	}
	for _, mount := range splitDraftList(d.Mounts) {
		args = append(args, "-v "+mount)
	}
	for _, env := range splitDraftList(d.Env) {
		args = append(args, "-e "+env)
	}
	args = append(args, emptyAs(d.Image, "image:tag"))
	if command := strings.TrimSpace(d.Command); command != "" {
		args = append(args, command)
	}
	return strings.Join(args, " \\\n  ")
}

func appendPreviewList(lines []string, title string, values []string, prefix string) []string {
	if len(values) == 0 {
		return lines
	}
	lines = append(lines, title)
	for _, value := range values {
		lines = append(lines, prefix+value)
	}
	return lines
}

func splitDraftList(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func emptyAs(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
