# Engineering Handoff

## Current State

This branch adds a creation workflow to WhatTheDock:

- `n` opens a create overlay.
- Command palette includes **Create container or Compose service**.
- The create overlay supports Compose service drafts and standalone container
  drafts, switched with `[`/`]` mode tabs.
- Standalone drafts can create and start Docker containers through the provider
  API after confirmation.
- Compose drafts write an override file and run Docker Compose after
  confirmation, against local or SSH systems.
- Compose file selection has a TUI file browser, local or remote depending on
  the active system.
- Opening create for an already-managed service detects and loads its
  existing override instead of regenerating it, and the form's fields sync to
  match whatever override content ends up loaded or hand-edited.
- `Ctrl+Y` opens the override YAML in a full-size Ripple editor, with
  real-time lint feedback, syntax highlighting, and an optional persistent
  vim mode.

Untracked local files at handoff:

- `.claude/`

## Main Code Paths

- `internal/actions/actions.go`
  - Adds the `create-container`, `delete-container`, `replicate-container`,
    and `clone-container` actions.
- `internal/app/provider.go`
  - Adds `CreateContainer`/`RemoveContainer`/`PullImage` and the typed
    container create spec.
- `internal/docker/client.go`
  - Implements Docker container creation, removal, and image pulling
    through the Docker Go client (`ContainerRemove`, `ImagePull` drained via
    `jsonmessage.DisplayJSONMessagesStream`).
- `internal/demo/provider.go`
  - Implements synthetic demo container creation and removal; `PullImage` is
    a no-op (no real registry in demo mode).
- `internal/ui/model.go`
  - Owns general app state, top-level `Update` message routing, and settings
    (including the persistent vim-mode toggle for the override editor).
- `internal/ui/view.go`
  - Renders the main three-pane layout, overlays in general, and the `?`
    keyboard help text.
