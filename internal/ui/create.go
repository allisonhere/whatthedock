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
	"unicode"

	"github.com/allisonhere/ripple"
	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/catalog"
	"github.com/allisonhere/whatthedock/internal/clipboard"
	"github.com/allisonhere/whatthedock/internal/config"
	"github.com/allisonhere/whatthedock/internal/domain"
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
	createFieldImageAction
	createFieldCommand
	createFieldPorts
	createFieldMounts
	createFieldEnv
	createFieldRestart
	createFieldComposeFile
	// createFieldNetworks is only ever visible on a paste draft (see
	// visibleCreateFields) — plain standalone create has never exposed
	// network selection in the form; Paste is the first feature that
	// actually captures a source container's networks and needs a place
	// to remap them for the destination.
	createFieldNetworks
)

type createDraft struct {
	Mode       createMode
	Confirming bool

	// Editing and EditingID, when set (by openEditOverlay), mean confirming
	// this draft should replace the container/service currently identified
	// by EditingID in place instead of creating a new one. A fresh Create
	// draft (n) or a Clone draft (C) never set these — both are meant to
	// produce something new, never to silently overwrite what's selected.
	Editing   bool
	EditingID domain.ResourceID

	Project       string
	Service       string
	ContainerName string
	Image         string
	ImageAction   string
	Command       string
	Ports         string
	Mounts        string
	Env           string
	Restart       string
	ComposeFile   string
	// Networks is a comma-joined list of destination network names, editable
	// only on a paste draft (see visibleCreateFields) — a plain standalone
	// create has no field for it at all; every other field a yanked
	// container carried (privileged, capabilities, healthcheck, resource
	// limits, ...) rides along unedited on PastePlan.Spec, which
	// ContainerSpec starts from instead of a blank spec when Pasting.
	Networks string

	// Pasting and PastePlan mark a draft opened from the paste review
	// screen (see draftFromPastePlan, paste.go) rather than a fresh Create
	// (n), Clone (C), or Edit (e/m) — confirming this draft applies through
	// pasteApplyCmd instead of the plain create path, since a paste may
	// first need to create a destination network the plan flagged as
	// missing.
	Pasting   bool
	PastePlan *clipboard.PastePlan

	// OverrideRaw, when OverrideRawSet, is override YAML that takes
	// precedence over the generated composeOverrideContent for this draft —
	// either loaded from an existing compose.whatthedock.<service>.yml (see
	// openCreateOverlay/checkRemoteOverrideCmd) or hand-edited via the
	// Ripple editor opened with ctrl+y. OverrideLoaded distinguishes the
	// two for the form's label; saving an edit in the editor clears it.
	OverrideRaw     string
	OverrideRawSet  bool
	OverrideLoaded  bool
	OverrideRawBase bool

	// BaseFileMissing is set (see openCreateOverlay/checkRemoteOverrideCmd)
	// when ComposeFile was already non-empty at form-open time — i.e. an
	// already-labeled container — but doesn't actually exist on disk, the
	// signature of a stack deployed by a tool (Portainer is the common
	// case) that manages its own compose file elsewhere rather than
	// leaving one at the path it stamps into the container's labels.
	// Confirming in this state (see the confirm-prompt switch in
	// create_view.go) takes the adopt path instead of the normal
	// merge/override path: writes a brand-new base file with the draft's
	// current definition instead of requiring one to already exist.
	BaseFileMissing bool
}

type composeCreateSpec struct {
	Project      string
	Service      string
	BaseFile     string
	OverrideFile string
	Content      string
	FullBase     bool
	PullBeforeUp bool
	Progress     func(string)
	System       config.System
}

const (
	imageActionKeep = "keep"
	imageActionPull = "pull latest"
)

type createFileEntry struct {
	Name     string
	Path     string
	Dir      bool
	Parent   bool
	Selected bool
}

// openCreateOverlay opens the create form on a blank, generic-placeholder
// draft (see defaultCreateDraft) — 'n' is for making something new, not a
// view onto whatever's currently selected. It still runs
// checkComposeOverrideCmd in case that placeholder ComposeFile/Service
// pair happens to already have an override on disk (the common real case:
// nothing, since they're generic placeholders); openEditOverlay is the
// path that actually targets an already-managed service.
func (m *Model) openCreateOverlay() tea.Cmd {
	m.openCreateOverlayWithDraft(m.defaultCreateDraft())
	return m.checkComposeOverrideCmd()
}

// checkComposeOverrideCmd looks up whether the draft's current
// (ComposeFile, Service) pair already has a WhatTheDock-managed override —
// or a missing base file — on disk, and loads/flags it into the draft.
// Factored out of openCreateOverlay so anything that changes ComposeFile
// or Service after the form is already open can re-run the same check:
// the file browser used to only set ComposeFile and leave every other
// field (Image, Ports, ...) exactly as it was before, which meant picking
// a different compose file never actually populated the form from it —
// still showing whatever an earlier selection had left behind.
func (m *Model) checkComposeOverrideCmd() tea.Cmd {
	if m.createDraft.Mode != createModeCompose {
		return nil
	}
	system := m.activeSystemConfig()
	if system.Kind == "ssh" {
		return checkRemoteOverrideCmd(system, m.createDraft.ComposeFile, m.createDraft.Service)
	}
	m.createDraft.BaseFileMissing = false
	if strings.TrimSpace(m.createDraft.ComposeFile) != "" {
		if _, err := os.Stat(m.createDraft.ComposeFile); err != nil {
			m.createDraft.BaseFileMissing = true
		}
	}
	if content, ok := existingOverrideContent(m.createDraft.ComposeFile, m.createDraft.Service); ok {
		m.createDraft.OverrideRaw = content
		m.createDraft.OverrideRawSet = true
		m.createDraft.OverrideLoaded = true
		m.createDraft.OverrideRawBase = false
		m.createDraft.applyOverrideFieldsFromYAML(content)
		m.status, m.statusErr = "loaded existing override for "+m.createDraft.Service, false
	}
	return nil
}

// openCloneOverlay opens the create form prefilled from the selected
// container under a new name (see defaultCloneDraft) — unlike
// openCreateOverlay, it never checks for an existing override: a "-clone"
// identity has none, and even a name collision must never load-and-overwrite
// the original's override under the new identity.
func (m *Model) openCloneOverlay() {
	m.openCreateOverlayWithDraft(m.defaultCloneDraft())
	m.status, m.statusErr = "clone draft ready — rename before confirming", false
}

// openEditOverlay opens the create form pre-loaded with the selected
// container/service's current configuration under its own identity, so
// confirming replaces it in place instead of creating something new —
// unlike n (openCreateOverlay), which must always default to a fresh
// name/service so it never surprises the user by overwriting a selection,
// and unlike C (openCloneOverlay), which deliberately renames to guarantee
// an independent second copy. For a Compose service this is exactly
// openCreateOverlay's existing already-managed-service behavior (it already
// loads the on-disk override and replaces in place via `docker compose up
// -d` on confirm) — reused as-is, just flagged as Editing so the overlay
// can label itself "edit" instead of "create". A standalone container has
// no such existing path, so it gets a dedicated defaultEditDraft prefill.
func (m *Model) openEditOverlay() tea.Cmd {
	selected := m.selectedContainer()
	if selected == nil {
		return nil
	}
	if selected.Compose.Project != "" {
		// Not openCreateOverlay: that opens the blank, generic-placeholder
		// draft (see defaultCreateDraft's doc comment) — editing needs the
		// selected service's real project/service/compose file so the
		// override check below looks in the right place, not "new-service"
		// beside "compose.yml".
		m.openCreateOverlayWithDraft(m.selectionCreateDraft())
		m.createDraft.Editing = true
		cmd := m.checkComposeOverrideCmd()
		if m.createDraft.OverrideRawSet {
			return cmd
		}
		return batchCreateCmds(cmd, m.loadSelectedComposeFileCmd(m.createDraft.ComposeFile))
	}
	m.openCreateOverlayWithDraft(m.defaultEditDraft())
	m.createDraft.Editing = true
	m.status, m.statusErr = "edit draft ready for "+selected.DisplayName(), false
	return nil
}

func batchCreateCmds(cmds ...tea.Cmd) tea.Cmd {
	filtered := make([]tea.Cmd, 0, len(cmds))
	for _, cmd := range cmds {
		if cmd != nil {
			filtered = append(filtered, cmd)
		}
	}
	switch len(filtered) {
	case 0:
		return nil
	case 1:
		return filtered[0]
	default:
		return tea.Batch(filtered...)
	}
}

// siblingServiceCount is how many services project's containers actually
// span right now, per the app's own live snapshot (domain.Project.Services
// — grouped by domain.BuildSnapshot from real Docker compose labels, not a
// YAML parse) — used by the "m" key handler to decide whether an
// individual container needs the warn-and-offer prompt before editing
// (siblingServiceCount(...) > 1) or can go straight to single-service
// edit as always (a project with only one service, the common case).
func siblingServiceCount(snapshot domain.Snapshot, project string) int {
	for _, p := range snapshot.Projects {
		if p.Name == project {
			return len(p.Services)
		}
	}
	return 0
}

// resolveProjectComposeFile finds project's base compose file path by
// reading any of its services' containers' own Compose.ConfigFiles label
// — every service in a normal compose deployment shares one base file,
// so the first one found is as good as any.
func (m Model) resolveProjectComposeFile(project string) string {
	for _, p := range m.snapshot.Projects {
		if p.Name != project {
			continue
		}
		for _, svc := range p.Services {
			for _, ctr := range svc.Containers {
				if files := splitComposeConfigFiles(ctr.Compose.ConfigFiles); len(files) > 0 {
					return files[0]
				}
			}
		}
	}
	return ""
}

// openEditWholeStackOverlay opens the create form seeded to edit an
// existing multi-service project's whole base compose file at once,
// rather than one service's own override — reached either by selecting
// the project/folder row directly (model.go's "m" handler) or by
// choosing "s" at the warn-and-offer prompt shown for an individual
// container in a multi-service project. Loads the base file's real
// current on-disk content; since that necessarily defines more than one
// service (that's how either entry point was reached), createDraft.
// IsStack activates automatically and the form renders in stack mode,
// pre-populated and flagged as an edit. Confirming overwrites that same
// file and reconciles with `up -d` (defaultApplyComposeStack) — no new
// apply logic, just seeded differently than a fresh stack paste.
func (m *Model) openEditWholeStackOverlay(project string) tea.Cmd {
	composeFile := m.resolveProjectComposeFile(project)
	m.openCreateOverlayWithDraft(createDraft{
		Mode:        createModeCompose,
		Editing:     true,
		Project:     project,
		ComposeFile: composeFile,
	})
	if composeFile == "" {
		m.status, m.statusErr = "could not resolve a compose file for "+project, true
		return nil
	}
	system := m.activeSystemConfig()
	if system.Kind == "ssh" {
		return checkStackFileCmd(system, project, composeFile)
	}
	content, err := os.ReadFile(composeFile)
	if err != nil {
		m.status, m.statusErr = "reading "+composeFile+": "+err.Error(), true
		return nil
	}
	m.createDraft.OverrideRaw = string(content)
	m.createDraft.OverrideRawSet = true
	m.createDraft.OverrideLoaded = true
	m.status, m.statusErr = "loaded "+project+" for editing", false
	return nil
}

