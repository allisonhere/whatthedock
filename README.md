# WhatTheDock

![WHAT THE DOCK?! logo](assets/whatthedock.png)

WhatTheDock is a terminal app for working with Docker containers and Compose
stacks.

It is meant for the stuff you do all the time:

- see what is running
- check logs and problems
- start, stop, restart, edit, clone, delete, or recreate containers
- manage local and remote Docker hosts
- keep a small library of Compose files you can edit, save, and make live
- yank a container's configuration on one host, switch hosts, and paste it
  onto another (the Container Clipboard — see below)
- gracefully shut down or reboot a host, right from the app

It uses Docker's normal defaults. If `docker ps` works on your machine,
`whatthedock` should have a decent shot at working too.

This is still an early project, but the main workflow is usable.

## Screenshots

<table>
<tr>
<td width="50%">

**Problems, with AI analysis**
![Problems pane with an AI-generated analysis of a restarting container](screenshots/problems-ai-analysis.png)

</td>
<td width="50%">

**Logs**
![Color-coded log stream for a selected container](screenshots/logs.png)

</td>
</tr>
<tr>
<td width="50%">

**Dashboard**
![Full-screen dashboard with a fleet summary line and a CPU/memory/network sparkline row per running container](screenshots/dashboard.png)

</td>
<td width="50%">

**Create / edit**
![Compose service create form with a live generated YAML preview](screenshots/create.png)

</td>
</tr>
</table>

## Install

Install the latest release:

```bash
curl -fsSL https://raw.githubusercontent.com/allisonhere/whatthedock/main/scripts/install.sh | sh
```

The installer downloads the latest release binary, checks that it runs, and
puts it in `/usr/local/bin`. If that is not writable, it uses `~/.local/bin`.
Set `INSTALL_DIR` if you want another location.

Build from source:

```bash
go install github.com/allisonhere/whatthedock/cmd/whatthedock@latest
```

Run it:

```bash
whatthedock
```

Run the demo if you do not have Docker handy:

```bash
whatthedock --demo
```

## Finding Things

There are two search helpers:

- Press `Ctrl+K` for the command palette. Type what you want, then press
  `Enter`.
- Press `?` for Help. Inside Help, press `/` and type a word like `catalog`,
  `logs`, `delete`, or `systems`.

That is usually faster than remembering every key.

## Main Keys

| Key | What it does |
|---|---|
| `j` / `k` or `Up` / `Down` | Move around |
| `Tab` / `Shift+Tab` | Move between panes |
| `Left` / `Right` | Move between panes |
| `Enter` | Open/select the focused item |
| `Space` | Expand/collapse a stack row; pause/resume logs in Logs |
| `/` | Filter containers and stacks; in Logs, filter log lines |
| `r` | Refresh Docker state |
| `s` | Start/stop the selected container |
| `Alt+r` | Restart the selected container |
| `n` | Create a container or Compose service |
| `m` | Edit the selected service; on a stack row, edit the whole stack |
| `C` | Clone the selected container/service |
| `y` | Yank the selected container's configuration (Container Clipboard) |
| `P` | Paste the yanked container onto the current host |
| `D` | Delete the selected container/service; on a stack row, delete the whole stack |
| `u` | Pull latest image and recreate in place |
| `e` | Open a shell in the selected running container |
| `c` | Copy selected details |
| `o` | Open a selected port, mount, or Compose path |
| `q` / `Ctrl+C` | Quit |

## Logs, Problems, And Stats

| Key | What it does |
|---|---|
| `l` | Show logs |
| `L` | Expand logs across most of the screen |
| `e` / `w` / `i` / `a` in Logs | Show errors, warnings, info, or all log lines |
| `f` / `End` in Logs | Jump to the end and follow live output |
| `Home` / `PgUp` / `PgDn` in Logs | Move through log history |
| `c` in Logs | Copy all currently visible (filtered) log lines to the clipboard |
| `Esc` in Logs | Clear the active log filter |
| `p` | Show problems |
| `a` in Problems | Ask the configured AI provider to analyze the selected problem |
| `g` | Show stats graphs |

AI analysis is opt-in. It only runs when you press `a` in Problems, and only
after you configure a provider in Settings.

## Create And Edit

Press `n` to create something new. Press `m` to edit what is selected.

Useful keys in the create/edit form:

