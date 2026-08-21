# Container And Compose Creation

WhatTheDock includes a keyboard-first creation overlay for drafting either a
standalone Docker container or a Compose service.

Open the creation overlay with `n` or from the command palette with
`Ctrl+K`, then choose **Create container or Compose service**.

## Creation Modes

The create overlay opens with a **mode tab row** above the form: `Compose
service` and `Standalone container`. The active mode is highlighted.

- `Compose service`: drafts a Compose service and applies it through a local
  Compose override file.
- `Standalone container`: creates and starts a Docker container through the
  Docker API.

Press `[` or `]` to switch modes — this works from any field, not just when
a particular row is focused. If the currently focused field exists in both
modes (`Image`, `Ports`, `Mounts`, `Env`, `Restart`), focus and its value
carry over; otherwise focus resets to the new mode's first field.

The rest of the form uses `Left`/`Right`, `h`/`l`, or `Enter` on choice rows
(currently just `Restart`) to change values. Text fields support normal
editing, including `Left`, `Right`, `Backspace`, `Delete`, `Home`, `End`, and
`Ctrl+U`.

A live validation line under the preview shows whether the current draft is
ready to create (`Draft looks good`) or what's missing/invalid — it updates
as you type, without needing to press `Ctrl+S`.

## Compose Service Flow

Compose creation works against both local and SSH systems. For an SSH
system, browsing, writing the override file, and running `docker compose`
all happen on the remote host, over the same `ssh` connection convention
used for the Docker socket tunnel (see the system's SSH host/port/user in
Settings → Systems) — nothing needs to exist locally.

Opening create (`n`) for an already-selected, already-managed service
checks whether it already has a WhatTheDock-generated
`compose.whatthedock.<service>.yml` — locally this is checked immediately;
for an SSH system it's one `ssh` round trip after the form opens. If found,
that file's content is loaded as the draft's override (shown as `Override
YAML  existing file loaded`) instead of silently offering to regenerate —
and overwrite — it, and the structured fields (`Image`, `Ports`, `Mounts`,
`Env`, `Restart`, `Command`) update to match what the loaded YAML actually
contains. Hand-editing and saving via `Ctrl+Y` switches the label to
`hand-edited` and syncs the fields the same way, from whatever you just
saved. If the override defines more than one service and none matches the
draft's `Service` field, the fields are left as-is rather than guessing.

1. Press `n`.
2. Make sure the `Compose service` tab is active (`[`/`]` to switch).
3. Fill in `Project`, `Service`, `Image`, optional `Ports`, `Mounts`, `Env`,
   `Restart`, and `Compose file`.
4. Use `Enter` or `Ctrl+O` on the `Compose file` field to browse for a file
   (local or, for an SSH system, on the remote host). Plain `o` types the
   letter into whatever field is focused — including `Compose file` itself,
   so you can type a path by hand — rather than opening the browser; only
   choice fields (`Mode`, `Restart`) treat bare `o` as a shortcut.
5. Optionally press `Ctrl+P` to load or save a reusable Compose template from
   the local catalog.
6. Optionally press `Ctrl+Y` to hand-edit the generated override YAML
   directly (see Override YAML Editor below).
7. Check the inline validation line, or press `Ctrl+S` to validate explicitly.
8. Press `Ctrl+Enter` or `Alt+Enter` to review the confirmation.
9. Press `y` to create, or `n`/`Esc` to cancel.

## Compose Catalog

Press `Ctrl+P` in Compose create/edit mode to open the local Compose catalog.
Catalog entries are reusable YAML templates stored under WhatTheDock's config
directory, separate from `settings.json`. They are available no matter which
Docker system is active; applying one to an SSH system still writes and runs
Compose on that remote host through the normal create flow.

Catalog keys:

| Key | Action |
|---|---|
| `j` / `Down` | Move down |
| `k` / `Up` | Move up |
| type text | Filter entries by name |
| `Ctrl+U` | Clear the filter or rename text |
| `Enter` | Load the selected template into the current draft |
| `s` | Save the current draft/template into the catalog |
| `r` | Rename the selected catalog entry |
| `d` | Delete the selected catalog entry after confirmation |
| `Esc` | Return to the create form |

Loading a single-service template fills the left-side fields and keeps the raw
YAML as the editor source so comments and keys outside the form's field list
persist. Loading a multi-service template switches the draft into stack mode.
Renaming or deleting a catalog entry only changes the local catalog; it never
renames or deletes a deployed Compose file or container.

## Override YAML Editor