// createStackFileCheckMsg carries the SSH round trip of loading a whole
// project's base compose file (openEditWholeStackOverlay's remote path)
// back into Update. project guards against applying a stale result if
// the draft's target project changed (or the overlay closed) before the
// round trip finished — same convention as createOverrideCheckMsg's
// service guard.
type createStackFileCheckMsg struct {
	project string
	content string
	err     error
}

func checkStackFileCmd(system config.System, project, composeFile string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		output, err := sshRun(ctx, system, "cat "+systems.ShellQuote(composeFile), "")
		if err != nil {
			return createStackFileCheckMsg{project: project, err: err}
		}
		return createStackFileCheckMsg{project: project, content: string(output)}
	}
}

// createSelectedComposeFileMsg carries the content of a compose file picked
// from the create overlay's browser. It is intentionally separate from
// createStackFileCheckMsg: browsing while creating a new service should only
// switch into whole-stack mode when the selected file itself defines multiple
// services, and must not disturb the existing per-service override load path.
type createSelectedComposeFileMsg struct {
	path    string
	content string
	err     error
}

func (m Model) loadSelectedComposeFileCmd(composeFile string) tea.Cmd {
	composeFile = strings.TrimSpace(composeFile)
	if composeFile == "" {
		return nil
	}
	system := m.activeSystemConfig()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if system.Kind == "ssh" {
			output, err := sshRun(ctx, system, "cat "+systems.ShellQuote(composeFile), "")
			if err != nil {
				return createSelectedComposeFileMsg{path: composeFile, err: err}
			}
			return createSelectedComposeFileMsg{path: composeFile, content: string(output)}
		}
		data, err := os.ReadFile(composeFile)
		if err != nil {
			return createSelectedComposeFileMsg{path: composeFile, err: err}
		}
		return createSelectedComposeFileMsg{path: composeFile, content: string(data)}
	}
}

// openCreateOverlayWithDraft sets the create-overlay state shared by a fresh
// Create draft and a Clone draft.
func (m *Model) openCreateOverlayWithDraft(draft createDraft) {
	m.createDraft = draft
	m.overlay = overlayCreate
	m.createField = m.visibleCreateFields()[0]
	m.syncCreateFieldEditor()
	m.createEditingCompose = false
	m.clearCreateNotice()
	m.status, m.statusErr = "create draft ready", false
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
	service         string
	base            string
	content         string
	found           bool
	baseFileMissing bool
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
		baseMissing := false
		if _, err := sshRun(ctx, system, "test -f "+systems.ShellQuote(base), ""); err != nil {
			baseMissing = true
		}
		output, err := sshRun(ctx, system, "cat "+systems.ShellQuote(overridePath), "")
		if err != nil {
			return createOverrideCheckMsg{service: service, base: base, found: false, baseFileMissing: baseMissing}
		}
		return createOverrideCheckMsg{service: service, base: base, content: string(output), found: true, baseFileMissing: baseMissing}
	}
}

// defaultCreateDraft is the blank slate 'n' opens: generic placeholders
// only, never pulled from whatever happens to be selected in the tree. It
// used to prefill Image/Project/Service/ComposeFile (and default Mode)
// from the current selection as a convenience, but that made "create
// something new" and "look at what's currently selected" indistinguishable
// at a glance — a container picked for an unrelated reason (just
// happened to be under the cursor) silently leaked its image/project into
// what was supposed to be an unrelated new service. Clone (C) and Edit (e)
// are the intentional "start from what's selected" actions — see
// selectionCreateDraft, which they use instead.
func (m Model) defaultCreateDraft() createDraft {
	return createDraft{
		Mode:          createModeCompose,
		Project:       "default",
		Service:       "new-service",
		ContainerName: "new-container",
		Image:         "image:tag",
		ImageAction:   imageActionKeep,
		Restart:       "unless-stopped",
		ComposeFile:   "compose.yml",
	}
}

// selectionCreateDraft is defaultCreateDraft's old prefill-from-selection
// shape, kept for Clone/Edit — the two actions that are actually meant to
// start from what's currently selected (see defaultCreateDraft's own doc
// comment for why plain Create no longer does).
func (m Model) selectionCreateDraft() createDraft {
	draft := m.defaultCreateDraft()
	selected := m.selectedContainer()
	if selected == nil {
		return draft
	}
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
	return draft
}

// defaultCloneDraft mirrors selectionCreateDraft but carries the selected
// container's full runtime shape (Ports/Mounts/Env/Restart/Command) into the
// draft — selectionCreateDraft only carries Image/Project/Service/ComposeFile,
// which is enough for a fresh draft but not for duplicating something that
// already exists — and suffixes the identity field so the user renames it
// before confirming. Clone must never silently overwrite the original.
func (m Model) defaultCloneDraft() createDraft {
	draft := m.selectionCreateDraft()
	selected := m.selectedContainer()
	if selected == nil {
		return draft
	}
	draft.Ports = formatDraftPorts(selected.Ports)
	draft.Mounts = formatDraftMounts(selected.Mounts)
	draft.Env = formatEnvEntries(selected.Env)
	if selected.RestartPolicy != "" {
		draft.Restart = selected.RestartPolicy
	}
	draft.Command = selected.Command
	if draft.Mode == createModeStandalone {
		draft.ContainerName = selected.DisplayName() + "-clone"
	} else {
		draft.Service = selected.Compose.Service + "-clone"
	}
	return draft
}

// defaultEditDraft mirrors defaultCloneDraft's full-shape prefill (it starts
// from the exact same base) but keeps the container's real name instead of
// a "-clone" suffix, and records EditingID so confirm dispatch
// (handleCreateKey) knows which container to replace. Standalone only —
// openEditOverlay handles Compose services through openCreateOverlay's
// existing already-managed-service path instead.
func (m Model) defaultEditDraft() createDraft {
	draft := m.defaultCloneDraft()
	selected := m.selectedContainer()
	if selected == nil {
		return draft
	}
	draft.ContainerName = selected.DisplayName()
	draft.EditingID = selected.ID
	return draft
}

// replicateContainerSpec builds an app.ContainerCreateSpec identical to
// selected's current shape (same name, image, ports, mounts, env, restart,
// command) — used by Replicate's standalone path to recreate the container
// in place after pulling a fresh image. Reuses the same field-mapping
// defaultCloneDraft uses, minus the "-clone" identity suffix, and the
// existing createDraft.ContainerSpec() parsing instead of duplicating it.
func replicateContainerSpec(selected domain.Container) (app.ContainerCreateSpec, error) {
	draft := createDraft{
		Mode:          createModeStandalone,
		ContainerName: selected.DisplayName(),
		Image:         selected.Image,
		Command:       selected.Command,
		Ports:         formatDraftPorts(selected.Ports),
		Mounts:        formatDraftMounts(selected.Mounts),
		Env:           formatEnvEntries(selected.Env),
		Restart:       selected.RestartPolicy,
	}
	return draft.ContainerSpec()
}

// formatDraftPorts renders a container's published ports into the
// comma-joined "host:container/proto" shape splitDraftList expects — the
// reverse of parseCreatePorts. Unpublished ports (Public == 0) have nothing
// meaningful to prefill.
func formatDraftPorts(ports []domain.Port) string {
	parts := make([]string, 0, len(ports))
	for _, p := range ports {
		if p.Public == 0 {
			continue
		}
		entry := fmt.Sprintf("%d:%d/%s", p.Public, p.Private, emptyAs(p.Type, "tcp"))
		if p.IP != "" && p.IP != "0.0.0.0" {
			entry = p.IP + ":" + entry
		}
		parts = append(parts, entry)
	}
	return strings.Join(parts, ", ")
}

// formatDraftMounts renders a container's mounts into the comma-joined
// "source:target[:ro]" shape splitDraftList expects — the reverse of
// parseCreateMounts.
func formatDraftMounts(mounts []domain.Mount) string {
	parts := make([]string, 0, len(mounts))
	for _, mnt := range mounts {
		entry := mnt.Source + ":" + mnt.Destination
		if !mnt.ReadWrite {
			entry += ":ro"
		}
		parts = append(parts, entry)
	}
	return strings.Join(parts, ", ")
}

