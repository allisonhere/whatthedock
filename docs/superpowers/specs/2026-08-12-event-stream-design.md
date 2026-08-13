# Docker event stream — design

Status: approved, ready for implementation planning.

## Problem

WhatTheDock currently has no live updates at all. The container tree only
reloads when the user presses `r` or after they run an action
(start/stop/restart). Container state changes made outside the app — another
terminal, `docker compose up`, a health check flipping, a container dying —
are invisible until the user manually refreshes.

This is the "Docker event stream" item from the README's Planned Features
list.

## Scope

Silent auto-refresh only. The event stream is plumbing: it drives the
existing tree/snapshot reload path, the same one the `r` key already
triggers. There is no new visible event feed, notification list, or activity
mode — that's an explicit non-goal for this iteration.

## Data model

New type in `internal/domain`:

```go
type ContainerEvent struct {
	ID     ResourceID
	Action string    // "start", "die", "health_status", "restart", "create", "destroy", etc.
	Time   time.Time
}
```

Kept minimal on purpose. The UI only needs "something changed" — it never
inspects `Action` or `Time`. They're included because they're nearly free to
carry and keep the door open for an actual event log later without a
breaking interface change, not because anything in this design consumes
them yet.

## Provider interface

`app.Provider` gains one method, mirroring the shape of the existing `Logs`
method:

```go
Events(ctx context.Context) (<-chan domain.ContainerEvent, error)
```

Returns a channel to consume; error if the subscription couldn't be
established. The channel closes when `ctx` is cancelled or the underlying
stream ends (daemon restart, connection drop, etc.).

### `internal/docker` (`LocalProvider.Events`)

Wraps the Docker SDK's `cli.Events(ctx, events.ListOptions{...})`, filtered
server-side to container events only:

```go
events.ListOptions{Filters: filters.NewArgs(filters.Arg("type", "container"))}
```

Docker's own event stream includes image pulls, network/volume changes,
exec events, etc. — filtering server-side means none of that noise reaches
the app, and the app-side dirty-flag logic stays simple. A forwarding
goroutine (same shape as the existing `readLogLines`) translates each
`events.Message` into a `domain.ContainerEvent` and writes it to the
returned channel.

### `internal/demo` (`Provider.Events`)

A goroutine on a jittered multi-second ticker picks a random container from
the in-memory fixture, mutates its state (flip health, toggle
running↔restarting — reusing whatever state transitions the existing demo
fixtures already model) and emits a matching `ContainerEvent`. The demo
provider's `Snapshot()` reflects the same mutation, so a poll-triggered
refresh in demo mode actually shows something different — the live-update
behavior is visible and testable without a real Docker host or the jarvis
tunnel.

## Bubble Tea integration

New `Model` fields (`internal/ui/model.go`):

```go
eventChan     chan domain.ContainerEvent
eventCancel   context.CancelFunc
snapshotDirty bool
eventBackoff  time.Duration // current reconnect delay; resets on success
```

### Startup

`Init()` batches the existing `refreshCmd()` with a new `startEventsCmd()`,
so the initial snapshot load and the event subscription kick off together.

### Listen loop

Consuming the channel uses a recursive blocking-read `tea.Cmd` — Bubble
Tea's standard idiom for a pub/sub channel — rather than a tick-based drain
like the log viewer uses. Docker container events are low-frequency, so
there's no batching benefit to a tick here (unlike log lines, which arrive
in bursts worth coalescing per render tick); a tick would just be a second
polling loop for no benefit.

- `startEventsCmd()` → `eventsStartedMsg{events, cancel, err}`, opening the
  subscription (mirrors `startLogsCmd`/`logsStartedMsg`).
- On success: store `eventChan`/`eventCancel`, reset `eventBackoff` to its
  base, and return `waitForContainerEvent(eventChan)`.
- `waitForContainerEvent` is a one-shot `tea.Cmd`: blocks on a single
  channel receive, returns `containerEventMsg{}` or (on channel close)
  `eventStreamClosedMsg{}`.
- On `containerEventMsg`: set `m.snapshotDirty = true` and immediately
  re-issue `waitForContainerEvent` to keep listening. No debounce logic is
  needed here — a burst of events just sets the same flag repeatedly; the
  poll tick (below) is what actually coalesces it into one refresh.

### Poll tick

A recurring `tea.Tick` at a new, user-configurable interval:

- New **Settings → "Snapshot refresh"** row, following the exact pattern of
  the existing "Stats refresh" row (same `{1s, 2s, 5s}`-style cycling UI,
  default 2s).
