// Package clipboard implements whatthedock's Container Clipboard: a
// Vim-flavored "yank container -> switch host -> paste" workflow for
// cloning a container's *configuration* (never its volume/bind-mount data)
// from one Docker host to another.
//
// The pipeline is deliberately layered so each stage stays independently
// testable and so later features (whole-Compose-project yank/paste,
// "Paste + Data") can slot in without redesigning what's here:
//
//  1. source inspection    — domain.Container, via the existing
//     app.Provider.Container (a full Docker inspect); not this package's
//     concern.
//  2. portable model        — PortableContainer (this file): a normalized,
//     host-independent snapshot of one container's deployment shape.
//  3. destination validation — Plan/PastePlan/PasteConflict (plan.go):
//     checks a PortableContainer against a *different* host before
//     anything is created.
//  4. object creation       — deliberately NOT duplicated here; a plan's
//     resolved app.ContainerCreateSpec is realized through the same
//     provider.CreateContainer path internal/ui already uses for a normal
//     create.
package clipboard

import (
	"strings"
	"time"

	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/domain"
)

// PortableEnv is one environment variable captured at yank time. Secret is
// a name-based heuristic (see IsLikelySecret) — Value always holds the
// real value (needed to actually recreate the container); masking it for
// display is the UI's job, not this package's.
type PortableEnv struct {
	Key    string
	Value  string
	Secret bool
}

// PortablePort is one port the source container declared. Published
// distinguishes a real publish (host-bound) from a merely-exposed
// container port — Published false means HostPort/HostIP carry no
// information (mirroring domain.Container.ExposedPorts).
type PortablePort struct {
	HostIP        string
	HostPort      uint16
	ContainerPort uint16
	Protocol      string
	Published     bool
}

// PortableMount is one bind mount, named volume, or tmpfs mount. For a
// tmpfs mount, Source is empty and TmpfsOptions carries the raw options
// string Docker itself uses (e.g. "size=64m,mode=1777").
type PortableMount struct {
	Type         string // "bind", "volume", or "tmpfs"
	Source       string
	Target       string
	ReadOnly     bool
	TmpfsOptions string
}

// PortableNetwork is one network the source container was attached to,
// with whatever aliases it had on that network.
type PortableNetwork struct {
	Name    string
	Aliases []string
}

// PortableCompose captures which Compose project/service (if any) the
// source container belonged to. v1 never acts on this beyond carrying it
// along — it exists so a future whole-project yank/paste, or a
// compose.yaml export, doesn't need to redesign PortableContainer to add
// it later.
type PortableCompose struct {
	Project string
	Service string
}

// PortableContainer is the normalized, host-independent snapshot a yank
// produces and a paste consumes. Every field here is meant to survive a
// round trip through ToCreateSpec with no host-specific assumptions baked
// in — anything that genuinely needs to change per-destination (name, host
// ports, mount sources, network mapping, env values) is exactly what the
// paste review/edit step exists to adjust before ToCreateSpec is called for
// real.
type PortableContainer struct {
	SourceHost     domain.HostID
	SourceHostName string
	YankedAt       time.Time

	Name        string
	Hostname    string
	Image       string
	ImageDigest string
	Command     string // space-joined, same convention as domain.Container.Command
	Entrypoint  string

	Env    []PortableEnv
	Labels map[string]string

	WorkingDir string
	User       string

	Privileged bool
	CapAdd     []string
	CapDrop    []string
	Devices    []domain.Device

	Ports    []PortablePort
	Mounts   []PortableMount
	Networks []PortableNetwork

	RestartPolicy string
	HealthCheck   *domain.HealthCheck

	MemoryBytes int64
	NanoCPUs    int64

	StopSignal  string
	StopTimeout *int

	DNS            []string
	DNSSearch      []string
	ReadonlyRootfs bool
	SecurityOpt    []string

	LogDriver  string
	LogOptions map[string]string

	Compose PortableCompose
}

// SecretEnvCount reports how many captured env vars look like secrets —
// used by the paste review screen's "N secret-like env vars" line.
func (pc PortableContainer) SecretEnvCount() int {
	count := 0
	for _, e := range pc.Env {
		if e.Secret {
			count++
		}
	}
	return count
}

