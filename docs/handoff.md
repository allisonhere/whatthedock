# Engineering Handoff

## Current State

This branch adds a creation workflow to WhatTheDock:

- `n` opens a create overlay.
- Command palette includes **Create container or Compose service**.
- The create overlay supports Compose service drafts and standalone container
  drafts.
- Standalone drafts can create and start Docker containers through the provider
  API after confirmation.
- Compose drafts create local override files and run Docker Compose after
  confirmation.
- Compose file selection has a local TUI file browser.

Untracked local files at handoff:

- `.claude/`
- `prototype/container-create.html`

## Main Code Paths

- `internal/actions/actions.go`
  - Adds the `create-container` action.
- `internal/app/provider.go`
  - Adds `CreateContainer` and the typed container create spec.
- `internal/docker/client.go`
  - Implements Docker container creation through the Docker Go client.
- `internal/demo/provider.go`
  - Implements synthetic demo container creation.
- `internal/ui/model.go`
  - Owns create overlay state, create draft validation, standalone spec parsing,
    Compose override generation, local file browser state, confirmation handling,
    and command execution.
- `internal/ui/view.go`
  - Renders the create form, confirmation view, and Compose file browser.
- `internal/ui/model_test.go`
  - Covers create overlay behavior, shortcut regressions, parsing, provider
    calls, Compose override generation, temp-file cleanup, and file browser
    selection.

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
- Compose file browser and Compose apply are local-only.
- SSH systems are intentionally blocked from Compose file browsing and Compose
  creation for now.

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

Recommended next steps:

- Add full Compose editing using a real text editor surface or the Ripple editor
  library behind an adapter.
- Design remote Compose file browsing and editing for SSH systems instead of
  assuming local filesystem paths.
- Improve standalone command parsing if quoted arguments become important.
- Consider extracting the large create workflow out of `internal/ui/model.go`
  once behavior settles.
- Add UI affordances for existing WhatTheDock-generated override files:
  open, edit, delete, or replace.
- Decide whether Compose service creation should default to generated override
  files forever or become a stepping stone toward merge-aware YAML edits.

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
