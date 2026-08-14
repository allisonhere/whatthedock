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

1. Press `n`.
2. Make sure the `Compose service` tab is active (`[`/`]` to switch).
3. Fill in `Project`, `Service`, `Image`, optional `Ports`, `Mounts`, `Env`,
   `Restart`, and `Compose file`.
4. Use `Enter` or `Ctrl+O` on the `Compose file` field to browse for a file
   (local or, for an SSH system, on the remote host). Plain `o` types the
   letter into whatever field is focused — including `Compose file` itself,
   so you can type a path by hand — rather than opening the browser; only
   choice fields (`Mode`, `Restart`) treat bare `o` as a shortcut.
5. Optionally press `Ctrl+Y` to hand-edit the generated override YAML
   directly (see Override YAML Editor below).
6. Check the inline validation line, or press `Ctrl+S` to validate explicitly.
7. Press `Ctrl+Enter` or `Alt+Enter` to review the confirmation.
8. Press `y` to create, or `n`/`Esc` to cancel.

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

When confirmed, WhatTheDock writes a generated override file beside the selected
Compose file:

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

For an SSH system, every step above (the `test -f` base-file check, `mkdir
-p`, writing the temp override, `docker compose config`, the rename, and
`docker compose up -d`) runs on the remote host over `ssh` instead of the
local filesystem/`docker` binary.

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

## Current Limits

- Compose creation writes generated override files; it does not yet edit or
  merge into existing Compose YAML.
- Remote (SSH) Compose operations are one `ssh` invocation per step (listing
  a directory, writing the override, each `docker compose` call) rather than
  a shared multiplexed connection, so each has its own connection-setup
  latency — noticeable but not prohibitive for typical use.
- Standalone command parsing uses whitespace splitting, not full shell quoting.
