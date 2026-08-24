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
- Single-service Create/Edit drafts show `Image action` (`keep` or
  `pull latest`); stack drafts do not.
- Create/Edit confirmation keeps the overlay open while work is running and
  shows the current phase plus a smooth 0–100% progress bar near the confirmed
  action, with no duplicate status-bar progress for the same operation. Fast
  successful actions wait for the roughly three-second cosmetic bar to finish;
  failures still return immediately.
- `Ctrl+S` validation, catalog save, and override-YAML save feedback render as
  create-local notices in the active create/catalog/editor overlay instead of
  relying on the bottom status bar.
- Opening create for an already-managed service detects and loads its
  existing override instead of regenerating it, and the form's fields sync to
  match whatever override content ends up loaded or hand-edited.
- `Ctrl+Y` opens the override YAML in a full-size Ripple editor, with
  real-time lint feedback, syntax highlighting, and an optional persistent
  vim mode.

Local working tree at handoff:

- Branch `main`, ahead of `origin/main` by 2 commits.
- Modified files are the current task surface:
  `README.md`, `docs/creation.md`, `docs/handoff.md`,
  `internal/ui/create.go`, `internal/ui/create_test.go`,
  `internal/ui/create_view.go`, `internal/ui/model.go`,
  `internal/ui/model_test.go`, and `internal/ui/view.go`.
  No untracked files were present in the latest `git status --short --branch`.

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
    Create/Edit/Replicate progress now uses generalized
    `actionProgress`/`actionProgressText` instead of the old
    replicate-only channel.
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
    Also owns create-local notices (`createNotice`) and the create/edit
    command progress callbacks for standalone Docker API pulls and Compose
    phase labels.
- `internal/ui/compose_merge.go`
  - Comment- and format-preserving edits to Compose YAML via `yaml.v3`
    `Node`s: merging structured fields into an existing service's block
    (`mergeComposeServiceFields`) and removing a service's block entirely
    (`removeComposeService`), used by Create/Edit and Delete respectively
    whenever the base compose file already defines the target service.
- `internal/catalog` + `internal/ui/compose_curator.go`
  - Local Compose catalog storage and the command-palette Compose curator:
    captures actual local/SSH Compose source file sets from running stacks,
    preserves notes as a managed header comment on the primary file, marks
    catalog entries unused when the active snapshot no longer references
    their source paths, supports draft/saved/applied status plus tags/status
    filters, adds entries from URLs/paths/file browser/blank drafts, previews
    catalog entries on Enter, edits primary Compose YAML through Ripple, loads
    entries into Create explicitly, duplicates entries as drafts, and makes
    catalog entries live with an overwrite summary before existing target
    files are replaced.
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
    browser, and the syntax-highlighted override preview/editor. The
    confirmation view renders `createActionProgressView`, a single
    spinner-plus-progress row; form/catalog/editor overlays render
    `createNoticeView` near their action hints.
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
3. `Ctrl+S` validates and shows an inline notice near the form actions.
4. `Ctrl+Enter` or `Alt+Enter` opens confirmation.
5. `y` writes the override and runs Compose while the confirmation overlay
   shows progress.

Standalone mode:

1. Fill draft fields.
2. Optionally set `Image action` to `pull latest`.
3. `Ctrl+S` validates and shows an inline notice near the form actions.
4. `Ctrl+Enter` or `Alt+Enter` opens confirmation.
5. `y` calls Docker create/start through `app.Provider` while the
   confirmation overlay shows progress.

## Safety Behavior

- Destructive or mutating create actions require confirmation.
- Once Create/Edit work is in flight, the confirmation overlay ignores cancel
  keys until completion. No cancellation path exists yet.
- Compose override creation validates a temporary file first.
- Failed Compose validation removes the temporary override and does not promote
  the final override.
- Standalone `pull latest` pulls the image before create/recreate. For Edit,
  pull failure returns to the editable form without removing the existing
  container.
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

- `go test ./internal/ui`
- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `go build -buildvcs=false ./...`
- `gofmt -l .`
- `git diff --check`

## Next Work

