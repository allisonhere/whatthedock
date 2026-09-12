# WhatTheDock: Compose edit wholesale-overwrites base file

## Status as of writing
- Bugs #1, #2, and #3 below are ALL FIXED and tested in the repo
  (fix for #3: `FullBase: d.OverrideRawBase && !d.FieldsDirty` in
  ComposeSpec, plus TestEditingFieldOnBaseDefinedServicePreservesUnmanagedKeys
  in internal/ui/create_test.go — reverted-and-confirmed to catch the bug
  before restoring the fix). Full check suite (go test/-race/vet/build/
  gofmt) passes. NOT YET COMMITTED — still uncommitted in the working tree.
- Live incident on jarvis (192.168.86.74) is UNCHANGED/NOT YET RECOVERED:
  `dash` container is still named
  `dash-dash-1`, running on the `dash_default` bridge network instead of
  `network_mode: host`. Service is UP, just misconfigured/possibly
  unreachable on its usual port from outside.

## The three bugs (found in this order)

### Bug #1 (FIXED) — async base-file load clobbers in-progress edits
Opening Edit on a Compose service kicks off an async load of the base
compose file to prefill the form (`openEditOverlay` ->
`loadSelectedComposeFileCmd`). On a remote (SSH) system that's a real
network round trip. If the user edits a field and confirms before that
load lands, the load's handler used to unconditionally overwrite the
just-edited field with the base file's original value — silently, no
error.

Fix: added `createDraft.FieldsDirty bool` (internal/ui/create.go), set
true on any non-navigation key in the create form (handleCreateKey),
reset false on a fresh draft open (openCreateOverlayWithDraft) and on a
raw-editor save (saveCreateEditor). The async handlers in model.go
(`createSelectedComposeFileMsg`, `createOverrideCheckMsg`) now skip
re-populating visible fields from the load when FieldsDirty is true —
they still record the structural OverrideRaw/OverrideRawSet/... bookkeeping
regardless.

### Bug #2 (FIXED) — ComposeSpec always preferred stale OverrideRaw
Even once #1 stopped the field from being visibly clobbered,
`createDraft.ComposeSpec()` had:
```go
content := d.composeOverrideContent()  // regenerated from live fields
if d.OverrideRawSet {
    content = d.OverrideRaw            // always won instead
}
```
`OverrideRawSet` is true on every edit of an existing Compose service (set
the moment the base/override file is loaded). So the regenerated content
reflecting the user's edit was *always* discarded in favor of the raw
snapshot — meaning editing any field on an existing Compose service
applied nothing, ever, with no error, regardless of the timing race in
bug #1.

Fix: `if d.OverrideRawSet && !d.FieldsDirty { content = d.OverrideRaw }` —
only trust the raw snapshot when nothing's been edited since it was
captured.

### Bug #3 (NOT FIXED) — regenerated content used to wholesale-replace the base file
Once bug #2 started correctly regenerating `spec.Content` from live
fields, this exposed a third, separate bug: `spec.Content` — now a SPARSE
document containing only the 6 fields the form covers (image, restart,
command, ports, volumes, environment) — gets used to **completely
overwrite the base compose.yaml**, instead of being merged field-by-field
into it.

Location: `mergeComposeCreateIntoBase` in `internal/ui/create.go`:
```go
func mergeComposeCreateIntoBase(ctx context.Context, spec composeCreateSpec, baseContent []byte) error {
	if spec.FullBase && singleComposeServiceName(spec.Content) == spec.Service && singleComposeServiceName(string(baseContent)) == spec.Service {
		// WHOLESALE REPLACE: baseContent gets thrown away entirely,
		// spec.Content (the sparse regenerated doc) becomes the new file.
		...
		os.WriteFile(tempBase, []byte(spec.Content), 0o644)
		...
		return composeUpService(ctx, baseOnly)
	}
	// else: surgical merge via mergeComposeServiceFields (preserves
	// container_name, network_mode, build, comments, etc. — only touches
	// the 6 known fields)
	...
}
```
`spec.FullBase` comes from `ComposeSpec()`'s `FullBase: d.OverrideRawBase`.
`OverrideRawBase` gets set to `true` whenever the base file was loaded as
a single-service document (see `createSelectedComposeFileMsg`'s handler in
model.go) — this was meant to signal "this draft's content IS the
complete base file document" (true when `OverrideRaw` is used verbatim,
e.g. unedited, or genuinely hand-typed via ctrl+y as a full replacement).

