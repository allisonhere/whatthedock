package demo

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/domain"
)

type Provider struct {
	mu         sync.Mutex
	host       domain.Host
	containers map[string]domain.Container
	logs       map[string][]string
	statsTicks map[string]int
	networks   []domain.Network
	volumes    []domain.Volume
}

func NewProvider() *Provider {
	host := domain.Host{ID: "demo", Name: "demo homelab"}
	provider := &Provider{
		host:       host,
		containers: map[string]domain.Container{},
		logs:       map[string][]string{},
		statsTicks: map[string]int{},
	}
	provider.seed()
	provider.seedNetworksAndVolumes()
	return provider
}

func (p *Provider) Host() domain.Host { return p.host }

func (p *Provider) Ping(context.Context) error { return nil }

func (p *Provider) Snapshot(_ context.Context) (domain.Snapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	containers := make([]domain.Container, 0, len(p.containers))
	for _, ctr := range p.containers {
		containers = append(containers, ctr)
	}
	return domain.BuildSnapshot(p.host, containers, time.Now()), nil
}

func (p *Provider) Events(ctx context.Context) (<-chan domain.ContainerEvent, error) {
	ch := make(chan domain.ContainerEvent)
	go func() {
		defer close(ch)
		<-ctx.Done()
	}()
	return ch, nil
}

func (p *Provider) Container(_ context.Context, id domain.ResourceID) (domain.Container, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ctr, ok := p.containers[id.ID]
	if !ok {
		return domain.Container{}, fmt.Errorf("demo container %s no longer exists", id.ID)
	}
	return ctr, nil
}

func (p *Provider) ContainerStats(_ context.Context, id domain.ResourceID) (domain.ContainerStats, error) {
	p.mu.Lock()
	ctr, ok := p.containers[id.ID]
	tick := p.statsTicks[id.ID]
	p.statsTicks[id.ID] = tick + 1
	p.mu.Unlock()
	if !ok {
		return domain.ContainerStats{}, fmt.Errorf("demo container %s no longer exists", id.ID)
	}
	return demoStats(ctr, tick), nil
}

func (p *Provider) Logs(ctx context.Context, id domain.ResourceID, options app.LogOptions) (io.ReadCloser, error) {
	p.mu.Lock()
	lines := append([]string(nil), p.logs[id.ID]...)
	_, ok := p.containers[id.ID]
	p.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("demo container %s no longer exists", id.ID)
	}
	if len(lines) == 0 {
		lines = []string{"demo log stream initialized"}
	}
	reader, writer := io.Pipe()
	go func() {
		defer writer.Close()
		if !options.Follow {
			for _, line := range lines {
				_, _ = fmt.Fprintf(writer, "%s %s\n", time.Now().Format(time.RFC3339), line)
			}
			return
		}
		ticker := time.NewTicker(700 * time.Millisecond)
		defer ticker.Stop()
		index := 0
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			line := lines[index%len(lines)]
			_, _ = fmt.Fprintf(writer, "%s %s\n", time.Now().Format(time.RFC3339), line)
			index++
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return reader, nil
}

func demoStats(ctr domain.Container, tick int) domain.ContainerStats {
	base := uint64(len(ctr.ID.ID))*1024*1024 + uint64(ctr.RestartCount)*8*1024*1024
	wave := demoWave(tick)
	byteWave := uint64(absInt(wave)) * 1024 * 1024
	stats := domain.ContainerStats{
		ID:          ctr.ID,
		Read:        time.Now(),
		CPUPercent:  clampFloat(float64((len(ctr.DisplayName())%6)+1)*4.5+float64(wave)*1.7, 0, 100),
		MemoryUsage: 192*1024*1024 + base + byteWave,
		MemoryLimit: 2 * 1024 * 1024 * 1024,
		NetworkRx:   40*1024*1024 + base*2 + uint64(tick%9)*5*1024*1024,
		NetworkTx:   12*1024*1024 + base + uint64((tick+3)%7)*3*1024*1024,
		BlockRead:   80*1024*1024 + base + uint64((tick+5)%8)*4*1024*1024,
		BlockWrite:  24*1024*1024 + base/2 + uint64((tick+2)%6)*2*1024*1024,
		PIDs:        uint64(8 + len(ctr.Networks)*4 + absInt(wave)%5),
	}
	if ctr.Restarting || ctr.State == domain.StateRestarting {
		stats.CPUPercent = clampFloat(74.2+float64(wave)*2.4, 0, 100)
		stats.MemoryUsage = 780*1024*1024 + byteWave
		stats.NetworkRx = 620*1024*1024 + uint64(tick%10)*8*1024*1024
		stats.NetworkTx = 210*1024*1024 + uint64((tick+4)%8)*6*1024*1024
		stats.BlockRead = 330*1024*1024 + uint64((tick+6)%7)*5*1024*1024
		stats.BlockWrite = 140*1024*1024 + uint64((tick+1)%5)*4*1024*1024
		stats.PIDs = uint64(23 + absInt(wave)%6)
	}
	return stats
}

