# WhatTheDock

**A keyboard-first Docker and Compose manager for the terminal.**

WhatTheDock is a new member of the Tide application family, alongside Tide and
TideMail. It uses [TideUI](https://github.com/allisonhere/tideui) for the shared
three-pane terminal shell, themes, pane chrome, row styling, overlays, and other
presentation primitives.

> Early-stage project: this repository currently contains the first working
> vertical slice, not the full planned Docker operations suite.

Screenshot placeholder: coming soon.

## Philosophy

WhatTheDock treats Compose projects as first-class objects. The default view is not
a flat dump of containers; it starts from projects and services, then keeps
standalone containers in a separate section.

The app should work with zero WhatTheDock-specific configuration. Running
`whatthedock` honors Docker's normal environment and context defaults and connects
to the local daemon when Docker is available.

User preferences are stored as JSON in the platform config directory, normally
`~/.config/whatthedock/settings.json` on Linux. Missing or invalid settings fall
back to built-in defaults.

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
| `p` | Show problems |
| `g` | Show stats graphs |
| `/` in logs | Filter visible log lines |
| `e` / `w` / `i` / `a` in logs | Show errors, warnings, info, or all log lines |
| `j` / `k` in logs | Scroll log history |
| `PgUp` / `PgDown` in logs | Page through log history |
| `Home` / `End` in logs | Jump to log start or resume tail |
| `f` in logs | Resume live log tail |
| `n` / `N` in logs | Next/previous log search match |
| `x` / `Esc` in logs | Clear active log filters |
| `T` | Theme picker |
| `,` / `Ctrl+,` | Settings |
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
go run -buildvcs=false ./cmd/whatthedock
```

Run without Docker using the built-in demo homelab:

```bash
go run -buildvcs=false ./cmd/whatthedock --demo
```

You can also use:

```bash
WHATTHEDOCK_PROVIDER=demo go run -buildvcs=false ./cmd/whatthedock
```

Or build a binary:

```bash
go build -buildvcs=false -o whatthedock ./cmd/whatthedock
./whatthedock
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
- Streams selected-container logs through a cancellable reader with color-coded
  timestamps, severity, HTTP methods, and HTTP status codes.
- Supports log filtering, severity quick filters, match navigation, scrollback,
  follow-tail pause/resume, and per-container log view state.
- Shows a problems view for unhealthy, restarting, stopped, dead, high-restart,
  unknown-health, and public-port containers.
- Shows polling stats graphs and sparklines for the selected container, with
  CPU, memory, network, disk I/O, restart, uptime, and PID readouts.
- Includes a grouped settings panel with reset defaults, persisted graph style,
  graph colors, log color mode, deltas, refresh interval, and default activity
  pane.
- Starts, stops, restarts, and refreshes containers.
- Shows actionable Docker connection and permission errors.
- Includes a built-in demo provider for development on machines without Docker.

## Relationship To TideUI

WhatTheDock depends on TideUI for reusable Tide-family TUI presentation:

- three-column layout
- themed panes and borders
- rows and muted/selected states
- status bar
- modal overlays
- terminal theme primitives

Application state, Docker access, filtering, actions, and log lifecycles stay in
WhatTheDock.

## Planned Features

- Docker event stream.
- Exec shell workflow.
- Copy/open actions for ports, labels, mounts, and environment values.
- Deeper problem detection for orphaned, stale, and resource-heavy Docker
  resources.
- Multi-host support for local sockets, Docker contexts, and SSH-backed
  contexts.
- Image update availability checks based on registry metadata rather than
  fragile tag assumptions.
