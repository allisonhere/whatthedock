package clipboard

import (
	"strings"
	"testing"
	"time"

	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/domain"
)

func fullInspectContainer() domain.Container {
	timeout := 5
	return domain.Container{
		ID:            domain.ResourceID{Host: "source-host", ID: "abc123"},
		Name:          "/radarr-1",
		Image:         "lscr.io/linuxserver/radarr:latest",
		ImageID:       "sha256:deadbeef",
		ImageDigest:   "lscr.io/linuxserver/radarr@sha256:cafef00d",
		Command:       "/init",
		Entrypoint:    "/entry.sh",
		Hostname:      "radarr-box",
		WorkingDir:    "/config",
		User:          "1000:1000",
		Privileged:    true,
		CapAdd:        []string{"NET_ADMIN"},
		CapDrop:       []string{"MKNOD"},
		RestartPolicy: "unless-stopped",
		Devices:       []domain.Device{{PathOnHost: "/dev/dri", PathInContainer: "/dev/dri", CgroupPermissions: "rwm"}},
		Env:           []string{"PUID=1000", "API_KEY=topsecret"},
		Labels:        map[string]string{"maintainer": "linuxserver.io"},
		Ports: []domain.Port{
			{IP: "0.0.0.0", Private: 7878, Public: 7878, Type: "tcp"},
			{Private: 9000, Public: 0, Type: "tcp"}, // not actually published
		},
		ExposedPorts: []domain.Port{{Private: 9000, Type: "tcp"}},
		Mounts: []domain.Mount{
			{Type: "bind", Source: "/srv/media/radarr", Destination: "/config", ReadWrite: true},
			{Type: "volume", Source: "radarr-data", Destination: "/data", ReadWrite: false},
		},
		Tmpfs:          map[string]string{"/tmp": "size=64m"},
		Networks:       []string{"media_default"},
		NetworkAliases: map[string][]string{"media_default": {"radarr", "radarr.media"}},
		MemoryBytes:    536870912,
		NanoCPUs:       1500000000,
		StopSignal:     "SIGTERM",
		StopTimeout:    &timeout,
		DNS:            []string{"1.1.1.1"},
		DNSSearch:      []string{"local"},
		ReadonlyRootfs: true,
		SecurityOpt:    []string{"no-new-privileges"},
		LogDriver:      "json-file",
		LogOptions:     map[string]string{"max-size": "10m"},
		Compose:        domain.ComposeRef{Project: "media", Service: "radarr"},
		HealthCheck:    &domain.HealthCheck{Test: []string{"CMD", "curl", "-f", "http://localhost:7878"}, Interval: 30 * time.Second, Retries: 3},
	}
}