func (m Model) handleCreateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.createEditingCompose {
		return m.handleCreateEditorKey(msg)
	}
	if m.createCatalogOpen {
		return m.handleCreateCatalogKey(msg)
	}
	if m.createBrowsing {
		return m.handleCreateFileBrowserKey(msg)
	}
	if m.createDraft.Confirming {
		if m.busy {
			return m, nil
		}
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
				m.busy = true
				m.actionProgressPercent = 0
				m.createDoneReady = false
				m.createDoneResult = createDoneMsg{}
				progress := make(chan string, 16)
				m.actionProgress = progress
				if m.createDraft.IsStack() {
					// No adopt/merge distinction for a stack — the whole
					// file gets written either way (see
					// defaultApplyComposeStack), whether or not
					// spec.BaseFile already existed.
					m.actionProgressText = "validating stack " + spec.Project + "…"
					m.status, m.statusErr = "deploying stack "+spec.Project, false
					return m, m.stackComposeCmd(spec, progress)
				}
				if m.createDraft.BaseFileMissing {
					m.actionProgressText = "validating " + spec.Service + "…"
					m.status, m.statusErr = "creating compose file for "+spec.Service, false
					return m, m.adoptComposeCmd(spec, progress)
				}
				m.actionProgressText = "validating " + spec.Service + "…"
				m.status, m.statusErr = "applying compose service "+spec.Service, false
				return m, m.createComposeCmd(spec, progress)
			}
			spec, err := m.createDraft.ContainerSpec()
			if err != nil {
				m.createDraft.Confirming = false
				m.status, m.statusErr = "create: "+err.Error(), true
				return m, nil
			}
			m.busy = true
			m.actionProgressPercent = 0
			m.createDoneReady = false
			m.createDoneResult = createDoneMsg{}
			progress := make(chan string, 16)
			m.actionProgress = progress
			if m.createDraft.Pasting {
				if m.createDraft.pullImageBeforeApply() {
					m.actionProgressText = "pulling " + spec.Image + "…"
				} else {
					m.actionProgressText = "deploying " + spec.Name + "…"
				}
				m.status, m.statusErr = "pasting "+spec.Name, false
				return m, m.pasteApplyCmd(spec, m.createDraft.pullImageBeforeApply(), progress)
			}
			if m.createDraft.Editing {
				if m.createDraft.pullImageBeforeApply() {
					m.actionProgressText = "pulling " + spec.Image + "…"
				} else {
					m.actionProgressText = "recreating " + spec.Name + "…"
				}
				m.status, m.statusErr = "updating "+spec.Name, false
				return m, m.editContainerCmd(m.createDraft.EditingID, spec, m.createDraft.pullImageBeforeApply(), progress)
			}
			if m.createDraft.pullImageBeforeApply() {
				m.actionProgressText = "pulling " + spec.Image + "…"
			} else {
				m.actionProgressText = "creating " + spec.Name + "…"
			}
			m.status, m.statusErr = "creating "+spec.Name, false
			return m, m.createContainerCmd(spec, m.createDraft.pullImageBeforeApply(), progress)
		}
		return m, nil
	}
	var cmd tea.Cmd
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
		cmd = m.forwardToFieldEditor(msg)
	case "j":
		if m.isCreateChoiceField() {
			m.moveCreateField(1)
			return m, nil
		}
		cmd = m.forwardToFieldEditor(msg)
	case "left", "right", "shift+left", "shift+right",
		"ctrl+left", "ctrl+right", "ctrl+shift+left", "ctrl+shift+right":
		if m.isCreateChoiceField() {
			// Only plain left/right cycle a choice — shift/ctrl variants
			// have no meaning there (no selection, no words to jump).
			switch msg.String() {
			case "left":
				m.cycleCreateChoice(-1)
			case "right":
				m.cycleCreateChoice(1)
			}
			return m, nil
		}
		cmd = m.forwardToFieldEditor(msg)
	case "h":
		if m.isCreateChoiceField() {
			m.cycleCreateChoice(-1)
			return m, nil
		}
		cmd = m.forwardToFieldEditor(msg)
	case "l":
		if m.isCreateChoiceField() {
			m.cycleCreateChoice(1)
			return m, nil
		}
		cmd = m.forwardToFieldEditor(msg)
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
		m.syncCreateFieldEditor()
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
			m.syncCreateFieldEditor()
			return m, m.openCreateFileBrowser()
		}
		cmd = m.forwardToFieldEditor(msg)
	case "[", "]":
		m.cycleCreateMode()
	case "ctrl+y":
		// Ripple's own default keymap binds ctrl+y to Redo, but this app
		// already uses ctrl+y globally for the Compose YAML editor — kept
		// intercepted here unconditionally (even in standalone mode, where
		// the body below does nothing) so it can never reach the field
		// editor and mean something else there.
		if m.createDraft.Mode == createModeCompose {
			m.openCreateEditor()
			return m, nil
		}
	case "ctrl+p":
		if m.createDraft.Mode == createModeCompose {
			m.openCreateCatalog()
			return m, nil
		}
	case "ctrl+s":
		m.validateCreateDraft()
	case "ctrl+enter", "alt+enter":
		if m.validateCreateDraft() {
			m.createDraft.Confirming = true
			switch {
			case m.createDraft.IsStack():
				m.status, m.statusErr = "confirm deploy stack "+m.createDraft.TargetName(), false
			case m.createDraft.Mode == createModeCompose && m.createDraft.BaseFileMissing:
				m.status, m.statusErr = "confirm create & adopt "+m.createDraft.TargetName(), false
			default:
				m.status, m.statusErr = "confirm "+confirmStepLabel(m.createDraft.Editing)+" "+m.createDraft.TargetName(), false
			}
		}
	case "backspace", "delete", "ctrl+z", "ctrl+c", "ctrl+x", "ctrl+v":
		// These have no msg.Runes (they're control keys, not printable
		// input), so they'd never reach the default case's Rune-gated
		// forward below — copy/cut/paste/undo need their own case to ever
		// get to the editor at all.
		cmd = m.forwardToFieldEditor(msg)
	case "home", "ctrl+a":
		// This app has long treated ctrl+a as a Home alias, not Ripple's
		// own default ctrl+a-selects-all — translate to a synthetic Home
		// key instead of forwarding the raw one, so that convention holds
		// (Shift+Home, in the case above, already gives a real "select to
		// start of field" if that's what's wanted).
		cmd = m.forwardToFieldEditor(tea.KeyMsg{Type: tea.KeyHome})
	case "end", "ctrl+e":
		cmd = m.forwardToFieldEditor(tea.KeyMsg{Type: tea.KeyEnd})
	case "ctrl+u":
		if !m.isCreateChoiceField() {
			m.createFieldEditor.SelectAll()
			m.createFieldEditor.DeleteSelection()
			m.clearCreateNotice()
			m.setCreateFieldValue(m.createFieldEditor.Value())
		}
	default:
		if len(msg.Runes) > 0 {
			cmd = m.forwardToFieldEditor(msg)
		}
	}
	return m, cmd
}

func (m *Model) openCreateCatalog() {
	if m.catalogDir == "" {
		m.status, m.statusErr = "compose catalog unavailable: settings path is not configured", true
		return
	}
	entries, err := catalog.Load(m.catalogDir)
	if err != nil {
		m.status, m.statusErr = "compose catalog: "+err.Error(), true
		return
	}
	m.createCatalogOpen = true
	m.createCatalogMode = createCatalogList
	m.createCatalogEntries = entries
	m.createCatalogCursor = clamp(m.createCatalogCursor, 0, max(0, len(m.filteredCatalogEntries())-1))
	m.createCatalogFilter = ""
	m.createCatalogEdit = ""
	m.createCatalogEditCursor = 0
	m.createCatalogErr = ""
	m.status, m.statusErr = "compose catalog", false
}

func (m Model) handleCreateCatalogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.createCatalogMode {
	case createCatalogRename:
		return m.handleCreateCatalogRenameKey(msg)
	case createCatalogDelete:
		return m.handleCreateCatalogDeleteKey(msg)
	}
	switch msg.String() {
	case "esc", "q":
		m.createCatalogOpen = false
		m.createCatalogErr = ""
	case "up", "k":
		m.moveCreateCatalogCursor(-1)
	case "down", "j", "tab":
		m.moveCreateCatalogCursor(1)
	case "home":
		m.createCatalogCursor = 0
	case "end":
		m.createCatalogCursor = max(0, len(m.filteredCatalogEntries())-1)
	case "backspace":
		if m.createCatalogFilter != "" {
			runes := []rune(m.createCatalogFilter)
			m.createCatalogFilter = string(runes[:len(runes)-1])
			m.createCatalogCursor = 0
		}
	case "ctrl+u":
		m.createCatalogFilter = ""
		m.createCatalogCursor = 0
	case "enter", "l":
		if err := m.loadCurrentCatalogEntry(); err != nil {
			m.createCatalogErr = err.Error()
			return m, nil
		}
		m.createCatalogOpen = false
	case "s":
		if err := m.saveCurrentDraftToCatalog(); err != nil {
			m.createCatalogErr = err.Error()
			m.setCreateNotice("catalog save: "+err.Error(), true)
			return m, nil
		}
		m.createCatalogErr = ""
	case "r":
		if entry, ok := m.currentCatalogEntry(); ok {
			m.createCatalogMode = createCatalogRename
			m.createCatalogEdit = entry.Name
			m.createCatalogEditCursor = len([]rune(entry.Name))
			m.createCatalogErr = ""
		}
	case "d":
		if _, ok := m.currentCatalogEntry(); ok {
			m.createCatalogMode = createCatalogDelete
			m.createCatalogErr = ""
		}
	default:
		if len(msg.Runes) > 0 {
			m.createCatalogFilter += string(msg.Runes)
			m.createCatalogCursor = 0
		}
	}
	return m, nil
}

func (m Model) handleCreateCatalogRenameKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.createCatalogMode = createCatalogList
		m.createCatalogEdit = ""
		m.createCatalogEditCursor = 0
	case "enter":
		entry, ok := m.currentCatalogEntry()
		if !ok {
			m.createCatalogMode = createCatalogList
			return m, nil
		}
		if err := catalog.Rename(m.catalogDir, entry.ID, m.createCatalogEdit); err != nil {
			m.createCatalogErr = err.Error()
			return m, nil
		}
		entries, err := catalog.Load(m.catalogDir)
		if err != nil {
			m.createCatalogErr = err.Error()
			return m, nil
		}
		m.createCatalogEntries = entries
		m.createCatalogMode = createCatalogList
		m.createCatalogEdit = ""
		m.createCatalogEditCursor = 0
		m.createCatalogErr = ""
		m.status, m.statusErr = "renamed catalog entry", false
	case "left":
		m.createCatalogEditCursor = max(0, m.createCatalogEditCursor-1)
	case "right":
		m.createCatalogEditCursor = min(len([]rune(m.createCatalogEdit)), m.createCatalogEditCursor+1)
	case "home", "ctrl+a":
		m.createCatalogEditCursor = 0
	case "end", "ctrl+e":
		m.createCatalogEditCursor = len([]rune(m.createCatalogEdit))
	case "backspace":
		runes := []rune(m.createCatalogEdit)
		if m.createCatalogEditCursor > 0 && m.createCatalogEditCursor <= len(runes) {
			runes = append(runes[:m.createCatalogEditCursor-1], runes[m.createCatalogEditCursor:]...)
			m.createCatalogEdit = string(runes)
			m.createCatalogEditCursor--
		}
	case "delete":
		runes := []rune(m.createCatalogEdit)
		if m.createCatalogEditCursor < len(runes) {
			runes = append(runes[:m.createCatalogEditCursor], runes[m.createCatalogEditCursor+1:]...)
			m.createCatalogEdit = string(runes)
		}
	case "ctrl+u":
		m.createCatalogEdit = ""
		m.createCatalogEditCursor = 0
	default:
		if len(msg.Runes) > 0 {
			runes := []rune(m.createCatalogEdit)
			cursor := clamp(m.createCatalogEditCursor, 0, len(runes))
			merged := append([]rune{}, runes[:cursor]...)
			merged = append(merged, msg.Runes...)
			merged = append(merged, runes[cursor:]...)
			m.createCatalogEdit = string(merged)
			m.createCatalogEditCursor = cursor + len(msg.Runes)
		}
	}
	return m, nil
}

func (m Model) handleCreateCatalogDeleteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "n", "q":
		m.createCatalogMode = createCatalogList
	case "y":
		entry, ok := m.currentCatalogEntry()
		if !ok {
			m.createCatalogMode = createCatalogList
			return m, nil
		}
		if err := catalog.Delete(m.catalogDir, entry.ID); err != nil {
			m.createCatalogErr = err.Error()
			m.createCatalogMode = createCatalogList
			return m, nil
		}
		entries, err := catalog.Load(m.catalogDir)
		if err != nil {
			m.createCatalogErr = err.Error()
			m.createCatalogMode = createCatalogList
			return m, nil
		}
		m.createCatalogEntries = entries
		m.createCatalogCursor = clamp(m.createCatalogCursor, 0, max(0, len(m.filteredCatalogEntries())-1))
		m.createCatalogMode = createCatalogList
		m.createCatalogErr = ""
		m.status, m.statusErr = "deleted catalog entry "+entry.Name, false
	}
	return m, nil
}

func (m *Model) moveCreateCatalogCursor(delta int) {
	entries := m.filteredCatalogEntries()
	if len(entries) == 0 {
		m.createCatalogCursor = 0
		return
	}
	m.createCatalogCursor = clamp(m.createCatalogCursor+delta, 0, len(entries)-1)
}

