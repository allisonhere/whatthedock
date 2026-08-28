package app

import (
	"context"
	"io"

	"github.com/allisonhere/whatthedock/internal/domain"
)

type Provider interface {
	Host() domain.Host
	Ping(context.Context) error
	Snapshot(context.Context) (domain.Snapshot, error)
	Events(context.Context) (<-chan domain.ContainerEvent, error)
	Container(context.Context, domain.ResourceID) (domain.Container, error)
	ContainerStats(context.Context, domain.ResourceID) (domain.ContainerStats, error)
	Logs(context.Context, domain.ResourceID, LogOptions) (io.ReadCloser, error)
	CreateContainer(context.Context, ContainerCreateSpec) (domain.ResourceID, error)
	RenameContainer(context.Context, domain.ResourceID, string) error
	StartContainer(context.Context, domain.ResourceID) error
	StopContainer(context.Context, domain.ResourceID) error
	RestartContainer(context.Context, domain.ResourceID) error
	RemoveContainer(ctx context.Context, id domain.ResourceID, force bool) error
	PullImage(ctx context.Context, image string, onProgress func(PullProgress)) error
	Images(context.Context) ([]domain.Image, error)
	RemoveImage(context.Context, string) error
	Networks(context.Context) ([]domain.Network, error)
	// CreateNetwork creates a user-defined bridge network by name — used by
	// Paste (internal/clipboard) when a yanked container's network doesn't
	// already exist on the destination host. Never needed for any of
	// whatthedock's three built-in networks (bridge/host/none), which
	// always exist.
	CreateNetwork(ctx context.Context, name string) error
	RemoveNetwork(context.Context, string) error
	Volumes(context.Context) ([]domain.Volume, error)
	RemoveVolume(context.Context, string) error
	Close() error
}

type LogOptions struct {
	Tail   string
	Follow bool
}

// PullProgress is one update from an in-progress image pull. Raw fields —
// formatting into a display string is presentation logic and belongs in
// internal/ui, not here.
type PullProgress struct {
	Status  string // e.g. "Downloading", "Extracting", "Pull complete"
	ID      string // short layer/blob ID
	Current int64  // bytes so far for this layer (0 if not byte-denominated)
	Total   int64  // bytes total for this layer (0 if unknown)
}

type ContainerCreateSpec struct {
	Name       string
	Image      string
	Command    []string
	Entrypoint []string
	Env        []string
	Ports      []PortBinding
	// ExposedPorts are container-side declarations with no host binding —
	// distinct from a PortBinding whose HostPort happens to be 0 (which
	// Docker would treat as "publish on a random host port", not "don't
	// publish at all").
	ExposedPorts  []ExposedPort
	Mounts        []MountBinding
	Tmpfs         map[string]string // mount target -> options string
	Networks      []NetworkAttachment
	RestartPolicy string
	Start         bool

	Hostname       string
	WorkingDir     string
	User           string
	Labels         map[string]string
	Privileged     bool
	CapAdd         []string
	CapDrop        []string
	Devices        []domain.Device
	MemoryBytes    int64
	NanoCPUs       int64
	StopSignal     string
	StopTimeout    *int
	DNS            []string
	DNSSearch      []string
	ReadonlyRootfs bool
	SecurityOpt    []string
	LogDriver      string
	LogOptions     map[string]string
	Healthcheck    *domain.HealthCheck
}

type PortBinding struct {
	HostIP        string
	HostPort      uint16
	ContainerPort uint16
	Protocol      string
}

type ExposedPort struct {
	ContainerPort uint16
	Protocol      string
}

// MountBinding is a bind or named-volume mount. Type, when set, overrides
// the historical "absolute path means bind, else volume" inference
// (docker.createMounts) with the mount's real, already-known kind — needed
// for Paste, whose portable model always knows the source mount's real
// type rather than having to guess it back from a path shape. Existing
// callers that never set it (create.go's parseCreateMounts) are unaffected.
type MountBinding struct {
	Type        string // "bind", "volume", or "" to infer from Source
	Source      string
	Destination string
	ReadOnly    bool
}

type NetworkAttachment struct {
	Name    string
	Aliases []string
}
