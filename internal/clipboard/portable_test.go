package clipboard

import (
	"strings"
	"testing"
	"time"

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

func TestToCreateSpecConvertsMountsBindVolumeTmpfs(t *testing.T) {
	pc := FromContainer(fullInspectContainer(), domain.Host{ID: "h", Name: "h"})
	spec := pc.ToCreateSpec()

	if len(spec.Mounts) != 2 {
		t.Fatalf("Mounts = %#v, want 2 (tmpfs goes to spec.Tmpfs, not Mounts)", spec.Mounts)
	}
	if spec.Tmpfs["/tmp"] != "size=64m" {
		t.Fatalf("Tmpfs = %#v", spec.Tmpfs)
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