| Key | What it does |
|---|---|
| `[` / `]` | Switch between standalone, Compose service, and Compose stack modes |
| `Up` / `Down` / `Tab` | Move between fields |
| `h` / `l` or `Left` / `Right` | Change choices or move the cursor in text fields |
| `Enter` | Move to the next field; on the Compose file field, browse |
| `Ctrl+O` | Browse for a Compose file |
| `Ctrl+Y` | Edit the Compose YAML directly |
| `Ctrl+P` | Open the small create/edit Compose catalog picker |
| `Ctrl+S` | Validate or save changes |
| `Ctrl+Enter` / `Alt+Enter` | Show the final confirmation |
| `Esc` / `q` | Cancel/close |

Compose support is practical, not magical:

- A single-service Compose file can be edited as a service.
- A multi-service Compose file is treated as a stack.
- Editing a stack row edits the whole Compose file.
- Editing one service in a stack asks whether you mean that service or the
  whole stack.
- Local and SSH systems both work for Compose reads, writes, and `docker compose`
  runs.

## Compose Catalog

The Compose catalog is a library of Compose files.

Use it when you want to keep a Compose file around, edit it safely, add notes,
or later make it live on a local or remote Docker host.

Open it from the command palette:

```text
Ctrl+K -> Curate Compose files
```

Catalog keys:

| Key | What it does |
|---|---|
| `Tab` | Switch between Library, Live, and Unused views |
| `j` / `k` | Move selection |
| `f` | Filter entries |
| `A` | Add a draft from a URL or path |
| `B` | Browse for a Compose file and add it as a draft |
| `N` | Start a blank draft |
| `Enter` / `l` | Preview an entry |
| `e` | Edit an entry |
| `c` | Create a runnable draft from a live stack |
| `S` | Save a live stack as a draft |
| `M` / `p` | Make the selected entry live |
| `s` | Change status: draft, live, or archived |
| `m` | Toggle missing/unused review |
| `n` / `t` | Edit note or tags |
| `a` | Archive or unarchive |
| `D` | Delete the catalog entry |

Notes are saved in catalog metadata and mirrored into a WhatTheDock comment
block at the top of the Compose file. That way the note travels with the YAML
instead of only living in the app.

Make Live asks for a destination path, shows conflicts, writes the Compose file
to the active local or SSH system, then runs `docker compose up -d`.

## Other Curators

Open these from `Ctrl+K`:

- `Curate Docker images`
- `Curate Docker networks`
- `Curate Docker volumes`

The keys are the same in each one:

| Key | What it does |
|---|---|
| `j` / `k` | Move through the list |
| `Space` | Select a removable item |
| `r` | Reload |
| `d` | Delete selected items |
| `y` / `Enter` | Confirm deletion |
| `n` / `Esc` | Cancel |

In-use items are shown, but they cannot be selected for removal.

## Shut Down / Reboot Host

Open from `Ctrl+K`:

- `Shut down host machine`
- `Reboot host machine`

Both gracefully stop every running container on the current host first,
then run the actual OS-level shutdown/reboot — locally or over SSH,
matching whatever system you're currently connected to. An itemized
confirm screen lists exactly which containers will be stopped before
anything happens.

The OS command runs non-interactively (`sudo -n ...`) so it never hangs
waiting for a password on a terminal that isn't there. If the host needs
one, WhatTheDock asks for it in-app — a masked prompt, never a terminal
handoff — and retries with it piped to `sudo -S`. Wrong password just
reopens the prompt; `Esc` always cancels. If you'd rather never be asked,
configure passwordless `sudo` for `shutdown` on the host once and this
never comes up again.

Not available in demo mode — there's no real host behind it.

## Systems

Press `S` to manage Docker systems.

You can:

- switch between systems
- test a connection
- add a local or SSH system
- edit a system
- delete an inactive system

System keys:

| Key | What it does |
|---|---|
| `Enter` | Switch to the selected system |
| `t` | Test the selected system |
| `a` | Add a system |
| `e` | Edit a system |
| `d` | Delete an inactive system |
| `Ctrl+S` while editing | Save |
| `Esc` | Cancel/close |

Supported system types:

- `local`: Docker on this machine, using normal Docker defaults unless you set
  a host.
- `ssh`: a tunnel to a remote Docker socket.

SSH auth modes:

- `config/agent`: use your normal SSH config, keys, and agent.
- `keychain`: store the SSH password in the OS keychain, not in
  `settings.json`.
- `password prompt`: let `ssh` ask for the password each time.

Settings live in your platform config directory. On Linux that is usually:

```text
~/.config/whatthedock/settings.json
```

## Container Clipboard

