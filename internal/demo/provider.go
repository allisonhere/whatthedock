package demo

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/allisonhere/tidedock/internal/app"
	"github.com/allisonhere/tidedock/internal/domain"
)

type Provider struct {
	mu         sync.Mutex
	host       domain.Host
	containers map[string]domain.Container
	logs       map[string][]string
}

func NewProvider() *Provider {
	host := domain.Host{ID: "demo", Name: "demo homelab"}
	provider := &Provider{
		host:       host,
		containers: map[string]domain.Container{},
		logs:       map[string][]string{},
	}
	provider.seed()
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

func (p *Provider) Container(_ context.Context, id domain.ResourceID) (domain.Container, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ctr, ok := p.containers[id.ID]
	if !ok {
		return domain.Container{}, fmt.Errorf("demo container %s no longer exists", id.ID)
	}
	return ctr, nil
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
	p.logs[id.ID] = append(p.logs[id.ID], "container started from TideDock")
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
	p.logs[id.ID] = append(p.logs[id.ID], "container stopped from TideDock")
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
	p.logs[id.ID] = append(p.logs[id.ID], "container restarted from TideDock")
	return nil
}

func (p *Provider) Close() error { return nil }

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
		"org.opencontainers.image.source": "https://example.test/tidedock/demo",
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
