# Dashboard: highlight only after navigation, rail-only (no row bg wash)

## Goal

Make the Dashboard show no selection highlight until the user moves the cursor
with j/k/up/down (or the mouse wheel), and when it does appear, show it as a
left-edge rail only — never a full-row background color wash.

## Current context / assumptions

Repo: `/home/allie/Projects/whatthedock` (Go module, `internal/ui` package).

The Dashboard overlay (`overlayDashboard`) renders one `dashboardRow` per
running container. Today:

- `openDashboardOverlay` (`internal/ui/view.go:3554`) sets `dashboardCursor = 0`,
  so row 0 is highlighted immediately on open.
- `dashboardOverlay` (`internal/ui/view.go:3961`) computes
  `selected := len(shown) > 0 && i == cursor` (`view.go:3980`), so the cursor
  row is always highlighted from the first frame.
- `dashboardRow` (`internal/ui/view.go:4462`) does two things when `selected`:
  1. swaps `rowBg` to `renderer.Styles.ItemSelected`'s background color
     (`view.go:4468-4472`) — the full-row background wash the user wants gone;
  2. builds a `rail` at the left edge — currently a *blank* two-cell background
     (an earlier edit), which the user now wants restored to the `▌` marker.
- `dashboardMoveCursor` (`internal/ui/view.go:4646`) is the single choke point
  for cursor movement: the key handler (`internal/ui/model.go:2333-2336`) calls
  it for `j`/`k`/`down`/`up`, and `handleDashboardMouse` (`view.go:4503-4508`)
  calls it for wheel up/down.