Yank a container's configuration on one Docker host, switch hosts, paste it
on another — the vim-flavored `y` / `P` workflow:

```text
select container
y
switch host
P
review conflicts
deploy
```

`y` captures a container's full configuration (image, command, entrypoint,
env, ports, mounts, networks, restart policy, healthcheck, resource limits,
and more) into an in-session clipboard — shown as `[YANK: name]` in the
status bar. It persists while you switch between systems, and keeps a
short history of your last few yanks.

`P` checks the yanked container against whatever host you're currently on
— name/port collisions, whether the image needs pulling, whether a network
needs creating, bind-mount paths that don't exist there, secret-like env
vars — and shows a review screen before anything happens:

| Key | What it does |
|---|---|
| `Enter` | Open the paste for editing (name, ports, mounts, env, networks) before deploying |
| `d` | Deploy as-is (refused if there's a blocking conflict, e.g. a name collision) |
| `t` | Redirect any missing bind-mount source(s) to a placeholder directory, so a deploy isn't blocked on them |
| `Esc` | Cancel — the clipboard item is kept, so you can try again |

This is a **configuration clone, not a data migration**: named volumes are
created if needed, but volume and bind-mount *contents* are never copied
between hosts, and the source container is never touched. Env vars that
look like secrets (passwords, tokens, keys, ...) are flagged and their
values are never shown in the review screen.

A bind mount whose source directory doesn't exist on the destination is a
blocking conflict by default — Docker refuses to create a container with a
missing bind source. Press `t` to redirect it instead: WhatTheDock creates
an empty placeholder directory under
`~/.local/share/whatthedock/paste-placeholders/<container>/<mount>` on the
destination, points the mount at it so the deploy can proceed, and records
the *original* path as a label on the created container
(`com.whatthedock.paste.original-bind-source:<path-in-container>`) — visible
later via `docker inspect` or the Inspector, not just a one-time message.
The placeholder is empty: no data is migrated into it, and the confirm
screen says so again right before you deploy.

## Settings

Open Settings with `,` or `Ctrl+,`.

Settings include:

- theme and graph style
- refresh interval
- default activity pane
- whether to start in Dashboard
- editor mode
- AI provider/model/API key/base URL
- app activity log options
- update checks

Press `Ctrl+S` to save.

If you export a provider's normal API key environment variable, that wins over a
stored key:

- `ANTHROPIC_API_KEY`
- `OPENAI_API_KEY`
- `GEMINI_API_KEY`

## Dashboard

Press `d` for the Dashboard.

It shows a quick view of the host, running containers, CPU, memory, network
activity, and problems.

| Key | What it does |
|---|---|
| `j` / `k` | Move through rows |
| `Enter` | Open the highlighted container |
| `p` | Jump to Problems |
| `Esc` / `q` / `d` | Close Dashboard |

Mouse wheel and click work where the terminal supports them.

## Updates

WhatTheDock can check GitHub releases for updates.

When an update is available, it asks before installing. If you say yes, it
downloads the matching release asset, verifies its signature, replaces the
running binary, and restarts.

The release signing key is baked into the app. Unsigned or invalid updates are
rejected.

## Development

Run the normal checks:

```bash
make check
```

That runs:

- `gofmt -l .`
- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `go build -buildvcs=false ./...`

Run locally from source:

```bash
go run -buildvcs=false ./cmd/whatthedock
```

Run the demo from source:

```bash
go run -buildvcs=false ./cmd/whatthedock --demo
```

Build a binary:

```bash
go build -buildvcs=false -o whatthedock ./cmd/whatthedock
./whatthedock
```

Print the version:

```bash
whatthedock --version
```

More detail:

- [Container and Compose creation workflow](docs/creation.md)
- [Engineering handoff](docs/handoff.md)

## Release

The release helper is a small TUI:

```bash
go run ./cmd/release
```

It shows the branch, latest tag, commit log, and diff since the last tag. It can
build, sign, tag, push the tag, and create the GitHub release.

Use a dry run first:

```bash
go run ./cmd/release -dry-run
```

Release binaries are signed with an ed25519 signature. Generate the signing key
once with:

```bash
go run ./cmd/release -genkey
```

Keep the private key in `WHATTHEDOCK_SIGNING_KEY`. Do not commit it.

## What It Uses TideUI For

WhatTheDock uses [TideUI](https://github.com/allisonhere/tideui) for the shared
terminal UI pieces: panes, borders, rows, overlays, themes, and status bars.

Docker behavior, Compose behavior, settings, actions, and app state live here in
WhatTheDock.
