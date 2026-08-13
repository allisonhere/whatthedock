# Docker Event Stream Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace WhatTheDock's manual-refresh-only container state with a Docker event stream that silently drives the existing snapshot-reload path, so the tree reflects reality without the user pressing `r`.

**Architecture:** `app.Provider` gains an `Events(ctx) (<-chan domain.ContainerEvent, error)` method. The UI consumes it with a recursive blocking-read `tea.Cmd` that sets a `snapshotDirty` flag on each event; a separate fixed-interval `tea.Tick` checks that flag and triggers the existing `refreshCmd()` only when dirty, coalescing bursts for free. A backoff-based reconnect loop handles the event stream dropping (expected over the jarvis SSH tunnel). A related fix stops the existing code from restarting the selected container's log tail on every background refresh.

**Tech Stack:** Go, Bubble Tea (`github.com/charmbracelet/bubbletea`), Docker SDK (`github.com/docker/docker/client`, `.../api/types/events`, `.../api/types/filters`).

## Global Constraints

- Spec: `docs/superpowers/specs/2026-08-12-event-stream-design.md` — read it first; this plan implements it exactly.
- Silent auto-refresh only — no new visible event feed, notification list, or activity mode.
- Docker's own server-side filter restricts the subscription to container-type events (`filters.Arg("type", "container")`) — no in-app event-type filtering.
- Demo provider's `Events()` is a no-op stub (closes on context cancellation, never sends) — no synthetic event generation.
- Poll tick interval is a new user-configurable "Snapshot refresh" setting, following the exact pattern of the existing "Stats refresh" setting (`{1s, 2s, 5s}` cycle, default 2s).
- Reconnect on stream failure with backoff starting at 1s, doubling, capped at 30s, uncapped retry count, no status-bar indicator.
- Every `go build ./...` and `go test ./...` must be green at the end of every task — the `app.Provider` interface changes in Task 1, so every implementer (docker, demo, the test double) must land together in that task.

---

### Task 1: Provider surface — domain type, interface method, docker/demo/fake implementations