// FromContainer builds a PortableContainer from a fully-inspected
// domain.Container (i.e. one returned by Provider.Container, not the
// lighter Snapshot()/FromSummary shape — see domain.Container's own doc
// comment on its inspect-only fields). This is the "yank" step. sourceHost
// is the provider's own Host() at yank time — ctr.ID.Host alone would give
// the HostID but not the display Name the topbar/review-screen want.
func FromContainer(ctr domain.Container, sourceHost domain.Host) PortableContainer {
	pc := PortableContainer{
		SourceHost:     sourceHost.ID,
		SourceHostName: sourceHost.Name,
		YankedAt:       time.Now(),
		Name:           ctr.DisplayName(),
		Hostname:       ctr.Hostname,
		Image:          ctr.Image,
		ImageDigest:    ctr.ImageDigest,
		Command:        ctr.Command,
		Entrypoint:     ctr.Entrypoint,
		Labels:         copyStringMap(ctr.Labels),
		WorkingDir:     ctr.WorkingDir,
		User:           ctr.User,
		Privileged:     ctr.Privileged,
		CapAdd:         append([]string(nil), ctr.CapAdd...),
		CapDrop:        append([]string(nil), ctr.CapDrop...),
		Devices:        append([]domain.Device(nil), ctr.Devices...),
		RestartPolicy:  ctr.RestartPolicy,
		HealthCheck:    ctr.HealthCheck,
		MemoryBytes:    ctr.MemoryBytes,
		NanoCPUs:       ctr.NanoCPUs,
		StopSignal:     ctr.StopSignal,
		StopTimeout:    ctr.StopTimeout,
		DNS:            append([]string(nil), ctr.DNS...),
		DNSSearch:      append([]string(nil), ctr.DNSSearch...),
		ReadonlyRootfs: ctr.ReadonlyRootfs,
		SecurityOpt:    append([]string(nil), ctr.SecurityOpt...),
		LogDriver:      ctr.LogDriver,
		LogOptions:     copyStringMap(ctr.LogOptions),
		Compose:        PortableCompose{Project: ctr.Compose.Project, Service: ctr.Compose.Service},
	}

	for _, e := range ctr.Env {
		key, value := splitEnv(e)
		pc.Env = append(pc.Env, PortableEnv{Key: key, Value: value, Secret: IsLikelySecret(key)})
	}

	for _, p := range ctr.Ports {
		if p.Public == 0 {
			continue // not actually published — see domain.Container.Ports' own convention
		}
		pc.Ports = append(pc.Ports, PortablePort{
			HostIP: p.IP, HostPort: p.Public, ContainerPort: p.Private, Protocol: p.Type, Published: true,
		})
	}
	for _, p := range ctr.ExposedPorts {
		pc.Ports = append(pc.Ports, PortablePort{ContainerPort: p.Private, Protocol: p.Type, Published: false})
	}

	for _, m := range ctr.Mounts {
		pc.Mounts = append(pc.Mounts, PortableMount{
			Type: normalizeMountType(m.Type), Source: m.Source, Target: m.Destination, ReadOnly: !m.ReadWrite,
		})
	}
	for target, options := range ctr.Tmpfs {
		pc.Mounts = append(pc.Mounts, PortableMount{Type: "tmpfs", Target: target, TmpfsOptions: options})
	}

	for _, name := range ctr.Networks {
		pc.Networks = append(pc.Networks, PortableNetwork{Name: name, Aliases: ctr.NetworkAliases[name]})
	}

	return pc
}

