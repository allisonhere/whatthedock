package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/clipboard"
	"github.com/allisonhere/whatthedock/internal/config"
	"github.com/allisonhere/whatthedock/internal/domain"
	"github.com/allisonhere/whatthedock/internal/systems"
)

// yankDoneMsg carries the result of yankSelectedCmd back into Update.
type yankDoneMsg struct {
	pc  clipboard.PortableContainer
	err error
}

// yankSelectedCmd is "y": captures id's full configuration (a real Docker
// inspect — provider.Container already does this, see docker.FromInspect)
// into the Container Clipboard's portable model. Read-only: never touches
// the source container.
func (m Model) yankSelectedCmd(id domain.ResourceID) tea.Cmd {
	provider := m.provider
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		ctr, err := provider.Container(ctx, id)
		if err != nil {
			return yankDoneMsg{err: err}
		}
		return yankDoneMsg{pc: clipboard.FromContainer(ctr, provider.Host())}
	}
}

// pastePlanMsg carries the result of preparePastePlanCmd back into Update.
type pastePlanMsg struct {
	plan clipboard.PastePlan
	err  error
}

// preparePastePlanCmd is "P": runs clipboard.Plan against the *current*
// provider (whatever system the user has switched to since yanking) and
// then folds in the one check Plan itself can't do — whether each bind
// mount's source path exists on this host — using the same SSH convention
// create.go's checkRemoteOverrideCmd already established for a remote
// system. Never creates anything; opens overlayPaste once the result
// lands (see the pastePlanMsg case in Update).
func (m Model) preparePastePlanCmd() tea.Cmd {
	current, ok := m.clipboard.Current()
	if !ok {
		return func() tea.Msg {
			return pastePlanMsg{err: errors.New("clipboard is empty — yank a container with y first")}
		}
	}
	provider := m.provider
	system := m.activeSystemConfig()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		plan, err := clipboard.Plan(ctx, provider, current)
		if err != nil {
			return pastePlanMsg{err: err}
		}
		appendBindPathConflicts(ctx, &plan, system)
		return pastePlanMsg{plan: plan}
	}
}

// appendBindPathConflicts checks each bind mount's source path against
// system (local os.Stat, or an SSH "test -e" for a remote system — the same
// convention checkRemoteOverrideCmd uses in create.go) and appends any
// conflict clipboard.BindPathConflict reports.
func appendBindPathConflicts(ctx context.Context, plan *clipboard.PastePlan, system config.System) {
	for _, mount := range plan.Spec.Mounts {
		if mount.Type != "bind" || strings.TrimSpace(mount.Source) == "" {
			continue
		}
		source := mount.Source
		exists := func(path string) bool { return pathExistsOn(ctx, system, path) }
		if conflict := clipboard.BindPathConflict(clipboard.PortableMount{Type: "bind", Source: source}, exists); conflict != nil {
			plan.Conflicts = append(plan.Conflicts, *conflict)
		}
	}
}

func pathExistsOn(ctx context.Context, system config.System, path string) bool {
	if system.Kind == "ssh" {
		_, err := sshRun(ctx, system, "test -e "+systems.ShellQuote(path), "")
		return err == nil
	}
	_, err := os.Stat(path)
	return err == nil
}

// handlePasteKey handles overlayPaste — the review/conflict screen shown
// after "P". "Enter" opens the ordinary create-form overlay prefilled from
// the plan for editing (Name/Ports/Mounts/Env/Networks — see
// visibleCreateFields' createFieldNetworks); "d" jumps straight to that
// same form's confirm step, skipping manual editing, unless the plan has a
// blocking conflict (a name or port collision) that must be fixed first.
// Both paths end up going through the exact same confirm+progress+
// createDoneMsg machinery every other create/edit does — deploying is
// never a separate, unconfirmed action.
func (m Model) handlePasteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pastePlan == nil {
		m.overlay = overlayNone
		return m, nil
	}
	plan := *m.pastePlan
	switch msg.String() {
	case "esc", "q":
		// The clipboard item itself is untouched — this is a copy, not a
		// cut, so the user can press P again for free.
		m.pastePlan = nil
		m.overlay = overlayNone
		m.status, m.statusErr = "paste cancelled", false
	case "enter":
		m.pastePlan = nil
		m.openCreateOverlayWithDraft(draftFromPastePlan(plan))
		m.status, m.statusErr = "review paste for "+plan.Spec.Name, false
	case "d":
		if plan.Blocked() {
			m.status, m.statusErr = "cannot deploy: fix the blocking conflict(s) first — Enter to review/fix", true
			return m, nil
		}
		m.pastePlan = nil
		m.openCreateOverlayWithDraft(draftFromPastePlan(plan))
		if m.validateCreateDraft() {
			m.createDraft.Confirming = true
		}
	}
	return m, nil
}