func demoWave(tick int) int {
	pattern := []int{0, 2, 4, 3, 1, -1, -3, -4, -2}
	return pattern[tick%len(pattern)]
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func clampFloat(value, minValue, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func (p *Provider) StartContainer(_ context.Context, id domain.ResourceID) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	ctr, ok := p.containers[id.ID]
	if !ok {
		return fmt.Errorf("demo container %s no longer exists", id.ID)
	}
	ctr.State = domain.StateRunning
	ctr.Status = "Up 1 second"
	ctr.Restarting = false
	p.containers[id.ID] = ctr
	p.logs[id.ID] = append(p.logs[id.ID], "container started from WhatTheDock")
	return nil
}

func (p *Provider) CreateContainer(_ context.Context, spec app.ContainerCreateSpec) (domain.ResourceID, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	id := fmt.Sprintf("demo-created-%d", len(p.containers)+1)
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		name = id
	}
	networks := make([]string, 0, len(spec.Networks))
	aliases := map[string][]string{}
	for _, n := range spec.Networks {
		networks = append(networks, n.Name)
		if len(n.Aliases) > 0 {
			aliases[n.Name] = append([]string(nil), n.Aliases...)
		}
	}
	labels := spec.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	ctr := domain.Container{
		ID:             domain.ResourceID{Host: p.host.ID, ID: id},
		Name:           name,
		Image:          spec.Image,
		Command:        domain.JoinShellWords(spec.Command),
		Entrypoint:     domain.JoinShellWords(spec.Entrypoint),
		Created:        time.Now(),
		State:          domain.StateStopped,
		Status:         "Created",
		Env:            append([]string(nil), spec.Env...),
		RestartPolicy:  spec.RestartPolicy,
		Labels:         labels,
		Hostname:       spec.Hostname,
		WorkingDir:     spec.WorkingDir,
		User:           spec.User,
		Privileged:     spec.Privileged,
		CapAdd:         append([]string(nil), spec.CapAdd...),
		CapDrop:        append([]string(nil), spec.CapDrop...),
		MemoryBytes:    spec.MemoryBytes,
		NanoCPUs:       spec.NanoCPUs,
		StopSignal:     spec.StopSignal,
		StopTimeout:    spec.StopTimeout,
		DNS:            append([]string(nil), spec.DNS...),
		DNSSearch:      append([]string(nil), spec.DNSSearch...),
		ReadonlyRootfs: spec.ReadonlyRootfs,
		SecurityOpt:    append([]string(nil), spec.SecurityOpt...),
		LogDriver:      spec.LogDriver,
		LogOptions:     spec.LogOptions,
		Tmpfs:          spec.Tmpfs,
		Networks:       networks,
		NetworkAliases: aliases,
		HealthCheck:    spec.Healthcheck,
	}
	if spec.Start {
		ctr.State = domain.StateRunning
		ctr.Status = "Up 1 second"
	}
	for _, binding := range spec.Ports {
		ctr.Ports = append(ctr.Ports, domain.Port{
			IP:      binding.HostIP,
			Private: binding.ContainerPort,
			Public:  binding.HostPort,
			Type:    binding.Protocol,
		})
	}
	for _, binding := range spec.Mounts {
		mountType := "volume"
		if strings.HasPrefix(binding.Source, "/") {
			mountType = "bind"
		}
		ctr.Mounts = append(ctr.Mounts, domain.Mount{
			Type:        mountType,
			Source:      binding.Source,
			Destination: binding.Destination,
			ReadWrite:   !binding.ReadOnly,
		})
	}
	p.containers[id] = ctr
	p.logs[id] = []string{"container created from WhatTheDock"}
	return ctr.ID, nil
}