func (m Model) filteredCatalogEntries() []catalog.Entry {
	filter := strings.ToLower(strings.TrimSpace(m.createCatalogFilter))
	if filter == "" {
		return m.createCatalogEntries
	}
	out := make([]catalog.Entry, 0, len(m.createCatalogEntries))
	for _, entry := range m.createCatalogEntries {
		if strings.Contains(strings.ToLower(entry.Name), filter) {
			out = append(out, entry)
		}
	}
	return out
}

func (m Model) currentCatalogEntry() (catalog.Entry, bool) {
	entries := m.filteredCatalogEntries()
	if len(entries) == 0 {
		return catalog.Entry{}, false
	}
	return entries[clamp(m.createCatalogCursor, 0, len(entries)-1)], true
}

func (m *Model) loadCurrentCatalogEntry() error {
	entry, ok := m.currentCatalogEntry()
	if !ok {
		return errors.New("no catalog entry selected")
	}
	content, err := catalog.Read(m.catalogDir, entry.ID)
	if err != nil {
		return err
	}
	m.createDraft.OverrideRaw = content
	m.createDraft.OverrideRawSet = strings.TrimSpace(m.createDraft.OverrideRaw) != ""
	m.createDraft.OverrideLoaded = true
	m.createDraft.OverrideRawBase = true
	m.createDraft.applyOverrideFieldsFromYAML(m.createDraft.OverrideRaw)
	m.revalidateCreateField()
	m.status, m.statusErr = "loaded catalog entry "+entry.Name, false
	return nil
}

func (m *Model) saveCurrentDraftToCatalog() error {
	content := m.createDraft.Preview()
	if m.createDraft.Mode == createModeCompose && m.createDraft.OverrideRawSet {
		content = m.createDraft.OverrideRaw
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	entry, err := catalog.Save(m.catalogDir, defaultCatalogEntryName(m.createDraft), content)
	if err != nil {
		return err
	}
	entries, err := catalog.Load(m.catalogDir)
	if err != nil {
		return err
	}
	m.createCatalogEntries = entries
	for i, candidate := range m.filteredCatalogEntries() {
		if candidate.ID == entry.ID {
			m.createCatalogCursor = i
			break
		}
	}
	m.setCreateNotice("saved catalog entry "+entry.Name, false)
	return nil
}

func defaultCatalogEntryName(d createDraft) string {
	if d.IsStack() {
		return emptyAs(d.Project, "Compose stack")
	}
	return emptyAs(d.Service, "Compose service")
}

func (m Model) createContainerCmd(spec app.ContainerCreateSpec, pullFirst bool, progress chan string) tea.Cmd {
	provider := m.provider
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		id, err := pullThenCreate(ctx, provider, spec, pullFirst, progress)
		return createDoneMsg{name: spec.Name, id: id, err: err}
	}
}

// pullThenCreate is createContainerCmd's actual body, factored out so
// pasteApplyCmd (paste.go) can run the exact same pull-then-create sequence
// after its own network-creation pre-step, instead of duplicating it.
func pullThenCreate(ctx context.Context, provider app.Provider, spec app.ContainerCreateSpec, pullFirst bool, progress chan string) (domain.ResourceID, error) {
	if pullFirst {
		onProgress := func(p app.PullProgress) {
			sendActionProgress(progress, formatPullProgress(spec.Image, p))
		}
		if err := provider.PullImage(ctx, spec.Image, onProgress); err != nil {
			return domain.ResourceID{}, err
		}
	}
	sendActionProgress(progress, "creating "+spec.Name+"…")
	return provider.CreateContainer(ctx, spec)
}

// editContainerCmd replaces id in place with a fresh container built from
// spec. Pulling and creating a stopped replacement happen before removing
// the current container, so a registry failure or create-time validation
// failure does not delete the user's still-working original. Starting still
// happens after removal because Docker cannot run two containers with the
// same published ports/name at once.
func (m Model) editContainerCmd(id domain.ResourceID, spec app.ContainerCreateSpec, pullFirst bool, progress chan string) tea.Cmd {
	provider := m.provider
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if pullFirst {
			onProgress := func(p app.PullProgress) {
				sendActionProgress(progress, formatPullProgress(spec.Image, p))
			}
			if err := provider.PullImage(ctx, spec.Image, onProgress); err != nil {
				return createDoneMsg{name: spec.Name, edited: true, err: err}
			}
		}
		tempSpec := spec
		tempSpec.Name = editReplacementName(spec.Name)
		tempSpec.Start = false
		sendActionProgress(progress, "creating replacement for "+spec.Name+"…")
		newID, err := provider.CreateContainer(ctx, tempSpec)
		if err != nil {
			return createDoneMsg{name: spec.Name, edited: true, err: err}
		}
		cleanupReplacement := true
		defer func() {
			if cleanupReplacement {
				_ = provider.RemoveContainer(context.Background(), newID, true)
			}
		}()
		sendActionProgress(progress, "removing "+spec.Name+"…")
		if err := provider.RemoveContainer(ctx, id, true); err != nil {
			return createDoneMsg{name: spec.Name, edited: true, err: err}
		}
		// The original is gone as of here, so the replacement must never be
		// cleaned up past this point no matter what fails next — deleting
		// it once the original is already gone would leave the user with
		// neither container. cleanupReplacement exists only to undo the
		// replacement create itself, while the original is still safely in
		// place.
		cleanupReplacement = false
		sendActionProgress(progress, "renaming replacement to "+spec.Name+"…")
		if err := provider.RenameContainer(ctx, newID, spec.Name); err != nil {
			return createDoneMsg{name: spec.Name, id: newID, edited: true, err: err}
		}
		sendActionProgress(progress, "starting "+spec.Name+"…")
		if err := provider.StartContainer(ctx, newID); err != nil {
			return createDoneMsg{name: spec.Name, id: newID, edited: true, err: err}
		}
		return createDoneMsg{name: spec.Name, id: newID, edited: true}
	}
}

func editReplacementName(name string) string {
	return "whatthedock-edit-" + safeComposeFilename(name) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func (m Model) createComposeCmd(spec composeCreateSpec, progress chan string) tea.Cmd {
	apply := applyComposeCreate
	spec.Progress = func(line string) { sendActionProgress(progress, line) }
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		err := apply(ctx, spec)
		return createDoneMsg{name: spec.Service, project: spec.Project, service: spec.Service, err: err}
	}
}

// stackComposeCmd is createComposeCmd's stack counterpart — see
// defaultApplyComposeStack. No single service name to report: name/
// project both use spec.Project, and service is left empty so the
// createDoneMsg handler's tree-selection logic (pendingSelectProject)
// knows to land on the project row rather than hunt for a container that
// isn't any more "the" result than its siblings.
func (m Model) stackComposeCmd(spec composeCreateSpec, progress chan string) tea.Cmd {
	apply := applyComposeStack
	spec.Progress = func(line string) { sendActionProgress(progress, line) }
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		err := apply(ctx, spec)
		return createDoneMsg{name: spec.Project, project: spec.Project, err: err}
	}
}

// adoptComposeCmd is createComposeCmd's counterpart for a draft whose base
// compose file doesn't exist yet (createDraft.BaseFileMissing) — see
// applyComposeAdopt.
func (m Model) adoptComposeCmd(spec composeCreateSpec, progress chan string) tea.Cmd {
	apply := applyComposeAdopt
	spec.Progress = func(line string) { sendActionProgress(progress, line) }
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		err := apply(ctx, spec)
		return createDoneMsg{name: spec.Service, project: spec.Project, service: spec.Service, err: err}
	}
}

func (m Model) deleteComposeCmd(spec composeCreateSpec) tea.Cmd {
	apply := applyComposeDelete
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		return actionDoneMsg{label: "delete " + spec.Service, err: apply(ctx, spec)}
	}
}

// deleteStackCmd is deleteComposeCmd's whole-project counterpart — see
// defaultApplyComposeDeleteStack.
func (m Model) deleteStackCmd(spec composeCreateSpec) tea.Cmd {
	apply := applyComposeDeleteStack
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		return actionDoneMsg{label: "delete stack " + spec.Project, err: apply(ctx, spec)}
	}
}

func (m Model) replicateComposeCmd(spec composeCreateSpec) tea.Cmd {
	apply := applyComposeReplicate
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute) // pulling an image can be slow
		defer cancel()
		return actionDoneMsg{label: "replicate " + spec.Service, err: apply(ctx, spec)}
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
		m.syncCreateFieldEditor()
		m.createBrowsing = false
		m.status, m.statusErr = "compose file selected", false
		return m, tea.Batch(m.checkComposeOverrideCmd(), m.loadSelectedComposeFileCmd(entry.Path))
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
	m.createDraft.OverrideRawBase = m.createDraft.OverrideRawBase && value != ""
	m.createEditingCompose = false
	if m.createDraft.OverrideRawSet {
		m.createDraft.applyOverrideFieldsFromYAML(value)
		m.setCreateNotice("override YAML edited", false)
		// applyOverrideFieldsFromYAML leaves the Service field alone when
		// the content doesn't unambiguously name one to sync from (e.g. a
		// pasted multi-service block) — surface that mismatch now, at
		// save time, instead of only failing at confirm with a bare
		// "no such service" from the compose CLI (see Validate).
		if err := m.createDraft.Validate(); err != nil {
			m.setCreateNotice("override saved, but "+err.Error(), true)
		}
	} else {
		m.setCreateNotice("override YAML reset to generated", false)
	}
	// Saving can change whether this draft is a stack (createDraft.
	// IsStack), which changes visibleCreateFields() in both directions —
	// re-anchor the selection if it just dropped out of view.
	m.revalidateCreateField()
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
		m.setCreateNotice("create: "+err.Error(), true)
		return false
	}
	m.setCreateNotice("create draft validated", false)
	return true
}

func (m *Model) setCreateNotice(message string, err bool) {
	m.createNotice = message
	m.createNoticeErr = err
}

