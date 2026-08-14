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
	StartContainer(context.Context, domain.ResourceID) error
	StopContainer(context.Context, domain.ResourceID) error
	RestartContainer(context.Context, domain.ResourceID) error
	Close() error
}

type LogOptions struct {
	Tail   string
	Follow bool
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
