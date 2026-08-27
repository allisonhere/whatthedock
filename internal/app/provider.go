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
	Name          string
	Image         string
	Command       []string
	Env           []string
	Ports         []PortBinding
	Mounts        []MountBinding
	RestartPolicy string
	Start         bool
}

type PortBinding struct {
	HostIP        string
	HostPort      uint16
	ContainerPort uint16
	Protocol      string
}

type MountBinding struct {
	Source      string
	Destination string
	ReadOnly    bool
}