func TestFromContainerRoundTripsEveryField(t *testing.T) {
	host := domain.Host{ID: "source-host", Name: "Vger"}
	pc := FromContainer(fullInspectContainer(), host)

	if pc.SourceHost != "source-host" || pc.SourceHostName != "Vger" {
		t.Fatalf("source host = %q/%q, want source-host/Vger", pc.SourceHost, pc.SourceHostName)
	}
	if pc.Name != "radarr-1" {
		t.Fatalf("Name = %q, want radarr-1 (leading slash trimmed)", pc.Name)
	}
	if pc.Image != "lscr.io/linuxserver/radarr:latest" || pc.ImageDigest != "lscr.io/linuxserver/radarr@sha256:cafef00d" {
		t.Fatalf("image/digest = %q/%q", pc.Image, pc.ImageDigest)
	}
	if pc.Command != "/init" || pc.Entrypoint != "/entry.sh" {
		t.Fatalf("command/entrypoint = %q/%q", pc.Command, pc.Entrypoint)
	}
	if pc.Hostname != "radarr-box" || pc.WorkingDir != "/config" || pc.User != "1000:1000" {
		t.Fatalf("hostname/workdir/user = %q/%q/%q", pc.Hostname, pc.WorkingDir, pc.User)
	}
	if !pc.Privileged || len(pc.CapAdd) != 1 || pc.CapAdd[0] != "NET_ADMIN" || len(pc.CapDrop) != 1 {
		t.Fatalf("privileged/caps = %v/%v/%v", pc.Privileged, pc.CapAdd, pc.CapDrop)
	}
	if len(pc.Devices) != 1 || pc.Devices[0].PathOnHost != "/dev/dri" {
		t.Fatalf("devices = %#v", pc.Devices)
	}
	if pc.RestartPolicy != "unless-stopped" {
		t.Fatalf("restart policy = %q", pc.RestartPolicy)
	}
	if pc.MemoryBytes != 536870912 || pc.NanoCPUs != 1500000000 {
		t.Fatalf("resources = %d/%d", pc.MemoryBytes, pc.NanoCPUs)
	}
	if pc.StopSignal != "SIGTERM" || pc.StopTimeout == nil || *pc.StopTimeout != 5 {
		t.Fatalf("stop signal/timeout = %q/%v", pc.StopSignal, pc.StopTimeout)
	}
	if len(pc.DNS) != 1 || pc.DNS[0] != "1.1.1.1" || len(pc.DNSSearch) != 1 {
		t.Fatalf("dns = %v/%v", pc.DNS, pc.DNSSearch)
	}
	if !pc.ReadonlyRootfs || len(pc.SecurityOpt) != 1 {
		t.Fatalf("readonly/security opt = %v/%v", pc.ReadonlyRootfs, pc.SecurityOpt)
	}
	if pc.LogDriver != "json-file" || pc.LogOptions["max-size"] != "10m" {
		t.Fatalf("log driver/options = %q/%v", pc.LogDriver, pc.LogOptions)
	}
	if pc.Compose.Project != "media" || pc.Compose.Service != "radarr" {
		t.Fatalf("compose ref = %#v", pc.Compose)
	}
	if pc.HealthCheck == nil || len(pc.HealthCheck.Test) != 4 {
		t.Fatalf("healthcheck = %#v", pc.HealthCheck)
	}

	// env: preserved, secret flagged, never dropped
	if len(pc.Env) != 2 {
		t.Fatalf("env = %#v, want 2 entries", pc.Env)
	}
	byKey := map[string]PortableEnv{}
	for _, e := range pc.Env {
		byKey[e.Key] = e
	}
	if byKey["PUID"].Value != "1000" || byKey["PUID"].Secret {
		t.Fatalf("PUID = %#v, want value 1000 and not secret", byKey["PUID"])
	}
	if byKey["API_KEY"].Value != "topsecret" {
		t.Fatal("API_KEY value was altered — clipboard must preserve real values internally")
	}
	if !byKey["API_KEY"].Secret {
		t.Fatal("API_KEY not flagged as a secret-like var")
	}

	// ports: published vs exposed-only
	var published, exposed int
	for _, p := range pc.Ports {
		if p.Published {
			published++
			if p.HostPort != 7878 || p.ContainerPort != 7878 {
				t.Fatalf("published port = %#v", p)
			}
		} else {
			exposed++
		}
	}
	if published != 1 || exposed != 1 {
		t.Fatalf("published/exposed counts = %d/%d, want 1/1 (port 9000 with Public=0 must not count as published)", published, exposed)
	}

	// mounts: bind, volume, tmpfs all converted
	kinds := map[string]PortableMount{}
	for _, m := range pc.Mounts {
		kinds[m.Type] = m
	}
	if kinds["bind"].Source != "/srv/media/radarr" || kinds["bind"].ReadOnly {
		t.Fatalf("bind mount = %#v", kinds["bind"])
	}
	if kinds["volume"].Source != "radarr-data" || !kinds["volume"].ReadOnly {
		t.Fatalf("volume mount = %#v", kinds["volume"])
	}
	if kinds["tmpfs"].Target != "/tmp" || kinds["tmpfs"].TmpfsOptions != "size=64m" {
		t.Fatalf("tmpfs mount = %#v", kinds["tmpfs"])
	}

	// networks + aliases
	if len(pc.Networks) != 1 || pc.Networks[0].Name != "media_default" {
		t.Fatalf("networks = %#v", pc.Networks)
	}
	if len(pc.Networks[0].Aliases) != 2 || pc.Networks[0].Aliases[0] != "radarr" {
		t.Fatalf("network aliases = %#v", pc.Networks[0].Aliases)
	}
}

