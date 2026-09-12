# Plan: Land Omarchy theme support in WhatTheDock

## Goal

Give WhatTheDock a "match-omarchy" theme that follows the active Omarchy desktop
palette, the same way the sibling Tide apps (`../tide`, `../tidemail`, `../tideftp`)
already do — and land it on `main`.

## Current context / assumptions (read first)

This work is **already implemented and sitting on an unmerged branch**. Do not
write it from scratch.

- `main` is at `317906a` and is the repo checked out at `/home/allie/Projects/whatthedock`.
- Branch `omarchy-theme-support` (`a155bc5`) contains the full implementation.
  It is **exactly `main` + 2 commits** (merge-base is `main`'s own HEAD, so there
  are zero `main`-only commits to reconcile — a clean fast-forward).
  - `54c699a` — "Make CPU a real gauge, and color braille dots by level" (an
    **unrelated** CPU-gauge feature bundled onto the same branch).
  - `a155bc5` — "Follow the Omarchy desktop theme, and make every widget obey the theme"
    (the actual omarchy work, 18 files, +1826/-251).
- The branch is materialized as a git worktree at
  `/home/allie/Projects/whatthedock/.claude/worktrees/omarchy-theme` (checked out
  to `omarchy-theme-support`). Edit **there**, not in the main checkout.
- The sibling apps share the identical pattern: `internal/omarchy/omarchy.go`
  (palette reader) + an `internal/ui/*omarchy*.go` theme mapper + contrast
  correction + a 2s poll loop that re-resolves when the desktop theme changes.
  WhatTheDock's version matches, and additionally adds `internal/ui/palette.go`
  to derive semantic accent roles (health/log/stats colors) from the theme so the
  ~150 hardcoded hex literals follow the theme instead of staying fixed.

What the branch already contains (all verified present):

- `internal/omarchy/omarchy.go` (374 lines) + `internal/omarchy/omarchy_test.go`
- `internal/ui/omarchy.go` (418) + `internal/ui/omarchy_test.go` (174)
- `internal/ui/palette.go` (118) — theme-derived accent roles
- `internal/ui/terminal.go` (47) + test — OSC 10/11 fg/bg sequences
- `cmd/whatthedock/main.go` — `--theme-info` flag, applies/restores terminal colors
- integration in `internal/ui/model.go` (theme picker, `omarchyThemeTickMsg`, 2s poll)
  and widget files (`view.go`, `images.go`, `networks.go`, `volumes.go`,
  `paste_view.go`, `inspector_detail.go`).

Verified against this machine:
- `gofmt -l .` → empty, `go vet ./...` → clean, `go build -buildvcs=false ./...` → clean.
- **3 ui tests fail** because `omarchy-theme-color` is on PATH
  (`/usr/share/omarchy/bin/omarchy-theme-color`) and reads the *real* desktop
  palette (`background #1c1213`, `foreground #e3a68c`), overriding the `OMARCHY_DIR`
  fixtures those tests set. See the fix below.
- **2 `cmd/whatthedock` doctor tests fail** on *both* `main` and the branch —
  pre-existing, environment-dependent, unrelated to omarchy (see "Out of scope").

## Architecture / proposed approach

Do not re-implement. Take the existing `omarchy-theme-support` branch, fix the one
real defect it has (the resolver ignoring `OMARCHY_DIR`, which makes three tests
non-hermetic on machines that have Omarchy installed), re-run the checks, and
fast-forward `main` onto it. The only new code is a one-line guard plus its test.

## Step-by-step tasks

Work in the branch worktree for all edits:

```sh
cd /home/allie/Projects/whatthedock/.claude/worktrees/omarchy-theme
```

### Task 0 — Confirm starting state (2 min)

```sh
git status --short        # expect: clean (no output)
git rev-parse HEAD        # expect: a155bc568ecf874b04b1652ddfff0b4f32eb9c0f
```

### Task 1 — Write the failing test (TDD, 3 min)

