package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	imagetypes "github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	networktypes "github.com/docker/docker/api/types/network"
	volumetypes "github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/docker/go-connections/nat"

	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/domain"
)

type LocalProvider struct {
	host domain.Host
	cli  *client.Client
}

func NewLocalProvider() (*LocalProvider, error) {
	return NewProvider("local", "local", "")
}

func NewProvider(id, name, dockerHost string) (*LocalProvider, error) {
	opts := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
	if dockerHost != "" {
		opts = append([]client.Opt{client.WithHost(dockerHost)}, client.WithAPIVersionNegotiation())
	}
	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, err
	}
	if id == "" {
		id = "local"
	}
	if name == "" {
		name = id
	}
	return &LocalProvider{host: domain.Host{ID: domain.HostID(id), Name: name}, cli: cli}, nil
}

func (p *LocalProvider) Host() domain.Host { return p.host }

func (p *LocalProvider) Ping(ctx context.Context) error {
	_, err := p.cli.Ping(ctx)
	return err
}

func (p *LocalProvider) Snapshot(ctx context.Context) (domain.Snapshot, error) {
	items, err := p.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return domain.Snapshot{Host: p.host, Refreshed: time.Now()}, err
	}
	containers := make([]domain.Container, 0, len(items))
	for _, item := range items {
		containers = append(containers, FromSummary(p.host.ID, item))
	}
	return domain.BuildSnapshot(p.host, containers, time.Now()), nil
}