**Files:**
- Modify: `internal/domain/models.go` (add `ContainerEvent` type, right after the existing `Host` struct)
- Modify: `internal/app/provider.go` (add `Events` to the `Provider` interface)
- Modify: `internal/docker/client.go` (implement `LocalProvider.Events` + `FromEventMessage`)
- Modify: `internal/docker/convert_test.go` (test `FromEventMessage`)
- Modify: `internal/demo/provider.go` (implement `Provider.Events` as a no-op stub)
- Modify: `internal/demo/provider_test.go` (test the stub)
- Modify: `internal/ui/model_test.go` (add `fakeProvider.Events` so `internal/ui`'s tests keep compiling)

**Interfaces:**
- Produces: `domain.ContainerEvent{ID domain.ResourceID; Action string; Time time.Time}`; `app.Provider` method `Events(ctx context.Context) (<-chan domain.ContainerEvent, error)`; `docker.FromEventMessage(host domain.HostID, msg events.Message) domain.ContainerEvent`. Every later task relies on these existing and on `go build ./...`/`go test ./...` being green after this task.

- [ ] **Step 1: Write the failing test for the domain→SDK conversion**

Add to `internal/docker/convert_test.go` (add `"github.com/docker/docker/api/types/events"` to the import block):

```go
func TestFromEventMessageMapsActorAndAction(t *testing.T) {
	msg := events.Message{
		Type:   events.ContainerEventType,
		Action: events.ActionDie,
		Actor:  events.Actor{ID: "abcdef"},
		Time:   1700000000,
	}
	evt := FromEventMessage("local", msg)
	if evt.ID.Host != "local" || evt.ID.ID != "abcdef" {
		t.Fatalf("id = %#v", evt.ID)
	}
	if evt.Action != "die" {
		t.Fatalf("action = %q, want %q", evt.Action, "die")
	}
	if !evt.Time.Equal(time.Unix(1700000000, 0)) {
		t.Fatalf("time = %v, want %v", evt.Time, time.Unix(1700000000, 0))
	}
}
```

- [ ] **Step 2: Run it to confirm it fails to compile**

Run: `go test ./internal/docker/... -run TestFromEventMessageMapsActorAndAction -v`
Expected: FAIL — `domain.ContainerEvent` and `FromEventMessage` are undefined.

- [ ] **Step 3: Add `domain.ContainerEvent`**

In `internal/domain/models.go`, immediately after the existing `Host` struct:

```go
type ContainerEvent struct {
	ID     ResourceID
	Action string
	Time   time.Time
}
```

- [ ] **Step 4: Add `Events` to `app.Provider`**

In `internal/app/provider.go`, add to the `Provider` interface (after `Logs`):

```go
	Events(context.Context) (<-chan domain.ContainerEvent, error)
```

- [ ] **Step 5: Implement `LocalProvider.Events` and `FromEventMessage`**

In `internal/docker/client.go`, add to the import block:

```go
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
```

Add, near the other `LocalProvider` methods (after `Logs`):

```go
func (p *LocalProvider) Events(ctx context.Context) (<-chan domain.ContainerEvent, error) {
	msgs, errs := p.cli.Events(ctx, events.ListOptions{
		Filters: filters.NewArgs(filters.Arg("type", string(events.ContainerEventType))),
	})
	out := make(chan domain.ContainerEvent)
	go func() {
		defer close(out)
		for {
			select {
			case msg, ok := <-msgs:
				if !ok {
					return
				}
				select {
				case out <- FromEventMessage(p.host.ID, msg):
				case <-ctx.Done():
					return
				}
			case _, ok := <-errs:
				if !ok {
					return
				}
				return
			}
		}
	}()
	return out, nil
}
```

Add near the other `From*` conversion functions:

```go
func FromEventMessage(host domain.HostID, msg events.Message) domain.ContainerEvent {
	return domain.ContainerEvent{
		ID:     domain.ResourceID{Host: host, ID: msg.Actor.ID},
		Action: string(msg.Action),
		Time:   time.Unix(msg.Time, 0),
	}
}
```

`cli.Events` (from the Docker SDK) returns two channels — messages and errors — and never closes the messages channel; it closes the errors channel exactly once when the stream ends (whether from `ctx` cancellation, a decode error, or EOF). That's why termination is detected on the `errs` case, not on `msgs` closing.

- [ ] **Step 6: Run the docker test to verify it passes**

Run: `go test ./internal/docker/... -run TestFromEventMessageMapsActorAndAction -v`
Expected: PASS

- [ ] **Step 7: Write the failing test for the demo stub**

Add to `internal/demo/provider_test.go` (add `"time"` to the import block):

```go
func TestProviderEventsClosesOnContextCancelWithoutSending(t *testing.T) {
	provider := NewProvider()
	ctx, cancel := context.WithCancel(context.Background())
	events, err := provider.Events(ctx)
	if err != nil {
		t.Fatalf("Events() err = %v", err)
	}
	cancel()
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("received an event from the demo provider, want none")
		}
	case <-time.After(time.Second):
		t.Fatal("events channel did not close within 1s of context cancellation")
	}
}
```

- [ ] **Step 8: Run it to confirm it fails to compile**

Run: `go test ./internal/demo/... -run TestProviderEventsClosesOnContextCancelWithoutSending -v`
Expected: FAIL — `Provider` has no method `Events`.

- [ ] **Step 9: Implement the demo stub**

In `internal/demo/provider.go`, add near the other `Provider` methods (after `Logs`):

```go
func (p *Provider) Events(ctx context.Context) (<-chan domain.ContainerEvent, error) {
	ch := make(chan domain.ContainerEvent)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}
```

- [ ] **Step 10: Run the demo test to verify it passes**

Run: `go test ./internal/demo/... -run TestProviderEventsClosesOnContextCancelWithoutSending -v`
Expected: PASS

- [ ] **Step 11: Add the test-double implementation so `internal/ui` keeps compiling**

In `internal/ui/model_test.go`, add to `fakeProvider`'s method set (after `Logs`):

```go
func (f *fakeProvider) Events(context.Context) (<-chan domain.ContainerEvent, error) {
	return make(chan domain.ContainerEvent), nil
}
```

- [ ] **Step 12: Run the full test suite to verify everything still compiles and passes**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages PASS

- [ ] **Step 13: Commit**

```bash
git add internal/domain/models.go internal/app/provider.go internal/docker/client.go internal/docker/convert_test.go internal/demo/provider.go internal/demo/provider_test.go internal/ui/model_test.go
git commit -m "Add Provider.Events for Docker container event subscriptions

Real implementation for LocalProvider (filtered to container-type
events server-side), a no-op stub for the demo provider, and a test
double for internal/ui's fakeProvider. Nothing consumes this yet."
```

---

### Task 2: UI event listen loop

**Files:**
- Modify: `internal/ui/model.go` (near `Model` struct, near the message-type block around `detailMsg`/`logsStartedMsg`, and inside `Update`)
- Modify: `internal/ui/model_test.go`

**Interfaces:**
- Consumes: `domain.ContainerEvent`, `app.Provider.Events(ctx) (<-chan domain.ContainerEvent, error)`, `fakeProvider.Events` (Task 1); existing `Model.provider` field.
- Produces: `Model` fields `eventChan <-chan domain.ContainerEvent`, `eventCancel context.CancelFunc`, `snapshotDirty bool`, `eventBackoff time.Duration`; msg types `eventsStartedMsg{events <-chan domain.ContainerEvent; cancel context.CancelFunc; err error}`, `containerEventMsg{event domain.ContainerEvent}`, `eventStreamClosedMsg{}`; functions `(m Model) startEventsCmd() tea.Cmd` and `waitForContainerEvent(events <-chan domain.ContainerEvent) tea.Cmd`. Task 3 (reconnect) and Task 5 (wiring into `Init`) depend on these exact names.

- [ ] **Step 1: Write the failing test for `waitForContainerEvent`**

Add to `internal/ui/model_test.go`:

```go
func TestWaitForContainerEventReturnsEventOrClosedSignal(t *testing.T) {
	ch := make(chan domain.ContainerEvent, 1)
	ch <- domain.ContainerEvent{ID: domain.ResourceID{Host: "local", ID: "1"}, Action: "start"}

	msg := runCmd(t, waitForContainerEvent(ch))
	if _, ok := msg.(containerEventMsg); !ok {
		t.Fatalf("msg = %#v, want containerEventMsg", msg)
	}

	close(ch)
	msg = runCmd(t, waitForContainerEvent(ch))
	if _, ok := msg.(eventStreamClosedMsg); !ok {
		t.Fatalf("msg = %#v, want eventStreamClosedMsg", msg)
	}
}
```

- [ ] **Step 2: Run it to confirm it fails to compile**

Run: `go test ./internal/ui/... -run TestWaitForContainerEventReturnsEventOrClosedSignal -v`
Expected: FAIL — `waitForContainerEvent`, `containerEventMsg`, `eventStreamClosedMsg` undefined.

- [ ] **Step 3: Add the new `Model` fields**

In `internal/ui/model.go`, inside the `Model` struct, add after the `logErr error` field (in the log-related block):

```go
	eventChan    <-chan domain.ContainerEvent
	eventCancel  context.CancelFunc
	snapshotDirty bool
	eventBackoff  time.Duration
```

- [ ] **Step 4: Add the new message types**

In `internal/ui/model.go`, add near the other message type declarations (next to `type logsStartedMsg struct { ... }`):

```go
type eventsStartedMsg struct {
	events <-chan domain.ContainerEvent
	cancel context.CancelFunc
	err    error
}

type containerEventMsg struct {
	event domain.ContainerEvent
}

type eventStreamClosedMsg struct{}
```

- [ ] **Step 5: Add `startEventsCmd` and `waitForContainerEvent`**

In `internal/ui/model.go`, add near `startLogsCmd`:

```go
func (m Model) startEventsCmd() tea.Cmd {
	provider := m.provider
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		events, err := provider.Events(ctx)
		if err != nil {
			cancel()
			return eventsStartedMsg{err: err}
		}
		return eventsStartedMsg{events: events, cancel: cancel}
	}
}

func waitForContainerEvent(events <-chan domain.ContainerEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return eventStreamClosedMsg{}
		}
		return containerEventMsg{event: event}
	}
}
```

- [ ] **Step 6: Handle the success path of `eventsStartedMsg` and `containerEventMsg` in `Update`**

In `internal/ui/model.go`, in the `Update` method's `switch msg := msg.(type)`, add a case right after the existing `case logsStartedMsg:` block:

```go
	case eventsStartedMsg:
		if msg.err != nil {
			return m, nil
		}
		m.eventChan = msg.events
		m.eventCancel = msg.cancel
		m.eventBackoff = 0
		return m, waitForContainerEvent(m.eventChan)
	case containerEventMsg:
		m.snapshotDirty = true
		return m, waitForContainerEvent(m.eventChan)
```

(The `msg.err != nil` branch is intentionally a no-op here — Task 3 changes it to `return m, m.eventsReconnectCmd()`. Keeping it a no-op for now means this task's build is green without needing Task 3's code yet.)

- [ ] **Step 7: Run the new test to verify it passes**

Run: `go test ./internal/ui/... -run TestWaitForContainerEventReturnsEventOrClosedSignal -v`
Expected: PASS

- [ ] **Step 8: Write the failing test for `containerEventMsg` marking the flag dirty and re-arming the loop**

Add to `internal/ui/model_test.go`:

```go
func TestContainerEventMarksSnapshotDirtyAndReArmsListenLoop(t *testing.T) {
	model := testModel()
	model.eventChan = make(chan domain.ContainerEvent)

	updated, cmd := model.Update(containerEventMsg{event: domain.ContainerEvent{
		ID:     domain.ResourceID{Host: "local", ID: "1"},
		Action: "die",
	}})
	model = updated.(Model)

	if !model.snapshotDirty {
		t.Fatal("snapshotDirty = false after containerEventMsg, want true")
	}
	if cmd == nil {
		t.Fatal("containerEventMsg returned nil cmd, want the listen loop re-armed")
	}
}

func TestEventsStartedResetsBackoffAndArmsListenLoop(t *testing.T) {
	model := testModel()
	model.eventBackoff = 10 * time.Second
	ch := make(chan domain.ContainerEvent)

	updated, cmd := model.Update(eventsStartedMsg{events: ch, cancel: func() {}})
	model = updated.(Model)

	if model.eventBackoff != 0 {
		t.Fatalf("eventBackoff = %v, want reset to 0 on successful (re)connect", model.eventBackoff)
	}
	if cmd == nil {
		t.Fatal("eventsStartedMsg success returned nil cmd, want the listen loop armed")
	}
}
```

- [ ] **Step 9: Run both new tests to verify they pass**

Run: `go test ./internal/ui/... -run 'TestContainerEventMarksSnapshotDirtyAndReArmsListenLoop|TestEventsStartedResetsBackoffAndArmsListenLoop' -v`
Expected: PASS

- [ ] **Step 10: Run the full suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages PASS

- [ ] **Step 11: Commit**

```bash
git add internal/ui/model.go internal/ui/model_test.go
git commit -m "Add the container-event listen loop to the UI model

Recursive blocking-read Cmd consumes Provider.Events() and marks a
snapshotDirty flag on each event. Nothing triggers a refresh from this
flag yet, and nothing starts this loop at program startup yet."
```

---

### Task 3: Reconnect with backoff

**Files:**
- Modify: `internal/ui/model.go`
- Modify: `internal/ui/model_test.go`

**Interfaces:**
- Consumes: `Model.eventChan`/`eventCancel`/`eventBackoff` and `startEventsCmd()` (Task 2).
- Produces: `(m Model) eventsReconnectCmd() tea.Cmd`; msg type `eventsReconnectTickMsg{}`. Task 5 doesn't need anything new from here — reconnect is self-contained once wired into `Update`.

- [ ] **Step 1: Write the failing test for backoff progression on stream close**

Add to `internal/ui/model_test.go`:

```go
func TestEventStreamClosedSchedulesReconnectWithIncreasingBackoff(t *testing.T) {
	model := testModel()
	model.eventBackoff = 0

	updated, cmd := model.Update(eventStreamClosedMsg{})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("eventStreamClosedMsg returned nil cmd, want a reconnect tick scheduled")
	}
	if model.eventBackoff != time.Second {
		t.Fatalf("eventBackoff = %v, want 1s after the first failure", model.eventBackoff)
	}

	updated, cmd = model.Update(eventStreamClosedMsg{})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("eventStreamClosedMsg returned nil cmd, want a reconnect tick scheduled")
	}
	if model.eventBackoff != 2*time.Second {
		t.Fatalf("eventBackoff = %v, want 2s after a second consecutive failure", model.eventBackoff)
	}
}

func TestEventBackoffCapsAt30Seconds(t *testing.T) {
	model := testModel()
	model.eventBackoff = 20 * time.Second

	updated, _ := model.Update(eventStreamClosedMsg{})
	model = updated.(Model)
	if model.eventBackoff != 30*time.Second {
		t.Fatalf("eventBackoff = %v, want capped at 30s", model.eventBackoff)
	}

	updated, _ = model.Update(eventStreamClosedMsg{})
	model = updated.(Model)
	if model.eventBackoff != 30*time.Second {
		t.Fatalf("eventBackoff = %v, want to stay capped at 30s", model.eventBackoff)
	}
}

func TestEventsReconnectTickRetriesSubscription(t *testing.T) {
	model := testModel()

	_, cmd := model.Update(eventsReconnectTickMsg{})
	if cmd == nil {
		t.Fatal("eventsReconnectTickMsg returned nil cmd, want a retry of startEventsCmd")
	}
	msg := runCmd(t, cmd)
	started, ok := msg.(eventsStartedMsg)
	if !ok || started.err != nil {
		t.Fatalf("msg = %#v, want a successful eventsStartedMsg", msg)
	}
}
```

- [ ] **Step 2: Run the tests to confirm they fail to compile**

Run: `go test ./internal/ui/... -run 'TestEventStreamClosedSchedulesReconnectWithIncreasingBackoff|TestEventBackoffCapsAt30Seconds|TestEventsReconnectTickRetriesSubscription' -v`
Expected: FAIL — `eventsReconnectTickMsg` undefined.

- [ ] **Step 3: Add `eventsReconnectTickMsg` and `eventsReconnectCmd`**

In `internal/ui/model.go`, add the message type next to `eventStreamClosedMsg`:

```go
type eventsReconnectTickMsg struct{}
```

Add the command near `startEventsCmd`:

```go
func (m Model) eventsReconnectCmd() tea.Cmd {
	backoff := m.eventBackoff
	if backoff <= 0 {
		backoff = time.Second
	}
	return tea.Tick(backoff, func(time.Time) tea.Msg { return eventsReconnectTickMsg{} })
}
```

- [ ] **Step 4: Wire backoff progression and the retry into `Update`**

In `internal/ui/model.go`, replace the placeholder `eventsStartedMsg` error branch added in Task 2:

```go
	case eventsStartedMsg:
		if msg.err != nil {
			return m, nil
		}
```

with:

```go
	case eventsStartedMsg:
		if msg.err != nil {
			return m, m.eventsReconnectCmd()
		}
```

Add two more cases, next to the `eventsStartedMsg`/`containerEventMsg` cases:

```go
	case eventStreamClosedMsg:
		m.eventChan = nil
		m.eventCancel = nil
		if m.eventBackoff <= 0 {
			m.eventBackoff = time.Second
		} else {
			m.eventBackoff *= 2
			if m.eventBackoff > 30*time.Second {
				m.eventBackoff = 30 * time.Second
			}
		}
		return m, m.eventsReconnectCmd()
	case eventsReconnectTickMsg:
		return m, m.startEventsCmd()
```

- [ ] **Step 5: Run the new tests to verify they pass**

Run: `go test ./internal/ui/... -run 'TestEventStreamClosedSchedulesReconnectWithIncreasingBackoff|TestEventBackoffCapsAt30Seconds|TestEventsReconnectTickRetriesSubscription' -v`
Expected: PASS

- [ ] **Step 6: Run the full suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages PASS

- [ ] **Step 7: Commit**

```bash
git add internal/ui/model.go internal/ui/model_test.go
git commit -m "Reconnect the event stream with capped exponential backoff

A closed/errored stream retries at 1s, 2s, 4s... capped at 30s, reset
to 1s on the next successful connection. Runs silently, matching the
design's no-status-bar-indicator choice."
```

---

### Task 4: Snapshot refresh setting + poll tick

**Files:**
- Modify: `internal/config/settings.go`
- Modify: `internal/config/settings_test.go`
- Modify: `internal/ui/model.go` (near `appSettings`, `defaultSettings`, `applyPersisted`, `persisted`, `settingsRows`, `cycleSetting`, and inside `Update`'s `snapshotMsg` case)
- Modify: `internal/ui/model_test.go`

**Interfaces:**
- Consumes: `Model.snapshotDirty` (Task 2); existing `appSettings`/`config.Settings`/`defaultSettings()`/`formatRefreshInterval()`/`modIndex()`/`m.refreshCmd()`.
- Produces: `appSettings.SnapshotRefresh time.Duration`; `config.Settings.SnapshotRefresh string`; `(m Model) snapshotPollTickCmd() tea.Cmd`; msg type `snapshotPollTickMsg{}`. Task 5 wires `snapshotPollTickCmd()` into `Init()`.

- [ ] **Step 1: Write the failing test for the config round trip**

In `internal/config/settings_test.go`, extend `TestSaveAndLoadSettingsRoundTrip`'s `want` literal and its comparison:

```go
	want := Settings{
		GraphStyle:      "braille",
		GraphColor:      "mono",
		LogColor:        "severity",
		ShowDeltas:      &showDeltas,
		StatsRefresh:    "5s",
		SnapshotRefresh: "5s",
		DefaultActivity: "stats",
	}
```

```go
	if got.GraphStyle != want.GraphStyle ||
		got.GraphColor != want.GraphColor ||
		got.LogColor != want.LogColor ||
		got.ShowDeltas == nil ||
		*got.ShowDeltas != showDeltas ||
		got.StatsRefresh != want.StatsRefresh ||
		got.SnapshotRefresh != want.SnapshotRefresh ||
		got.DefaultActivity != want.DefaultActivity {
		t.Fatalf("settings = %#v, want %#v", got, want)
	}
```

- [ ] **Step 2: Run it to confirm it fails to compile**

Run: `go test ./internal/config/... -run TestSaveAndLoadSettingsRoundTrip -v`
Expected: FAIL — `Settings` has no field `SnapshotRefresh`.

- [ ] **Step 3: Add the field to `config.Settings`**

In `internal/config/settings.go`, add to the `Settings` struct (after `StatsRefresh`):

```go
	SnapshotRefresh string `json:"snapshotRefresh,omitempty"`
```

- [ ] **Step 4: Run the config test to verify it passes**

Run: `go test ./internal/config/... -run TestSaveAndLoadSettingsRoundTrip -v`
Expected: PASS

- [ ] **Step 5: Write the failing test for the UI-side setting (persisted load + reset-to-default + cycling)**

Add to `internal/ui/model_test.go`:

```go
func TestSnapshotRefreshSettingLoadsPersistedValue(t *testing.T) {
	model := NewModelWithSettings(newFakeProvider(), config.Settings{SnapshotRefresh: "5s"}, "")
	if model.settings.SnapshotRefresh != 5*time.Second {
		t.Fatalf("SnapshotRefresh = %v, want 5s", model.settings.SnapshotRefresh)
	}
}

func TestSnapshotRefreshSettingCycles(t *testing.T) {
	model := testModel()
	model.settings.SnapshotRefresh = 2 * time.Second

	index := -1
	for i, row := range model.settingsRows() {
		if row.label == "Snapshot refresh" {
			index = i
			break
		}
	}
	if index == -1 {
		t.Fatal(`settingsRows() missing "Snapshot refresh"`)
	}

	model.cycleSetting(index, 1)
	if model.settings.SnapshotRefresh != 5*time.Second {
		t.Fatalf("SnapshotRefresh after cycling forward = %v, want 5s", model.settings.SnapshotRefresh)
	}
	model.cycleSetting(index, 1)
	if model.settings.SnapshotRefresh != time.Second {
		t.Fatalf("SnapshotRefresh after cycling forward again = %v, want 1s (wraps)", model.settings.SnapshotRefresh)
	}
}
```

- [ ] **Step 6: Run the tests to confirm they fail**

Run: `go test ./internal/ui/... -run 'TestSnapshotRefreshSettingLoadsPersistedValue|TestSnapshotRefreshSettingCycles' -v`
Expected: FAIL — `SnapshotRefresh` undefined on `appSettings`, or `settingsRows()` has no such row.

- [ ] **Step 7: Add `SnapshotRefresh` to `appSettings`, `defaultSettings`, `applyPersisted`, `persisted`**

In `internal/ui/model.go`, add to the `appSettings` struct (after `StatsRefresh`):

```go
	SnapshotRefresh time.Duration
```

In `defaultSettings()`, add (after `StatsRefresh: 2 * time.Second,`):

```go
		SnapshotRefresh: 2 * time.Second,
```

In `(s *appSettings) applyPersisted`, add (after the `StatsRefresh` block):

```go
	if persisted.SnapshotRefresh != "" {
		if interval, err := time.ParseDuration(persisted.SnapshotRefresh); err == nil && interval > 0 {
			s.SnapshotRefresh = interval
		}
	}
```

In `(s appSettings) persisted()`, add to the returned `config.Settings` literal (after `StatsRefresh: formatRefreshInterval(s.StatsRefresh),`):

```go
		SnapshotRefresh: formatRefreshInterval(s.SnapshotRefresh),
```

- [ ] **Step 8: Add the settings row and cycling behavior**

In `internal/ui/model.go`, in `settingsRows()`, add a row in the "Behavior" section (after the `"Default pane"` row):

```go
		{label: "Snapshot refresh", value: formatRefreshInterval(m.settings.SnapshotRefresh)},
```

In `cycleSetting`, add a case (next to `case "Stats refresh":`):

```go
	case "Snapshot refresh":
		intervals := []time.Duration{time.Second, 2 * time.Second, 5 * time.Second}
		current := 1
		for i, interval := range intervals {
			if m.settings.SnapshotRefresh == interval {
				current = i
				break
			}
		}
		m.settings.SnapshotRefresh = intervals[modIndex(current+direction, len(intervals))]
```

- [ ] **Step 9: Run the new UI tests to verify they pass**

Run: `go test ./internal/ui/... -run 'TestSnapshotRefreshSettingLoadsPersistedValue|TestSnapshotRefreshSettingCycles' -v`
Expected: PASS

- [ ] **Step 10: Write the failing test for the poll tick's dirty-flag gating**

Add to `internal/ui/model_test.go`:

```go
func TestSnapshotPollTickOnlyRefreshesWhenDirty(t *testing.T) {
	model := testModel()
	model.snapshotDirty = false

	_, cmd := model.Update(snapshotPollTickMsg{})
	if cmd == nil {
		t.Fatal("snapshotPollTickMsg returned nil cmd, want the next tick scheduled even when clean")
	}

	model.snapshotDirty = true
	updated, cmd := model.Update(snapshotPollTickMsg{})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("snapshotPollTickMsg while dirty returned nil cmd, want a refresh plus the next tick")
	}
	batch, ok := runCmd(t, cmd).(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("cmd() = %#v, want a 2-command batch (refresh + next tick) while dirty", batch)
	}
}

func TestSnapshotMsgClearsDirtyFlag(t *testing.T) {
	model := testModel()
	model.snapshotDirty = true

	updated, _ := model.Update(snapshotMsg{snapshot: newFakeProvider().snapshot})
	model = updated.(Model)
	if model.snapshotDirty {
		t.Fatal("snapshotDirty = true after snapshotMsg landed, want false")
	}
}
```

Note: the clean-tick branch only checks `cmd == nil` and never executes it — the clean-path `Cmd` is a bare `tea.Tick`, which blocks for real wall-clock time if invoked. The dirty-path `Cmd` is safe to execute because `tea.Batch`'s returned `Cmd` returns a `tea.BatchMsg` immediately without running its inner commands itself.

- [ ] **Step 11: Run the tests to confirm they fail to compile**

Run: `go test ./internal/ui/... -run 'TestSnapshotPollTickOnlyRefreshesWhenDirty|TestSnapshotMsgClearsDirtyFlag' -v`
Expected: FAIL — `snapshotPollTickMsg` undefined.

- [ ] **Step 12: Add `snapshotPollTickMsg`, `snapshotPollTickCmd`, and wire both into `Update`**

In `internal/ui/model.go`, add the message type next to `eventsReconnectTickMsg`:

```go
type snapshotPollTickMsg struct{}
```

Add the command near `eventsReconnectCmd`:

```go
func (m Model) snapshotPollTickCmd() tea.Cmd {
	interval := m.settings.SnapshotRefresh
	if interval <= 0 {
		interval = defaultSettings().SnapshotRefresh
	}
	return tea.Tick(interval, func(time.Time) tea.Msg { return snapshotPollTickMsg{} })
}
```

In `Update`, add a case next to `eventsReconnectTickMsg`:

```go
	case snapshotPollTickMsg:
		if m.snapshotDirty {
			return m, tea.Batch(m.refreshCmd(), m.snapshotPollTickCmd())
		}
		return m, m.snapshotPollTickCmd()
```

In the existing `case snapshotMsg:` block, add `m.snapshotDirty = false` as the first line inside the case (before the existing `m.loading = false`):

```go
	case snapshotMsg:
		m.snapshotDirty = false
		m.loading = false
```

- [ ] **Step 13: Run the new tests to verify they pass**

Run: `go test ./internal/ui/... -run 'TestSnapshotPollTickOnlyRefreshesWhenDirty|TestSnapshotMsgClearsDirtyFlag' -v`
Expected: PASS

- [ ] **Step 14: Run the full suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages PASS

- [ ] **Step 15: Commit**

```bash
git add internal/config/settings.go internal/config/settings_test.go internal/ui/model.go internal/ui/model_test.go
git commit -m "Add a configurable snapshot-refresh poll tick

New 'Snapshot refresh' setting (same 1s/2s/5s cycle as Stats refresh,
default 2s). The poll tick only triggers a snapshot reload when the
event listen loop has marked state dirty, coalescing bursts of events
into a single refresh."
```

---

### Task 5: Wire startup/shutdown, fix log-tail restart churn

**Files:**
- Modify: `internal/ui/model.go` (`Init`, `cleanup`, the `detailMsg` case in `Update`)
- Modify: `internal/ui/model_test.go`

**Interfaces:**
- Consumes: `startEventsCmd()` (Task 2), `snapshotPollTickCmd()` (Task 4), `m.refreshCmd()` (existing), `m.eventCancel` (Task 2), the existing `detailMsg` case.
- Produces: nothing further — this is the last code task. `Init()` and `cleanup()` now cover the event stream; `detailMsg` no longer restarts an already-running log tail for a same-container background refresh.

- [ ] **Step 1: Write the failing test for the log-restart fix**

Add to `internal/ui/model_test.go`:

```go
func TestBackgroundRefreshDoesNotRestartLogStreamForSameContainer(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.logChan = make(chan string)
	container := *model.selected

	updated, cmd := model.Update(detailMsg{id: model.selectedID, container: container})
	model = updated.(Model)
	if cmd != nil {
		t.Fatalf("cmd = %#v, want nil (no log stream restart) for a same-container background refresh", cmd)
	}
	if model.logChan == nil {
		t.Fatal("logChan was cleared, want the already-running stream left alone")
	}
}

func TestSelectionChangeStillStartsLogStream(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.logChan = make(chan string)
	other := newFakeProvider().containers["2"]
	model.selectedID = other.ID

	updated, cmd := model.Update(detailMsg{id: other.ID, container: other})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("cmd = nil, want startLogsCmd after a real selection change")
	}
}
```

- [ ] **Step 2: Run the tests to confirm the first one fails**

Run: `go test ./internal/ui/... -run 'TestBackgroundRefreshDoesNotRestartLogStreamForSameContainer|TestSelectionChangeStillStartsLogStream' -v`
Expected: `TestBackgroundRefreshDoesNotRestartLogStreamForSameContainer` FAILs (current code always returns `startLogsCmd`); `TestSelectionChangeStillStartsLogStream` already PASSes.

- [ ] **Step 3: Fix the `detailMsg` case**

In `internal/ui/model.go`, in the `case detailMsg:` block, replace:

```go
		return m, m.startLogsCmd(msg.container.ID)
```

(the final line of that case, reached when `m.mode != activityStats`) with:

```go
		if selectionChanged || m.logChan == nil {
			return m, m.startLogsCmd(msg.container.ID)
		}
		return m, nil
```

- [ ] **Step 4: Run both tests to verify they pass**

Run: `go test ./internal/ui/... -run 'TestBackgroundRefreshDoesNotRestartLogStreamForSameContainer|TestSelectionChangeStillStartsLogStream' -v`
Expected: PASS

- [ ] **Step 5: Write the failing test confirming `Init()` starts the event loop and poll tick**

Add to `internal/ui/model_test.go`:

```go
func TestInitStartsEventListenLoopAndPollTick(t *testing.T) {
	model := NewModel(newFakeProvider())
	model.width, model.height = 100, 30

	cmd := model.Init()
	if cmd == nil {
		t.Fatal("Init() returned nil cmd, want a batch including the snapshot load, event loop, and poll tick")
	}
	batch, ok := runCmd(t, cmd).(tea.BatchMsg)
	if !ok || len(batch) != 3 {
		t.Fatalf("Init() cmd() = %#v, want a 3-command batch (refresh, events, poll tick)", batch)
	}
}
```

- [ ] **Step 6: Run it to confirm it fails**

Run: `go test ./internal/ui/... -run TestInitStartsEventListenLoopAndPollTick -v`
Expected: FAIL — `Init()` currently returns `refreshCmd()` alone (not a 3-way batch).

- [ ] **Step 7: Wire `Init()` and `cleanup()`**

In `internal/ui/model.go`, replace:

```go
func (m Model) Init() tea.Cmd {
	return m.refreshCmd()
}
```

with:

```go
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.refreshCmd(), m.startEventsCmd(), m.snapshotPollTickCmd())
}
```

Replace `cleanup`:

```go
func (m *Model) cleanup() {
	if m.logCancel != nil {
		m.logCancel()
		m.logCancel = nil
	}
	_ = m.provider.Close()
}
```

with:

```go
func (m *Model) cleanup() {
	if m.logCancel != nil {
		m.logCancel()
		m.logCancel = nil
	}
	if m.eventCancel != nil {
		m.eventCancel()
		m.eventCancel = nil
	}
	_ = m.provider.Close()
}
```

- [ ] **Step 8: Run the `Init()` test to verify it passes**

Run: `go test ./internal/ui/... -run TestInitStartsEventListenLoopAndPollTick -v`
Expected: PASS

- [ ] **Step 9: Run the full suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages PASS

- [ ] **Step 10: Commit**

```bash
git add internal/ui/model.go internal/ui/model_test.go
git commit -m "Start the event stream and poll tick at startup

Init() now batches the initial snapshot load with the event listen
loop and the poll tick; cleanup() cancels the event subscription on
quit alongside the existing log-stream cancellation.

Also fixes detailMsg to only restart the selected container's log
tail on a real selection change (or when no stream is running yet),
not on every background refresh of the same container — auto-refresh
would otherwise make that churn constant and visible."
```

---

### Task 6: Manual live verification + README update

**Files:**
- Modify: `README.md` (remove the now-implemented "Docker event stream." line from Planned Features)

**Interfaces:** none — this task validates the finished feature against the real Docker host and updates docs; it produces nothing further code depends on.

- [ ] **Step 1: Update the README**

In `README.md`, remove the `- Docker event stream.` line from the "Planned Features" list.

- [ ] **Step 2: Build the binary**

Run: `go build -o /tmp/wtd-verify ./cmd/whatthedock`
Expected: builds cleanly.

- [ ] **Step 3: Run it against jarvis**

Run (from the repo root, reusing the existing `start.sh` tunnel setup):
```bash
./start.sh
```
(or, if the jarvis tunnel socket is already up: `DOCKER_HOST=unix:///tmp/jarvis-docker.sock /tmp/wtd-verify`)

- [ ] **Step 4: Verify auto-refresh actually happens**

While WhatTheDock is running and focused on the "Projects" tree, from a separate terminal on jarvis (or via `docker -H unix:///tmp/jarvis-docker.sock ...` locally), restart some low-stakes container, e.g.:
```bash
DOCKER_HOST=unix:///tmp/jarvis-docker.sock docker restart <some-container-name>
```
Expected: within one "Snapshot refresh" interval (2s default) of the restart event, the container's row in the tree updates (status glyph/text changes) **without pressing `r`**.

- [ ] **Step 5: Verify reconnect behavior**

Kill and restart the SSH tunnel (or otherwise interrupt the Docker connection) for a few seconds while WhatTheDock keeps running, then restore it. Expected: no crash, no visible error; once the tunnel is back, repeat Step 4 and confirm auto-refresh resumes (the reconnect backoff loop should pick the subscription back up on its own within ~30s).

- [ ] **Step 6: Run the full test suite one more time**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all packages PASS

- [ ] **Step 7: Commit the README change**

```bash
git add README.md
git commit -m "Remove the Docker event stream item from Planned Features

Implemented and verified against the live jarvis Docker host."
```