- Reevaluate all status-bar messages. Recent create/edit work moved progress,
  validation, and save feedback closer to the action that triggered it; the
  rest of the app should get the same pass so transient confirmations and
  recoverable errors appear in the active overlay/pane first, with the status
  bar as a secondary fallback or log surface.

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
- Delete/Replicate/Create/Edit dispatch now sets `m.busy` and an immediate
  phase label. A spinner animates from the shared
  `github.com/allisonhere/cli-spinners` frame data, driven by the same tick
  that already animates the connected-status dot
  (`m.statusPulseFrame`/`tickStatusPulse`). Standalone Docker API pull paths
  stream real pull progress (`app.PullProgress`, via `PullImage`'s
  `onProgress` callback) into generalized `m.actionProgress`, drained one
  line per tick in the `statusPulseTickMsg` handler. Create/Edit confirmation
  renders that progress as a smooth percentage bar inside the overlay near the
  confirmed action; the status bar intentionally does not mirror that same
  progress while the overlay is open. The bar is deliberately fake on timing:
  fast successes are held until it reaches 100%, while failures finalize
  immediately. Compose Create/Edit emit deterministic phase labels around
  write/validate/pull/up, because `docker compose` is shelled out to and has no
  structured progress to surface. If you touch dispatch for any of these
  actions again, keep setting `m.busy` and the local phase label there — it's
  easy to reintroduce a silent fire-and-forget dispatch by accident.
- Added `m` (edit) — opens the create overlay prefilled from the selected
  container/service under its own identity (not renamed, unlike Clone), so
  confirming replaces it in place. For Compose this is exactly
  `openCreateOverlay`'s existing already-managed-service path, reused as-is
  (`Model.openEditOverlay` in `create.go` just flags `createDraft.Editing`
  after calling it). Standalone gets a dedicated `defaultEditDraft`
  (`create.go`, full Ports/Mounts/Env/Restart/Command like `defaultCloneDraft`
  but keeping the real name) and a new `editContainerCmd` (remove + recreate
  under the edited spec, with optional pull-before-remove when `Image action`
  is `pull latest`). Deliberately does *not* change `n`/Create's behavior for a
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
  the model — an automatic check on every launch, a `updateLastCheck`
  persisted setting used only to display the last completed check in
  Settings, a "Check for update" row in Settings; and an "update available"
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

## Planned: Inspector Pane Row Cursor, Copy & Expand

Done — implemented in the same session this was drafted, following the
prompt below step by step. Left here as a design reference: `inspectorRow`/
`inspectorRows()` (view.go), the row cursor (`inspectorCursor`,
`moveInspectorCursor`/`clampInspectorScroll` in model.go), the ellipsis and
`labelWidth` fixes, alternating-row backgrounds (`altRowBackground`), and
the Enter-triggered expand/select-to-copy overlay
(`internal/ui/inspector_detail.go`) all match this plan as written, with
one correction found during implementation: `model.go` mouse-wheel
handling (`handleMouse`) had its own pair of `scrollInspector` call sites
this prompt's file/line references didn't mention — those were updated to
`moveInspectorCursor` too, same as the j/k branches.

~~~
The Inspector pane (the right-hand detail column showing the selected
container's Status/Image/Compose/Network/Files/Metadata) is currently
passive: j/k while focused there only scrolls the whole rendered block
(scrollInspector, model.go — a single int offset), with no concept of
"which row." Implement row-level navigation, copy, and expand for it.
Confirmed scope: read-only. Nothing here mutates container/host data —
"editable" means select-and-copy only.

Do this in order, running `make check` after each step:

1. Row-list refactor. Replace renderInspector's closures-that-append-
   strings (view.go, ~1590-1640: addSection/add building a flat []string)
   with a structured row list built once per frame:

     type inspectorRowKind int
     const (
         inspectorRowSection inspectorRowKind = iota
         inspectorRowTitle
         inspectorRowField
     )
     type inspectorRow struct {
         kind             inspectorRowKind
         section, title   string
         label            string          // field rows only
         display          string          // capped/formatted value shown inline
         value            string          // FULL value — copy/expand source of truth
         suffix           string          // existing "c"/"c/o" hint from detailHint
         color            lipgloss.Color
         muted            bool
     }
     func (m Model) inspectorRows() []inspectorRow

   Important: formatList/formatMap (view.go ~3800-3833, used for the Env
   and Labels rows) take a `limit` and bake a "... N more" line into the
   string they return once a container has more than 8 entries — today's
   value is already silently truncated. For those two fields only, compute
   `display` with the existing capped call (what the inline pane shows) and
   `value` with an uncapped call (formatList(ctr.Env, len(ctr.Env)) /
   formatMap(ctr.Labels, len(ctr.Labels))) so expand/copy later actually
   surface every entry, not just the already-capped 8. Every other field
   (Image, Ports, Mounts, etc. — none of these are capped) uses the same
   string for both.

   Also important: renderInspectorField (view.go 1658-1695) already splits
   a field's value on "\n" and renders one screen line per entry, so one
   logical field (e.g. Labels with 6 entries) can span multiple physical
   screen lines. inspectorRows() entries are NOT 1:1 with screen lines.
   Build, alongside the row list, the existing flat []string of rendered
   screen lines plus a parallel []int (lineRow) recording which
   inspectorRows() field-row index (-1 for section/title lines) each
   screen line belongs to — every later step (highlight, alt-stripe,
   scroll-follow) needs this mapping and must NOT assume one row equals
   one screen line. Do not reuse tideui's visibleRange (view.go:265,
   built for the tree pane, which is 1:1 row-to-line) directly against
   inspectorRows() — it will mis-budget/mis-scroll against multi-line
   rows. Instead extend the existing start/end clamp at view.go:1637-1638
   to look at the cursor row's full screen-line span via lineRow.

   Verify this step renders byte-identical output to today (no cursor, no
   stripes yet) before moving on — check internal/ui/view_test.go for
   existing Inspector coverage to diff against.