func TestToCreateSpecPreservesCommandEntrypointRestartPolicy(t *testing.T) {
	pc := FromContainer(fullInspectContainer(), domain.Host{ID: "h", Name: "h"})
	spec := pc.ToCreateSpec()

	if len(spec.Command) != 1 || spec.Command[0] != "/init" {
		t.Fatalf("Command = %#v, want [/init]", spec.Command)
	}
	if len(spec.Entrypoint) != 1 || spec.Entrypoint[0] != "/entry.sh" {
		t.Fatalf("Entrypoint = %#v, want [/entry.sh]", spec.Entrypoint)
	}
	if spec.RestartPolicy != "unless-stopped" {
		t.Fatalf("RestartPolicy = %q", spec.RestartPolicy)
	}
	if !spec.Start {
		t.Fatal("Start = false, want true — paste should start the container like a normal create")
	}
}

// TestToCreateSpecStripsComposeIdentityLabels is a regression test for a
// real bug reported live: pasting a container whose *source* was
// Compose-managed carried its com.docker.compose.* labels straight onto
// the new standalone container, making it look Compose-managed on the
// destination too — with a config_files path that was never real there —
// which routed an ordinary later edit into the Adopt flow instead of a
// plain standalone edit. Every other label (the app's own
// com.whatthedock.* ones included) must still survive.
func TestToCreateSpecStripsComposeIdentityLabels(t *testing.T) {
	ctr := fullInspectContainer()
	ctr.Labels = map[string]string{
		"maintainer":                                         "linuxserver.io",
		"com.docker.compose.project":                         "media",
		"com.docker.compose.service":                         "radarr",
		"com.docker.compose.project.config_files":            "/srv/media/compose.yml",
		"com.whatthedock.paste.original-bind-source:/config": "/srv/media/radarr",
	}
	pc := FromContainer(ctr, domain.Host{ID: "h", Name: "h"})
	spec := pc.ToCreateSpec()

	for key := range spec.Labels {
		if strings.HasPrefix(key, "com.docker.compose.") {
			t.Fatalf("spec.Labels = %#v, want no com.docker.compose.* keys", spec.Labels)
		}
	}
	if spec.Labels["maintainer"] != "linuxserver.io" {
		t.Fatalf("spec.Labels[maintainer] = %q, want it preserved", spec.Labels["maintainer"])
	}
	if spec.Labels["com.whatthedock.paste.original-bind-source:/config"] != "/srv/media/radarr" {
		t.Fatalf("spec.Labels = %#v, want the whatthedock label preserved", spec.Labels)
	}
}

func TestToCreateSpecConvertsMountsBindVolumeTmpfs(t *testing.T) {
	pc := FromContainer(fullInspectContainer(), domain.Host{ID: "h", Name: "h"})
	spec := pc.ToCreateSpec()

	if len(spec.Mounts) != 2 {
		t.Fatalf("Mounts = %#v, want 2 (tmpfs goes to spec.Tmpfs, not Mounts)", spec.Mounts)
	}
	if spec.Tmpfs["/tmp"] != "size=64m" {
		t.Fatalf("Tmpfs = %#v", spec.Tmpfs)
	}

	// Type must be explicit and correct on each — a bind mount must never
	// come out tagged "volume" (or vice versa); ToCreateSpec carries
	// PortableMount.Type straight through rather than re-inferring it.
	byDest := map[string]app.MountBinding{}
	for _, m := range spec.Mounts {
		byDest[m.Destination] = m
	}
	if got := byDest["/config"]; got.Type != "bind" || got.Source != "/srv/media/radarr" || got.ReadOnly {
		t.Fatalf("bind mount = %#v, want Type=bind, read-write", got)
	}
	if got := byDest["/data"]; got.Type != "volume" || got.Source != "radarr-data" || !got.ReadOnly {
		t.Fatalf("volume mount = %#v, want Type=volume, read-only", got)
	}
}

func TestToCreateSpecConvertsNetworksWithAliases(t *testing.T) {
	pc := FromContainer(fullInspectContainer(), domain.Host{ID: "h", Name: "h"})
	spec := pc.ToCreateSpec()

	if len(spec.Networks) != 1 || spec.Networks[0].Name != "media_default" {
		t.Fatalf("Networks = %#v", spec.Networks)
	}
	if strings.Join(spec.Networks[0].Aliases, ",") != "radarr,radarr.media" {
		t.Fatalf("Network aliases = %#v", spec.Networks[0].Aliases)
	}
}