func (p *LocalProvider) Events(ctx context.Context) (<-chan domain.ContainerEvent, error) {
	msgs, errs := p.cli.Events(ctx, events.ListOptions{
		Filters: filters.NewArgs(filters.Arg("type", string(events.ContainerEventType))),
	})
	out := make(chan domain.ContainerEvent)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case err, ok := <-errs:
				if !ok || err != nil {
					return
				}
			case msg, ok := <-msgs:
				if !ok {
					return
				}
				select {
				case out <- FromEventMessage(p.host.ID, msg):
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

func (p *LocalProvider) Container(ctx context.Context, id domain.ResourceID) (domain.Container, error) {
	inspect, err := p.cli.ContainerInspect(ctx, id.ID)
	if err != nil {
		return domain.Container{}, err
	}
	ctr := FromInspect(p.host.ID, inspect)
	if inspect.Image != "" {
		// Best-effort: a container's own inspect has no RepoDigests of its
		// own (only its content-addressed image ID) — Paste's "image digest
		// if available" needs the image's, so this is a second lookup.
		// Never fails the container inspect itself if the image lookup
		// doesn't work out (image since removed, digest never pulled, etc).
		if imageInspect, err := p.cli.ImageInspect(ctx, inspect.Image); err == nil && len(imageInspect.RepoDigests) > 0 {
			ctr.ImageDigest = imageInspect.RepoDigests[0]
		}
	}
	return ctr, nil
}

func (p *LocalProvider) ContainerStats(ctx context.Context, id domain.ResourceID) (domain.ContainerStats, error) {
	reader, err := p.cli.ContainerStatsOneShot(ctx, id.ID)
	if err != nil {
		return domain.ContainerStats{}, err
	}
	defer reader.Body.Close()
	var stats container.StatsResponse
	if err := json.NewDecoder(reader.Body).Decode(&stats); err != nil {
		return domain.ContainerStats{}, err
	}
	return FromStats(p.host.ID, id.ID, stats), nil
}

func (p *LocalProvider) Logs(ctx context.Context, id domain.ResourceID, options app.LogOptions) (io.ReadCloser, error) {
	tail := options.Tail
	if tail == "" {
		tail = "200"
	}
	return p.cli.ContainerLogs(ctx, id.ID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     options.Follow,
		Tail:       tail,
		Timestamps: true,
	})
}

func (p *LocalProvider) CreateContainer(ctx context.Context, spec app.ContainerCreateSpec) (domain.ResourceID, error) {
	exposedPorts, portBindings, err := createPortBindings(spec.Ports, spec.ExposedPorts)
	if err != nil {
		return domain.ResourceID{}, err
	}
	var healthcheck *container.HealthConfig
	if spec.Healthcheck != nil {
		healthcheck = &container.HealthConfig{
			Test:        spec.Healthcheck.Test,
			Interval:    spec.Healthcheck.Interval,
			Timeout:     spec.Healthcheck.Timeout,
			Retries:     spec.Healthcheck.Retries,
			StartPeriod: spec.Healthcheck.StartPeriod,
		}
	}
	resp, err := p.cli.ContainerCreate(ctx,
		&container.Config{
			Hostname:     spec.Hostname,
			Image:        spec.Image,
			Cmd:          spec.Command,
			Entrypoint:   spec.Entrypoint,
			Env:          spec.Env,
			ExposedPorts: exposedPorts,
			Labels:       spec.Labels,
			WorkingDir:   spec.WorkingDir,
			User:         spec.User,
			StopSignal:   spec.StopSignal,
			StopTimeout:  spec.StopTimeout,
			Healthcheck:  healthcheck,
		},
		&container.HostConfig{
			PortBindings:   portBindings,
			Mounts:         createMounts(spec.Mounts),
			Tmpfs:          spec.Tmpfs,
			RestartPolicy:  container.RestartPolicy{Name: container.RestartPolicyMode(spec.RestartPolicy)},
			Privileged:     spec.Privileged,
			CapAdd:         spec.CapAdd,
			CapDrop:        spec.CapDrop,
			DNS:            spec.DNS,
			DNSSearch:      spec.DNSSearch,
			ReadonlyRootfs: spec.ReadonlyRootfs,
			SecurityOpt:    spec.SecurityOpt,
			LogConfig:      container.LogConfig{Type: spec.LogDriver, Config: spec.LogOptions},
			Resources: container.Resources{
				Memory:   spec.MemoryBytes,
				NanoCPUs: spec.NanoCPUs,
				Devices:  createDevices(spec.Devices),
			},
		},
		createNetworkingConfig(spec.Networks),
		nil,
		spec.Name,
	)
	if err != nil {
		return domain.ResourceID{}, err
	}
	id := domain.ResourceID{Host: p.host.ID, ID: resp.ID}
	if spec.Start {
		if err := p.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
			// A container that exists but never started (the common case:
			// a port already bound by something else) is pure clutter —
			// it can't be selected/used for anything, and its name blocks
			// retrying with the same one until it's removed by hand. Best-
			// effort cleanup with the original start error, not the
			// removal outcome: if this also fails, the start error is
			// still what the user needs to see and act on.
			_ = p.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
			return domain.ResourceID{}, err
		}
	}
	return id, nil
}

func (p *LocalProvider) RenameContainer(ctx context.Context, id domain.ResourceID, name string) error {
	return p.cli.ContainerRename(ctx, id.ID, name)
}

func createPortBindings(bindings []app.PortBinding, exposedOnly []app.ExposedPort) (nat.PortSet, nat.PortMap, error) {
	exposed := nat.PortSet{}
	ports := nat.PortMap{}
	for _, binding := range bindings {
		protocol := strings.TrimSpace(binding.Protocol)
		if protocol == "" {
			protocol = "tcp"
		}
		port := nat.Port(fmt.Sprintf("%d/%s", binding.ContainerPort, protocol))
		exposed[port] = struct{}{}
		host := nat.PortBinding{HostPort: fmt.Sprintf("%d", binding.HostPort)}
		if strings.TrimSpace(binding.HostIP) != "" {
			host.HostIP = strings.TrimSpace(binding.HostIP)
		}
		ports[port] = append(ports[port], host)
	}
	for _, expose := range exposedOnly {
		protocol := strings.TrimSpace(expose.Protocol)
		if protocol == "" {
			protocol = "tcp"
		}
		exposed[nat.Port(fmt.Sprintf("%d/%s", expose.ContainerPort, protocol))] = struct{}{}
	}
	return exposed, ports, nil
}