// draftFromPastePlan seeds a standalone createDraft from plan's proposed
// spec — the exact same form Create/Clone/Edit already use, so paste needs
// no new field-editing UI. Every field the form doesn't expose (privileged,
// capabilities, devices, resource limits, healthcheck, DNS, security opts,
// stop signal/timeout, log driver...) still rides along on
// draft.PastePlan.Spec: createDraft.ContainerSpec starts from it instead of
// a blank spec whenever draft.Pasting is set, and only overwrites the
// fields below.
func draftFromPastePlan(plan clipboard.PastePlan) createDraft {
	spec := plan.Spec
	imageAction := imageActionKeep
	if plan.NeedsPull {
		imageAction = imageActionPull
	}
	planCopy := plan
	return createDraft{
		Mode:          createModeStandalone,
		ContainerName: spec.Name,
		Image:         spec.Image,
		ImageAction:   imageAction,
		Command:       domain.JoinShellWords(spec.Command),
		Ports:         formatSpecPorts(spec.Ports),
		Mounts:        formatSpecMounts(spec.Mounts),
		Env:           formatEnvEntries(spec.Env),
		Restart:       emptyAs(spec.RestartPolicy, "unless-stopped"),
		Networks:      formatSpecNetworks(spec.Networks),
		Pasting:       true,
		PastePlan:     &planCopy,
	}
}

func formatSpecPorts(ports []app.PortBinding) string {
	parts := make([]string, 0, len(ports))
	for _, p := range ports {
		entry := fmt.Sprintf("%d:%d/%s", p.HostPort, p.ContainerPort, emptyAs(p.Protocol, "tcp"))
		if p.HostIP != "" && p.HostIP != "0.0.0.0" {
			entry = p.HostIP + ":" + entry
		}
		parts = append(parts, entry)
	}
	return strings.Join(parts, ", ")
}

func formatSpecMounts(mounts []app.MountBinding) string {
	parts := make([]string, 0, len(mounts))
	for _, mnt := range mounts {
		entry := mnt.Source + ":" + mnt.Destination
		if mnt.ReadOnly {
			entry += ":ro"
		}
		parts = append(parts, entry)
	}
	return strings.Join(parts, ", ")
}

func formatSpecNetworks(networks []app.NetworkAttachment) string {
	names := make([]string, 0, len(networks))
	for _, n := range networks {
		names = append(names, n.Name)
	}
	return strings.Join(names, ", ")
}

// pasteApplyCmd is the paste confirm step's apply action (wired into
// handleCreateKey's confirm-dispatch switch alongside plain create/edit) —
// it creates any destination networks spec.Networks needs but doesn't
// already have (recomputed fresh, not trusted from the plan — see the
// comment inside) before delegating into the exact same pullThenCreate
// sequence a plain create uses. If the container create itself then
// fails, any network this call just created is removed again — the same
// create-then-disarm-cleanup-on-failure idiom editContainerCmd uses, so a
// failed paste never leaves an orphaned network behind, and never touches
// a network that already existed before this paste. The source container
// is never read from or written to here at all.
func (m Model) pasteApplyCmd(spec app.ContainerCreateSpec, pullFirst bool, progress chan string) tea.Cmd {
	provider := m.provider
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		// Recomputed fresh against spec.Networks — the plan's own
		// NetworksToCreate was a snapshot taken when the review screen
		// first opened, and goes stale the moment the user edits which
		// networks to attach to (Enter → the create form's Networks
		// field): a renamed/added network that doesn't exist yet would
		// otherwise never get created, and CreateContainer would fail
		// with a raw "network not found" instead of it just working.
		var networksToCreate []string
		if len(spec.Networks) > 0 {
			existing, err := provider.Networks(ctx)
			if err != nil {
				return createDoneMsg{name: spec.Name, pasted: true, err: fmt.Errorf("reading destination networks: %w", err)}
			}
			networksToCreate = clipboard.NetworksToCreate(existing, spec.Networks)
		}
		var created []string
		cleanup := true
		defer func() {
			if cleanup {
				for _, name := range created {
					_ = provider.RemoveNetwork(context.Background(), name)
				}
			}
		}()
		for _, name := range networksToCreate {
			sendActionProgress(progress, "creating network "+name+"…")
			if err := provider.CreateNetwork(ctx, name); err != nil {
				return createDoneMsg{name: spec.Name, pasted: true, err: fmt.Errorf("creating network %s: %w", name, err)}
			}
			created = append(created, name)
		}
		id, err := pullThenCreate(ctx, provider, spec, pullFirst, progress)
		if err != nil {
			return createDoneMsg{name: spec.Name, pasted: true, err: err}
		}
		cleanup = false
		return createDoneMsg{name: spec.Name, id: id, pasted: true}
	}
}
