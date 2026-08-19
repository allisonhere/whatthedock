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
    systems. Also owns Delete's real removal (stop/remove container + delete
    its definition) and Replicate's pull-then-up-d functions (both
    local/SSH), and Clone's extended prefill (`defaultCloneDraft`, carrying
    Ports/Mounts/Env/Restart/Command that a fresh Create draft doesn't need).
- `internal/ui/compose_merge.go`
  - Comment- and format-preserving edits to Compose YAML via `yaml.v3`
    `Node`s: merging structured fields into an existing service's block
    (`mergeComposeServiceFields`) and removing a service's block entirely
    (`removeComposeService`), used by Create/Edit and Delete respectively
    whenever the base compose file already defines the target service.
- `internal/update/update.go`
  - Pure, UI-independent logic for the self-update feature: fetching the
    latest GitHub release tag (`LatestRelease`), comparing versions
    (`IsNewer`), and downloading + atomically installing a release asset
    over the running binary (`ReplaceRunningExecutable`). Never re-execs
    itself — see `internal/ui/update.go` and `cmd/whatthedock/main.go` for
    why that's deliberately left to `main()`.
- `internal/ui/update.go`
  - Wires `internal/update` into the Bubble Tea model: the auto-check on
    every launch (`autoCheckForUpdateCmd` — no throttle; a once-a-day gate
    on `updateLastCheck` was removed after a live report that it made a
    same-day relaunch silently skip checking at all, easy to mistake for
    the check being broken), the manual check from Settings, the "update
    available" confirm overlay (`handleUpdateKey`) and its
    persisted-per-version "ignore", and `RestartExecPath` — the seam
    `cmd/whatthedock/main.go` polls after `program.Run()` returns to decide
    whether to re-exec into a freshly installed binary.
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
- Added `m` (edit) — opens the create overlay prefilled from the selected
  container/service under its own identity (not renamed, unlike Clone), so
  confirming replaces it in place. For Compose this is exactly
  `openCreateOverlay`'s existing already-managed-service path, reused as-is
  (`Model.openEditOverlay` in `create.go` just flags `createDraft.Editing`
  after calling it). Standalone gets a dedicated `defaultEditDraft`
  (`create.go`, full Ports/Mounts/Env/Restart/Command like `defaultCloneDraft`
  but keeping the real name) and a new `editContainerCmd` (remove + recreate
  under the edited spec, mirroring `startReplicate`'s standalone path minus
  the image pull). Deliberately does *not* change `n`/Create's behavior for a
  selected standalone container — that was the first design considered and
  explicitly rejected: pressing Create must never silently overwrite what's
  selected, only Edit should.

- Added merge-aware base-compose-file editing (`internal/ui/compose_merge.go`)
  and made it Create/Edit's default for any service the base compose file
  already defines: `defaultApplyComposeCreate`/`applyComposeCreateRemote`
  read the base file first, and if it already has the service, merge the
  draft's fields (`Image`, `Restart`, `Command`, `Ports`, `Mounts`, `Env`)
  directly into that service's existing block via `mergeComposeServiceFields`
  — a `yaml.v3` `Node`-level edit that only touches the keys being replaced,
  leaving every other key, every other service, comments, and key order
  exactly as they were — instead of writing a `compose.whatthedock.<service>.yml`
  override on top. Any override left over from before the service existed in
  base gets deleted as part of the merge, so there's exactly one place the
  service is defined afterward. Brand-new services the base file doesn't
  define yet are unaffected — they still go through the original
  generated-override path. This closes the "Remaining" item below about
  override-forever vs. merge-aware edits.
- Redesigned Delete for Compose services
  (`defaultApplyComposeDelete`/`applyComposeDeleteRemote`) to be a real,
  permanent removal instead of override-removal-and-reconcile-to-base: it now
  runs `docker compose rm -sf <service>` to actually stop and remove the
  container, then deletes the service's definition everywhere WhatTheDock
  knows about it — the override, if any, and the service's block in the base
  file (`removeComposeService`, same comment-preserving `yaml.v3` approach) if
  base defines it. The previous behavior silently left the container running
  under its base definition instead of removing it, which read as a bug
  against normal "Delete" expectations (e.g. Portainer-style stack service
  deletion) — this was reported and fixed in the same session the
  merge-aware editing above was added.
- Added a self-update feature: `internal/update` (pure GitHub-release-check
  and download/install logic) plus `internal/ui/update.go` (wiring it into
  the model — an automatic once-a-day check on launch, throttled/cached via
  a new `updateLastCheck` persisted setting; a "Check for update" row in
  Settings that always bypasses the throttle; and an "update available"
  confirm overlay whose "ignore" persists per-version via a new
  `updateIgnoredVersion` setting, so a later release still prompts).
  Confirming downloads the matching release asset, atomically replaces the
  running binary, and re-execs into it. The re-exec is deliberately done in
  `cmd/whatthedock/main.go`, not inside the Bubble Tea model — it checks
  the new `ui.Model.RestartExecPath()` only after `program.Run()` returns,
  so Bubble Tea has already restored the terminal before the process image
  gets replaced. `cmd/whatthedock`'s `main.go` now also passes its
  ldflags-injected `version` into the model via a new `Model.WithVersion`
  method — the model previously had no idea what version it was running.

Remaining:

- Improve standalone command parsing if quoted arguments become important.
- Base-file merge editing only touches the structured fields the create form
  has (`Image`, `Restart`, `Command`, `Ports`, `Mounts`, `Env`); keys the form
  doesn't expose (`networks`, `depends_on`, `labels`, `build`, ...) pass
  through untouched on both merge and removal, by design — extending the form
  itself would be a separate, larger change.
- Remote (SSH) Compose operations are one `ssh` invocation per step rather
  than a shared multiplexed connection — noticeable latency, not correctness,
  but worth revisiting if it becomes annoying in practice.

## Known Constraints

- Compose generated YAML (the override path, for services not yet in base) is
  intentionally simple and quoted — that generation step is still not
  merge-aware. Base-file edits (for services already in base) do go through
  the merge-aware `yaml.v3` `Node` path in `compose_merge.go`, but only for
  the fields the create form exposes; see Remaining above.
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