func (m *Model) clearCreateNotice() {
	m.createNotice = ""
	m.createNoticeErr = false
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
			return errors.New("stack is required")
		}
		if strings.TrimSpace(d.ComposeFile) == "" {
			return errors.New("compose file is required")
		}
		if d.IsStack() {
			// No single Service/Image to require — the content itself
			// (already confirmed to define more than one service, or
			// IsStack wouldn't be true) is what gets written.
			return nil
		}
		if strings.TrimSpace(d.Service) == "" {
			return errors.New("service name is required")
		}
		if strings.TrimSpace(d.Image) == "" {
			return errors.New("image is required")
		}
		// Hand-edited or loaded override content (OverrideRawSet) isn't
		// generated from the Service field the way composeOverrideContent
		// is, so the two can drift apart — most commonly, pasting a
		// multi-service YAML block whose services don't include one
		// matching the current Service field leaves that field
		// untouched (applyOverrideFieldsFromYAML won't guess which one
		// was meant) with no indication anything's now mismatched. Left
		// unguarded, ComposeSpec would go on to write that content and
		// run `docker compose up -d <Service>` against it, surfacing as
		// a bare "no such service: <name>" from the compose CLI instead
		// of a clear, actionable message here before it ever runs.
		if d.OverrideRawSet {
			var doc composeOverrideDoc
			if err := yaml.Unmarshal([]byte(d.OverrideRaw), &doc); err == nil && len(doc.Services) > 0 {
				if _, ok := doc.Services[strings.TrimSpace(d.Service)]; !ok {
					names := make([]string, 0, len(doc.Services))
					for name := range doc.Services {
						names = append(names, name)
					}
					sort.Strings(names)
					return fmt.Errorf("override content has no service named %q (found: %s) — set Service to match, or edit the override", d.Service, strings.Join(names, ", "))
				}
			}
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
	// A paste draft starts from the yanked container's full spec —
	// everything the form doesn't expose (privileged, capabilities,
	// devices, resource limits, healthcheck, DNS, ...) rides along
	// unedited — and only overwrites the fields the form actually lets the
	// user change below. A fresh/cloned/edited draft has no such spec to
	// start from, so every field comes from the form as it always has.
	spec := app.ContainerCreateSpec{}
	if d.Pasting && d.PastePlan != nil {
		spec = d.PastePlan.Spec
		spec.Networks = parseCreateNetworks(d.Networks, d.PastePlan.Spec.Networks)
	}
	spec.Name = strings.TrimSpace(d.ContainerName)
	spec.Image = strings.TrimSpace(d.Image)
	spec.Command = splitCommand(d.Command)
	spec.Env = env
	spec.Ports = ports
	spec.Mounts = mounts
	spec.RestartPolicy = normalizeRestartPolicy(d.Restart)
	spec.Start = true
	return spec, nil
}

// parseCreateNetworks parses the Networks field's comma-joined destination
// network names back into []app.NetworkAttachment, carrying over each
// original entry's aliases by position — the field only lets the user
// rename/remap which network to attach to, not edit aliases directly.
// Fewer or more names than original loses aliases past the shorter length
// rather than erroring, since a genuinely new network name has no aliases
// to inherit anyway.
func parseCreateNetworks(value string, original []app.NetworkAttachment) []app.NetworkAttachment {
	names := splitDraftList(value)
	if len(names) == 0 {
		return nil
	}
	out := make([]app.NetworkAttachment, 0, len(names))
	for i, name := range names {
		attachment := app.NetworkAttachment{Name: name}
		if i < len(original) {
			attachment.Aliases = append([]string(nil), original[i].Aliases...)
		}
		out = append(out, attachment)
	}
	return out
}

func (d createDraft) ComposeSpec(system config.System) (composeCreateSpec, error) {
	if d.Mode != createModeCompose {
		return composeCreateSpec{}, errors.New("compose spec requires compose mode")
	}
	if err := d.Validate(); err != nil {
		return composeCreateSpec{}, err
	}
	if d.IsStack() {
		// No per-service override filename, no Ports/Mounts/Env to parse
		// (there's no single service field they'd belong to) — the whole
		// document is the base file's own content.
		return composeCreateSpec{
			Project:  strings.TrimSpace(d.Project),
			BaseFile: strings.TrimSpace(d.ComposeFile),
			Content:  d.OverrideRaw,
			FullBase: true,
			System:   system,
		}, nil
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
		FullBase:     d.OverrideRawBase,
		PullBeforeUp: d.pullImageBeforeApply(),
		System:       system,
	}, nil
}

func (d createDraft) pullImageBeforeApply() bool {
	return !d.IsStack() && strings.TrimSpace(d.ImageAction) == imageActionPull
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
	lines = appendQuotedYAMLList(lines, "    environment:", splitEnvEntries(d.Env), "      - ")
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
// mirror image of composeOverrideContent's generation. Environment and
// Command are both typed as interface{} because Compose allows either of
// them as more than one shape — Environment as a list ("KEY=value" entries)
// or a map (KEY: value); Command as a single string or an exec-form list
// (["sh", "-c", "..."]) — normalizeComposeEnvironment/normalizeComposeCommand
// reconcile each into the single string/list form the rest of this file
// works with. Decoding Command as a plain string used to hard-fail
// yaml.Unmarshal on any real-world file with even one exec-form command
// among its services — cannot unmarshal !!seq into string — which silently
// aborted applyOverrideFieldsFromYAML before it ever got a chance to derive
// anything, even though doc.Services had already been populated for every
// service that didn't hit the mismatch.
type composeOverrideDoc struct {
	Services map[string]composeOverrideService `yaml:"services"`
}

type composeOverrideService struct {
	Image       string      `yaml:"image"`
	Restart     string      `yaml:"restart"`
	Command     interface{} `yaml:"command"`
	Ports       []string    `yaml:"ports"`
	Volumes     []string    `yaml:"volumes"`
	Environment interface{} `yaml:"environment"`
}

// applyOverrideFieldsFromYAML parses content as Compose override YAML and
// updates d's structured fields — Service and Image among them — to match
// whichever service selectOverrideService picks (falling back to the
// first service in the document's own source order when that's ambiguous
// — see below), so the form (the create overlay's own field column)
// actually reflects content that was loaded (override-detection) or
// hand-edited/pasted (the Ripple editor) instead of continuing to show
// whatever was there before. Only content that fails to parse, or names
// no services at all, leaves d unchanged — there's nothing to derive from.
func (d *createDraft) applyOverrideFieldsFromYAML(content string) {
	var doc composeOverrideDoc
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil || len(doc.Services) == 0 {
		return
	}
	name, svc, ok := selectOverrideService(doc.Services, d.Service)
	if !ok {
		// Multiple services, none matching d.Service — a paste of a whole
		// multi-service stack (the common real case) used to leave every
		// field showing stale data guaranteed to mismatch the content that
		// was actually about to get written (see Validate's own check for
		// that failure mode surfacing as a bare "no such service" from the
		// compose CLI). Derive from the first service in the document's
		// own source order instead: the form is reviewable/correctable
		// before confirming, unlike mergeComposeCreateIntoBase's apply-time
		// use of selectOverrideService below, which stays strict/no-
		// guessing since a wrong guess there writes into the user's real
		// compose file, not just a UI field.
		if first := firstOverrideServiceName(content); first != "" {
			if fsvc, fok := doc.Services[first]; fok {
				name, svc, ok = first, fsvc, true
			}
		}
	}
	if !ok {
		return
	}
	d.Service = name
	d.Image = svc.Image
	if strings.TrimSpace(svc.Restart) != "" {
		d.Restart = svc.Restart
	}
	d.Command = normalizeComposeCommand(svc.Command)
	d.Ports = strings.Join(svc.Ports, ", ")
	d.Mounts = strings.Join(svc.Volumes, ", ")
	d.Env = formatEnvEntries(normalizeComposeEnvironment(svc.Environment))
}

// selectOverrideService picks which parsed service to sync fields from: the
// one named preferredService if present, else the sole service when there's
// exactly one. Multiple services with no name match is ambiguous, so callers
// leave the draft untouched rather than guessing which one the user means —
// mergeComposeCreateIntoBase's apply-time use of this needs that strictness
// (a wrong guess would write another service's fields into the user's real
// compose file); applyOverrideFieldsFromYAML's form-population use adds its
// own best-effort fallback on top instead of loosening this shared helper.
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

// firstOverrideServiceName returns the first key under content's top-level
// "services:" mapping, in the YAML document's own source order — the
// gopkg.in/yaml.v3 unmarshal into composeOverrideDoc's map loses that
// order, so applyOverrideFieldsFromYAML's fallback walks the raw node tree
// instead of the decoded map to get a deterministic, document-order
// answer rather than Go's randomized map iteration.
func firstOverrideServiceName(content string) string {
	names := allOverrideServiceNames(content)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

func singleComposeServiceName(content string) string {
	names := allOverrideServiceNames(content)
	if len(names) != 1 {
		return ""
	}
	return names[0]
}

// allOverrideServiceNames returns every key under content's top-level
// "services:" mapping, in the YAML document's own source order —
// firstOverrideServiceName's generalization, used by the derived "is this
// draft a stack" check (createDraft.IsStack) and the stack confirm-step's
// service list, where every name matters, not just the first. gopkg.in/
// yaml.v3's unmarshal into a Go map loses source order, so this walks the
// raw node tree instead of decoding into composeOverrideDoc.
func allOverrideServiceNames(content string) []string {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(content), &root); err != nil || len(root.Content) == 0 {
		return nil
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value != "services" {
			continue
		}
		services := doc.Content[i+1]
		if services.Kind != yaml.MappingNode {
			return nil
		}
		names := make([]string, 0, len(services.Content)/2)
		for j := 0; j+1 < len(services.Content); j += 2 {
			names = append(names, services.Content[j].Value)
		}
		return names
	}
	return nil
}

// summarizeServiceNames renders a service-name list capped to max entries
// for the stack confirm-step prompt/status text ("web, api, ... +3 more")
// — a document with a dozen services shouldn't grow that single prompt
// line unboundedly (the confirm overlay's own body-height truncation,
// see softOverlayBodyBudget in view.go, handles the YAML preview below
// it separately).
func summarizeServiceNames(names []string, max int) string {
	if len(names) <= max {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:max], ", ") + fmt.Sprintf(" +%d more", len(names)-max)
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

// normalizeComposeCommand reconciles Compose's two allowed command shapes
// — a plain string ("run --flag"), or an exec-form list (["sh", "-c",
// "..."]) — into the single space-joined string the Command field stores
// and splitCommand (strings.Fields) later re-splits on, the same
// convention domain.Container.Command and defaultCloneDraft already use.
func normalizeComposeCommand(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case []interface{}:
		parts := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " ")
	default:
		return ""
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
	composeProgress(spec, "validating "+spec.Service+"…")
	baseContent, err := os.ReadFile(spec.BaseFile)
	if err != nil {
		return friendlyComposeBaseFileError(err, spec)
	}
	if baseComposeDefinesService(baseContent, spec.Service) {
		return mergeComposeCreateIntoBase(ctx, spec, baseContent)
	}
	if err := os.MkdirAll(filepath.Dir(spec.OverrideFile), 0o755); err != nil {
		return err
	}
	composeProgress(spec, "writing compose override for "+spec.Service+"…")
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
	return composeUpService(ctx, spec)
}