Press `Ctrl+Y` in Compose mode to open the generated override in a full-size
editor — sized close to the whole terminal, not the small form panel — backed
by [Ripple](https://github.com/allisonhere/ripple), the same editor component
tidemail uses. It supports both conventional (standard) editing and vim modal
editing.

- **Standard mode** (default): type normally; arrows/Home/End/Backspace/
  Delete/page up/down and their shift-selection variants work as usual;
  `Ctrl+S` saves and returns to the form, `Esc` cancels and discards the edit.
- **Vim mode**: motions (`h`/`j`/`k`/`l`, `w`/`b`/`e`, `0`/`^`/`$`, `gg`/`G`,
  counts), insert entry (`i`/`a`/`o`/`O`/`I`/`A`), edits (`x`/`dd`/`yy`/`p`/
  `P`, operators like `dw`/`cw`/`d$`), visual `v`/`V`, and `u`/`Ctrl+R`
  undo/redo. `:w`, `:wq`, or `:x` saves and returns to the form; `:q` or a
  second `Esc` from Normal mode cancels. `Ctrl+S`/`Esc` still work directly
  as a shortcut too. The current sub-mode (`NORMAL`/`INSERT`/…) or the `:`
  command line shows in the editor's header.

Enable vim mode from **Settings → Editor → Vim mode** — it's a persistent
setting (like tidemail's), applied to every editor for the session, not a
per-edit toggle.

Once saved, the hand-edited YAML becomes what gets written to the override
file instead of the generated content, and the form shows `Override YAML
hand-edited (ctrl+y to re-edit)` so it's clear the fields below no longer
drive the output. Saving an edit that's empty (or only whitespace) resets
back to the generated content.

Clipboard: `Ctrl+C`/`Ctrl+X`/`Ctrl+V` inside the editor use WhatTheDock's
existing OSC52 terminal clipboard for copy/cut (same as elsewhere in the
app) — there's no OS clipboard *read* integration, so `Ctrl+V` may report
paste as unsupported depending on your terminal; your terminal's own
bracketed-paste (e.g. a middle-click or terminal-menu paste) still works
independently of that.

When confirmed, WhatTheDock checks whether the base compose file already
defines the service (i.e. it's a real, already-running service, not a
brand-new one WhatTheDock is about to introduce). The two cases apply the
draft differently:

**Service already defined in base** — the draft's fields (`Image`,
`Restart`, `Command`, `Ports`, `Mounts`/`volumes`, `Env`/`environment`) are
merged directly into that service's existing block in the base compose
file, using a comment- and key-order-preserving YAML edit: only the fields
that changed are touched, everything else on the service (`networks`,
`depends_on`, `labels`, `build`, ...), every other service, and the rest of
the document is left exactly as it was. Any override left over from before
the service existed in base is deleted, so there's exactly one place the
service is defined going forward. The rewritten base file is validated the
same way an override is (written to a `.tmp` file, checked with `docker
compose config`, then promoted) before `docker compose up -d <service>`
runs.

**Brand-new service, not yet in base** — WhatTheDock writes a generated
override file beside the selected Compose file instead:

```text
compose.whatthedock.<service>.yml
```

The app first writes and validates a temporary file:

```text
compose.whatthedock.<service>.yml.tmp
```

It runs `docker compose ... config` against the temporary override. If validation
fails, the temporary file is removed and the final override is not written. If
validation passes, the temporary file is renamed to the final override and
WhatTheDock runs:

```bash
docker compose -p <project> -f <base-compose-file> -f <override-file> up -d <service>
```

For an SSH system, every step above (the `test -f`/`cat` base-file check,
`mkdir -p`, writing the temp override or temp base file, `docker compose
config`, the rename, and `docker compose up -d`) runs on the remote host over
`ssh` instead of the local filesystem/`docker` binary.

**Base file doesn't exist at all — adopting an orphaned container** — an
already-labeled container/service (opened via `m` edit, or `n` re-opened on
an already-managed service) can have a Compose file path that doesn't
actually exist on the host. This happens for stacks deployed by a tool that
manages its own compose file elsewhere rather than leaving one at the path
it stamps into the container's labels — Portainer is the common case.
WhatTheDock checks for this the moment the form opens (alongside the
existing-override check above — local: a `stat`; SSH: `test -f`, one more
step in the same round trip) and, if the file is missing, the confirm step
(`Ctrl+Enter`/`Alt+Enter`) shows a different prompt explaining exactly
what's about to happen instead of the ordinary "write override and run
compose up" copy: confirming (`y`, labeled `create & adopt`) writes the
draft's current generated definition as a **brand-new base file** at that
path (no merge, no override — the base file *is* the full definition) and
starts the service from it, exactly the same validated
write-temp/`docker compose config`/promote/`up -d` sequence used elsewhere
in this document. From that point on the container is a normal, real
Compose service under WhatTheDock's (and `docker compose`'s) direct
management — the tool that deployed it originally no longer has a file to
manage it from.

This only ever triggers for an already-labeled container whose Compose file
path was already set when the form opened — typing a brand-new, not-yet-
existing path into a fresh `n` draft is unaffected and behaves as before.

## Compose File Browser

In Compose create mode, `Ctrl+O` opens the file browser from any field;
`Enter` opens it from the `Compose file` field specifically. For a local
system this reads the local filesystem; for an SSH system it lists the
remote host's filesystem instead (the panel title shows the system name,
e.g. `Directory (jarvis)`), one `ssh` round trip per directory — while that's
in flight the browser shows "Listing remote directory…".

Browser keys:

| Key | Action |
|---|---|
| `j` / `Down` | Move down |
| `k` / `Up` | Move up |
| `Enter` / `l` | Open directory or select file |
| `h` / `Backspace` | Go to parent directory |
| `Home` / `End` | Jump to first or last row |
| `Esc` | Return to the create form |

The browser shows directories plus likely Compose files:

- `compose*.yml`
- `compose*.yaml`
- `docker-compose*.yml`
- `docker-compose*.yaml`

Selecting a file writes its path back into the `Compose file` field.

## Standalone Container Flow

1. Press `n`.
2. Switch to the `Standalone container` tab (`[`/`]`).
3. Fill in `Name`, `Image`, optional `Command`, `Ports`, `Mounts`, `Env`, and
   `Restart`.
4. Check the inline validation line, or press `Ctrl+S` to validate explicitly.
5. Press `Ctrl+Enter` or `Alt+Enter` to review the confirmation.
6. Press `y` to create and start the container, or `n`/`Esc` to cancel.

Standalone creation supports:

- image
- container name
- command, split on shell-style whitespace
- env values as `KEY=value`
- ports as `host:container` or `host-ip:host:container/protocol`
- mounts as `source:target` or `source:target:ro`
- restart policy

After creation, WhatTheDock refreshes Docker state and targets the returned
container ID when Docker provides one.

## Delete, Replicate, Clone, and Edit

These act on the currently selected container/Compose service, from the
inspector pane or the command palette.

**Delete (`D`)** — a real, permanent removal, the same for either kind of
service: stop and remove the container, then delete its definition. For a
Compose service that's `docker compose rm -sf <service>` to stop/remove the
container, followed by removing the generated
`compose.whatthedock.<service>.yml` override (if one exists) and, if the base
compose file itself defines the service, removing that service's block from
base too (comment-preserving, the same style of edit Create's merge-into-base
path uses) — the service is gone, not reconciled back to a base definition
that recreates it. For a standalone container, Delete is a real `docker rm
-f` (stop-if-running + remove). Confirm with `y`, cancel with `n`/`Esc`.

**Replicate (`u`)** — pulls a fresh copy of the image and recreates the same
container/service in place, under its existing identity (name stays the
same). For a Compose service: `docker compose pull <service>` then
`up -d <service>` — Compose's own `up -d` recreates the container when the
image changed. For a standalone container: pull the image, stop and remove
the existing container, then recreate it with an identical spec (same name,
ports, mounts, env, restart policy, command). Confirm with `y`, cancel with
`n`/`Esc`.

The status bar reflects what's happening while Delete/Replicate/Create run,
instead of going quiet until they finish. Standalone Replicate shows live
per-layer pull progress (status and percentage) since it talks to the Docker
API directly; Compose Delete/Replicate/Create show a spinner with a single
phase label for the whole operation, since `docker compose` is shelled out
to and doesn't expose structured progress the way the Docker API does.

**Clone (`C`)** — duplicates the selected container/service under a *new*
name. Opens the create overlay prefilled with the original's image, ports,
mounts, env, restart policy, and command, with the identity field
(`Service` or `Name`) suffixed `-clone` so you rename it before confirming.
Unlike opening create for an already-managed service, Clone never loads an
existing override — the original is left completely untouched, and
confirming produces an independent second container/service.

**Edit (`m`)** — opens the create overlay prefilled from the selected
container/service under its *own* identity (not renamed), so confirming
replaces it in place instead of creating something new. For a Compose
service this is exactly what opening create (`n`) for an already-managed
service already does — loads the existing override, lets you change any
field, and re-applies via `docker compose up -d <service>` on confirm. For a
standalone container, Edit prefills the full current shape (image, ports,
mounts, env, restart policy, command) the same way Clone does, but confirming
stops and removes the *existing* container and recreates it under the same
name with your changes, instead of creating an independent second one.
`n` (Create) is deliberately left alone by this — it always defaults to a
fresh draft (or, for an already-managed Compose service, its own
load-existing-override behavior) and never silently overwrites a selected
standalone container the way Edit intentionally does.

## Current Limits

- Base-file merge editing only touches the structured fields the create form
  itself exposes (`Image`, `Restart`, `Command`, `Ports`, `Mounts`, `Env`); it
  can't add or edit keys the form doesn't have a field for (`networks`,
  `depends_on`, `labels`, `build`, ...) — those pass through untouched, but
  changing them still requires editing the compose file outside WhatTheDock.
- Environment is always written back as a YAML list of `KEY=value` strings on
  a base-file merge, even if it was previously a `KEY: value` map — both are
  valid Compose syntax, so this is a formatting change, not a semantic one.
- Remote (SSH) Compose operations are one `ssh` invocation per step (listing
  a directory, writing the override or base file, each `docker compose` call)
  rather than a shared multiplexed connection, so each has its own
  connection-setup latency — noticeable but not prohibitive for typical use.
- Standalone command parsing uses whitespace splitting, not full shell quoting.
- Replicate only has a visible effect on mutable tags (`:latest`-style);
  pulling an image pinned to a fixed tag or digest is a no-op.