- New `SnapshotRefresh time.Duration` field on `appSettings`, and
  `SnapshotRefresh string` on `config.Settings` (persisted), following the
  existing `StatsRefresh` field exactly — same `applyPersisted`/`persisted`
  round-trip, same `defaultSettings()` treatment.
- On tick: if `m.snapshotDirty`, clear it and `return m, m.refreshCmd()`;
  either way, reschedule the next tick.
- `refreshCmd`'s existing `snapshotMsg` handler already re-clamps the
  cursor/selection and re-fetches the selected container's detail
  (`preserveSelection` → `loadSelectedCmd`) on every refresh — no new logic
  needed there beyond also clearing `snapshotDirty` when a snapshot lands,
  which covers the race where a manual `r` refresh and a poll tick land
  close together.

### Reconnect with backoff

- On `eventStreamClosedMsg`, or an error on `eventsStartedMsg`: schedule a
  `tea.Tick` at `m.eventBackoff` (starting ~1s) and then retry
  `startEventsCmd()`. Backoff doubles on each consecutive failure, capped at
  30s. A successful `eventsStartedMsg` resets it to base.
- This runs silently — no status-bar indicator. Manual `r` refresh keeps
  working throughout any reconnect window, so the user always has a
  fallback even if the event stream is down.
- `Model.cleanup()` (already called on quit) also cancels `eventCancel`,
  the same way it already cancels `logCancel`.

## Related fix: log-tail restart churn

Today, `detailMsg` handling unconditionally restarts the selected
container's live log tail:

```go
return m, m.startLogsCmd(msg.container.ID)
```

This runs on *every* detail refresh, even when the same container is just
being re-fetched with nothing changed. It's barely noticeable today because
detail refreshes only happen on manual navigation. Auto-refresh turns this
into a real problem: if a container keeps emitting events (an active health
check, a flapping restart), the poll tick will trigger a detail refresh
every couple of seconds, tearing down and reopening the log stream each
time — visible as reset scroll position/dropped lines if the user is
actively reading logs.

Fix, in the same `detailMsg` handler:

```go
if selectionChanged || m.logChan == nil {
	return m, m.startLogsCmd(msg.container.ID)
}
return m, nil
```

A background refresh of the *same* selected container leaves an
already-running log stream alone. A real selection change, or the case
where no stream is running yet, still starts one.

## Error handling

- `Events()` failing at startup (old Docker API, permissions, tunnel down):
  handled like a `logsStartedMsg` error — doesn't block or crash the rest of
  the UI, just falls into the reconnect-backoff loop. These failures are
  almost always transient (daemon restart, SSH tunnel blip), so retrying is
  the right default.
- A `Snapshot()` call triggered by the poll tick failing: falls through the
  existing `snapshotMsg` error path, which already surfaces
  `friendlyDockerError` in the status bar. No new error handling needed —
  this path already exists for manual refresh failures.
- Demo provider's synthetic events never error.

## Testing plan

- `internal/domain`: none needed — `ContainerEvent` is a plain struct.
- `internal/docker`: add a focused test converting a sample `events.Message`
  into a `domain.ContainerEvent` (action/ID mapping), following the existing
  pattern in `convert_test.go`. No live-Docker test.
- `internal/demo`: test that `Events()` eventually emits at least one event,
  and that the corresponding container's state actually changed in a
  subsequent `Snapshot()` — bounded wait (e.g. a timeout-guarded channel
  read), not a sleep-and-hope.
- `internal/ui`: extend `fakeProvider` with a controllable `Events()`
  channel (nil/never-fires by default, so all existing tests are
  unaffected). New model tests:
  - A `containerEventMsg` sets `snapshotDirty` and the returned `Cmd`
    re-arms the listen loop (verified via `runCmd`, not by sleeping).
  - The poll tick only calls `refreshCmd` when dirty, and clears
    `snapshotDirty` after.
  - `eventStreamClosedMsg` schedules a reconnect, and the backoff duration
    increases on repeated failures and resets on success.
  - The log-restart fix: a same-container `detailMsg` (selection unchanged,
    stream already running) does *not* return a `startLogsCmd`, following
    the same style as `TestStaleDetailResponseIgnoredAfterRapidNavigation`.

## Non-goals

- No visible event feed/notification list/activity mode.
- No per-event debouncing or coalescing beyond the dirty-flag + fixed-tick
  mechanism described above.
- No status-bar indicator for reconnect state.
- No changes to how stats polling or log filtering work, beyond the
  log-restart fix above.
