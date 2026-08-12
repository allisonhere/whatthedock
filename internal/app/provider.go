package app

import (
	"context"
	"io"

	"github.com/allisonhere/tidedock/internal/domain"
)

type Provider interface {
	Host() domain.Host
	Ping(context.Context) error
	Snapshot(context.Context) (domain.Snapshot, error)
	Container(context.Context, domain.ResourceID) (domain.Container, error)
	Logs(context.Context, domain.ResourceID, LogOptions) (io.ReadCloser, error)
	StartContainer(context.Context, domain.ResourceID) error
	StopContainer(context.Context, domain.ResourceID) error
	RestartContainer(context.Context, domain.ResourceID) error
	Close() error
}

type LogOptions struct {
	Tail   string
	Follow bool
}