func TestToCreateSpecPreservesEnvironment(t *testing.T) {
	pc := FromContainer(fullInspectContainer(), domain.Host{ID: "h", Name: "h"})
	spec := pc.ToCreateSpec()

	want := map[string]bool{"PUID=1000": true, "API_KEY=topsecret": true}
	if len(spec.Env) != len(want) {
		t.Fatalf("Env = %#v", spec.Env)
	}
	for _, e := range spec.Env {
		if !want[e] {
			t.Fatalf("unexpected env entry %q", e)
		}
	}
}

func TestToCreateSpecSplitsPublishedVsExposedPorts(t *testing.T) {
	pc := FromContainer(fullInspectContainer(), domain.Host{ID: "h", Name: "h"})
	spec := pc.ToCreateSpec()

	if len(spec.Ports) != 1 || spec.Ports[0].HostPort != 7878 {
		t.Fatalf("Ports = %#v, want one published binding on 7878", spec.Ports)
	}
	if len(spec.ExposedPorts) != 1 || spec.ExposedPorts[0].ContainerPort != 9000 {
		t.Fatalf("ExposedPorts = %#v, want container port 9000 with no host binding", spec.ExposedPorts)
	}
}

// TestFromContainerDoesNotDuplicatePublishedPortAsExposedOnly is the
// regression test for a confirmed bug: a real Docker inspect lists every
// published port in BOTH NetworkSettings.Ports (ctr.Ports, with a real host
// binding) and Config.ExposedPorts (ctr.ExposedPorts) — publishing a port
// always exposes it too, so a real domain.Container always carries the same
// container port in both slices (see internal/docker/client.go's
// FromInspect). fullInspectContainer's fixture happened to only put the
// non-published port 9000 in ExposedPorts, which hid this: without the
// dedup this test checks for, FromContainer would append a second,
// contradictory PortablePort{Published: false} for port 7878 alongside the
// correct published one, and ToCreateSpec would carry that bogus entry
// through to spec.ExposedPorts.
func TestFromContainerDoesNotDuplicatePublishedPortAsExposedOnly(t *testing.T) {
	ctr := fullInspectContainer()
	// Mirror real Docker inspect output: 7878 is published AND exposed.
	ctr.ExposedPorts = append(ctr.ExposedPorts, domain.Port{Private: 7878, Type: "tcp"})

	pc := FromContainer(ctr, domain.Host{ID: "h", Name: "h"})

	var published, exposedOnly int
	for _, p := range pc.Ports {
		if p.ContainerPort != 7878 {
			continue
		}
		if p.Published {
			published++
		} else {
			exposedOnly++
		}
	}
	if published != 1 {
		t.Fatalf("published entries for port 7878 = %d, want exactly 1", published)
	}
	if exposedOnly != 0 {
		t.Fatalf("exposed-only entries for port 7878 = %d, want 0 — it's already published, not merely exposed", exposedOnly)
	}

	spec := pc.ToCreateSpec()
	for _, p := range spec.ExposedPorts {
		if p.ContainerPort == 7878 {
			t.Fatalf("spec.ExposedPorts = %#v, want no entry for the already-published port 7878", spec.ExposedPorts)
		}
	}
}

// TestFromContainerThenToCreateSpecPreservesHostileEnvValues is a
// table-driven round trip for env edge cases: commas, spaces, embedded "="
// signs, and empty values must all survive FromContainer -> ToCreateSpec
// unchanged, since this package's own env handling (splitEnv/ToCreateSpec's
// "Key=Value" rejoin) never does any CSV-style splitting of its own — that
// only happens one layer up, in internal/ui's editable text field.
func TestFromContainerThenToCreateSpecPreservesHostileEnvValues(t *testing.T) {
	tests := []struct {
		name  string
		entry string // raw "KEY=VALUE" as a real Docker inspect would report it
		key   string
		value string
	}{
		{name: "comma in value", entry: "TAGS=a,b,c", key: "TAGS", value: "a,b,c"},
		{name: "spaces in value", entry: "GREETING=hello world", key: "GREETING", value: "hello world"},
		{name: "embedded equals sign", entry: "DSN=postgres://u:p@host/db?sslmode=require", key: "DSN", value: "postgres://u:p@host/db?sslmode=require"},
		{name: "empty value", entry: "DEBUG=", key: "DEBUG", value: ""},
		{name: "value with leading/trailing spaces", entry: "PADDED=  spaced  ", key: "PADDED", value: "  spaced  "},
		{name: "comma and equals together", entry: "OPTS=a=1,b=2", key: "OPTS", value: "a=1,b=2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctr := domain.Container{Env: []string{tt.entry}}
			pc := FromContainer(ctr, domain.Host{ID: "h", Name: "h"})
			if len(pc.Env) != 1 || pc.Env[0].Key != tt.key || pc.Env[0].Value != tt.value {
				t.Fatalf("PortableContainer.Env = %#v, want key=%q value=%q", pc.Env, tt.key, tt.value)
			}
			spec := pc.ToCreateSpec()
			want := tt.key + "=" + tt.value
			if len(spec.Env) != 1 || spec.Env[0] != want {
				t.Fatalf("spec.Env = %#v, want [%q]", spec.Env, want)
			}
		})
	}
}