Desired end state (the user's last three messages, reconciled):

1. On open (and before any navigation): no row is highlighted — no rail, no
   background wash.
2. After j/k/up/down or wheel: the cursor row gets a left-edge `▌` rail.
3. Never a full-row background color wash on the selected row.

The existing hot-row tint (amber/red wash when a container's CPU/memory crosses
`dashboardWarnPct`) is a *separate* warning signal and must be preserved — it is
not a selection highlight.

## Architecture / proposed approach

Add one bool field to the model — `dashboardCursorActive` — that starts false
when the overlay opens and flips true the first time the cursor moves. Gate the
`selected` flag in `dashboardOverlay` on it. In `dashboardRow`, delete the
`ItemSelected` background swap and restore the rail to
`renderer.SoftRail(selected, rowBg)` so the left-edge `▌` marker is the only
selection indicator. `dashboardCursor` keeps its existing semantics (still 0 on
open, still the target of Enter and mouse click), so Enter and click-open
behavior are unchanged; only the *visual* highlight is gated.

## Step-by-step tasks

### Task 1 — Add the `dashboardCursorActive` field

File: `internal/ui/model.go`, in the `Model` struct right after the
`dashboardCursor` field (currently around `model.go:424-427`).

Current text:

```go
	// dashboardCursor is the currently highlighted row index within the
	// Dashboard overlay's own visible container list (see
	// dashboardBodyPlan) — reset to 0 each time the overlay opens.
	dashboardCursor int
```

Add directly below it:

```go
	// dashboardCursorActive is true once the user has moved the Dashboard
	// cursor (j/k/up/down or the mouse wheel); until then the Dashboard
	// shows no selection highlight at all. Reset to false on open.
	dashboardCursorActive bool
```

Verification: `go build -buildvcs=false ./...` still compiles (the field is
unused for now, but Go allows unused struct fields, so this passes).

### Task 2 — Reset the flag on open

File: `internal/ui/view.go`, `openDashboardOverlay` (around `view.go:3554`).

Current text:

```go
func (m Model) openDashboardOverlay() (tea.Model, tea.Cmd) {
	m.overlay = overlayDashboard
	m.dashboardCursor = 0
	m.dashboardRefreshFrame = m.statusPulseFrame
	return m, m.dashboardRefreshCmd()
}
```

Change to:

```go
func (m Model) openDashboardOverlay() (tea.Model, tea.Cmd) {
	m.overlay = overlayDashboard
	m.dashboardCursor = 0
	m.dashboardCursorActive = false
	m.dashboardRefreshFrame = m.statusPulseFrame
	return m, m.dashboardRefreshCmd()
}
```

Note: the `StartInDashboard` path (`internal/ui/model.go:1044-1046`) sets
`m.overlay = overlayDashboard` directly without calling `openDashboardOverlay`,
but the bool's zero value is already `false`, so that path needs no change.

### Task 3 — Set the flag on cursor movement

File: `internal/ui/view.go`, `dashboardMoveCursor` (around `view.go:4646`).

Current text:

```go
func (m *Model) dashboardMoveCursor(delta int) {
	shown, _, _, _, _ := m.dashboardBodyPlan()
	if len(shown) == 0 {
		m.dashboardCursor = 0
		return
	}
	m.dashboardCursor = clamp(m.dashboardCursor+delta, 0, len(shown)-1)
}
```

Change to:

```go
func (m *Model) dashboardMoveCursor(delta int) {
	shown, _, _, _, _ := m.dashboardBodyPlan()
	if len(shown) == 0 {
		m.dashboardCursor = 0
		return
	}
	m.dashboardCursor = clamp(m.dashboardCursor+delta, 0, len(shown)-1)
	m.dashboardCursorActive = true
}
```

This single change covers j/k/up/down keys *and* the mouse wheel, since both
route through `dashboardMoveCursor`.

### Task 4 — Gate `selected` on the flag

File: `internal/ui/view.go`, `dashboardOverlay` (around `view.go:3977-3981`).

Current text:

```go
	shown, more, stopped, _, _ := m.dashboardBodyPlan()
	cursor := clamp(m.dashboardCursor, 0, max(0, len(shown)-1))
	for i, ctr := range shown {
		selected := len(shown) > 0 && i == cursor
		lines = append(lines, dashboardPadLine(renderer, m.dashboardRow(renderer, ctr, contentWidth, selected), contentWidth))
	}
```

Change the `selected` line to:

```go
		selected := m.dashboardCursorActive && len(shown) > 0 && i == cursor
```

Leave the rest of the loop body unchanged.

### Task 5 — Remove the row bg wash, restore the `▌` rail

File: `internal/ui/view.go`, `dashboardRow` (around `view.go:4467-4498`).

Current text (two regions):

```go
	rowBg := renderer.Styles.Theme.Bg
	if selected {
		if c, ok := renderer.Styles.ItemSelected.GetBackground().(lipgloss.Color); ok {
			rowBg = c
		}
	}
	baseFg := styleForeground(renderer.Styles.DetailBody, renderer.Styles.Theme.Fg)
```

Change to (drop the `if selected { ... }` block entirely):

```go
	rowBg := renderer.Styles.Theme.Bg
	baseFg := styleForeground(renderer.Styles.DetailBody, renderer.Styles.Theme.Fg)
```

Then, further down, the rail (currently `view.go:4494-4498`):

```go
	// Selection rail: keep the highlighted background running to the left
	// edge, but drop tideui.SoftRail's accent-colored "▌" marker — the
	// selected-row highlight itself already reads as "this is the cursor
	// row" without an extra, differently-colored line in front of it.
	rail := lipgloss.NewStyle().Background(rowBg).Width(dashboardRailWidth).Render("")
```

Change to:

```go
	// Selection rail: a left-edge "▌" marker is the only selection
	// indicator — no full-row background wash. SoftRail keeps its own
	// 2-cell width, so dashboardRailWidth's column math stays valid.
	rail := renderer.SoftRail(selected, rowBg)
```

`selected` remains used (passed to `SoftRail`), so no unused-variable error.

Verification for Task 5 (compiles + existing width tests still green):

```bash
gofmt -l internal/ui/view.go internal/ui/model.go
go build -buildvcs=false ./...
go test ./internal/ui/ -run 'TestDashboardRowNeverExceedsWidth|TestDashboardRowAndSummaryLineNeverFallShortOfContentWidth|TestDashboardGlyphsCarryExplicitBackground' -count=1
```

Expected: no output from `gofmt -l`; build succeeds; tests pass.

## Tests / validation (TDD)

Add tests to `internal/ui/dashboard_test.go` (package `ui`). The file already
imports `fmt`, `regexp`, `strings`, `termenv`, `lipgloss`, `tideui`, `ansi`,
`tea`, and `domain`.

### Test 1 — rail present, no ItemSelected bg wash on the selected row

Write this first, run it (it will fail against the pre-change code because the
selected row still paints `ItemSelected`'s background), then apply Tasks 1-5 and
re-run to green.

```go
// TestDashboardRowSelectionIsRailOnlyNotBackground verifies the selected row
// shows the left-edge "▌" rail but does NOT paint the row with ItemSelected's
// background color — selection is a rail marker, never a full-row wash.
func TestDashboardRowSelectionIsRailOnlyNotBackground(t *testing.T) {
	original := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(original)

	model := testModel()
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})

	var ctr domain.Container
	for _, c := range model.snapshotContainers() {
		if c.IsRunning() {
			ctr = c
			break
		}
	}
	if ctr.ID.ID == "" {
		t.Fatal("no running container found to render a row for")
	}

	selected := model.dashboardRow(renderer, ctr, 120, true)
	unselected := model.dashboardRow(renderer, ctr, 120, false)

	// Rail: the selected row starts with the "▌" marker; the unselected
	// row does not.
	if got := ansi.Strip(selected); !strings.HasPrefix(got, "▌ ") {
		t.Fatalf("selected row missing left-edge rail: %q", got)
	}
	if got := ansi.Strip(unselected); strings.HasPrefix(got, "▌") {
		t.Fatalf("unselected row unexpectedly has a rail: %q", got)
	}

	// No full-row wash: ItemSelected's background SGR must not appear in
	// the selected row.
	if bg, ok := renderer.Styles.ItemSelected.GetBackground().(lipgloss.Color); ok {
		r, g, b := parseHexColor(string(bg))
		sgr := fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
		if strings.Contains(selected, sgr) {
			t.Fatalf("selected row still paints ItemSelected background %q:\n%q", bg, selected)
		}
	}
}
```

Run to confirm it fails *before* implementation:

```bash
go test ./internal/ui/ -run TestDashboardRowSelectionIsRailOnlyNotBackground -count=1
```

Expected (pre-change): failure with "selected row still paints ItemSelected
background …". After Tasks 1-5: pass.

### Test 2 — no highlight until navigation

```go
// TestDashboardNoHighlightUntilNavigation verifies the Dashboard shows no
// selection highlight when it opens, and only after a cursor move does the
// highlight appear.
func TestDashboardNoHighlightUntilNavigation(t *testing.T) {
	model := testModel()
	model.width, model.height = 120, 34
	model.rows = model.buildRows()

	opened, _ := model.openDashboardOverlay()
	model = opened.(Model)

	// Fresh open: cursor inactive, no highlight.
	if model.dashboardCursorActive {
		t.Fatal("dashboardCursorActive = true on open, want false")
	}

	// j moves the cursor and activates the highlight.
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	model = updated.(Model)
	if !model.dashboardCursorActive {
		t.Fatal("dashboardCursorActive = false after j, want true")
	}
}
```

Run to confirm it fails *before* Task 1 (field doesn't exist yet — compile
error), then passes after Tasks 1-4:

```bash
go test ./internal/ui/ -run TestDashboardNoHighlightUntilNavigation -count=1
```

### Full suite

```bash
gofmt -l .
go test ./... 
go test -race ./...
go vet ./...
go build -buildvcs=false ./...
```

All must pass. Note `TestDashboardKeyboardSelectionMovesAndOpens`
(`dashboard_test.go:1088`) already exercises j + Enter against the real update
path and must remain green — it does not assert highlight state, so it is
unaffected.

## Risks, tradeoffs, and open questions

- **Enter before navigation.** With the flag approach, `dashboardCursor` is still
  0 on open, so pressing Enter immediately opens row 0 even though nothing is
  highlighted. The user only asked about the *visual* highlight, so this is
  preserved behavior — but flag it in case they'd prefer Enter to no-op until a
  highlight exists.
- **Mouse click.** A left-click still jumps straight to `dashboardOpenSelected`
  and closes the overlay, so the highlight is never seen for a click. Wheel
  navigation *does* activate the highlight (via `dashboardMoveCursor`).
- **`dashboardRailWidth` column math.** `SoftRail` renders exactly 2 cells
  (matching `dashboardRailWidth = 2`), so restoring it does not disturb
  `dashboardColumnsFor`'s width accounting; the existing width-invariant tests
  guard against any drift.
- **Hot-row tint preserved.** The amber/red background wash for hot containers
  is independent of `selected` and untouched — it still applies to the whole
  row regardless of selection state.
- **Alternative considered (rejected).** Using a sentinel `dashboardCursor = -1`
  for "no selection" would change cursor semantics in several places
  (`dashboardOpenSelected`, `dashboardHitTest`, `dashboardMoveCursor`) and risks
  off-by-one bugs, all to achieve what a single bool does with zero risk to
  Enter/click behavior.