- `internal/ui/create.go`
  - Owns create overlay state, create draft validation, standalone spec
    parsing, Compose override generation and parsing (including syncing the
    form's fields back from loaded or hand-edited override YAML), local and
    remote (SSH) file browser state, override-detection on open, and
    confirmation handling and command execution for both local and SSH
    systems. Also owns Delete's override-removal-and-reconcile and
    Replicate's pull-then-up-d functions (both local/SSH), and Clone's
    extended prefill (`defaultCloneDraft`, carrying Ports/Mounts/Env/
    Restart/Command that a fresh Create draft doesn't need).
- `internal/ui/create_view.go`
  - Renders the create form, mode tabs, confirmation view, Compose file
    browser, and the syntax-highlighted override preview/editor.
- `internal/ui/editor_area.go`
  - Adapts the [Ripple](https://github.com/allisonhere/ripple) editor
    component for the override-YAML editor: clipboard wiring, vim-mode setup,
    cursor rendering, and the Compose YAML tokenizer/highlighter shared by the
    live editor and the static form preview.
- `internal/systems/factory.go`
  - Adds `RemoteCommand`/`ShellQuote` helpers used by every SSH-backed
    Compose operation, and `DockerHostFor` (resolves the DOCKER_HOST value
    for an already-connected system, used by exec shell).
- `internal/ui/create_test.go`, `internal/ui/editor_area_test.go`,
  `internal/ui/model_test.go`
  - Cover create overlay behavior, shortcut regressions, parsing, provider
    calls, Compose override generation and field-sync, temp-file cleanup,
    local and remote file browser selection, and the override editor.

## User Workflow

Creation starts with `n`.

Compose mode:

1. Fill draft fields.
2. Use `o` or `Ctrl+O` to browse for a local Compose file.
3. `Ctrl+S` validates.
4. `Ctrl+Enter` or `Alt+Enter` opens confirmation.
5. `y` writes the override and runs Compose.

Standalone mode:

1. Fill draft fields.
2. `Ctrl+S` validates.
3. `Ctrl+Enter` or `Alt+Enter` opens confirmation.
4. `y` calls Docker create/start through `app.Provider`.

## Safety Behavior

- Destructive or mutating create actions require confirmation.
- Compose override creation validates a temporary file first.
- Failed Compose validation removes the temporary override and does not promote
  the final override.
- Compose file browsing, override writing, and `docker compose` execution all
  work against SSH systems too, running each step on the remote host over the
  same `ssh` convention used for the Docker socket tunnel.

## Verification Commands

Run these from the repository root before claiming the branch is ready:

```bash
go test ./...
go test -race ./...
go vet ./...
go build -buildvcs=false ./...
gofmt -l .
git diff --check
git status --short
```

The latest pass completed successfully with:

- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `go build -buildvcs=false ./...`
- `gofmt -l .`
- `git diff --check`

## Next Work

Done since this doc was first written:

- Full Compose editing via the Ripple editor library, with real-time YAML
  linting and syntax highlighting — see the Override YAML Editor section
  above.
- Remote Compose file browsing, writing, and `docker compose` execution for
  SSH systems, over the same `ssh` convention as the Docker socket tunnel.
- The create workflow was extracted out of `internal/ui/model.go` into
  `internal/ui/create.go` (state/logic) and `internal/ui/create_view.go`
  (rendering).
- Opening create for an already-managed service now detects and loads its
  existing `compose.whatthedock.<service>.yml` instead of silently
  regenerating (and overwriting) it.
- Loading or hand-editing override YAML now parses it back into the
  structured form fields (Image, Restart, Command, Ports, Mounts, Env), so
  the form stays in sync with whatever content will actually be written.
- Added `D`/`u`/`C` (Delete/Replicate/Clone) on the selected container or
  Compose service — Delete removes just the generated override and
  reconciles to base (or a real `docker rm -f` for a standalone container),
  Replicate pulls a fresh image and recreates in place, and Clone opens
  create prefilled from the original under a new name. This is the delete-
  or-replace-an-existing-override capability this doc used to list as
  remaining work.
- Added `e` (exec shell) — hands the real terminal to `docker exec -it` on
  the selected running container (bash if present, else sh) via the same
  `tea.ExecProcess` terminal-handoff mechanism already used for the SSH
  password prompt, then resumes WhatTheDock. Works for SSH systems too, over
  the already-established socket tunnel (`systems.DockerHostFor`).
- Delete/Replicate/Create-apply dispatch now sets `m.busy` and a status-bar
  phase label immediately (previously Delete/Replicate showed nothing at
  all until the final result, up to 2 minutes later for a slow pull). A
  spinner animates from the shared `github.com/allisonhere/cli-spinners`
  frame data, driven by the same tick that already animates the connected-
  status dot (`m.statusPulseFrame`/`tickStatusPulse`). Standalone Replicate
  additionally streams real Docker pull progress (`app.PullProgress`, via
  `PullImage`'s `onProgress` callback) into `m.replicateProgress`, drained
  one line per tick in the `statusPulseTickMsg` handler — Compose
  Delete/Replicate/Create only get the spinner + a static phase label,
  since `docker compose` is shelled out to and has no structured progress
  to surface. If you touch dispatch for any of these actions again, keep
  setting `m.busy`/the phase label there — it's easy to reintroduce a
  silent fire-and-forget dispatch by accident.

Remaining:

- Improve standalone command parsing if quoted arguments become important.
- Decide whether Compose service creation should default to generated override
  files forever or become a stepping stone toward merge-aware YAML edits.
- Remote (SSH) Compose operations are one `ssh` invocation per step rather
  than a shared multiplexed connection — noticeable latency, not correctness,
  but worth revisiting if it becomes annoying in practice.

## Known Constraints

- Compose generated YAML is intentionally simple and quoted. It is not a
  merge-aware YAML editor.
- Compose override files are named:

```text
compose.whatthedock.<service>.yml
```

- Temporary validation files are named:

```text
compose.whatthedock.<service>.yml.tmp
```

- File browser candidates are limited to likely Compose YAML names:
  `compose*.yml`, `compose*.yaml`, `docker-compose*.yml`, and
  `docker-compose*.yaml`.