func (p *Provider) RenameContainer(_ context.Context, id domain.ResourceID, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	ctr, ok := p.containers[id.ID]
	if !ok {
		return fmt.Errorf("demo container %s no longer exists", id.ID)
	}
	ctr.Name = strings.TrimSpace(name)
	p.containers[id.ID] = ctr
	p.logs[id.ID] = append(p.logs[id.ID], "container renamed from WhatTheDock")
	return nil
}

func (p *Provider) StopContainer(_ context.Context, id domain.ResourceID) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	ctr, ok := p.containers[id.ID]
	if !ok {
		return fmt.Errorf("demo container %s no longer exists", id.ID)
	}
	ctr.State = domain.StateStopped
	ctr.Status = "Exited (0) 1 second ago"
	ctr.Health = domain.HealthNone
	ctr.Restarting = false
	p.containers[id.ID] = ctr
	p.logs[id.ID] = append(p.logs[id.ID], "container stopped from WhatTheDock")
	return nil
}

func (p *Provider) RestartContainer(_ context.Context, id domain.ResourceID) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	ctr, ok := p.containers[id.ID]
	if !ok {
		return fmt.Errorf("demo container %s no longer exists", id.ID)
	}
	ctr.State = domain.StateRunning
	ctr.Status = "Up 1 second"
	ctr.Restarting = false
	ctr.RestartCount++
	p.containers[id.ID] = ctr
	p.logs[id.ID] = append(p.logs[id.ID], "container restarted from WhatTheDock")
	return nil
}

func (p *Provider) RemoveContainer(_ context.Context, id domain.ResourceID, _ bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.containers[id.ID]; !ok {
		return fmt.Errorf("demo container %s no longer exists", id.ID)
	}
	delete(p.containers, id.ID)
	delete(p.logs, id.ID)
	delete(p.statsTicks, id.ID)
	return nil
}

// PullImage synthesizes a handful of progress callbacks so the status-bar
// indicator is demoable without a real registry — there's no real registry
// to pull from, so the "layers" and byte counts below are fabricated, but
// calling onProgress with them exercises exactly the same status-bar path a
// real pull does. No time.Sleep: no other demo action in this package fakes
// wall-clock delay (demo mode is deliberately instant everywhere else), so
// this doesn't become the one part of the package that blocks.
func (p *Provider) PullImage(_ context.Context, _ string, onProgress func(app.PullProgress)) error {
	if onProgress == nil {
		return nil
	}
	layers := []string{"a1b2c3d4e5f6", "b2c3d4e5f6a1", "c3d4e5f6a1b2"}
	for _, id := range layers {
		const total = int64(42 * 1024 * 1024)
		onProgress(app.PullProgress{Status: "Downloading", ID: id, Current: total / 2, Total: total})
		onProgress(app.PullProgress{Status: "Downloading", ID: id, Current: total, Total: total})
		onProgress(app.PullProgress{Status: "Pull complete", ID: id})
	}
	return nil
}

func (p *Provider) Images(_ context.Context) ([]domain.Image, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	seen := map[string]bool{}
	images := make([]domain.Image, 0)
	for _, ctr := range p.containers {
		if seen[ctr.Image] {
			continue
		}
		seen[ctr.Image] = true
		images = append(images, domain.Image{
			ID:         domain.ResourceID{Host: p.host.ID, ID: ctr.ImageID},
			RepoTags:   []string{ctr.Image},
			Created:    ctr.Created,
			Containers: 1,
		})
	}
	sort.Slice(images, func(i, j int) bool { return images[i].DisplayName() < images[j].DisplayName() })
	return images, nil
}

