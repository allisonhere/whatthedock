# WhatTheDock

![WHAT THE DOCK?! logo](assets/whatthedock.png)

**A keyboard-first Docker and Compose manager for the terminal.**

WhatTheDock is a new member of the Tide application family, alongside Tide and
TideMail. It uses [TideUI](https://github.com/allisonhere/tideui) for the shared
three-pane terminal shell, themes, pane chrome, row styling, overlays, and other
presentation primitives.

> Early-stage project: this repository currently contains the first working
> vertical slice, not the full planned Docker operations suite.

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

## Systems

Press `S` to manage Docker systems from inside the app. The Systems overlay can
switch between named systems, test a system connection, add new systems, edit
existing systems, and delete inactive systems.

Supported system types:

- `local`: uses Docker's normal defaults, or an optional explicit Docker host.
- `ssh`: opens an SSH socket tunnel from a local unix socket to a remote Docker
  socket.

SSH systems have separate `Host`, `User`, and optional `Port` fields. Existing
settings that used `user@host` in the host field are still accepted and are
shown as separate user and host values in the app.

SSH systems support two auth modes:

- `config/agent`: uses your existing SSH config, keys, and agent in the
  background.
- `password prompt`: temporarily hands the terminal to `ssh` while switching
  systems so OpenSSH can ask for the password, then returns to WhatTheDock.

WhatTheDock does not store SSH passwords or private keys.

Settings and Systems form changes are saved with `Ctrl+S`; `Esc` cancels
unsaved form edits. Systems editor text fields support normal caret editing
with `Left`/`Right`, `Backspace`, `Delete`, `Home`, `End`, and `Ctrl+U` to clear
the active field. Choice fields such as `Kind` and `Auth` cycle with
`Left`/`Right`, `h`/`l`, or `Enter`. SSH systems are validated before save,
switch, or test: `Host` is required, `Port` must be numeric when set, and both
Docker socket paths must be present.

## Current Controls

| Key | Action |
|---|---|
| `j` / `Down` | Move down |
| `k` / `Up` | Move up |
| `Enter` | Select/open, or expand/collapse Compose project |
| `Space` | Expand/collapse Compose project |
| `/` | Filter projects, services, and containers |
| `n` | Create container or Compose service draft |
| `s` | Start/stop selected container |
| `r` | Refresh Docker state |
| `Alt+r` | Restart selected container |
| `u` | Replicate: pull the latest image and recreate in place |
| `D` | Delete (override + reconcile for a Compose service, `docker rm` for a standalone container) |
| `C` | Clone under a new name via the create overlay |
| `e` | Open a shell inside the selected running container |
| `c` | Copy selected container details |
| `o` | Open selected container ports, mounts, or Compose paths |
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
| `S` | Manage local and remote Docker systems |
| `,` / `Ctrl+,` | Settings |
| `Ctrl+S` in settings/forms | Save changes |
| `[` / `]` in create form | Switch between Compose service and standalone container |
| `o` / `Ctrl+O` in Compose create form | Browse for a Compose file, locally or on the active SSH system |
| `Ctrl+Y` in Compose create form | Hand-edit the override YAML in a full-size editor |
| `Ctrl+Enter` / `Alt+Enter` in create form | Review the confirmation step |
| `y` / `n` in create confirmation | Confirm or cancel |
| `Ctrl+K` | Command palette |
| `?` | Keyboard help |
| `A` | About screen with Burn-style ANSI splash animation |
| `q` | Quit |

Mouse row selection and wheel navigation are enabled where the terminal and
Bubble Tea support them.

## Build

Install the latest `main` build with Go:

```bash
go install github.com/allisonhere/whatthedock/cmd/whatthedock@latest
```

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

Print the build version:

```bash
whatthedock --version
```

Release builds can stamp version metadata with Go linker flags:

```bash
go build -buildvcs=false \
  -ldflags "-X main.version=v0.1.0 -X main.commit=$(git rev-parse --short HEAD) -X main.date=$(date -u +%Y-%m-%d)" \
  -o whatthedock ./cmd/whatthedock
```

## Development

```bash
make fmt
make test
make vet
make build
```

More detail:

- [Container and Compose creation workflow](docs/creation.md)
- [Engineering handoff](docs/handoff.md)

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
  graph colors, log color mode, log health color, deltas, refresh interval, and
  default activity pane.
- Starts, stops, restarts, and refreshes containers.
- Drafts new Compose services or standalone containers in a keyboard-first
  creation overlay with live generated previews, syntax-highlighted YAML, and
  inline validation as you type. Standalone drafts can be created and started
  after an explicit confirmation. Compose drafts write a
  `compose.whatthedock.<service>.yml` override beside the selected Compose
  file after first validating a temporary override with `docker compose
  config`, then run `docker compose up -d <service>` after confirmation. This
  works against both local systems and SSH systems, running every step —
  browsing, writing the override, and the `docker compose` calls — on the
  remote host. Opening create for an already-managed service detects and
  loads its existing override instead of silently regenerating it, and the
  form's fields update to match whatever override content is actually loaded
  or hand-edited. The Compose file field includes a file browser (local or
  remote) for finding `compose*.yml`, `compose*.yaml`, `docker-compose*.yml`,
  and `docker-compose*.yaml` files. Press `Ctrl+Y` to hand-edit the generated
  override directly in a full-size [Ripple](https://github.com/allisonhere/ripple)
  editor, with real-time lint feedback and an optional persistent vim mode
  (Settings → Editor).
- Deletes (`D`), replicates (`u`), and clones (`C`) the selected container or
  Compose service. Delete removes just the generated override and reconciles
  the service back to its base definition for a Compose service, or a real
  `docker rm -f` for a standalone container. Replicate pulls a fresh copy of
  the image and recreates the same container/service in place under its
  existing identity. Clone opens the create overlay prefilled with the
  original's full ports/mounts/env/restart/command under a new,
  `-clone`-suffixed name, producing an independent second container/service.
- Opens an interactive shell inside the selected running container (`e`),
  handing the real terminal to `docker exec -it` (preferring `bash`, falling
  back to `sh`) and resuming WhatTheDock when the session ends. Works against
  SSH systems the same way, over the already-established socket tunnel.
- Copies selected container IDs, images, Compose metadata, ports, mounts, and
  labels via terminal OSC52 clipboard escape sequences.
- Opens published ports, bind mounts, and Compose config paths from the
  inspector.
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
- Merge-aware editing of the base Compose file itself, rather than only the
  generated override.
- Open/copy actions for environment values.
- Deeper problem detection for orphaned, stale, and resource-heavy Docker
  resources.
- Multi-host support for local sockets, Docker contexts, and SSH-backed
  contexts.
- Image update availability checks based on registry metadata rather than
  fragile tag assumptions.