The root cause: `runResolver()` in `internal/omarchy/omarchy.go` shells out to
`omarchy-theme-color` whenever it's on PATH, even when `OMARCHY_DIR` is set, so it
reads the developer's real desktop palette and clobbers the test fixtures.

Add this test to the **end** of `internal/omarchy/omarchy_test.go` (imports
`os` and `path/filepath` are already present):

```go
// OMARCHY_DIR must win over the resolver: a resolver left on PATH would read
// the real desktop theme and override the pinned dir, so runResolver must not
// run when OMARCHY_DIR is set.
func TestCurrentPaletteOMARCHYDirSkipsResolver(t *testing.T) {
	bin := t.TempDir()
	fake := "#!/bin/sh\nprintf 'background\\t#ffffff\\nforeground\\t#000000\\naccent\\t#ff0000\\nmode\\tdark\\n'\n"
	if err := os.WriteFile(filepath.Join(bin, "omarchy-theme-color"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	root := t.TempDir()
	t.Setenv("OMARCHY_DIR", root)
	themeDir := filepath.Join(root, "current", "theme")
	if err := os.MkdirAll(themeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(themeDir, "colors.toml"), []byte("background = \"#123456\"\nforeground = \"#abcdef\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	p, ok := CurrentPalette()
	if !ok {
		t.Fatal("CurrentPalette should read the OMARCHY_DIR theme")
	}
	if p.Background != "#123456" {
		t.Fatalf("Background = %q, want OMARCHY_DIR's #123456 (resolver must be skipped)", p.Background)
	}
}
```