The bug: once bug #2's fix makes `ComposeSpec()` regenerate `spec.Content`
via `composeOverrideContent()` (because FieldsDirty), that content is no
longer "the complete base file" — it's sparse, missing anything not in
the 6 form fields. But `FullBase` (from `OverrideRawBase`) is still true,
so the wholesale-replace branch still fires, and
`singleComposeServiceName(spec.Content) == spec.Service` still passes
(the regenerated content is also single-service), so the guard doesn't
catch it either. Real result on `dash`: `container_name: dash`,
`network_mode: host`, `build: .`, and every comment in the file were
deleted; only image/restart/volumes/environment survived. Docker Compose
then fell back to its own default container naming
(`<project>-<service>-<n>` = `dash-dash-1`) and default bridge networking
(`dash_default`) since `container_name`/`network_mode` were gone.

### Fix applied for bug #3
The wholesale-replace branch must only run when `spec.Content` is
genuinely a complete, trustworthy document — i.e., when it's still
`d.OverrideRaw` verbatim (unedited raw content, or hand-typed via ctrl+y),
never when it's the regenerated sparse `composeOverrideContent()`. The
cleanest fix: thread a "content is a complete document, not just the
form's 6 fields" signal into `composeCreateSpec` (e.g. rename/reinterpret
`FullBase`, or add a distinct field) and set it `false` whenever
`ComposeSpec()` falls back to `composeOverrideContent()` because
`FieldsDirty` — even if `OverrideRawBase` is true. Concretely: in
`ComposeSpec()`, `FullBase` should be `d.OverrideRawBase && !d.FieldsDirty`
(mirroring the same condition already used for the content-selection
line), not just `d.OverrideRawBase` alone. That way, once a field is
edited, the merge path always goes through the safe surgical
`mergeComposeServiceFields` route (which only ever touches the 6 known
keys and leaves everything else — container_name, network_mode, build,
comments — untouched), regardless of whether the base file happens to be
a single-service document.

Needs a regression test: build a draft with `OverrideRawBase: true`,
`FieldsDirty: true`, and a base file containing `container_name`/
`network_mode`/`build`/comments beyond the 6 form fields; call through
the actual apply path (`mergeComposeCreateIntoBase` or equivalent) and
assert those extra keys and comments survive, and only the edited field
changes.

## Live incident recovery (jarvis, 192.168.86.74)
`dash`'s compose.yaml is a git-tracked file at `/home/allie/dash/compose.yaml`.
Recovery (not yet done — blocked on a remote-write permission prompt when
Claude attempted it via scp):
```bash
cd /home/allie/dash
git diff compose.yaml      # see what WhatTheDock wrote
git checkout compose.yaml  # restore the original (container_name, network_mode, build, comments)
# then hand-edit just the "restart: unless-stopped" line to "restart: always"
docker compose up -d
```
Current live state before recovery: container is named `dash-dash-1`,
running, `RestartPolicy: always` (correct), `NetworkMode: dash_default`
(wrong — should be host). Not actually down, just not on host networking.

## Files touched (bugs #1, #2, #3 — all fixed, not yet committed)
- internal/ui/create.go — FieldsDirty field + doc comment, dirty-marking
  in handleCreateKey, ComposeSpec's content-selection guard,
  openCreateOverlayWithDraft reset, saveCreateEditor reset.
- internal/ui/model.go — createSelectedComposeFileMsg and
  createOverrideCheckMsg handlers guarded on m.createDraft.FieldsDirty.
- internal/ui/create_test.go — TestEditingRestartBeforeBaseComposeLoadArrivesIsNotClobbered,
  TestComposeSpecUsesRegeneratedContentAfterFieldEdit,
  TestComposeSpecPrefersOverrideRawWhenFieldsUntouched, plus a fix to
  TestCreateStrayKeyOnModeFieldIsIgnoredNotTypedElsewhere to ignore the
  new FieldsDirty field in its full-struct comparison.
- start.sh — earlier, unrelated fix: no longer hardcodes a tunnel to
  jarvis; just runs against local Docker.

Run before committing: go test ./..., go test -race ./..., go vet ./...,
go build -buildvcs=false ./..., gofmt -l .