// TestFromContainerThenToCreateSpecPreservesHostileCommands checks Command
// and Entrypoint survive quoting/splitting for arguments containing spaces
// and quotes — via domain.SplitShellWords, the same convention used
// everywhere else a joined command string round-trips.
func TestFromContainerThenToCreateSpecPreservesHostileCommands(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    []string
	}{
		{name: "simple", command: "/init", want: []string{"/init"}},
		{name: "quoted argument with space", command: `sh -c "echo hello world"`, want: []string{"sh", "-c", "echo hello world"}},
		{name: "multiple flags", command: "run --flag --other value", want: []string{"run", "--flag", "--other", "value"}},
		{name: "empty", command: "", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctr := domain.Container{Command: tt.command}
			pc := FromContainer(ctr, domain.Host{ID: "h", Name: "h"})
			if pc.Command != tt.command {
				t.Fatalf("PortableContainer.Command = %q, want %q (untouched string copy)", pc.Command, tt.command)
			}
			spec := pc.ToCreateSpec()
			if len(spec.Command) != len(tt.want) {
				t.Fatalf("spec.Command = %#v, want %#v", spec.Command, tt.want)
			}
			for i := range tt.want {
				if spec.Command[i] != tt.want[i] {
					t.Fatalf("spec.Command = %#v, want %#v", spec.Command, tt.want)
				}
			}
		})
	}
}

// TestFromContainerThenToCreateSpecPreservesHostileLabels checks labels
// with empty values, and the compose-identity-strip vs whatthedock-label-
// preserve split, holds for edge-case keys/values too.
func TestFromContainerThenToCreateSpecPreservesHostileLabels(t *testing.T) {
	ctr := domain.Container{
		Labels: map[string]string{
			"empty-value":                  "",
			"com.whatthedock.paste.marker": "1",
			"com.docker.compose.project":   "media",
			"com.docker.compose.oneoff":    "False",
			"maintainer":                   "team@example.com",
		},
	}
	pc := FromContainer(ctr, domain.Host{ID: "h", Name: "h"})
	// FromContainer's own Labels field is the full, unfiltered record.
	if len(pc.Labels) != 5 {
		t.Fatalf("PortableContainer.Labels = %#v, want all 5 labels kept on the yanked record", pc.Labels)
	}
	if v, ok := pc.Labels["empty-value"]; !ok || v != "" {
		t.Fatalf("PortableContainer.Labels[empty-value] = %q, ok=%v, want empty string kept (not dropped)", v, ok)
	}

	spec := pc.ToCreateSpec()
	if len(spec.Labels) != 3 {
		t.Fatalf("spec.Labels = %#v, want the 2 compose.* keys stripped, 3 left", spec.Labels)
	}
	for key := range spec.Labels {
		if strings.HasPrefix(key, "com.docker.compose.") {
			t.Fatalf("spec.Labels = %#v, want no com.docker.compose.* keys", spec.Labels)
		}
	}
	if v, ok := spec.Labels["empty-value"]; !ok || v != "" {
		t.Fatalf("spec.Labels[empty-value] = %q, ok=%v, want empty string preserved through the filter", v, ok)
	}
	if spec.Labels["com.whatthedock.paste.marker"] != "1" {
		t.Fatalf("spec.Labels = %#v, want the whatthedock label preserved", spec.Labels)
	}
	if spec.Labels["maintainer"] != "team@example.com" {
		t.Fatalf("spec.Labels = %#v, want the ordinary label preserved", spec.Labels)
	}
}

