package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	LabelComposeProject     = "com.docker.compose.project"
	LabelComposeService     = "com.docker.compose.service"
	LabelComposeContainerNo = "com.docker.compose.container-number"
	LabelComposeConfigFiles = "com.docker.compose.project.config_files"
)

type HostID string

type ResourceID struct {
	Host HostID
	ID   string
}

func (id ResourceID) String() string {
	if id.Host == "" {
		return id.ID
	}
	return fmt.Sprintf("%s/%s", id.Host, id.ID)
}

type Host struct {
	ID   HostID
	Name string
}

type ContainerEvent struct {
	ID     ResourceID
	Action string
	Time   time.Time
}

type ContainerState string

const (
	StateUnknown    ContainerState = "unknown"
	StateRunning    ContainerState = "running"
	StateStopped    ContainerState = "stopped"
	StatePaused     ContainerState = "paused"
	StateRestarting ContainerState = "restarting"
	StateExited     ContainerState = "exited"
	StateDead       ContainerState = "dead"
)

type HealthState string

const (
	HealthNone      HealthState = ""
	HealthStarting  HealthState = "starting"
	HealthHealthy   HealthState = "healthy"
	HealthUnhealthy HealthState = "unhealthy"
	HealthUnknown   HealthState = "unknown"
)

type Port struct {
	IP      string
	Private uint16
	Public  uint16
	Type    string
}

type Mount struct {
	Type        string
	Source      string
	Destination string
	Mode        string
	ReadWrite   bool
}

type HealthCheck struct {
	Test        []string
	Interval    time.Duration
	Timeout     time.Duration
	Retries     int
	StartPeriod time.Duration
}

type Container struct {
	ID            ResourceID
	Name          string
	Image         string
	ImageID       string
	Command       string
	Created       time.Time
	State         ContainerState
	Status        string
	Health        HealthState
	Restarting    bool
	RestartCount  int
	Ports         []Port
	Mounts        []Mount
	Env           []string
	Networks      []string
	Labels        map[string]string
	RestartPolicy string
	Compose       ComposeRef
	HealthCheck   *HealthCheck
}

type ContainerStats struct {
	ID          ResourceID
	Read        time.Time
	CPUPercent  float64
	MemoryUsage uint64
	MemoryLimit uint64
	NetworkRx   uint64
	NetworkTx   uint64
	BlockRead   uint64
	BlockWrite  uint64
	PIDs        uint64
}

type ComposeRef struct {
	Project         string
	Service         string
	ContainerNumber string
	ConfigFiles     string
}

func (c Container) ShortID() string {
	id := c.ID.ID
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func (c Container) DisplayName() string {
	return strings.TrimPrefix(c.Name, "/")
}

func (c Container) IsRunning() bool {
	return c.State == StateRunning || c.State == StateRestarting || c.Restarting
}

func (c Container) SearchText() string {
	parts := []string{c.DisplayName(), c.Image, string(c.State), string(c.Health), c.Compose.Project, c.Compose.Service}
	return strings.ToLower(strings.Join(parts, " "))
}

type Service struct {
	Name       string
	Containers []Container
}

type Project struct {
	Name     string
	Services []Service
}

type Snapshot struct {
	Host       Host
	Projects   []Project
	Standalone []Container
	Refreshed  time.Time
}

func BuildSnapshot(host Host, containers []Container, refreshed time.Time) Snapshot {
	projects := map[string]map[string][]Container{}
	var standalone []Container
	for _, ctr := range containers {
		if ctr.Compose.Project == "" {
			standalone = append(standalone, ctr)
			continue
		}
		if projects[ctr.Compose.Project] == nil {
			projects[ctr.Compose.Project] = map[string][]Container{}
		}
		service := ctr.Compose.Service
		if service == "" {
			service = ctr.DisplayName()
		}
		projects[ctr.Compose.Project][service] = append(projects[ctr.Compose.Project][service], ctr)
	}

	out := Snapshot{Host: host, Refreshed: refreshed, Standalone: sortContainers(standalone)}
	for projectName, services := range projects {
		project := Project{Name: projectName}
		for serviceName, serviceContainers := range services {
			project.Services = append(project.Services, Service{Name: serviceName, Containers: sortContainers(serviceContainers)})
		}
		sort.Slice(project.Services, func(i, j int) bool { return project.Services[i].Name < project.Services[j].Name })
		out.Projects = append(out.Projects, project)
	}
	sort.Slice(out.Projects, func(i, j int) bool { return out.Projects[i].Name < out.Projects[j].Name })
	return out
}

func sortContainers(containers []Container) []Container {
	out := append([]Container(nil), containers...)
	sort.Slice(out, func(i, j int) bool {
		left, right := out[i], out[j]
		if left.Compose.Service != right.Compose.Service {
			return left.Compose.Service < right.Compose.Service
		}
		return left.DisplayName() < right.DisplayName()
	})
	return out
}