2. Formatting fixes, on top of the now-structured renderer:
   - Ellipsis: renderer.RenderRow -> tideui's alignRow (external module,
     layout.go 377-410) calls ansi.Truncate(_, _, "") — an EMPTY ellipsis
     argument, so long lines are hard-cut with no "…" at all, unlike other
     truncations in this same file (view.go 316, 369, 3269, which
     correctly pass "…"). Do not patch the shared tideui module. Instead
     pre-truncate each split line locally with ansi.Truncate(line,
     maxWidth, "…") before it reaches RenderRow, computing maxWidth the
     same way alignRow does (layout.go 397-406) so the two agree and
     tideui's own truncation becomes a no-op.
   - labelWidth := 8 (view.go:1659) is a hardcoded const too narrow for
     some labels ("Restart Policy"-length ones). Compute it once per
     renderInspector call from the actual labels in use
     (lipgloss.Width over every label in inspectorRows()).
   - short() (view.go:3890, used for Image ID at view.go:1612) does a raw
     value[:n] byte slice — unsafe on non-ASCII digests, no truncation
     indicator. Fix that one call site to ansi.Truncate(value, n, "…").
     Leave short()'s other call site (handleCopyKey's status message,
     model.go:2253) alone.

3. Row cursor + scroll-follow + highlight. New Model field
   `inspectorCursor int` (near inspectorScroll, model.go:330), indexing
   only inspectorRowField rows:

     func (m *Model) moveInspectorCursor(delta int) {
         fields := m.inspectorFieldRowCount()
         if fields == 0 { m.inspectorCursor = 0; return }
         m.inspectorCursor = clamp(m.inspectorCursor+delta, 0, fields-1)
     }

   Wire into the three `case m.focus == paneInspector:` arms currently
   calling scrollInspector (model.go 1672-1673/1684-1685 for j/k;
   4016-4017/4026-4027 for pgup/pgdown, page-sized). Delete
   scrollInspector (model.go:4079) once unused. Reset
   `m.inspectorCursor = 0` everywhere `m.inspectorScroll = 0` already is
   (model.go 1164, 3010, 4273 — container-selection changes). Highlight
   background: reuse `renderer.Styles.ItemSelected.GetBackground()`, the
   same lookup the image/network/volume curation overlays already use for
   their cursor row (internal/ui/images.go and networks.go/volumes.go) —
   one highlight color throughout the app.

4. Row-level quick copy. copyTextCmd(value string) tea.Cmd (model.go
   3305-3311, OSC52 write to clipboardWriter) is already a pure,
   overlay-independent primitive — reuse it directly. Change the global
   `c` handler (model.go 1805-1806, currently unconditional
   m.openCopyOverlay()) to check focus first:

     case "c":
         if m.focus == paneInspector {
             if row, ok := m.currentInspectorFieldRow(); ok {
                 m.status, m.statusErr = "copied "+strings.ToLower(row.label)+" "+short(row.value, 48), false
                 return m, copyTextCmd(row.value)
             }
         }
         m.openCopyOverlay()

   Only overrides when focus is actually on Inspector with a valid row
   under the cursor — tree/activity focus must keep today's Copy overlay
   unchanged; verify that explicitly.