// defaultApplyComposeAdopt is defaultApplyComposeCreate's counterpart for a
// draft whose base compose file doesn't exist at all — the "adopt this
// container out of Portainer (or whatever else manages it)" path offered by
// the confirm step when createDraft.BaseFileMissing is set. Unlike
// defaultApplyComposeCreate, there's no existing base to read or merge
// into: this writes spec.Content (the draft's own generated single-service
// definition, already used for the override case) directly as a brand-new
// base file, with no override layered on top, then starts the service from
// it. Same write-temp/validate/rename/up shape as the override-write branch
// above and mergeComposeCreateIntoBase below, just targeting BaseFile
// instead of OverrideFile.
func defaultApplyComposeAdopt(ctx context.Context, spec composeCreateSpec) error {
	if strings.TrimSpace(spec.BaseFile) == "" {
		return errors.New("compose file is required")
	}
	if spec.System.Kind == "ssh" {
		return applyComposeAdoptRemote(ctx, spec)
	}
	composeProgress(spec, "writing compose file for "+spec.Service+"…")
	if err := os.MkdirAll(filepath.Dir(spec.BaseFile), 0o755); err != nil {
		return err
	}
	tempSpec := spec
	tempSpec.BaseFile = spec.BaseFile + ".tmp"
	tempSpec.OverrideFile = ""
	if err := os.WriteFile(tempSpec.BaseFile, []byte(spec.Content), 0o644); err != nil {
		return err
	}
	composeProgress(spec, "validating "+spec.Service+"…")
	if err := composeCommand(ctx, tempSpec, "config"); err != nil {
		_ = os.Remove(tempSpec.BaseFile)
		return err
	}
	if err := os.Rename(tempSpec.BaseFile, spec.BaseFile); err != nil {
		_ = os.Remove(tempSpec.BaseFile)
		return err
	}
	finalSpec := spec
	finalSpec.OverrideFile = ""
	return composeUpService(ctx, finalSpec)
}

// applyComposeAdoptRemote is defaultApplyComposeAdopt's SSH counterpart,
// mirroring mergeComposeCreateIntoBaseRemote's shape (write temp, validate,
// promote) but writing spec.Content fresh instead of a merge result, and
// with no pre-existing base file to have read in the first place.
func applyComposeAdoptRemote(ctx context.Context, spec composeCreateSpec) error {
	composeProgress(spec, "writing compose file for "+spec.Service+"…")
	if _, err := sshRun(ctx, spec.System, "mkdir -p "+systems.ShellQuote(path.Dir(spec.BaseFile)), ""); err != nil {
		return err
	}
	finalSpec := spec
	finalSpec.OverrideFile = ""
	tempBase := spec.BaseFile + ".tmp"
	if _, err := sshRun(ctx, spec.System, "cat > "+systems.ShellQuote(tempBase), spec.Content); err != nil {
		return err
	}
	tempSpec := finalSpec
	tempSpec.BaseFile = tempBase
	composeProgress(spec, "validating "+spec.Service+"…")
	if err := composeCommand(ctx, tempSpec, "config"); err != nil {
		_, _ = sshRun(ctx, spec.System, "rm -f "+systems.ShellQuote(tempBase), "")
		return err
	}
	if _, err := sshRun(ctx, spec.System, "mv "+systems.ShellQuote(tempBase)+" "+systems.ShellQuote(spec.BaseFile), ""); err != nil {
		_, _ = sshRun(ctx, spec.System, "rm -f "+systems.ShellQuote(tempBase), "")
		return err
	}
	return composeUpService(ctx, finalSpec)
}

// defaultApplyComposeStack writes spec.Content as the entire base compose
// file (full replace, not a merge — see createDraft.IsStack's doc comment
// on why a stack draft has no per-service override to layer instead) and
// brings up every service it defines in one `docker compose up -d` call,
// deliberately with no service argument — the literal fix for the bug
// this feature exists for, where every other compose apply function here
// always appends spec.Service and only ever starts one service. A single
// whole-file invocation, not a loop of `up -d <name>` per service: it
// lets `docker compose` resolve `depends_on` ordering across the whole
// file itself, and keeps the same one-invocation-is-one-thing-to-report
// convention every other apply function in this file already follows
// (see runDockerCompose/composeCommand) rather than inventing per-service
// status tracking. Same write-temp/validate/rename/up shape as
// defaultApplyComposeAdopt just above.
func defaultApplyComposeStack(ctx context.Context, spec composeCreateSpec) error {
	if strings.TrimSpace(spec.BaseFile) == "" {
		return errors.New("compose file is required")
	}
	if spec.System.Kind == "ssh" {
		return applyComposeStackRemote(ctx, spec)
	}
	composeProgress(spec, "writing stack "+spec.Project+"…")
	if err := os.MkdirAll(filepath.Dir(spec.BaseFile), 0o755); err != nil {
		return err
	}
	tempSpec := spec
	tempSpec.BaseFile = spec.BaseFile + ".tmp"
	tempSpec.OverrideFile = ""
	if err := os.WriteFile(tempSpec.BaseFile, []byte(spec.Content), 0o644); err != nil {
		return err
	}
	composeProgress(spec, "validating stack "+spec.Project+"…")
	if err := composeCommand(ctx, tempSpec, "config"); err != nil {
		_ = os.Remove(tempSpec.BaseFile)
		return err
	}
	if err := os.Rename(tempSpec.BaseFile, spec.BaseFile); err != nil {
		_ = os.Remove(tempSpec.BaseFile)
		return err
	}
	finalSpec := spec
	finalSpec.OverrideFile = ""
	composeProgress(spec, "starting stack "+spec.Project+"…")
	return composeCommand(ctx, finalSpec, "up", "-d")
}

// applyComposeStackRemote is defaultApplyComposeStack's SSH counterpart,
// mirroring applyComposeAdoptRemote's shape exactly minus the trailing
// service argument on the final up.
func applyComposeStackRemote(ctx context.Context, spec composeCreateSpec) error {
	composeProgress(spec, "writing stack "+spec.Project+"…")
	if _, err := sshRun(ctx, spec.System, "mkdir -p "+systems.ShellQuote(path.Dir(spec.BaseFile)), ""); err != nil {
		return err
	}
	finalSpec := spec
	finalSpec.OverrideFile = ""
	tempBase := spec.BaseFile + ".tmp"
	if _, err := sshRun(ctx, spec.System, "cat > "+systems.ShellQuote(tempBase), spec.Content); err != nil {
		return err
	}
	tempSpec := finalSpec
	tempSpec.BaseFile = tempBase
	composeProgress(spec, "validating stack "+spec.Project+"…")
	if err := composeCommand(ctx, tempSpec, "config"); err != nil {
		_, _ = sshRun(ctx, spec.System, "rm -f "+systems.ShellQuote(tempBase), "")
		return err
	}
	if _, err := sshRun(ctx, spec.System, "mv "+systems.ShellQuote(tempBase)+" "+systems.ShellQuote(spec.BaseFile), ""); err != nil {
		_, _ = sshRun(ctx, spec.System, "rm -f "+systems.ShellQuote(tempBase), "")
		return err
	}
	composeProgress(spec, "starting stack "+spec.Project+"…")
	return composeCommand(ctx, finalSpec, "up", "-d")
}

// mergeComposeCreateIntoBase is defaultApplyComposeCreate's path for a
// service the base compose file already defines: rather than layering a
// generated compose.whatthedock.<service>.yml override on top — WhatTheDock's
// original behavior, still used for brand-new services the base file doesn't
// know about yet — it merges the draft's fields directly into that service's
// existing block in base (comment-preserving, see mergeComposeServiceFields)
// and drops any override left over from before this service existed in
// base, so there's exactly one place the service is defined going forward.
func mergeComposeCreateIntoBase(ctx context.Context, spec composeCreateSpec, baseContent []byte) error {
	if spec.FullBase && singleComposeServiceName(spec.Content) == spec.Service && singleComposeServiceName(string(baseContent)) == spec.Service {
		baseOnly := spec
		baseOnly.OverrideFile = ""
		composeProgress(spec, "writing compose file for "+spec.Service+"…")
		tempBase := spec.BaseFile + ".tmp"
		if err := os.WriteFile(tempBase, []byte(spec.Content), 0o644); err != nil {
			return err
		}
		tempSpec := baseOnly
		tempSpec.BaseFile = tempBase
		composeProgress(spec, "validating "+spec.Service+"…")
		if err := composeCommand(ctx, tempSpec, "config"); err != nil {
			_ = os.Remove(tempBase)
			return err
		}
		if err := os.Rename(tempBase, spec.BaseFile); err != nil {
			_ = os.Remove(tempBase)
			return err
		}
		if strings.TrimSpace(spec.OverrideFile) != "" {
			_ = os.Remove(spec.OverrideFile)
		}
		return composeUpService(ctx, baseOnly)
	}
	fields, ok := composeServiceFieldsFromContent(spec.Content, spec.Service)
	if !ok {
		return fmt.Errorf("could not read fields for service %q", spec.Service)
	}
	merged, err := mergeComposeServiceFields(baseContent, spec.Service, fields)
	if err != nil {
		return err
	}
	baseOnly := spec
	baseOnly.OverrideFile = ""
	composeProgress(spec, "writing compose file for "+spec.Service+"…")
	tempBase := spec.BaseFile + ".tmp"
	if err := os.WriteFile(tempBase, merged, 0o644); err != nil {
		return err
	}
	tempSpec := baseOnly
	tempSpec.BaseFile = tempBase
	composeProgress(spec, "validating "+spec.Service+"…")
	if err := composeCommand(ctx, tempSpec, "config"); err != nil {
		_ = os.Remove(tempBase)
		return err
	}
	if err := os.Rename(tempBase, spec.BaseFile); err != nil {
		_ = os.Remove(tempBase)
		return err
	}
	if strings.TrimSpace(spec.OverrideFile) != "" {
		_ = os.Remove(spec.OverrideFile)
	}
	return composeUpService(ctx, baseOnly)
}

// applyComposeCreateRemote is defaultApplyComposeCreate's SSH-system
// counterpart: every filesystem operation runs on the remote host over the
// same ssh convention as the Docker socket tunnel (see sshRun), writing and
// validating a temp override before promoting it, exactly like the local
// path — just with `test`/`mkdir`/`cat >`/`mv` in place of the os package.
func applyComposeCreateRemote(ctx context.Context, spec composeCreateSpec) error {
	composeProgress(spec, "validating "+spec.Service+"…")
	if _, err := sshRun(ctx, spec.System, "test -f "+systems.ShellQuote(spec.BaseFile), ""); err != nil {
		return fmt.Errorf("compose file %s not found on %s (deployed via Portainer or another tool that manages it elsewhere?): %w", spec.BaseFile, spec.System.Name, err)
	}
	baseContent, err := sshRun(ctx, spec.System, "cat "+systems.ShellQuote(spec.BaseFile), "")
	if err != nil {
		return err
	}
	if baseComposeDefinesService(baseContent, spec.Service) {
		return mergeComposeCreateIntoBaseRemote(ctx, spec, baseContent)
	}
	if _, err := sshRun(ctx, spec.System, "mkdir -p "+systems.ShellQuote(path.Dir(spec.OverrideFile)), ""); err != nil {
		return err
	}
	composeProgress(spec, "writing compose override for "+spec.Service+"…")
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
	return composeUpService(ctx, spec)
}