// TestFromContainerThenToCreateSpecPreservesMultipleHostIPSpecificBindings
// covers items 7+8 from the audit: several host bindings on different
// container ports, one of them with an explicit non-wildcard host IP, must
// all survive as separate bindings — none collapsed, none losing their IP.
func TestFromContainerThenToCreateSpecPreservesMultipleHostIPSpecificBindings(t *testing.T) {
	ctr := domain.Container{
		Ports: []domain.Port{
			{IP: "0.0.0.0", Private: 80, Public: 8080, Type: "tcp"},
			{IP: "127.0.0.1", Private: 443, Public: 8443, Type: "tcp"},
			{IP: "0.0.0.0", Private: 53, Public: 53, Type: "udp"},
		},
	}
	pc := FromContainer(ctr, domain.Host{ID: "h", Name: "h"})
	if len(pc.Ports) != 3 {
		t.Fatalf("PortableContainer.Ports = %#v, want all 3 bindings kept, none collapsed", pc.Ports)
	}

	spec := pc.ToCreateSpec()
	if len(spec.Ports) != 3 {
		t.Fatalf("spec.Ports = %#v, want all 3 bindings kept", spec.Ports)
	}
	byContainerPort := map[uint16]app.PortBinding{}
	for _, p := range spec.Ports {
		byContainerPort[p.ContainerPort] = p
	}
	if got := byContainerPort[443]; got.HostIP != "127.0.0.1" || got.HostPort != 8443 {
		t.Fatalf("port 443 binding = %#v, want host IP 127.0.0.1 preserved", got)
	}
	if got := byContainerPort[80]; got.HostPort != 8080 {
		t.Fatalf("port 80 binding = %#v, want host port 8080", got)
	}
	if got := byContainerPort[53]; got.Protocol != "udp" {
		t.Fatalf("port 53 binding = %#v, want udp protocol preserved", got)
	}
}

// TestToCreateSpecCarriesHostNetworkMode guards the host-network paste
// failure: "host" is a mode, not a network to attach, so the spec must carry
// NetworkMode and attach nothing.
func TestToCreateSpecCarriesHostNetworkMode(t *testing.T) {
	pc := PortableContainer{
		Name: "dash", Image: "dash:latest", NetworkMode: "host",
		Networks: []PortableNetwork{{Name: "host"}},
	}
	spec := pc.ToCreateSpec()
	if spec.NetworkMode != "host" {
		t.Fatalf("NetworkMode = %q, want host", spec.NetworkMode)
	}
	if len(spec.Networks) != 0 {
		t.Fatalf("Networks = %#v, want none for host mode", spec.Networks)
	}
}

func TestToCreateSpecCarriesNoneNetworkMode(t *testing.T) {
	pc := PortableContainer{Name: "x", Image: "img", NetworkMode: "none", Networks: []PortableNetwork{{Name: "none"}}}
	spec := pc.ToCreateSpec()
	if spec.NetworkMode != "none" || len(spec.Networks) != 0 {
		t.Fatalf("NetworkMode/Networks = %q/%#v, want none/none", spec.NetworkMode, spec.Networks)
	}
}

func TestToCreateSpecKeepsCustomNetworksAndImpliesDefaultBridge(t *testing.T) {
	custom := PortableContainer{Name: "x", Image: "img", NetworkMode: "media_default",
		Networks: []PortableNetwork{{Name: "media_default", Aliases: []string{"x"}}}}
	spec := custom.ToCreateSpec()
	if len(spec.Networks) != 1 || spec.Networks[0].Name != "media_default" {
		t.Fatalf("Networks = %#v, want the custom network attached", spec.Networks)
	}
	if spec.NetworkMode != "" {
		t.Fatalf("NetworkMode = %q, want empty for a custom network", spec.NetworkMode)
	}

	bridge := PortableContainer{Name: "y", Image: "img", NetworkMode: "default",
		Networks: []PortableNetwork{{Name: "bridge"}}}
	spec = bridge.ToCreateSpec()
	if spec.NetworkMode != "" || len(spec.Networks) != 0 {
		t.Fatalf("default bridge: mode/networks = %q/%#v, want implied default", spec.NetworkMode, spec.Networks)
	}
}