// ToCreateSpec converts pc into the create-time shape provider.CreateContainer
// needs. This is what both the initial paste plan's proposed spec and the
// paste review form's prefilled draft are built from; further edits the
// user makes on that form re-derive the final spec through the ordinary
// create-form parsing (createDraft.ContainerSpec), not through this method
// again — ToCreateSpec only ever needs to run once per paste, to produce
// the starting point.
func (pc PortableContainer) ToCreateSpec() app.ContainerCreateSpec {
	spec := app.ContainerCreateSpec{
		Name:           pc.Name,
		Image:          pc.Image,
		Command:        splitFields(pc.Command),
		Entrypoint:     splitFields(pc.Entrypoint),
		Hostname:       pc.Hostname,
		WorkingDir:     pc.WorkingDir,
		User:           pc.User,
		Labels:         filterComposeIdentityLabels(pc.Labels),
		RestartPolicy:  pc.RestartPolicy,
		Privileged:     pc.Privileged,
		CapAdd:         append([]string(nil), pc.CapAdd...),
		CapDrop:        append([]string(nil), pc.CapDrop...),
		Devices:        append([]domain.Device(nil), pc.Devices...),
		MemoryBytes:    pc.MemoryBytes,
		NanoCPUs:       pc.NanoCPUs,
		StopSignal:     pc.StopSignal,
		StopTimeout:    pc.StopTimeout,
		DNS:            append([]string(nil), pc.DNS...),
		DNSSearch:      append([]string(nil), pc.DNSSearch...),
		ReadonlyRootfs: pc.ReadonlyRootfs,
		SecurityOpt:    append([]string(nil), pc.SecurityOpt...),
		LogDriver:      pc.LogDriver,
		LogOptions:     copyStringMap(pc.LogOptions),
		Healthcheck:    pc.HealthCheck,
		Start:          true,
	}
	for _, e := range pc.Env {
		spec.Env = append(spec.Env, e.Key+"="+e.Value)
	}
	for _, p := range pc.Ports {
		if !p.Published {
			spec.ExposedPorts = append(spec.ExposedPorts, app.ExposedPort{ContainerPort: p.ContainerPort, Protocol: p.Protocol})
			continue
		}
		spec.Ports = append(spec.Ports, app.PortBinding{
			HostIP: p.HostIP, HostPort: p.HostPort, ContainerPort: p.ContainerPort, Protocol: p.Protocol,
		})
	}
	for _, m := range pc.Mounts {
		if m.Type == "tmpfs" {
			if spec.Tmpfs == nil {
				spec.Tmpfs = map[string]string{}
			}
			spec.Tmpfs[m.Target] = m.TmpfsOptions
			continue
		}
		spec.Mounts = append(spec.Mounts, app.MountBinding{Type: m.Type, Source: m.Source, Destination: m.Target, ReadOnly: m.ReadOnly})
	}
	for _, n := range pc.Networks {
		spec.Networks = append(spec.Networks, app.NetworkAttachment{Name: n.Name, Aliases: append([]string(nil), n.Aliases...)})
	}
	return spec
}

// splitEnv splits a "KEY=VALUE" docker env entry. A malformed entry with no
// "=" (shouldn't happen from a real inspect, but cheap to guard) is kept as
// a key with an empty value rather than dropped, so nothing yanked is ever
// silently lost.
func splitEnv(entry string) (string, string) {
	key, value, ok := strings.Cut(entry, "=")
	if !ok {
		return entry, ""
	}
	return key, value
}

// splitFields is the shared quote-aware splitting convention
// domain.Container.Command/Entrypoint (and internal/ui's identical
// splitCommand) already use for a joined display string — kept in sync
// with that convention (domain.SplitShellWords) rather than introducing a
// second one just for this package.
func splitFields(value string) []string {
	return domain.SplitShellWords(value)
}

func normalizeMountType(t string) string {
	switch t {
	case "bind", "volume", "tmpfs":
		return t
	default:
		if t == "" {
			return "volume"
		}
		return t
	}
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// composeIdentityLabelPrefix is Docker Compose's own label namespace —
// project/service/config-files/container-number/oneoff/etc. — every one
// of which describes a specific `docker compose` deployment, not anything
// true of a container ToCreateSpec is about to create via a bare
// CreateContainer call. Carrying these over verbatim on a paste (the
// original bug: a container yanked from a Compose-managed source kept its
// source's project/service/config_files labels after being pasted as a
// plain standalone container elsewhere) makes the *new* container look
// Compose-managed to anything that later inspects it — this app's own
// docker.FromInspect included — routing an ordinary edit into the Adopt
// flow for a base file that was never real to begin with.
const composeIdentityLabelPrefix = "com.docker.compose."

// filterComposeIdentityLabels drops any composeIdentityLabelPrefix key
// from labels — everything else (including this app's own
// com.whatthedock.* labels) passes through unchanged. Used only by
// ToCreateSpec: FromContainer's own Labels copy is untouched, since that's
// the full yanked record kept for reference, not what gets deployed.
func filterComposeIdentityLabels(labels map[string]string) map[string]string {
	copied := copyStringMap(labels)
	if len(copied) == 0 {
		return copied
	}
	for k := range copied {
		if strings.HasPrefix(k, composeIdentityLabelPrefix) {
			delete(copied, k)
		}
	}
	if len(copied) == 0 {
		return nil
	}
	return copied
}
