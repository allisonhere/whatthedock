# TideDock

**A keyboard-first Docker and Compose manager for the terminal.**

TideDock is a new member of the Tide application family, alongside Tide and
TideMail. It uses [TideUI](https://github.com/allisonhere/tideui) for the shared
three-pane terminal shell, themes, pane chrome, row styling, overlays, and other
presentation primitives.

> Early-stage project: this repository currently contains the first working
> vertical slice, not the full planned Docker operations suite.

Screenshot placeholder: coming soon.

## Philosophy

TideDock treats Compose projects as first-class objects. The default view is not
a flat dump of containers; it starts from projects and services, then keeps
standalone containers in a separate section.

The app should work with zero TideDock-specific configuration. Running
`tidedock` honors Docker's normal environment and context defaults and connects
to the local daemon when Docker is available.

## Current Controls

| Key | Action |
|---|---|
| `j` / `Down` | Move down |
| `k` / `Up` | Move up |
| `Enter` | Select/open |
| `Space` | Expand/collapse Compose project |
| `/` | Filter projects, services, and containers |
| `s` | Start/stop selected container |
| `r` | Refresh Docker state |
| `Alt+r` | Restart selected container |
| `l` | Focus logs |
| `T` | Theme picker |
| `Ctrl+K` | Command palette |
| `?` | Keyboard help |
| `q` | Quit |

Mouse row selection and wheel navigation are enabled where the terminal and
Bubble Tea support them.

## Build

```bash
go mod download
go build ./...
```

Run locally:

```bash
go run -buildvcs=false ./cmd/tidedock
```

Run without Docker using the built-in demo homelab:

```bash
go run -buildvcs=false ./cmd/tidedock --demo
```

You can also use:

```bash
TIDEDOCK_PROVIDER=demo go run -buildvcs=false ./cmd/tidedock
```

Or build a binary:

```bash
go build -buildvcs=false -o tidedock ./cmd/tidedock
./tidedock
```

## Development

```bash
make fmt
make test
make vet
make build
```

## Current Features

- Connects through the official Docker Go client using Docker defaults.
- Discovers Compose projects from standard Compose labels.
- Shows standalone containers separately.
- Supports project collapse/expand and fast substring filtering.
- Displays useful selected-container details without dumping Docker JSON.
- Streams selected-container logs through a cancellable reader.
- Starts, stops, restarts, and refreshes containers.
- Shows actionable Docker connection and permission errors.
- Includes a built-in demo provider for development on machines without Docker.

## Relationship To TideUI

TideDock depends on TideUI for reusable Tide-family TUI presentation:

- three-column layout
- themed panes and borders
- rows and muted/selected states
- status bar
- modal overlays
- terminal theme primitives

Application state, Docker access, filtering, actions, and log lifecycles stay in
TideDock.

## Planned Features

- Problems view for unhealthy, restarting, orphaned, stale, and resource-heavy
  Docker resources.
- Stats graphs and sparklines.
- Docker event stream.
- Exec shell workflow.
- Copy/open actions for ports, labels, mounts, and environment values.
- Multi-host support for local sockets, Docker contexts, and SSH-backed
  contexts.
- Image update availability checks based on registry metadata rather than
  fragile tag assumptions.