// mergeComposeCreateIntoBaseRemote is mergeComposeCreateIntoBase's SSH
// counterpart — every filesystem step runs remotely over sshRun instead of
// the os package.
func mergeComposeCreateIntoBaseRemote(ctx context.Context, spec composeCreateSpec, baseContent []byte) error {
	if spec.FullBase && singleComposeServiceName(spec.Content) == spec.Service && singleComposeServiceName(string(baseContent)) == spec.Service {
		baseOnly := spec
		baseOnly.OverrideFile = ""
		composeProgress(spec, "writing compose file for "+spec.Service+"…")
		tempBase := spec.BaseFile + ".tmp"
		if _, err := sshRun(ctx, spec.System, "cat > "+systems.ShellQuote(tempBase), spec.Content); err != nil {
			return err
		}
		tempSpec := baseOnly
		tempSpec.BaseFile = tempBase
		composeProgress(spec, "validating "+spec.Service+"…")
		if err := composeCommand(ctx, tempSpec, "config"); err != nil {
			_, _ = sshRun(ctx, spec.System, "rm -f "+systems.ShellQuote(tempBase), "")
			return err
		}
		if _, err := sshRun(ctx, spec.System, "mv "+systems.ShellQuote(tempBase)+" "+systems.ShellQuote(spec.BaseFile), ""); err != nil {
			_, _ = sshRun(ctx, spec.System, "rm -f "+systems.ShellQuote(tempBase), "")
			return err
		}
		if strings.TrimSpace(spec.OverrideFile) != "" {
			_, _ = sshRun(ctx, spec.System, "rm -f "+systems.ShellQuote(spec.OverrideFile), "")
		}
		return composeUpService(ctx, baseOnly)
	}
	fields, ok := composeServiceFieldsFromContent(spec.Content, spec.Service)
	if !ok {
		return fmt.Errorf("could not read fields for service %q", spec.Service)
	}
	merged, err := mergeComposeServiceFields(baseContent, spec.Service, fields)
	if err != nil {
		return err
	}
	baseOnly := spec
	baseOnly.OverrideFile = ""
	composeProgress(spec, "writing compose file for "+spec.Service+"…")
	tempBase := spec.BaseFile + ".tmp"
	if _, err := sshRun(ctx, spec.System, "cat > "+systems.ShellQuote(tempBase), string(merged)); err != nil {
		return err
	}
	tempSpec := baseOnly
	tempSpec.BaseFile = tempBase
	composeProgress(spec, "validating "+spec.Service+"…")
	if err := composeCommand(ctx, tempSpec, "config"); err != nil {
		_, _ = sshRun(ctx, spec.System, "rm -f "+systems.ShellQuote(tempBase), "")
		return err
	}
	if _, err := sshRun(ctx, spec.System, "mv "+systems.ShellQuote(tempBase)+" "+systems.ShellQuote(spec.BaseFile), ""); err != nil {
		_, _ = sshRun(ctx, spec.System, "rm -f "+systems.ShellQuote(tempBase), "")
		return err
	}
	if strings.TrimSpace(spec.OverrideFile) != "" {
		_, _ = sshRun(ctx, spec.System, "rm -f "+systems.ShellQuote(spec.OverrideFile), "")
	}
	return composeUpService(ctx, baseOnly)
}

func composeUpService(ctx context.Context, spec composeCreateSpec) error {
	if spec.PullBeforeUp {
		composeProgress(spec, "pulling "+spec.Service+"…")
		if err := composeCommand(ctx, spec, "pull", spec.Service); err != nil {
			return err
		}
	}
	composeProgress(spec, "starting "+spec.Service+"…")
	return composeCommand(ctx, spec, "up", "-d", spec.Service)
}

func composeProgress(spec composeCreateSpec, line string) {
	if spec.Progress != nil {
		spec.Progress(line)
	}
}

// composeSpecForSelected builds a composeCreateSpec targeting selected's
// actual Compose service, for Delete/Replicate — which act on what's
// already running rather than a freshly-filled-in draft. Content is left
// empty: Delete never writes override content, and Replicate's pull/up
// calls only read Project/BaseFile/OverrideFile/System, never Content.
// Reuses createDraft.ComposeSpec for path derivation and validation instead
// of duplicating it.
func composeSpecForSelected(selected *domain.Container, system config.System) (composeCreateSpec, error) {
	draft := createDraft{
		Mode:    createModeCompose,
		Project: selected.Compose.Project,
		Service: selected.Compose.Service,
		Image:   selected.Image,
	}
	if files := splitComposeConfigFiles(selected.Compose.ConfigFiles); len(files) > 0 {
		draft.ComposeFile = files[0]
	}
	return draft.ComposeSpec(system)
}

// withExistingOverrideOnly clears spec.OverrideFile if no WhatTheDock
// override actually exists on disk, so a plain, never-customized Compose
// service can still be replicated or deleted without docker compose failing
// on a missing -f target. Delete and Replicate both need this since,
// unlike Create, neither is ever about to write one.
func withExistingOverrideOnly(ctx context.Context, spec composeCreateSpec) composeCreateSpec {
	if spec.System.Kind == "ssh" {
		if _, err := sshRun(ctx, spec.System, "test -f "+systems.ShellQuote(spec.OverrideFile), ""); err != nil {
			spec.OverrideFile = ""
		}
		return spec
	}
	if _, err := os.Stat(spec.OverrideFile); err != nil {
		spec.OverrideFile = ""
	}
	return spec
}

// defaultApplyComposeDelete permanently removes service: it stops and
// removes its container (docker compose rm -sf), then deletes its
// definition everywhere WhatTheDock knows about it — the generated
// override, if any, and the service's own block in the base compose file if
// the base file defines it (comment-preserving, see removeComposeService).
// This is a real deletion, matching what "Delete" means for a stack service
// in tools like Portainer, not the override-removal-and-reconcile-to-base
// behavior earlier versions of Delete used, which left the container
// running under its base definition instead of actually removing it.
func defaultApplyComposeDelete(ctx context.Context, spec composeCreateSpec) error {
	if spec.System.Kind == "ssh" {
		return applyComposeDeleteRemote(ctx, spec)
	}
	spec = withExistingOverrideOnly(ctx, spec)
	if err := composeCommand(ctx, spec, "rm", "-sf", spec.Service); err != nil {
		return err
	}
	if strings.TrimSpace(spec.OverrideFile) != "" {
		if err := os.Remove(spec.OverrideFile); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	baseContent, err := os.ReadFile(spec.BaseFile)
	if err != nil || !baseComposeDefinesService(baseContent, spec.Service) {
		return nil
	}
	updated, err := removeComposeService(baseContent, spec.Service)
	if err != nil {
		return err
	}
	return os.WriteFile(spec.BaseFile, updated, 0o644)
}

// applyComposeDeleteRemote is defaultApplyComposeDelete's SSH counterpart.
func applyComposeDeleteRemote(ctx context.Context, spec composeCreateSpec) error {
	spec = withExistingOverrideOnly(ctx, spec)
	if err := composeCommand(ctx, spec, "rm", "-sf", spec.Service); err != nil {
		return err
	}
	if strings.TrimSpace(spec.OverrideFile) != "" {
		if _, err := sshRun(ctx, spec.System, "rm -f "+systems.ShellQuote(spec.OverrideFile), ""); err != nil {
			return err
		}
	}
	baseContent, err := sshRun(ctx, spec.System, "cat "+systems.ShellQuote(spec.BaseFile), "")
	if err != nil || !baseComposeDefinesService(baseContent, spec.Service) {
		return nil
	}
	updated, err := removeComposeService(baseContent, spec.Service)
	if err != nil {
		return err
	}
	_, err = sshRun(ctx, spec.System, "cat > "+systems.ShellQuote(spec.BaseFile), string(updated))
	return err
}

// defaultApplyComposeDeleteStack permanently removes an entire project:
// stops and removes every container spec.BaseFile defines (`docker
// compose down` — deliberately no `-v`, named volumes are left alone,
// matching `down`'s own default), removes any now-orphaned per-service
// override file, then deletes the base file itself. The whole-stack
// counterpart to defaultApplyComposeDelete's per-service symmetry: that
// one doesn't just stop a container, it removes the service's own
// definition (its override, or its block in the base file) — the
// project-wide equivalent of "the service's definition" is the base file
// itself, so it goes too, not just the containers it was running.
func defaultApplyComposeDeleteStack(ctx context.Context, spec composeCreateSpec) error {
	if spec.System.Kind == "ssh" {
		return applyComposeDeleteStackRemote(ctx, spec)
	}
	baseContent, err := os.ReadFile(spec.BaseFile)
	if err != nil {
		return err
	}
	if err := composeCommand(ctx, spec, "down"); err != nil {
		return err
	}
	for _, service := range allOverrideServiceNames(string(baseContent)) {
		overridePath := filepath.Join(filepath.Dir(spec.BaseFile), "compose.whatthedock."+safeComposeFilename(service)+".yml")
		if err := os.Remove(overridePath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return os.Remove(spec.BaseFile)
}

// applyComposeDeleteStackRemote is defaultApplyComposeDeleteStack's SSH
// counterpart.
func applyComposeDeleteStackRemote(ctx context.Context, spec composeCreateSpec) error {
	baseContent, err := sshRun(ctx, spec.System, "cat "+systems.ShellQuote(spec.BaseFile), "")
	if err != nil {
		return err
	}
	if err := composeCommand(ctx, spec, "down"); err != nil {
		return err
	}
	for _, service := range allOverrideServiceNames(string(baseContent)) {
		overridePath := path.Join(path.Dir(spec.BaseFile), "compose.whatthedock."+safeComposeFilename(service)+".yml")
		if _, err := sshRun(ctx, spec.System, "rm -f "+systems.ShellQuote(overridePath), ""); err != nil {
			return err
		}
	}
	_, err = sshRun(ctx, spec.System, "rm -f "+systems.ShellQuote(spec.BaseFile), "")
	return err
}

// defaultApplyComposeReplicate pulls a fresh copy of the image and
// recreates the service in place — docker compose's own "up -d" already
// recreates a service when its image changed, so no separate remove step
// is needed here (unlike the standalone-container path).
func defaultApplyComposeReplicate(ctx context.Context, spec composeCreateSpec) error {
	spec = withExistingOverrideOnly(ctx, spec)
	if err := composeCommand(ctx, spec, "pull", spec.Service); err != nil {
		return err
	}
	return composeCommand(ctx, spec, "up", "-d", spec.Service)
}

func runDockerCompose(ctx context.Context, spec composeCreateSpec, args ...string) error {
	baseArgs := []string{"compose"}
	if strings.TrimSpace(spec.Project) != "" {
		baseArgs = append(baseArgs, "-p", spec.Project)
	}
	baseArgs = append(baseArgs, "-f", spec.BaseFile)
	if strings.TrimSpace(spec.OverrideFile) != "" {
		baseArgs = append(baseArgs, "-f", spec.OverrideFile)
	}
	baseArgs = append(baseArgs, args...)
	if spec.System.Kind == "ssh" {
		quoted := make([]string, len(baseArgs))
		for i, a := range baseArgs {
			quoted[i] = systems.ShellQuote(a)
		}
		_, err := sshRun(ctx, spec.System, "docker "+strings.Join(quoted, " "), "")
		return friendlyComposeBaseFileError(err, spec)
	}
	cmd := exec.CommandContext(ctx, "docker", baseArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		if text == "" {
			text = err.Error()
		}
		return friendlyComposeBaseFileError(errors.New(text), spec)
	}
	return nil
}

// friendlyComposeBaseFileError catches the one error every Compose action
// (Create/Edit, Delete, Replicate; local or SSH) can hit before it even gets
// to do anything: spec.BaseFile — the path recorded on the container's own
// com.docker.compose.project.config_files label — doesn't actually exist on
// disk. That happens for stacks deployed by a tool (Portainer is the common
// case) that manages its compose files internally rather than leaving them
// at that path on the host, so `docker compose -f <path>` fails immediately
// with a raw "open <path>: no such file or directory" that gives no hint
// why. This is the single choke point every compose invocation runs
// through, so it's the one place that needs to catch it and say so plainly
// instead of leaving the raw error to be puzzled over — or, worse, missed
// in the status bar, leaving whatever the action was trying to do looking
// like it silently did nothing.
func friendlyComposeBaseFileError(err error, spec composeCreateSpec) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	if strings.Contains(text, spec.BaseFile) && strings.Contains(text, "no such file or directory") {
		host := "the local filesystem"
		if spec.System.Kind == "ssh" {
			host = spec.System.Name
		}
		return fmt.Errorf("compose file %s not found on %s (deployed via Portainer or another tool that manages it elsewhere?): %s", spec.BaseFile, host, text)
	}
	return err
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
	parts := splitEnvEntries(value)
	for _, part := range parts {
		key, _, ok := strings.Cut(part, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("env %q must be KEY=value", part)
		}
	}
	return parts, nil
}

func splitCommand(value string) []string {
	return domain.SplitShellWords(value)
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
		m.syncCreateFieldEditor()
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
	m.syncCreateFieldEditor()
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
		fields := []createField{createFieldMode, createFieldContainerName, createFieldImage}
		fields = append(fields, createFieldImageAction)
		fields = append(fields, createFieldCommand, createFieldPorts, createFieldMounts, createFieldEnv, createFieldRestart)
		if m.createDraft.Pasting {
			fields = append(fields, createFieldNetworks)
		}
		return fields
	}
	if m.createDraft.IsStack() {
		// No single Service/Image/Ports/etc. to edit — the pasted/loaded
		// document itself is the content (see createDraft.IsStack, Preview).
		return []createField{createFieldMode, createFieldProject, createFieldComposeFile}
	}
	fields := []createField{createFieldMode, createFieldProject, createFieldService, createFieldImage}
	fields = append(fields, createFieldImageAction)
	return append(fields, createFieldPorts, createFieldMounts, createFieldEnv, createFieldRestart, createFieldComposeFile)
}

func (m Model) isCreateChoiceField() bool {
	return m.createField == createFieldMode || m.createField == createFieldRestart || m.createField == createFieldImageAction
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
	case createFieldImageAction:
		options := []string{imageActionKeep, imageActionPull}
		current := 0
		for i, option := range options {
			if m.createDraft.ImageAction == option {
				current = i
				break
			}
		}
		m.createDraft.ImageAction = options[modIndex(current+direction, len(options))]
	}
	m.clearCreateNotice()
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
	m.clearCreateNotice()
	m.revalidateCreateField()
}