// createMounts converts spec.Mounts to Docker mount points. binding.Type,
// when set, is authoritative (see app.MountBinding's doc comment); the
// absolute-path heuristic only covers the older callers that never set it.
func createMounts(bindings []app.MountBinding) []mount.Mount {
	mounts := make([]mount.Mount, 0, len(bindings))
	for _, binding := range bindings {
		mountType := mount.Type(binding.Type)
		if mountType == "" {
			mountType = mount.TypeVolume
			if filepath.IsAbs(binding.Source) {
				mountType = mount.TypeBind
			}
		}
		mounts = append(mounts, mount.Mount{
			Type:     mountType,
			Source:   binding.Source,
			Target:   binding.Destination,
			ReadOnly: binding.ReadOnly,
		})
	}
	return mounts
}

func createDevices(devices []domain.Device) []container.DeviceMapping {
	if len(devices) == 0 {
		return nil
	}
	out := make([]container.DeviceMapping, 0, len(devices))
	for _, d := range devices {
		out = append(out, container.DeviceMapping{
			PathOnHost:        d.PathOnHost,
			PathInContainer:   d.PathInContainer,
			CgroupPermissions: d.CgroupPermissions,
		})
	}
	return out
}

// createNetworkingConfig builds the *network.NetworkingConfig ContainerCreate
// needs to actually attach non-default networks (and their aliases) at
// create time — previously always nil, so a create never attached anything
// but the implicit default bridge network. nil for zero networks (the
// common, pre-existing case) rather than an empty-but-non-nil map, matching
// what every caller that never sets spec.Networks already implicitly relied
// on.
func createNetworkingConfig(networks []app.NetworkAttachment) *networktypes.NetworkingConfig {
	if len(networks) == 0 {
		return nil
	}
	endpoints := make(map[string]*networktypes.EndpointSettings, len(networks))
	for _, n := range networks {
		endpoints[n.Name] = &networktypes.EndpointSettings{Aliases: append([]string(nil), n.Aliases...)}
	}
	return &networktypes.NetworkingConfig{EndpointsConfig: endpoints}
}

func FromStats(host domain.HostID, containerID string, stats container.StatsResponse) domain.ContainerStats {
	networkRx, networkTx := uint64(0), uint64(0)
	for _, network := range stats.Networks {
		networkRx += network.RxBytes
		networkTx += network.TxBytes
	}
	blockRead, blockWrite := blockIO(stats.BlkioStats, stats.StorageStats)
	return domain.ContainerStats{
		ID:          domain.ResourceID{Host: host, ID: containerID},
		Read:        stats.Read,
		CPUPercent:  cpuPercent(stats),
		MemoryUsage: memoryUsage(stats.MemoryStats),
		MemoryLimit: stats.MemoryStats.Limit,
		NetworkRx:   networkRx,
		NetworkTx:   networkTx,
		BlockRead:   blockRead,
		BlockWrite:  blockWrite,
		PIDs:        stats.PidsStats.Current,
	}
}

func cpuPercent(stats container.StatsResponse) float64 {
	if stats.CPUStats.CPUUsage.TotalUsage <= stats.PreCPUStats.CPUUsage.TotalUsage ||
		stats.CPUStats.SystemUsage <= stats.PreCPUStats.SystemUsage {
		return 0
	}
	cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage - stats.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(stats.CPUStats.SystemUsage - stats.PreCPUStats.SystemUsage)
	onlineCPUs := float64(stats.CPUStats.OnlineCPUs)
	if onlineCPUs == 0 {
		onlineCPUs = float64(len(stats.CPUStats.CPUUsage.PercpuUsage))
	}
	if cpuDelta <= 0 || systemDelta <= 0 || onlineCPUs <= 0 {
		return 0
	}
	return cpuDelta / systemDelta * onlineCPUs * 100
}