func (p *Provider) RemoveImage(_ context.Context, ref string) error {
	return fmt.Errorf("demo image %s is in use", ref)
}

func (p *Provider) Networks(_ context.Context) ([]domain.Network, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]domain.Network, len(p.networks))
	copy(out, p.networks)
	return out, nil
}

func (p *Provider) RemoveNetwork(_ context.Context, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, net := range p.networks {
		if net.ID == id {
			p.networks = append(p.networks[:i], p.networks[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("demo network %s not found", id)
}

func (p *Provider) CreateNetwork(_ context.Context, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, net := range p.networks {
		if net.Name == name {
			return fmt.Errorf("network with name %s already exists", name)
		}
	}
	p.networks = append(p.networks, domain.Network{
		ID:     "demo-net-" + strings.ReplaceAll(strings.ToLower(name), " ", "-"),
		Name:   name,
		Driver: "bridge",
		Scope:  "local",
	})
	return nil
}

func (p *Provider) Volumes(_ context.Context) ([]domain.Volume, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]domain.Volume, len(p.volumes))
	copy(out, p.volumes)
	return out, nil
}

func (p *Provider) RemoveVolume(_ context.Context, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, vol := range p.volumes {
		if vol.Name == name {
			p.volumes = append(p.volumes[:i], p.volumes[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("demo volume %s not found", name)
}

func (p *Provider) Close() error { return nil }

// seedNetworksAndVolumes gives the demo homelab a mix of in-use and
// orphaned networks/volumes, so Curate Docker networks/volumes has
// something real to show both branches of Removable() without a live
// Docker daemon — including a couple of networks sitting in the
// 172.x/16 range, the same shape as the address-pool exhaustion this
// feature exists to help with.
func (p *Provider) seedNetworksAndVolumes() {
	p.networks = []domain.Network{
		{ID: "demo0bridge00", Name: "bridge", Driver: "bridge", Scope: "local", Containers: 0},
		{ID: "demo0media0001", Name: "media-stack_default", Driver: "bridge", Scope: "local", Containers: 4, Subnet: "172.18.0.0/16"},
		{ID: "demo0monitor01", Name: "monitoring_default", Driver: "bridge", Scope: "local", Containers: 2, Subnet: "172.19.0.0/16"},
		{ID: "demo0orphan001", Name: "old-stack_default", Driver: "bridge", Scope: "local", Containers: 0, Subnet: "172.20.0.0/16"},
		{ID: "demo0orphan002", Name: "test_default", Driver: "bridge", Scope: "local", Containers: 0, Subnet: "172.21.0.0/16"},
	}
	p.volumes = []domain.Volume{
		{Name: "media-stack_postgres-data", Driver: "local", Mountpoint: "/var/lib/docker/volumes/media-stack_postgres-data/_data", InUse: true},
		{Name: "monitoring_grafana-data", Driver: "local", Mountpoint: "/var/lib/docker/volumes/monitoring_grafana-data/_data", InUse: true},
		{Name: "old-stack_config", Driver: "local", Mountpoint: "/var/lib/docker/volumes/old-stack_config/_data", InUse: false},
		{Name: "leftover-cache", Driver: "local", Mountpoint: "/var/lib/docker/volumes/leftover-cache/_data", InUse: false},
	}
}

func (p *Provider) seed() {
	now := time.Now().Add(-36 * time.Hour)
	p.add(containerSeed{
		ID: "demo-jellyfin", Name: "media-jellyfin-1", Image: "jellyfin/jellyfin:latest", State: domain.StateRunning, Health: domain.HealthHealthy,
		Project: "media-stack", Service: "jellyfin", Ports: []domain.Port{{Private: 8096, Public: 8096, Type: "tcp"}},
		Mounts:  []domain.Mount{{Type: "bind", Source: "/srv/media", Destination: "/media", ReadWrite: true}},
		Logs:    []string{"[INF] Jellyfin ready on http://0.0.0.0:8096", "[INF] Playback profile loaded", "[INF] Scheduled library scan complete"},
		Created: now,
	})
	p.add(containerSeed{
		ID: "demo-radarr", Name: "media-radarr-1", Image: "lscr.io/linuxserver/radarr:latest", State: domain.StateRunning, Health: domain.HealthHealthy,
		Project: "media-stack", Service: "radarr", Ports: []domain.Port{{Private: 7878, Public: 7878, Type: "tcp"}},
		Logs: []string{"[Info] RSS sync completed", "[Info] Import list sync finished", "[Info] Health check passed"}, Created: now.Add(2 * time.Hour),
	})
	p.add(containerSeed{
		ID: "demo-sonarr", Name: "media-sonarr-1", Image: "lscr.io/linuxserver/sonarr:latest", State: domain.StateRunning, Health: domain.HealthHealthy,
		Project: "media-stack", Service: "sonarr", Ports: []domain.Port{{Private: 8989, Public: 8989, Type: "tcp"}},
		Logs: []string{"[Info] Episode search queue idle", "[Info] Series refresh complete"}, Created: now.Add(3 * time.Hour),
	})
	p.add(containerSeed{
		ID: "demo-qbit", Name: "media-qbittorrent-1", Image: "lscr.io/linuxserver/qbittorrent:latest", State: domain.StateRestarting, Health: domain.HealthNone,
		Project: "media-stack", Service: "qbittorrent", Ports: []domain.Port{{Private: 8080, Public: 8081, Type: "tcp"}},
		Restarting: true, RestartCount: 7, Logs: []string{"Web UI started", "VPN interface changed, restarting network stack", "Restart policy requested container restart"}, Created: now.Add(4 * time.Hour),
	})
	p.add(containerSeed{
		ID: "demo-grafana", Name: "monitoring-grafana-1", Image: "grafana/grafana:latest", State: domain.StateRunning, Health: domain.HealthHealthy,
		Project: "monitoring", Service: "grafana", Ports: []domain.Port{{Private: 3000, Public: 3000, Type: "tcp"}},
		Logs: []string{"logger=http.server msg=\"HTTP Server Listen\" address=[::]:3000", "logger=ngalert.scheduler msg=\"starting scheduler\""}, Created: now.Add(5 * time.Hour),
	})
	p.add(containerSeed{
		ID: "demo-prometheus", Name: "monitoring-prometheus-1", Image: "prom/prometheus:latest", State: domain.StateRunning, Health: domain.HealthHealthy,
		Project: "monitoring", Service: "prometheus", Ports: []domain.Port{{Private: 9090, Public: 9090, Type: "tcp"}},
		Logs: []string{"Server is ready to receive web requests.", "Completed loading of configuration file"}, Created: now.Add(6 * time.Hour),
	})
	p.add(containerSeed{
		ID: "demo-nginx", Name: "reverse-proxy", Image: "nginx:alpine", State: domain.StateRunning, Health: domain.HealthNone,
		Ports: []domain.Port{{Private: 80, Public: 8088, Type: "tcp"}, {Private: 443, Public: 8443, Type: "tcp"}},
		Logs:  []string{"GET /healthz 200", "GET /grafana/ 200", "GET /jellyfin/ 200"}, Created: now.Add(7 * time.Hour),
	})
	p.add(containerSeed{
		ID: "demo-postgres", Name: "media-postgres-1", Image: "postgres:16-alpine", State: domain.StateRunning, Health: domain.HealthUnhealthy,
		Project: "media-stack", Service: "postgres", Ports: []domain.Port{{Private: 5432, Public: 0, Type: "tcp"}},
		Mounts: []domain.Mount{{Type: "volume", Source: "media_pgdata", Destination: "/var/lib/postgresql/data", ReadWrite: true}},
		Logs:   []string{"FATAL: the database system is starting up", "LOG: could not bind IPv4 socket: Address already in use", "HINT: previous instance may still be holding the port"}, Created: now.Add(8 * time.Hour),
	})
	p.add(containerSeed{
		ID: "demo-watchtower", Name: "media-watchtower-1", Image: "containrrr/watchtower:latest", State: domain.StateDead, Health: domain.HealthNone,
		Project: "media-stack", Service: "watchtower",
		Logs: []string{"Watchtower started", "level=fatal msg=\"could not do a head request\" error=\"context deadline exceeded\""}, Created: now.Add(9 * time.Hour),
	})
	p.add(containerSeed{
		ID: "demo-adguard", Name: "monitoring-adguard-1", Image: "adguard/adguardhome:latest", State: domain.StateExited, Health: domain.HealthNone,
		Project: "monitoring", Service: "adguard", Ports: []domain.Port{{Private: 53, Public: 53, Type: "udp"}},
		Logs: []string{"AdGuard Home started", "Stopping AdGuard Home", "AdGuard Home is exiting"}, Created: now.Add(10 * time.Hour),
	})
	p.add(containerSeed{
		ID: "demo-portainer", Name: "monitoring-portainer-1", Image: "portainer/portainer-ce:latest", State: domain.StateRunning, Health: domain.HealthNone,
		Project: "monitoring", Service: "portainer", Ports: []domain.Port{{Private: 9000, Public: 9000, Type: "tcp"}},
		RestartCount: 6, Logs: []string{"level=info msg=\"starting Portainer\"", "level=warn msg=\"lost connection to Docker socket, retrying\""}, Created: now.Add(11 * time.Hour),
	})
	p.add(containerSeed{
		ID: "demo-homeassistant", Name: "monitoring-homeassistant-1", Image: "homeassistant/home-assistant:latest", State: domain.StateRunning, Health: domain.HealthUnknown,
		Project: "monitoring", Service: "homeassistant", Ports: []domain.Port{{Private: 8123, Public: 8123, Type: "tcp"}},
		Logs: []string{"Home Assistant Core initialized", "Waiting for integration setup to finish"}, Created: now.Add(12 * time.Hour),
	})
}

type containerSeed struct {
	ID           string
	Name         string
	Image        string
	State        domain.ContainerState
	Health       domain.HealthState
	Project      string
	Service      string
	Ports        []domain.Port
	Mounts       []domain.Mount
	Logs         []string
	Created      time.Time
	Restarting   bool
	RestartCount int
}

func (p *Provider) add(seed containerSeed) {
	labels := map[string]string{
		"org.opencontainers.image.source": "https://example.test/whatthedock/demo",
	}
	if seed.Project != "" {
		labels[domain.LabelComposeProject] = seed.Project
		labels[domain.LabelComposeService] = seed.Service
		labels[domain.LabelComposeContainerNo] = "1"
		labels[domain.LabelComposeConfigFiles] = "/srv/" + seed.Project + "/compose.yml"
	}
	env := []string{"TZ=America/Chicago", "PUID=1000", "PGID=1000"}
	networks := []string{"bridge"}
	if seed.Project != "" {
		networks = []string{seed.Project + "_default"}
	}
	sort.Strings(networks)
	ctr := domain.Container{
		ID:            domain.ResourceID{Host: p.host.ID, ID: seed.ID},
		Name:          seed.Name,
		Image:         seed.Image,
		ImageID:       "sha256:" + strings.ReplaceAll(seed.ID, "-", ""),
		Created:       seed.Created,
		State:         seed.State,
		Status:        statusFor(seed.State, seed.Health),
		Health:        seed.Health,
		Restarting:    seed.Restarting,
		RestartCount:  seed.RestartCount,
		Ports:         seed.Ports,
		Mounts:        seed.Mounts,
		Env:           env,
		Networks:      networks,
		Labels:        labels,
		RestartPolicy: "unless-stopped",
		Compose:       domain.ComposeRef{Project: seed.Project, Service: seed.Service, ContainerNumber: labels[domain.LabelComposeContainerNo], ConfigFiles: labels[domain.LabelComposeConfigFiles]},
	}
	p.containers[seed.ID] = ctr
	p.logs[seed.ID] = seed.Logs
}

func statusFor(state domain.ContainerState, health domain.HealthState) string {
	switch {
	case state == domain.StateRestarting:
		return "Restarting (1) 3 seconds ago"
	case state == domain.StateStopped:
		return "Exited (0) 2 hours ago"
	case health != "":
		return "Up 36 hours (" + string(health) + ")"
	case state == domain.StateRunning:
		return "Up 36 hours"
	default:
		return string(state)
	}
}

var _ app.Provider = (*Provider)(nil)