// revalidateCreateField resets m.createField to the first visible field
// if the current one has dropped out of visibleCreateFields() — a mode
// switch is the obvious case, but it also happens within Compose mode
// alone: saving a paste that turns a single-service draft into a stack
// (createDraft.IsStack) drops Service/Image/Ports/etc. from the visible
// list, and the reverse happens on the way back down to one service.
// Without this, a field that's no longer shown stays "selected" with
// nothing highlighted in the UI, and further keystrokes silently edit
// that hidden field instead of doing anything visible — call this
// anywhere the visible field list can change out from under the current
// selection, not just on an explicit mode cycle.
func (m *Model) revalidateCreateField() {
	fields := m.visibleCreateFields()
	for _, field := range fields {
		if field == m.createField {
			m.syncCreateFieldEditor()
			return
		}
	}
	if len(fields) > 0 {
		m.createField = fields[0]
	}
	m.syncCreateFieldEditor()
}

// syncCreateFieldEditor (re)seeds createFieldEditor to match whichever
// field is now focused (m.createField), with the cursor parked at the end
// of that field's current value (SetValue does this on its own — see
// ripple's own doc comment on it) — call this at every point the focused
// field or its value changes out from under the editor: this used to be
// every "m.createCursor = len([]rune(m.createFieldValue()))" site before
// createCursor existed as a plain int. A no-op for a choice field (Mode/
// Restart/ImageAction), which never uses this editor — those are cycled,
// never typed into.
func (m *Model) syncCreateFieldEditor() {
	if m.isCreateChoiceField() {
		return
	}
	ed := ripple.New()
	ed.SetClipboard(editorClipboard)
	if editorVimMode {
		ed.SetInputMode(ripple.ModeVim)
	}
	ed.SetValue(m.createFieldValue())
	if ed.InputMode() == ripple.ModeVim {
		// Matches openCreateEditor's own reasoning for the Compose YAML
		// editor: land in Insert so typing works immediately on focusing a
		// field, instead of the first keystroke (and first Esc) landing in
		// Normal mode and doing nothing visible.
		ed.StartInsert()
	}
	ed.Focus()
	m.createFieldEditor = ed
}

// forwardToFieldEditor routes msg into the embedded field editor and syncs
// its value back into createDraft's own plain string field (still the
// single source of truth Preview()/ContainerSpec()/etc. all read from) —
// the shared path every text-editing key in handleCreateKey uses instead
// of hand-rolling insert/delete/cursor movement. Ripple's own key handling
// already covers everything that needs: character insert, backspace/
// delete, left/right and shift+left/right selection, undo/redo, and OS
// clipboard copy/cut/paste (the same OSC52 bridge every other editor in
// the app already uses) — there's nothing left for a hand-rolled version
// to do. Returns the cmd Ripple's Update produced (a clipboard write, most
// commonly) so the caller can actually return it — swallowing it here
// would silently break copy. A no-op (nil cmd) on a choice field.
func (m *Model) forwardToFieldEditor(msg tea.KeyMsg) tea.Cmd {
	if m.isCreateChoiceField() {
		return nil
	}
	var cmd tea.Cmd
	m.createFieldEditor, cmd = m.createFieldEditor.Update(msg)
	m.clearCreateNotice()
	m.setCreateFieldValue(m.createFieldEditor.Value())
	return cmd
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
	case createFieldImageAction:
		return emptyAs(m.createDraft.ImageAction, imageActionKeep)
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
	case createFieldNetworks:
		return m.createDraft.Networks
	default:
		return ""
	}
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
	case createFieldImageAction:
		m.createDraft.ImageAction = value
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
	case createFieldNetworks:
		m.createDraft.Networks = value
	}
}

func (mode createMode) String() string {
	if mode == createModeStandalone {
		return "standalone container"
	}
	return "compose service"
}

// IsStack is a derived property, not stored state: a Compose draft is "a
// stack" purely because its current content happens to define more than
// one service — never because the user picked a separate mode. Trimming
// content back down to one service and re-saving reverts this to false
// for free, since nothing was ever set to begin with. Only meaningful for
// createModeCompose; standalone drafts have no override content at all.
func (d createDraft) IsStack() bool {
	return d.Mode == createModeCompose && d.OverrideRawSet && len(allOverrideServiceNames(d.OverrideRaw)) > 1
}

func (d createDraft) TargetName() string {
	if d.Mode == createModeStandalone {
		return emptyAs(d.ContainerName, "new-container")
	}
	if d.IsStack() {
		return emptyAs(d.Project, "stack")
	}
	return emptyAs(d.Service, "new-service")
}

func createFieldLabel(field createField) string {
	switch field {
	case createFieldMode:
		return "Mode"
	case createFieldProject:
		return "Stack"
	case createFieldService:
		return "Service"
	case createFieldContainerName:
		return "Name"
	case createFieldImage:
		return "Image"
	case createFieldImageAction:
		return "Image action"
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
	case createFieldNetworks:
		return "Networks"
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
	lines = appendPreviewList(lines, "    environment:", splitEnvEntries(d.Env), "      - ")
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
	for _, env := range splitEnvEntries(d.Env) {
		args = append(args, "-e "+env)
	}
	for _, network := range splitDraftList(d.Networks) {
		args = append(args, "--network "+network)
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

// splitEnvEntries is splitDraftList for the Env field specifically: same
// comma/newline-separated, trimmed shape, except a double-quoted entry is
// taken literally end to end — a comma or newline inside the quotes
// doesn't split it, and a doubled "" inside represents one literal ".
// formatEnvEntries is this function's inverse, applying that quoting
// whenever an entry needs it.
//
// Ports/Mounts/Networks stay on plain splitDraftList — their values don't
// realistically contain a literal comma. Env values regularly do (JSON,
// CSV lists, connection strings), and this field is the one place a
// yanked/cloned container's real value has to survive an actual
// human-editable, comma-joined text field: without quoting,
// APP_OPTS=a,b,c used to come back as ["APP_OPTS=a","b","c"] — "b" and
// "c" then fail "must be KEY=value" validation (or, worse, silently
// become their own bogus env vars if either fragment happens to contain
// its own "=").
func splitEnvEntries(value string) []string {
	runes := []rune(value)
	n := len(runes)
	var out []string
	i := 0
	for i < n {
		for i < n && (runes[i] == ',' || runes[i] == '\n' || unicode.IsSpace(runes[i])) {
			i++
		}
		if i >= n {
			break
		}
		var entry []rune
		quoted := false
		if runes[i] == '"' {
			quoted = true
			i++
			for i < n {
				if runes[i] == '"' {
					if i+1 < n && runes[i+1] == '"' {
						entry = append(entry, '"')
						i += 2
						continue
					}
					i++ // consume the closing quote
					break
				}
				entry = append(entry, runes[i])
				i++
			}
			// Ignore anything between the closing quote and the next
			// separator instead of erroring — malformed trailing text
			// after a quoted entry is rare enough not to be worth a
			// parse failure over.
			for i < n && runes[i] != ',' && runes[i] != '\n' {
				i++
			}
		} else {
			start := i
			for i < n && runes[i] != ',' && runes[i] != '\n' {
				i++
			}
			entry = runes[start:i]
		}
		value := string(entry)
		if !quoted {
			value = strings.TrimSpace(value)
		}
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

// formatEnvEntries joins entries (each a whole "KEY=VALUE" string) into
// the same comma-separated text splitEnvEntries parses, quoting
// (CSV-style, doubling any embedded ") whichever entries need it — see
// splitEnvEntries' own doc comment for why.
func formatEnvEntries(entries []string) string {
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.ContainsAny(e, ",\n\"") {
			e = `"` + strings.ReplaceAll(e, `"`, `""`) + `"`
		}
		parts = append(parts, e)
	}
	return strings.Join(parts, ", ")
}

func emptyAs(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