func memoryUsage(stats container.MemoryStats) uint64 {
	usage := stats.Usage
	for _, key := range []string{"inactive_file", "total_inactive_file", "cache"} {
		if cache := stats.Stats[key]; cache > 0 && cache < usage {
			return usage - cache
		}
	}
	return usage
}

func blockIO(linux container.BlkioStats, windows container.StorageStats) (uint64, uint64) {
	read, write := windows.ReadSizeBytes, windows.WriteSizeBytes
	for _, entry := range linux.IoServiceBytesRecursive {
		switch strings.ToLower(entry.Op) {
		case "read":
			read += entry.Value
		case "write":
			write += entry.Value
		}
	}
	return read, write
}

func FromEventMessage(host domain.HostID, msg events.Message) domain.ContainerEvent {
	eventTime := time.Unix(msg.Time, 0)
	if msg.TimeNano != 0 {
		eventTime = time.Unix(0, msg.TimeNano)
	}
	return domain.ContainerEvent{
		ID:     domain.ResourceID{Host: host, ID: msg.Actor.ID},
		Action: string(msg.Action),
		Time:   eventTime,
	}
}

func (p *LocalProvider) StartContainer(ctx context.Context, id domain.ResourceID) error {
	return p.cli.ContainerStart(ctx, id.ID, container.StartOptions{})
}

func (p *LocalProvider) StopContainer(ctx context.Context, id domain.ResourceID) error {
	return p.cli.ContainerStop(ctx, id.ID, container.StopOptions{})
}

func (p *LocalProvider) RestartContainer(ctx context.Context, id domain.ResourceID) error {
	return p.cli.ContainerRestart(ctx, id.ID, container.StopOptions{})
}

func (p *LocalProvider) RemoveContainer(ctx context.Context, id domain.ResourceID, force bool) error {
	return p.cli.ContainerRemove(ctx, id.ID, container.RemoveOptions{Force: force})
}

// PullImage decodes the pull's streamed JSON messages itself (rather than
// delegating to jsonmessage.DisplayJSONMessagesStream, which formats them
// for a human-readable io.Writer) so onProgress can drive a real status-bar
// indicator instead of the messages being discarded. Error detection
// mirrors DisplayJSONMessagesStream exactly (see JSONMessage.Display in the
// vendored source): a message's Error field fails the pull; nothing else
// does — in particular the deprecated ErrorMessage string field is never
// checked, matching today's behavior.
func (p *LocalProvider) PullImage(ctx context.Context, image string, onProgress func(app.PullProgress)) error {
	rc, err := p.cli.ImagePull(ctx, image, imagetypes.PullOptions{})
	if err != nil {
		return err
	}
	defer rc.Close()
	dec := json.NewDecoder(rc)
	for {
		var msg jsonmessage.JSONMessage
		if err := dec.Decode(&msg); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if msg.Error != nil {
			return msg.Error
		}
		if onProgress != nil {
			progress := app.PullProgress{Status: msg.Status, ID: msg.ID}
			if msg.Progress != nil {
				progress.Current = msg.Progress.Current
				progress.Total = msg.Progress.Total
			}
			onProgress(progress)
		}
	}
}

