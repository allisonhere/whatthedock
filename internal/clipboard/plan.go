package clipboard

import (
	"context"
	"fmt"
	"strings"

	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/domain"
)

// ConflictSeverity classifies a PasteConflict for the review screen: "ok"
// renders a checkmark, "warn" an informational "!" the user can still
// deploy past, "block" an "!" that Deploy should refuse to proceed past
// without a fix (see PastePlan.Blocked).
type ConflictSeverity string

const (
	SeverityOK    ConflictSeverity = "ok"
	SeverityWarn  ConflictSeverity = "warn"
	SeverityBlock ConflictSeverity = "block"
)

// PasteConflict is one line of the paste review screen.
type PasteConflict struct {
	Kind     string // "name", "port", "image", "network", "volume", "bind-path", "privileged", "secret-env"
	Message  string
	Detail   string
	Severity ConflictSeverity
}

// PastePlan is the result of checking a PortableContainer against a
// specific destination host — everything the review screen shows, and
// everything the eventual apply step needs beyond the plain create spec.
type PastePlan struct {
	Source           PortableContainer
	TargetHost       domain.Host
	Spec             app.ContainerCreateSpec
	Conflicts        []PasteConflict
	NeedsPull        bool
	NetworksToCreate []string
}

// Blocked reports whether any conflict is severe enough that Deploy must
// not proceed without the user fixing it first (a name collision or a
// port already in use — Docker would just reject the create either way,
// this only predicts that same failure earlier and more clearly).
func (p PastePlan) Blocked() bool {
	for _, c := range p.Conflicts {
		if c.Severity == SeverityBlock {
			return true
		}
	}
	return false
}

// Plan checks pc against target and returns a PastePlan describing what
// would happen. It never creates, modifies, or removes anything — Plan is
// pure with respect to target beyond the read-only calls already on
// app.Provider (Snapshot/Images/Networks/Volumes).
//
// What Plan deliberately does NOT check (documented limitations, not
// silently dropped): a bind mount's source path existing on the
// destination host (no bare Provider can run an arbitrary shell command —
// internal/ui's orchestration appends that separately via BindPathConflict,
// using the same SSH convention create.go already has for remote path
// checks), and platform/architecture mismatch (no existing Provider
// capability exposes it).
func Plan(ctx context.Context, target app.Provider, pc PortableContainer) (PastePlan, error) {
	spec := pc.ToCreateSpec()
	plan := PastePlan{Source: pc, TargetHost: target.Host(), Spec: spec}

	snapshot, err := target.Snapshot(ctx)
	if err != nil {
		return PastePlan{}, fmt.Errorf("reading destination containers: %w", err)
	}
	existingNames := map[string]bool{}
	usedPorts := map[string]bool{}
	for _, ctr := range allContainers(snapshot) {
		existingNames[ctr.DisplayName()] = true
		for _, p := range ctr.Ports {
			if p.Public != 0 {
				usedPorts[portKey(p.Public, p.Type)] = true
			}
		}
	}

	if existingNames[spec.Name] {
		plan.Conflicts = append(plan.Conflicts, PasteConflict{
			Kind: "name", Severity: SeverityBlock, Detail: spec.Name,
			Message: fmt.Sprintf("a container named %q already exists on %s", spec.Name, target.Host().Name),
		})
	} else {
		plan.Conflicts = append(plan.Conflicts, PasteConflict{Kind: "name", Severity: SeverityOK, Message: "name available"})
	}

	blockedPort := false
	for _, p := range spec.Ports {
		if usedPorts[portKey(p.HostPort, p.Protocol)] {
			blockedPort = true
			plan.Conflicts = append(plan.Conflicts, PasteConflict{
				Kind: "port", Severity: SeverityBlock, Detail: portKey(p.HostPort, p.Protocol),
				Message: fmt.Sprintf("port %d already in use", p.HostPort),
			})
		}
	}
	if !blockedPort && len(spec.Ports) > 0 {
		plan.Conflicts = append(plan.Conflicts, PasteConflict{Kind: "port", Severity: SeverityOK, Message: "ports available"})
	}

	images, err := target.Images(ctx)
	if err != nil {
		return PastePlan{}, fmt.Errorf("reading destination images: %w", err)
	}
	if pc.Image != "" {
		if imageAvailable(images, pc.Image) {
			plan.Conflicts = append(plan.Conflicts, PasteConflict{Kind: "image", Severity: SeverityOK, Message: "image available"})
		} else {
			plan.NeedsPull = true
			plan.Conflicts = append(plan.Conflicts, PasteConflict{
				Kind: "image", Severity: SeverityWarn, Detail: pc.Image,
				Message: fmt.Sprintf("image %s not present — will pull", pc.Image),
			})
		}
	}

	networks, err := target.Networks(ctx)
	if err != nil {
		return PastePlan{}, fmt.Errorf("reading destination networks: %w", err)
	}
	plan.NetworksToCreate = NetworksToCreate(networks, spec.Networks)
	switch {
	case len(plan.NetworksToCreate) > 0:
		plan.Conflicts = append(plan.Conflicts, PasteConflict{
			Kind: "network", Severity: SeverityWarn,
			Message: fmt.Sprintf("network(s) %s will be created", strings.Join(plan.NetworksToCreate, ", ")),
		})
	case len(spec.Networks) > 0:
		plan.Conflicts = append(plan.Conflicts, PasteConflict{Kind: "network", Severity: SeverityOK, Message: "network available"})
	}

	// Named volumes: Docker auto-creates a volume referenced by name if it
	// doesn't exist yet, so a missing one is informational, never blocking.
	if volumes, err := target.Volumes(ctx); err == nil {
		existingVolumes := map[string]bool{}
		for _, v := range volumes {
			existingVolumes[v.Name] = true
		}
		var missing []string
		for _, m := range spec.Mounts {
			if m.Type == "volume" && !existingVolumes[m.Source] {
				missing = append(missing, m.Source)
			}
		}
		if len(missing) > 0 {
			plan.Conflicts = append(plan.Conflicts, PasteConflict{
				Kind: "volume", Severity: SeverityOK,
				Message: fmt.Sprintf("named volume(s) %s will be created automatically", strings.Join(missing, ", ")),
			})
		}
	}

	if pc.Privileged || len(pc.CapAdd) > 0 || len(pc.CapDrop) > 0 || len(pc.Devices) > 0 {
		plan.Conflicts = append(plan.Conflicts, PasteConflict{
			Kind: "privileged", Severity: SeverityWarn,
			Message: "privileged/capabilities/device access requested — verify the destination host allows this",
		})
	}

	if n := pc.SecretEnvCount(); n > 0 {
		plan.Conflicts = append(plan.Conflicts, PasteConflict{
			Kind: "secret-env", Severity: SeverityWarn,
			Message: fmt.Sprintf("%d secret-like env var(s)", n),
		})
	}

	return plan, nil
}