Run it and confirm it **fails** (the fake resolver's `#ffffff` wins today):

```sh
go test ./internal/omarchy/ -run TestCurrentPaletteOMARCHYDirSkipsResolver -v
# expect: --- FAIL: TestCurrentPaletteOMARCHYDirSkipsResolver
#         omarchy_test.go:…: Background = "#ffffff", want OMARCHY_DIR's "#123456"
```

### Task 2 — Fix `runResolver` (2 min)

In `internal/omarchy/omarchy.go`, replace the start of `runResolver`:

```go
// runResolver runs `omarchy-theme-color --all` and returns its key→value map.
func runResolver() (map[string]string, bool) {
	bin, err := exec.LookPath("omarchy-theme-color")
```

with:

```go
// runResolver runs `omarchy-theme-color --all` and returns its key→value map.
func runResolver() (map[string]string, bool) {
	// OMARCHY_DIR pins the palette source to a specific directory for
	// containers and deterministic tests. The resolver shells out to the real
	// `omarchy-theme-color` binary, which reads the global Omarchy state and
	// ignores OMARCHY_DIR — letting it run here would override the pinned dir
	// (and make any OMARCHY_DIR-based test read the developer's own desktop
	// theme). Skip it whenever the override is set.
	if os.Getenv("OMARCHY_DIR") != "" {
		return nil, false
	}
	bin, err := exec.LookPath("omarchy-theme-color")
```

(`os` is already imported in this file.)

Run the test again and confirm it **passes**:

```sh
go test ./internal/omarchy/ -run TestCurrentPaletteOMARCHYDirSkipsResolver -v
# expect: --- PASS: TestCurrentPaletteOMARCHYDirSkipsResolver
```

### Task 3 — Confirm the three previously-failing ui tests now pass (2 min)

```sh
go test ./internal/ui/ -run 'TestOmarchyRecoveryAndPickerRenderCurrentPalette|TestMatchOmarchyLoadsAndReloadsLegacyCurrentTheme|TestMatchOmarchyProvidesTerminalColorSequences' -v
# expect: 3x PASS, ok  github.com/allisonhere/whatthedock/internal/ui
```

(These now pass because the model builds its palette through `CurrentPalette`,
which no longer lets the on-PATH resolver override `OMARCHY_DIR`.)

### Task 4 — Full check suite (3 min)

```sh
gofmt -l .                     # expect: no output
go vet ./...                   # expect: no output
go build -buildvcs=false ./... # expect: no output
go test ./internal/omarchy/ ./internal/ui/   # expect: both `ok`
go test -race ./internal/omarchy/ ./internal/ui/  # expect: both `ok`
```

Then run the whole suite once to see the known pre-existing failures:

```sh
go test ./... 2>&1 | grep -E '^(ok|FAIL|---)' 
# expect: ok for every package EXCEPT:
#   FAIL  github.com/allisonhere/whatthedock/cmd/whatthedock   (2 pre-existing doctor tests, see below)
```

### Task 5 — Commit the fix (1 min)

```sh
git add internal/omarchy/omarchy.go internal/omarchy/omarchy_test.go
git commit -m "Make OMARCHY_DIR override win over the omarchy-theme-color resolver"
```

### Task 6 — Land on main (2 min)

```sh
cd /home/allie/Projects/whatthedock
git merge --ff-only omarchy-theme-support   # clean fast-forward; expect "Fast-forward"
git log --oneline -3                        # expect the new commit on top of 317906a
```

Do **not** push unless the user asks (per repo rules: no push/commit without an
explicit ask — Task 5/6 commits are listed so the work is captured, but confirm
with the user before `git push origin main`).

## Tests / validation

TDD cycle is baked into Tasks 1–2 (write failing test → run to confirm failure →
minimal fix → run to confirm pass). The primary acceptance gate is:

- `go test ./internal/omarchy/ ./internal/ui/` is fully green (with and without
  `-race`).
- `gofmt -l .`, `go vet ./...`, `go build -buildvcs=false ./...` are all clean.
- The only remaining `go test ./...` failures are the 2 known pre-existing doctor
  tests (below) — confirmed to also fail on `main` before this change.

Manual sanity (optional, only if the user wants a visual check): run the demo and
pick "match-omarchy" from the theme picker, or run the new CLI probe:

```sh
go run -buildvcs=false ./cmd/whatthedock --theme-info
# expect: JSON with the live Omarchy palette (name/mode/background/foreground/…),
#         or exit 1 with "no usable Omarchy palette found" if Omarchy isn't running.
```

## Risks, tradeoffs, and open questions

1. **The branch bundles an unrelated CPU-gauge commit (`54c699a`).** The omarchy
   commit is authored on top of it and both touch `view.go`/`view_test.go`, so
   they cannot be cleanly separated without a risky rebase. Recommendation: merge
   the whole branch (both commits) — the CPU-gauge change is small and already
   tested. **Open question for the user:** is including the CPU-gauge commit
   acceptable, or should it be split out first (extra, riskier work)?

2. **The resolver gate trade-off.** With `OMARCHY_DIR` set, the `omarchy-theme-color`
   resolver is now skipped, so a container that sets `OMARCHY_DIR` *and* relies on
   the resolver's alias/shade cascade would fall back to raw `colors.toml` parsing.
   This only affects explicit `OMARCHY_DIR` users (tests/containers), never normal
   desktop runs; acceptable given `OMARCHY_DIR`'s documented "deterministic tests"
   purpose.

3. **`AGENTS.md` path is stale.** It says `/home/allieb/Projects/whatthedock`; the
   repo actually lives at `/home/allie/Projects/whatthedock`. Use the real path.

## Out of scope (pre-existing, unrelated to omarchy)

Two `cmd/whatthedock` doctor tests fail on this machine on **both** `main` and the
branch: `TestBuildDoctorReportHealthyLocalConfig` and `TestRunDoctorCommandExitCodes`.
Cause: the doctor's "Local system > Tunnel sockets" check scans the real
`os.TempDir()` (i.e. `/tmp`) and finds real leftover `whatthedock-remote-*.sock`
files from actual app runs (`/tmp/whatthedock-remote-2.sock`, `-3.sock`, `-4.sock`),
so a "clean" run emits a tunnel-socket WARN where those tests expect zero warnings.

Fix if the user wants a fully green `go test ./...` (small, but a separate concern):
add `t.Setenv("TMPDIR", t.TempDir())` to the doctor tests (or give the tunnel-socket
scan a temp-dir seam). Do not fold this into the omarchy change unless asked.