func (p *LocalProvider) Images(ctx context.Context) ([]domain.Image, error) {
	items, err := p.cli.ImageList(ctx, imagetypes.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	images := make([]domain.Image, 0, len(items))
	for _, item := range items {
		images = append(images, domain.Image{
			ID:          domain.ResourceID{Host: p.host.ID, ID: item.ID},
			RepoTags:    append([]string(nil), item.RepoTags...),
			RepoDigests: append([]string(nil), item.RepoDigests...),
			Size:        item.Size,
			Created:     time.Unix(item.Created, 0),
			Containers:  item.Containers,
			Dangling:    len(item.RepoTags) == 0,
		})
	}
	sort.Slice(images, func(i, j int) bool { return images[i].DisplayName() < images[j].DisplayName() })
	return images, nil
}

// RemoveImage deliberately leaves Force false so Docker protects images that
// are still referenced by a container or another image tag.
func (p *LocalProvider) RemoveImage(ctx context.Context, ref string) error {
	_, err := p.cli.ImageRemove(ctx, ref, imagetypes.RemoveOptions{})
	return err
}

// Networks inspects each listed network before deriving attachment counts.
// The Docker client currently aliases network.Summary to network.Inspect,
// but its own docs allow list responses to diverge from full inspect data;
// removable/delete UI should not trust a possibly-sparse list response.
func (p *LocalProvider) Networks(ctx context.Context) ([]domain.Network, error) {
	items, err := p.cli.NetworkList(ctx, networktypes.ListOptions{})
	if err != nil {
		return nil, err
	}
	networks := make([]domain.Network, 0, len(items))
	for _, item := range items {
		inspect, err := p.cli.NetworkInspect(ctx, item.ID, networktypes.InspectOptions{Verbose: true})
		if err != nil {
			return nil, err
		}
		subnet := ""
		if len(inspect.IPAM.Config) > 0 {
			subnet = inspect.IPAM.Config[0].Subnet
		}
		networks = append(networks, domain.Network{
			ID:         inspect.ID,
			Name:       inspect.Name,
			Driver:     inspect.Driver,
			Scope:      inspect.Scope,
			Containers: len(inspect.Containers),
			Subnet:     subnet,
		})
	}
	sort.Slice(networks, func(i, j int) bool { return networks[i].Name < networks[j].Name })
	return networks, nil
}

func (p *LocalProvider) RemoveNetwork(ctx context.Context, id string) error {
	return p.cli.NetworkRemove(ctx, id)
}

// CreateNetwork creates a plain user-defined bridge network by name — the
// same shape `docker network create <name>` produces. Used by Paste when a
// yanked container's network doesn't already exist on the destination host.
func (p *LocalProvider) CreateNetwork(ctx context.Context, name string) error {
	_, err := p.cli.NetworkCreate(ctx, name, networktypes.CreateOptions{})
	return err
}

// Volumes cross-references VolumeList against a fresh ContainerList (whose
// summary Mounts already carry each mount's Type and volume Name) to derive
// InUse, rather than trusting VolumeList's own usage data, which Docker only
// populates when explicitly requested and can be slow to compute.
func (p *LocalProvider) Volumes(ctx context.Context) ([]domain.Volume, error) {
	resp, err := p.cli.VolumeList(ctx, volumetypes.ListOptions{})
	if err != nil {
		return nil, err
	}
	containers, err := p.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	inUse := map[string]bool{}
	for _, ctr := range containers {
		for _, m := range ctr.Mounts {
			if m.Type == mount.TypeVolume && m.Name != "" {
				inUse[m.Name] = true
			}
		}
	}
	volumes := make([]domain.Volume, 0, len(resp.Volumes))
	for _, item := range resp.Volumes {
		if item == nil {
			continue
		}
		volumes = append(volumes, domain.Volume{
			Name:       item.Name,
			Driver:     item.Driver,
			Mountpoint: item.Mountpoint,
			InUse:      inUse[item.Name],
		})
	}
	sort.Slice(volumes, func(i, j int) bool { return volumes[i].Name < volumes[j].Name })
	return volumes, nil
}

// RemoveVolume deliberately leaves force false, matching RemoveImage's
// default, so Docker protects volumes still referenced by a container.
func (p *LocalProvider) RemoveVolume(ctx context.Context, name string) error {
	return p.cli.VolumeRemove(ctx, name, false)
}

func (p *LocalProvider) Close() error {
	if p.cli == nil {
		return nil
	}
	return p.cli.Close()
}

func FromSummary(host domain.HostID, item container.Summary) domain.Container {
	name := ""
	if len(item.Names) > 0 {
		name = strings.TrimPrefix(item.Names[0], "/")
	}
	created := time.Unix(item.Created, 0)
	labels := copyLabels(item.Labels)
	return domain.Container{
		ID:         domain.ResourceID{Host: host, ID: item.ID},
		Name:       name,
		Image:      item.Image,
		ImageID:    item.ImageID,
		Command:    item.Command,
		Created:    created,
		State:      normalizeState(item.State),
		Status:     item.Status,
		Health:     healthFromStatus(item.Status),
		Restarting: item.State == "restarting",
		Ports:      fromPorts(item.Ports),
		Labels:     labels,
		Compose:    composeFromLabels(labels),
	}
}

func FromInspect(host domain.HostID, inspect container.InspectResponse) domain.Container {
	labels := map[string]string{}
	if inspect.Config != nil {
		labels = copyLabels(inspect.Config.Labels)
	}
	ctr := domain.Container{
		ID:      domain.ResourceID{Host: host, ID: inspect.ID},
		Name:    strings.TrimPrefix(inspect.Name, "/"),
		Labels:  labels,
		Compose: composeFromLabels(labels),
	}
	if inspect.Config != nil {
		ctr.Image = inspect.Config.Image
		ctr.Env = append([]string(nil), inspect.Config.Env...)
		ctr.Command = domain.JoinShellWords([]string(inspect.Config.Cmd))
		ctr.Entrypoint = domain.JoinShellWords([]string(inspect.Config.Entrypoint))
		ctr.Hostname = inspect.Config.Hostname
		ctr.WorkingDir = inspect.Config.WorkingDir
		ctr.User = inspect.Config.User
		ctr.StopSignal = inspect.Config.StopSignal
		ctr.StopTimeout = inspect.Config.StopTimeout
		for port := range inspect.Config.ExposedPorts {
			ctr.ExposedPorts = append(ctr.ExposedPorts, domain.Port{Private: uint16(port.Int()), Type: port.Proto()})
		}
		sort.Slice(ctr.ExposedPorts, func(i, j int) bool { return ctr.ExposedPorts[i].Private < ctr.ExposedPorts[j].Private })
		if inspect.Config.Healthcheck != nil {
			ctr.HealthCheck = &domain.HealthCheck{
				Test:        append([]string(nil), inspect.Config.Healthcheck.Test...),
				Interval:    inspect.Config.Healthcheck.Interval,
				Timeout:     inspect.Config.Healthcheck.Timeout,
				Retries:     inspect.Config.Healthcheck.Retries,
				StartPeriod: inspect.Config.Healthcheck.StartPeriod,
			}
		}
	}
	if inspect.Image != "" {
		ctr.ImageID = inspect.Image
	}
	if inspect.ContainerJSONBase != nil {
		ctr.Created = parseDockerTime(inspect.Created)
	}
	if inspect.State != nil {
		ctr.State = normalizeState(inspect.State.Status)
		ctr.Status = inspect.State.Status
		ctr.Restarting = inspect.State.Restarting
		ctr.RestartCount = inspect.RestartCount
		if inspect.State.Health != nil {
			ctr.Health = domain.HealthState(inspect.State.Health.Status)
		}
	}
	if inspect.HostConfig != nil {
		hc := inspect.HostConfig
		ctr.RestartPolicy = string(hc.RestartPolicy.Name)
		ctr.Privileged = hc.Privileged
		ctr.CapAdd = append([]string(nil), []string(hc.CapAdd)...)
		ctr.CapDrop = append([]string(nil), []string(hc.CapDrop)...)
		ctr.MemoryBytes = hc.Memory
		ctr.NanoCPUs = hc.NanoCPUs
		ctr.DNS = append([]string(nil), hc.DNS...)
		ctr.DNSSearch = append([]string(nil), hc.DNSSearch...)
		ctr.ReadonlyRootfs = hc.ReadonlyRootfs
		ctr.SecurityOpt = append([]string(nil), hc.SecurityOpt...)
		ctr.LogDriver = hc.LogConfig.Type
		if len(hc.LogConfig.Config) > 0 {
			ctr.LogOptions = copyLabels(hc.LogConfig.Config)
		}
		if len(hc.Tmpfs) > 0 {
			ctr.Tmpfs = copyLabels(hc.Tmpfs)
		}
		for _, d := range hc.Devices {
			ctr.Devices = append(ctr.Devices, domain.Device{
				PathOnHost:        d.PathOnHost,
				PathInContainer:   d.PathInContainer,
				CgroupPermissions: d.CgroupPermissions,
			})
		}
	}
	ctr.Mounts = fromMounts(inspect.Mounts)
	if inspect.NetworkSettings != nil && inspect.NetworkSettings.Networks != nil {
		ctr.NetworkAliases = map[string][]string{}
		for name, ep := range inspect.NetworkSettings.Networks {
			ctr.Networks = append(ctr.Networks, name)
			if ep != nil && len(ep.Aliases) > 0 {
				ctr.NetworkAliases[name] = append([]string(nil), ep.Aliases...)
			}
		}
		sort.Strings(ctr.Networks)
	}
	if inspect.NetworkSettings != nil {
		for port, bindings := range inspect.NetworkSettings.Ports {
			private := uint16(port.Int())
			proto := port.Proto()
			if len(bindings) == 0 {
				ctr.Ports = append(ctr.Ports, domain.Port{Private: private, Type: proto})
				continue
			}
			for _, binding := range bindings {
				ctr.Ports = append(ctr.Ports, domain.Port{IP: binding.HostIP, Private: private, Public: parsePort(binding.HostPort), Type: proto})
			}
		}
	}
	sort.Slice(ctr.Ports, func(i, j int) bool {
		if ctr.Ports[i].Private == ctr.Ports[j].Private {
			return ctr.Ports[i].Public < ctr.Ports[j].Public
		}
		return ctr.Ports[i].Private < ctr.Ports[j].Private
	})
	return ctr
}

func composeFromLabels(labels map[string]string) domain.ComposeRef {
	return domain.ComposeRef{
		Project:         labels[domain.LabelComposeProject],
		Service:         labels[domain.LabelComposeService],
		ContainerNumber: labels[domain.LabelComposeContainerNo],
		ConfigFiles:     labels[domain.LabelComposeConfigFiles],
	}
}

func normalizeState(state string) domain.ContainerState {
	switch strings.ToLower(state) {
	case "running":
		return domain.StateRunning
	case "paused":
		return domain.StatePaused
	case "restarting":
		return domain.StateRestarting
	case "exited", "created", "removing":
		return domain.StateStopped
	case "dead":
		return domain.StateDead
	default:
		return domain.StateUnknown
	}
}

func healthFromStatus(status string) domain.HealthState {
	lower := strings.ToLower(status)
	switch {
	case strings.Contains(lower, "unhealthy"):
		return domain.HealthUnhealthy
	case strings.Contains(lower, "healthy"):
		return domain.HealthHealthy
	case strings.Contains(lower, "health: starting"):
		return domain.HealthStarting
	default:
		return domain.HealthNone
	}
}

func fromPorts(ports []container.Port) []domain.Port {
	out := make([]domain.Port, 0, len(ports))
	for _, port := range ports {
		out = append(out, domain.Port{
			IP:      port.IP,
			Private: uint16(port.PrivatePort),
			Public:  uint16(port.PublicPort),
			Type:    port.Type,
		})
	}
	return out
}

func fromMounts(mounts []container.MountPoint) []domain.Mount {
	out := make([]domain.Mount, 0, len(mounts))
	for _, mount := range mounts {
		out = append(out, domain.Mount{
			Type:        string(mount.Type),
			Source:      mount.Source,
			Destination: mount.Destination,
			Mode:        mount.Mode,
			ReadWrite:   mount.RW,
		})
	}
	return out
}

func copyLabels(labels map[string]string) map[string]string {
	out := make(map[string]string, len(labels))
	for key, value := range labels {
		out[key] = value
	}
	return out
}

func parseDockerTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return t
}

func parsePort(value string) uint16 {
	var port uint16
	_, _ = fmt.Sscanf(value, "%d", &port)
	return port
}
