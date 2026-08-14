package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/config"
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

	// OverrideRaw, when OverrideRawSet, is hand-edited override YAML (see the
	// Ripple editor opened with ctrl+y) that takes precedence over the
	// generated composeOverrideContent for this draft.
	OverrideRaw    string
	OverrideRawSet bool
}

type composeCreateSpec struct {
	Project      string
	Service      string
	BaseFile     string
	OverrideFile string
	Content      string
}

type createFileEntry struct {
	Name     string
	Path     string
	Dir      bool
	Parent   bool
	Selected bool
}

func (m *Model) openCreateOverlay() {
	m.createDraft = m.defaultCreateDraft()
	m.overlay = overlayCreate
	m.createField = m.visibleCreateFields()[0]
	m.createCursor = len([]rune(m.createFieldValue()))
	m.createEditingCompose = false
	m.status, m.statusErr = "create draft ready", false
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
				spec, err := m.createDraft.ComposeSpec()
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
			m.openCreateFileBrowser()
			return m, nil
		}
		m.moveCreateField(1)
	case "ctrl+o":
		m.createDraft.Mode = createModeCompose
		m.createField = createFieldComposeFile
		m.openCreateFileBrowser()
		return m, nil
	case "o":
		if m.createDraft.Mode == createModeCompose {
			m.createField = createFieldComposeFile
			m.openCreateFileBrowser()
			return m, nil
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
		m.browseCreateDir(filepath.Dir(m.createBrowseDir))
	case "enter", "right", "l":
		if len(m.createFiles) == 0 {
			return m, nil
		}
		entry := m.createFiles[clamp(m.createFileCursor, 0, len(m.createFiles)-1)]
		if entry.Dir {
			m.browseCreateDir(entry.Path)
			return m, nil
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
	m.createEditingCompose = false
	if m.createDraft.OverrideRawSet {
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

func (m *Model) openCreateFileBrowser() {
	if m.activeSystemConfig().Kind == "ssh" {
		m.status, m.statusErr = "compose file browser is local-only for now", true
		return
	}
	m.createBrowsing = true
	m.browseCreateDir(createBrowserStartDir(m.createDraft.ComposeFile))
}

func createBrowserStartDir(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		if cwd, err := os.Getwd(); err == nil {
			return cwd
		}
		return "."
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return path
	}
	dir := filepath.Dir(path)
	if dir == "." {
		if cwd, err := os.Getwd(); err == nil {
			return cwd
		}
	}
	return dir
}

func (m *Model) browseCreateDir(dir string) {
	if strings.TrimSpace(dir) == "" {
		dir = "."
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
	if err != nil {
		m.createFileErr = err.Error()
	}
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
	if err := m.createDraft.Validate(m.activeSystemConfig()); err != nil {
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

func (d createDraft) Validate(system config.System) error {
	if d.Mode == createModeCompose {
		if system.Kind == "ssh" {
			return errors.New("compose editing is local-only for now")
		}
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
	if err := d.Validate(config.DefaultSystem()); err != nil {
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

func (d createDraft) ComposeSpec() (composeCreateSpec, error) {
	if d.Mode != createModeCompose {
		return composeCreateSpec{}, errors.New("compose spec requires compose mode")
	}
	if err := d.Validate(config.DefaultSystem()); err != nil {
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
	override := filepath.Join(filepath.Dir(base), "compose.whatthedock."+safeComposeFilename(service)+".yml")
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

func runDockerCompose(ctx context.Context, spec composeCreateSpec, args ...string) error {
	baseArgs := []string{"compose"}
	if strings.TrimSpace(spec.Project) != "" {
		baseArgs = append(baseArgs, "-p", spec.Project)
	}
	baseArgs = append(baseArgs, "-f", spec.BaseFile, "-f", spec.OverrideFile)
	baseArgs = append(baseArgs, args...)
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

func (m Model) visibleCreateFields() []createField {
	if m.createDraft.Mode == createModeStandalone {
		return []createField{createFieldContainerName, createFieldImage, createFieldCommand, createFieldPorts, createFieldMounts, createFieldEnv, createFieldRestart}
	}
	return []createField{createFieldProject, createFieldService, createFieldImage, createFieldPorts, createFieldMounts, createFieldEnv, createFieldRestart, createFieldComposeFile}
}

func (m Model) isCreateChoiceField() bool {
	return m.createField == createFieldRestart
}

func (m *Model) cycleCreateChoice(direction int) {
	if direction == 0 {
		direction = 1
	}
	switch m.createField {
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