// BindPathConflict checks one bind mount's source path on the destination
// host, via an injected exists predicate — kept provider-agnostic (and so
// unit-testable without touching a real filesystem or SSH session) because
// a bare app.Provider has no "run a shell command on this host" capability;
// internal/ui supplies the real predicate (local os.Stat, or an SSH "test
// -e" for a remote system) and appends the result to the same
// PastePlan.Conflicts slice Plan produced. Returns nil for anything other
// than a non-empty bind mount, or when the path exists.
//
// Severity is Block, not Warn: unlike a missing named volume (which Docker
// auto-creates), a missing bind-mount source is a guaranteed create-time
// failure — confirmed live ("Error response from daemon: invalid mount
// config for type \"bind\": bind source path does not exist: ...") when
// this was still Warn and a paste past it failed exactly this way. Letting
// the user "d" past a conflict that's certain to fail Docker's own create
// call isn't a real choice worth offering.
func BindPathConflict(m PortableMount, exists func(string) bool) *PasteConflict {
	if m.Type != "bind" || strings.TrimSpace(m.Source) == "" {
		return nil
	}
	if exists(m.Source) {
		return nil
	}
	return &PasteConflict{Kind: "bind-path", Severity: SeverityBlock, Detail: m.Source, Message: m.Source + " does not exist"}
}

func allContainers(snapshot domain.Snapshot) []domain.Container {
	out := append([]domain.Container(nil), snapshot.Standalone...)
	for _, project := range snapshot.Projects {
		for _, service := range project.Services {
			out = append(out, service.Containers...)
		}
	}
	return out
}

func portKey(port uint16, protocol string) string {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol == "" {
		protocol = "tcp"
	}
	return fmt.Sprintf("%d/%s", port, protocol)
}

func imageAvailable(images []domain.Image, ref string) bool {
	want := normalizeImageRef(ref)
	for _, img := range images {
		for _, tag := range img.RepoTags {
			if normalizeImageRef(tag) == want {
				return true
			}
		}
		for _, digest := range img.RepoDigests {
			if digest == ref {
				return true
			}
		}
	}
	return false
}

// normalizeImageRef appends the implicit ":latest" tag Docker itself
// assumes for a bare reference, so "nginx" and "nginx:latest" compare
// equal — only after the last "/", so a registry host with a port
// ("registry.local:5000/nginx") isn't mistaken for an explicit tag.
func normalizeImageRef(ref string) string {
	slash := strings.LastIndex(ref, "/")
	rest := ref
	if slash >= 0 {
		rest = ref[slash+1:]
	}
	if strings.Contains(rest, ":") {
		return ref
	}
	return ref + ":latest"
}

// NetworksToCreate reports which of wanted's networks aren't already
// present in existing (never one of Docker's three built-in networks,
// which always exist). Exported so a paste's apply step can recompute
// this fresh against the *actual* spec being deployed and the
// destination's *current* networks — Plan's own use of this is only ever
// a snapshot at review time, and re-running it right before creating
// anything is what keeps a plan from going stale if the user edits which
// networks to attach to before confirming (see pasteApplyCmd).
func NetworksToCreate(existing []domain.Network, wanted []app.NetworkAttachment) []string {
	existingNames := map[string]bool{}
	for _, n := range existing {
		existingNames[n.Name] = true
	}
	var missing []string
	for _, n := range wanted {
		if isBuiltinNetwork(n.Name) || existingNames[n.Name] {
			continue
		}
		missing = append(missing, n.Name)
	}
	return missing
}

func isBuiltinNetwork(name string) bool {
	switch name {
	case "bridge", "host", "none":
		return true
	default:
		return false
	}
}