5. Alternating row backgrounds. tideui.Theme (theme.go 13-27) has no
   alt/stripe token — don't add one to the shared module. Derive a subtle
   variant of Theme.Bg locally: go-colorful is already an indirect
   dependency (via tideui); promote it to a direct import and add

     func altRowBackground(bg lipgloss.Color) lipgloss.Color

   blending Theme.Bg a small fixed amount toward white or black depending
   on the theme's own perceived lightness (lighten dark themes, darken
   light ones). Compute per-render, not cached — several built-in themes
   are light (catppuccin-latte, gruvbox-light, tokyo-night-day,
   rose-pine-dawn, per theme.go's BuiltinThemes) and the live theme picker
   (T key) can swap themes at runtime, so a hardcoded hex would look wrong
   under at least one theme. Stripe parity keys off the field row's
   ordinal among field rows (via lineRow from step 1), not raw screen-line
   index, so a multi-line row stays one consistent stripe color. Selected-
   row highlight always wins over the stripe.

6. Enter -> expand + select-a-portion overlay. One overlay for both "show
   the full value" and "select a substring to copy":

   New overlayMode value `overlayInspectorDetail` (append after
   overlayVolumeCuration, model.go ~75), wired into the same two switches
   that dispatch every other overlay (model.go ~2023 region for keys,
   view.go ~2066 region for rendering).

   New flat Model fields (matching how settingsEditCursor/systemCursor are
   already flat fields, not a sub-struct):

     inspectorDetailLabel  string
     inspectorDetailValue  []rune  // snapshotted full value when opened
     inspectorDetailCursor int     // caret, rune index
     inspectorDetailAnchor int     // selection anchor, rune index; -1 = none

   `enter` while m.focus == paneInspector (new branch alongside the
   existing enter cases, model.go 1737-1753) snapshots the current row
   into these fields and sets m.overlay = overlayInspectorDetail.

   New key handler, shaped like handleCopyKey (model.go 2236-2257) for
   top-level actions, with left/right/shift+arrow caret movement adapted
   from the existing Systems-editor caret pattern
   (settingsEditValueWithCaret, model.go 2413-2423, and
   systemFieldValueWithCaret, model.go:2764) minus every mutation branch
   (no backspace/insert — read-only):
   - left/right: move caret, clamp [0, len(value)], collapse any selection.
   - shift+left/shift+right: if no anchor yet, set
     inspectorDetailAnchor = inspectorDetailCursor first (lazy-start,
     matching Ripple's own shift+arrow selection convention), then move.
   - home/end: jump caret to start/end.
   - c or enter: copy the selection if inspectorDetailAnchor != -1
     (substring between min/max of anchor and cursor), else the whole
     value, via copyTextCmd; status message mirrors step 4's phrasing;
     close the overlay.
   - esc: close, row cursor position in the main pane unchanged.

   Rendering (new method inspectorDetailBody, living together with this
   step's state/key logic in a new internal/ui/inspector_detail.go rather
   than growing view.go/model.go further): word-wrap the value to the
   overlay's inner width (check internal/ui/editor_area.go first for an
   existing wrap helper already used by the YAML editor before writing a
   new one), walk the wrapped lines to map each rune index to a
   line/column, then either splice a literal `|` at the caret's wrapped
   position (no selection — same idiom as systemFieldValueWithCaret, just
   per-line) or wrap the selected range using backgroundSpan
   (view.go:1714, already built for "highlight a substring without
   clobbering the row's own background"), sourced from the same
   ItemSelected highlight color as the row cursor. Assemble with
   tideui.SoftPanelOverlay — the same primitive the image/network/volume
   curation overlays already use, no new chrome. Hints row: "←/→ move ·
   shift+←/→ select · c/enter copy · esc close".

Verification: make check after each step. Then a pty-scripted --demo smoke
test: Tab into Inspector, confirm j/k moves a highlighted row (not a raw
scroll), confirm the alt-stripe is visible, confirm a long value shows "…"
instead of a hard cut, press c on a short field and confirm the status bar
shows "copied <label> …" with no overlay, press Enter on a truncated/long
row and confirm the full value renders wrapped with a caret, move the caret
with arrows, shift+arrow to select a substring, copy it, confirm the status
message reflects the selection (not the whole value), Esc back and confirm
the row cursor position was preserved. Confirm switching the selected
container resets inspectorCursor to 0. Confirm c on the tree/activity panes
still opens the original Copy overlay unchanged.
~~~

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
