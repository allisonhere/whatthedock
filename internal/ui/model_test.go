package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/allisonhere/tideui"
	"github.com/allisonhere/whatthedock/internal/actions"
	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/config"
	"github.com/allisonhere/whatthedock/internal/domain"
)

type fakeProvider struct {
	host          domain.Host
	snapshot      domain.Snapshot
	containers    map[string]domain.Container
	stats         map[string]domain.ContainerStats
	pingErr       error
	starts        int
	stops         int
	restarts      int
	creates       []app.ContainerCreateSpec
	removed       []domain.ResourceID
	forced        []bool
	pulled        []string
	progressCalls []app.PullProgress
	removeErr     error
	pullErr       error
}

func (f *fakeProvider) Host() domain.Host          { return f.host }
func (f *fakeProvider) Ping(context.Context) error { return f.pingErr }
func (f *fakeProvider) Snapshot(context.Context) (domain.Snapshot, error) {
	return f.snapshot, nil
}
func (f *fakeProvider) Container(_ context.Context, id domain.ResourceID) (domain.Container, error) {
	return f.containers[id.ID], nil
}
func (f *fakeProvider) ContainerStats(_ context.Context, id domain.ResourceID) (domain.ContainerStats, error) {
	return f.stats[id.ID], nil
}
func (f *fakeProvider) Logs(context.Context, domain.ResourceID, app.LogOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("one\ntwo\n")), nil
}
func (f *fakeProvider) Events(context.Context) (<-chan domain.ContainerEvent, error) {
	return make(chan domain.ContainerEvent), nil
}
func (f *fakeProvider) StartContainer(context.Context, domain.ResourceID) error {
	f.starts++
	return nil
}
func (f *fakeProvider) StopContainer(context.Context, domain.ResourceID) error {
	f.stops++
	return nil
}
func (f *fakeProvider) RestartContainer(context.Context, domain.ResourceID) error {
	f.restarts++
	return nil
}
func (f *fakeProvider) CreateContainer(_ context.Context, spec app.ContainerCreateSpec) (domain.ResourceID, error) {
	f.creates = append(f.creates, spec)
	id := domain.ResourceID{Host: f.host.ID, ID: "created-" + spec.Name}
	ctr := domain.Container{ID: id, Name: spec.Name, Image: spec.Image, State: domain.StateRunning, Status: "Up 1 second", Labels: map[string]string{}}
	f.containers[id.ID] = ctr
	f.snapshot.Standalone = append(f.snapshot.Standalone, ctr)
	return id, nil
}
func (f *fakeProvider) RemoveContainer(_ context.Context, id domain.ResourceID, force bool) error {
	f.removed = append(f.removed, id)
	f.forced = append(f.forced, force)
	if f.removeErr != nil {
		return f.removeErr
	}
	delete(f.containers, id.ID)
	return nil
}
func (f *fakeProvider) PullImage(_ context.Context, image string, onProgress func(app.PullProgress)) error {
	f.pulled = append(f.pulled, image)
	if onProgress != nil {
		p := app.PullProgress{Status: "Downloading", ID: "layer1", Current: 50, Total: 100}
		f.progressCalls = append(f.progressCalls, p)
		onProgress(p)
	}
	return f.pullErr
}
func (f *fakeProvider) Close() error { return nil }

func TestStaleDetailResponseIgnoredAfterRapidNavigation(t *testing.T) {
	model := testModel()
	model.rows = model.buildRows()
	model.focus = paneTree

	var firstIdx, secondIdx int
	var firstID, secondID domain.ResourceID
	for i, row := range model.rows {
		if row.container == nil {
			continue
		}
		if firstID.ID == "" {
			firstID, firstIdx = row.container.ID, i
		} else if secondID.ID == "" {
			secondID, secondIdx = row.container.ID, i
			break
		}
	}
	if firstID.ID == "" || secondID.ID == "" {
		t.Fatal("fixture needs at least two container rows")
	}

	model.cursor = firstIdx
	cmd1 := model.loadSelectedCmd() // simulates the first keypress; sets selectedID = firstID
	model.cursor = secondIdx
	cmd2 := model.loadSelectedCmd() // simulates a rapid second keypress; sets selectedID = secondID

	// Deliver responses out of order: the second (current) selection resolves first,
	// then the stale first request arrives late over a slow connection.
	msg2 := runCmd(t, cmd2).(detailMsg)
	updated, _ := model.Update(msg2)
	model = updated.(Model)
	if model.selected == nil || model.selected.ID != secondID {
		t.Fatalf("selected = %#v, want %v after its own response", model.selected, secondID)
	}

	msg1 := runCmd(t, cmd1).(detailMsg)
	updated, _ = model.Update(msg1)
	model = updated.(Model)
	if model.selected == nil || model.selected.ID != secondID {
		t.Fatalf("selected = %#v, want still %v; a stale response clobbered the current selection", model.selected, secondID)
	}
}

func TestTreeShowsLoadingBeforeFirstSnapshot(t *testing.T) {
	model := NewModel(newFakeProvider())
	if !model.loading {
		t.Fatal("loading = false, want true before the first snapshot arrives")
	}
	model.width, model.height = 100, 30
	view := ansi.Strip(model.View())
	if !strings.Contains(view, "Loading containers...") {
		t.Fatalf("view missing loading indicator before first snapshot:\n%s", view)
	}
	if strings.Contains(view, "No containers found") {
		t.Fatalf("view showed empty-host message while still loading:\n%s", view)
	}

	updated, _ := model.Update(snapshotMsg{snapshot: model.provider.(*fakeProvider).snapshot})
	got := updated.(Model)
	if got.loading {
		t.Fatal("loading = true, want false after snapshot arrives")
	}
}

func TestBuildRowsCollapseAndFilter(t *testing.T) {
	model := testModel()
	model.rows = model.buildRows()
	if len(model.rows) != 5 {
		t.Fatalf("rows = %d, want 5", len(model.rows))
	}
	model.collapsed["media"] = true
	model.rows = model.buildRows()
	if len(model.rows) != 1 {
		t.Fatalf("collapsed rows = %d, want project row only", len(model.rows))
	}
	model.collapsed["media"] = false
	model.filter = "rad"
	model.rows = model.buildRows()
	if got := rowLabels(model.rows); strings.Join(got, ",") != "media,radarr,radarr-1" {
		t.Fatalf("filtered rows = %#v", got)
	}
}

func TestSelectionMovementClamps(t *testing.T) {
	model := testModel()
	model.rows = model.buildRows()
	model.moveCursor(100)
	if model.cursor != len(model.rows)-1 {
		t.Fatalf("cursor = %d, want last row", model.cursor)
	}
	model.moveCursor(-100)
	if model.cursor != 0 {
		t.Fatalf("cursor = %d, want first row", model.cursor)
	}
}

func TestTreeScrollsToKeepCursorVisible(t *testing.T) {
	model := testModel()
	model.rows = model.buildRows()
	model.focus = paneTree
	model.width, model.height = 100, 8 // treeVisibleRows() == 2

	if got := len(model.rows); got <= model.treeVisibleRows() {
		t.Fatalf("fixture has %d rows, want more than the %d visible rows for this test to be meaningful", got, model.treeVisibleRows())
	}

	view := ansi.Strip(model.View())
	if !strings.Contains(view, "media") {
		t.Fatalf("initial view missing top row 'media':\n%s", view)
	}

	model.moveCursor(len(model.rows) - 1)
	view = ansi.Strip(model.View())
	if strings.Contains(view, "media") {
		t.Fatalf("view still shows the scrolled-off top row 'media' after moving the cursor to the bottom row:\n%s", view)
	}
	if !strings.Contains(view, "jellyfin") {
		t.Fatalf("view missing the now-selected bottom row 'jellyfin':\n%s", view)
	}
}

func TestSnapshotRefreshPreservesSelectedContainerByID(t *testing.T) {
	model := testModelWithSelectedContainer()
	for i, row := range model.rows {
		if row.container != nil && row.container.ID.ID == "2" {
			model.cursor = i
			model.selectedID = row.container.ID
			model.selected = row.container
			break
		}
	}

	updated, cmd := model.Update(snapshotMsg{snapshot: model.provider.(*fakeProvider).snapshot})
	model = updated.(Model)
	if row := model.currentRow(); row == nil || row.container == nil || row.container.ID.ID != "2" {
		t.Fatalf("currentRow = %#v, want refresh to keep selected container id 2", row)
	}
	if cmd == nil {
		t.Fatal("cmd is nil, want preserved selected container detail reload")
	}
	msg := runCmd(t, cmd).(detailMsg)
	if msg.id.ID != "2" {
		t.Fatalf("detail load id = %q, want 2", msg.id.ID)
	}
}

func TestSnapshotRefreshDoesNotPromoteFallbackWhenSelectedContainerDisappears(t *testing.T) {
	model := testModelWithSelectedContainer()
	provider := model.provider.(*fakeProvider)
	remaining := provider.containers["2"]
	snapshot := domain.BuildSnapshot(provider.host, []domain.Container{remaining}, time.Unix(2, 0))

	updated, cmd := model.Update(snapshotMsg{snapshot: snapshot})
	model = updated.(Model)
	if cmd != nil {
		t.Fatalf("cmd = %#v, want nil because passive refresh should not load a fallback selection", cmd)
	}
	if model.selectedID.ID != "" {
		t.Fatalf("selectedID = %q, want cleared instead of promoted to remaining container", model.selectedID.ID)
	}
	if model.selected != nil {
		t.Fatalf("selected = %#v, want nil after selected container disappeared", model.selected)
	}
}

func TestSnapshotRefreshPreservesFocusedProjectRowOverSelectedContainer(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.cursor = 0
	if row := model.currentRow(); row == nil || row.kind != rowProject {
		t.Fatalf("currentRow = %#v, want project row fixture setup", row)
	}
	if model.selectedID.ID == "" {
		t.Fatal("selectedID is empty, want prior container selection to reproduce refresh jump")
	}

	updated, cmd := model.Update(snapshotMsg{snapshot: model.provider.(*fakeProvider).snapshot})
	model = updated.(Model)
	if cmd != nil {
		t.Fatalf("cmd = %#v, want nil because refresh kept focus on a non-container row", cmd)
	}
	if row := model.currentRow(); row == nil || row.kind != rowProject || row.project != "media" {
		t.Fatalf("currentRow = %#v, want refresh to preserve focused project row", row)
	}
}

func TestSnapshotRefreshPreservesFocusedServiceRowOverSelectedContainer(t *testing.T) {
	model := testModelWithSelectedContainer()
	for i, row := range model.rows {
		if row.kind == rowService && row.service == "radarr" {
			model.cursor = i
			break
		}
	}
	if row := model.currentRow(); row == nil || row.kind != rowService {
		t.Fatalf("currentRow = %#v, want service row fixture setup", row)
	}
	if model.selectedID.ID == "" {
		t.Fatal("selectedID is empty, want prior container selection to reproduce refresh jump")
	}

	updated, cmd := model.Update(snapshotMsg{snapshot: model.provider.(*fakeProvider).snapshot})
	model = updated.(Model)
	if cmd != nil {
		t.Fatalf("cmd = %#v, want nil because refresh kept focus on a non-container row", cmd)
	}
	if row := model.currentRow(); row == nil || row.kind != rowService || row.service != "radarr" {
		t.Fatalf("currentRow = %#v, want refresh to preserve focused service row", row)
	}
}

func TestTreeHeaderFocusClearsContainerTarget(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.focus = paneTree

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	model = updated.(Model)
	if cmd != nil {
		t.Fatalf("cmd = %#v, want nil when moving onto a service header", cmd)
	}
	if row := model.currentRow(); row == nil || row.kind != rowService {
		t.Fatalf("currentRow = %#v, want service header after moving up from container", row)
	}
	if model.selectedID.ID != "" || model.selected != nil {
		t.Fatalf("selectedID/selected = %q/%#v, want cleared on header focus", model.selectedID.ID, model.selected)
	}
	if selected := model.selectedContainer(); selected != nil {
		t.Fatalf("selectedContainer = %#v, want nil while focused on header", selected)
	}
}

func TestSnapshotRefreshPreservesFocusedContainerByIDWhenGroupingChanges(t *testing.T) {
	model := testModelWithSelectedContainer()
	provider := model.provider.(*fakeProvider)
	ctr := provider.containers["1"]
	ctr.Compose.Service = "zz-radarr"
	snapshot := domain.BuildSnapshot(provider.host, []domain.Container{provider.containers["2"], ctr}, time.Unix(2, 0))

	updated, cmd := model.Update(snapshotMsg{snapshot: snapshot})
	model = updated.(Model)
	if row := model.currentRow(); row == nil || row.container == nil || row.container.ID.ID != "1" {
		t.Fatalf("currentRow = %#v, want refresh to keep focused container id 1 after regrouping", row)
	}
	if cmd == nil {
		t.Fatal("cmd is nil, want preserved selected container detail reload")
	}
	msg := runCmd(t, cmd).(detailMsg)
	if msg.id.ID != "1" {
		t.Fatalf("detail load id = %q, want 1", msg.id.ID)
	}
}

func TestMovementKeysNoOpWhenInspectorFocused(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.focus = paneInspector
	cursor := model.cursor
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	got := updated.(Model)
	if got.cursor != cursor {
		t.Fatalf("cursor = %d, want unchanged %d when Inspector is focused", got.cursor, cursor)
	}
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	if got.cursor != cursor {
		t.Fatalf("cursor = %d, want unchanged %d after Down with Inspector focused", got.cursor, cursor)
	}
}

func TestMovementKeysNoOpInStatsMode(t *testing.T) {
	model := testModelInStatsMode()
	cursor := model.cursor
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	got := updated.(Model)
	if got.cursor != cursor {
		t.Fatalf("cursor = %d, want unchanged %d in stats mode", got.cursor, cursor)
	}
}

func TestCollapseTogglesOnlyWhenTreeFocused(t *testing.T) {
	model := testModel()
	model.rows = model.buildRows()
	model.focus = paneInspector
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	got := updated.(Model)
	if got.collapsed["media"] {
		t.Fatalf("space collapsed a project row while Inspector was focused")
	}
	got.focus = paneTree
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	got = updated.(Model)
	if !got.collapsed["media"] {
		t.Fatalf("space did not collapse the project row while tree was focused")
	}
}

func TestInspectorScrollsWhileFocused(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.focus = paneInspector

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	got := updated.(Model)
	if got.inspectorScroll != 1 {
		t.Fatalf("inspectorScroll = %d, want 1 after down", got.inspectorScroll)
	}

	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	got = updated.(Model)
	if got.inspectorScroll != 0 {
		t.Fatalf("inspectorScroll = %d, want 0 after up", got.inspectorScroll)
	}

	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	got = updated.(Model)
	if got.inspectorScroll != 0 {
		t.Fatalf("inspectorScroll = %d, want clamped at 0, not negative", got.inspectorScroll)
	}
}

func TestInspectorScrollResetsOnSelectionChange(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.focus = paneInspector
	model.inspectorScroll = 3
	model.rows = model.buildRows()
	for i, row := range model.rows {
		if row.container != nil && row.container.ID.ID == "2" {
			model.cursor = i
			break
		}
	}
	msg := runCmd(t, model.loadSelectedCmd()).(detailMsg)
	updated, _ := model.Update(msg)
	got := updated.(Model)
	if got.inspectorScroll != 0 {
		t.Fatalf("inspectorScroll = %d, want reset to 0 after selection changed", got.inspectorScroll)
	}
}

func TestActionStartStopUsesSelectedState(t *testing.T) {
	provider := newFakeProvider()
	model := NewModel(provider)
	model.snapshot = provider.snapshot
	model.rows = model.buildRows()
	for i, row := range model.rows {
		if row.container != nil && row.container.ID.ID == "1" {
			model.cursor = i
			break
		}
	}
	msg := runCmd(t, model.actionCmd("start-stop-container", "start/stop")).(actionDoneMsg)
	if msg.err != nil {
		t.Fatalf("action err = %v", msg.err)
	}
	if provider.stops != 1 {
		t.Fatalf("stops = %d, want 1", provider.stops)
	}
}

func TestProblemsModeFindsUnhealthyAndStoppedContainers(t *testing.T) {
	model := testModel()
	problems := model.snapshotProblems()
	if len(problems) != 2 {
		t.Fatalf("problems = %d, want 2", len(problems))
	}
	got := []string{problems[0].name + ":" + problems[0].detail, problems[1].name + ":" + problems[1].detail}
	want := "radarr-1:unhealthy,jellyfin-1:stopped"
	if strings.Join(got, ",") != want {
		t.Fatalf("problems = %#v, want %s", got, want)
	}
}

// TestProblemInsight covers problemInsight's rule-based, no-network
// baseline text: one fixture per snapshotProblems classification branch,
// asserting the text is specific to that container (name, restart count,
// etc.) rather than generic boilerplate.
func TestProblemInsight(t *testing.T) {
	tests := []struct {
		name string
		row  problemRow
		want []string // substrings that must all appear
	}{
		{
			name: "unhealthy",
			row: problemRow{name: "radarr-1", container: domain.Container{
				Name: "radarr-1", Health: domain.HealthUnhealthy,
			}},
			want: []string{"radarr-1", "health check"},
		},
		{
			name: "restarting with count",
			row: problemRow{name: "telegraf-agent", container: domain.Container{
				Name: "telegraf-agent", Restarting: true, RestartCount: 7,
			}},
			want: []string{"telegraf-agent", "7 times", "crash loop"},
		},
		{
			name: "restarting no count yet",
			row: problemRow{name: "sonarr-1", container: domain.Container{
				Name: "sonarr-1", State: domain.StateRestarting,
			}},
			want: []string{"sonarr-1", "restarting"},
		},
		{
			name: "dead",
			row: problemRow{name: "adguard-1", container: domain.Container{
				Name: "adguard-1", State: domain.StateDead,
			}},
			want: []string{"adguard-1", "dead"},
		},
		{
			name: "high restart count while running",
			row: problemRow{name: "watchtower-1", container: domain.Container{
				Name: "watchtower-1", State: domain.StateRunning, RestartCount: 9,
			}},
			want: []string{"watchtower-1", "9 times"},
		},
		{
			name: "stopped with status",
			row: problemRow{name: "grafana-1", container: domain.Container{
				Name: "grafana-1", State: domain.StateExited, Status: "Exited (1) 3 hours ago", RestartPolicy: "no",
			}},
			want: []string{"grafana-1", "Exited (1) 3 hours ago"},
		},
		{
			name: "health unknown",
			row: problemRow{name: "postgres-1", container: domain.Container{
				Name: "postgres-1", Health: domain.HealthUnknown,
			}},
			want: []string{"postgres-1", "start period"},
		},
		{
			name: "public ports on a standalone container",
			row: problemRow{name: "nginx-1", container: domain.Container{
				Name: "nginx-1", Ports: []domain.Port{{Public: 80}},
			}},
			want: []string{"nginx-1", "public"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := problemInsight(tt.row)
			for _, substr := range tt.want {
				if !strings.Contains(got, substr) {
					t.Fatalf("problemInsight() = %q, want it to contain %q", got, substr)
				}
			}
		})
	}
}

// TestRenderProblemsSplitTracksCursorSelection checks the split-pane
// render actually reflects the currently selected problem row — moving the
// cursor changes which container's insight text is shown.
func TestRenderProblemsSplitTracksCursorSelection(t *testing.T) {
	model := testModel()
	model.width, model.height = 120, 34
	model.focus = paneActivity
	model.mode = activityProblems
	model.problemCursor = 0

	first := ansi.Strip(model.View())
	if !strings.Contains(first, "radarr-1") || !strings.Contains(first, "health check") {
		t.Fatalf("view at cursor 0 missing radarr-1's insight:\n%s", first)
	}

	model.problemCursor = 1
	second := ansi.Strip(model.View())
	if !strings.Contains(second, "jellyfin-1") {
		t.Fatalf("view at cursor 1 missing jellyfin-1's insight:\n%s", second)
	}
	if strings.Contains(second, "health check") {
		t.Fatalf("view at cursor 1 still shows radarr-1's insight:\n%s", second)
	}
}

// TestWrapInsightTextNoTruncationNeeded checks the common case — text that
// already fits within maxLines is returned unchanged, no ellipsis added.
func TestWrapInsightTextNoTruncationNeeded(t *testing.T) {
	lines := wrapInsightText("short and sweet", 40, 6)
	if len(lines) != 1 {
		t.Fatalf("lines = %#v, want 1 line for text well under the budget", lines)
	}
	if strings.Contains(lines[0], "…") {
		t.Fatalf("lines[0] = %q, want no ellipsis when nothing was truncated", lines[0])
	}
}

// TestWrapInsightTextTruncatesAtWordBoundaryWithEllipsis is the regression
// test for the bug reported live: a suggestion longer than the insight
// panel's row budget was getting hard-cut mid-sentence with no indication
// anything was omitted. Truncation must land on a word boundary and end
// with an ellipsis, and every returned line — including the truncated one
// — must never exceed width.
func TestWrapInsightTextTruncatesAtWordBoundaryWithEllipsis(t *testing.T) {
	text := "media-postgres-1 is not running (Exited (137) 2 days ago due to an out-of-memory " +
		"condition triggered by the host's cgroup limits). If this wasn't intentional, check its " +
		"logs (l) for the exit reason, or its restart policy (on-failure:5) if you expected Docker " +
		"to bring it back on its own."
	width, maxLines := 45, 6

	lines := wrapInsightText(text, width, maxLines)
	if len(lines) != maxLines {
		t.Fatalf("len(lines) = %d, want exactly %d", len(lines), maxLines)
	}
	for i, line := range lines {
		if got := len([]rune(line)); got > width {
			t.Fatalf("line %d = %q, rendered width %d exceeds %d", i, line, got, width)
		}
	}
	last := strings.TrimRight(lines[maxLines-1], " ")
	if !strings.HasSuffix(last, "…") {
		t.Fatalf("last line = %q, want it to end with an ellipsis to signal truncation", last)
	}
	beforeEllipsis := strings.TrimSuffix(last, "…")
	if strings.HasSuffix(strings.TrimRight(beforeEllipsis, " "), "-") {
		t.Fatalf("last line = %q, truncation landed mid-word instead of on a word boundary", last)
	}
}

// TestWrapInsightTextEllipsisNeverExceedsWidth guards the truncation loop
// itself: for a spread of widths, the truncated line (with its appended
// ellipsis) must never overflow the requested width.
func TestWrapInsightTextEllipsisNeverExceedsWidth(t *testing.T) {
	text := strings.Repeat("word ", 60) + "final."
	for _, width := range []int{20, 30, 45, 60, 80} {
		lines := wrapInsightText(text, width, 3)
		for i, line := range lines {
			if got := len([]rune(line)); got > width {
				t.Fatalf("width=%d line %d = %q, rendered width %d exceeds %d", width, i, line, got, width)
			}
		}
	}
}

// TestRenderProblemsSplitDoesNotPanicOnShortTerminal guards
// problemsListRows/problemsInsightRows' budget math against a terminal too
// short to fit both the list and the insight block comfortably — must
// degrade gracefully (clamped, non-negative row counts), never panic on a
// negative slice bound.
func TestRenderProblemsSplitDoesNotPanicOnShortTerminal(t *testing.T) {
	model := testModel()
	model.width, model.height = 80, 9
	model.focus = paneActivity
	model.mode = activityProblems

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("View() panicked on a short terminal: %v", r)
		}
	}()
	_ = model.View()
}

// TestRenderProblemsSplitSummaryLineFillsFullWidth is the regression test
// for a background bug reported live: the "N problem(s) found" summary
// line was built without Width(width). renderProblemsSplit's final step —
// lipgloss.JoinVertical(lipgloss.Left, list, divider, insight) — re-pads
// every line across all three blocks to the widest line among them using
// its own plain, unstyled spaces; a line that already reached that width
// on its own (every problem row, via RenderRow) is untouched, but a
// shorter one gets JoinVertical's own unstyled padding baked in before it
// ever reaches the pane's normal background-aware compositor, which by
// then just sees an already-"full-width" line with nothing to fix. That
// unstyled tail showed up as a stray colored box (picking up whatever
// happened to render on top of raw terminal default) sitting half over
// the header. This is specifically about JoinVertical — see
// TestRenderProblemInsightFillsFullWidth for the same fix applied to the
// AI-heading lines, which pass through the same JoinVertical call via
// insight; ordinary standalone or plainly strings.Join'd short lines
// elsewhere in the app (e.g. the Logs pane's placeholder messages, or
// this same pane's "No container problems detected." empty state) are
// NOT affected, since they never pass through JoinVertical, and adding
// Width(width) to those was confirmed unnecessary — worse, it can
// silently word-wrap or truncate what's meant to stay a single line.
func TestRenderProblemsSplitSummaryLineFillsFullWidth(t *testing.T) {
	model := testModel()
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})

	content, width := model.renderProblemsSplit(renderer)
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		t.Fatal("renderProblemsSplit content has no lines")
	}
	summary := ansi.Strip(lines[0])
	if got := len([]rune(summary)); got != width {
		t.Fatalf("summary line visible width = %d (%q), want exactly %d", got, summary, width)
	}
}

// TestRenderProblemInsightFillsFullWidth is
// TestRenderProblemsSplitSummaryLineFillsFullWidth's counterpart for
// renderProblemInsight's own four return paths: each builds its heading
// line via foregroundSpan (short, not itself padded to width) and used to
// hand that off to a bare DetailBody.Render(...) with no Width(width).
// renderProblemsSplit then stacks this insight block against the
// full-width list/divider blocks via lipgloss.JoinVertical, whose own
// plain, unstyled padding stretched the short heading line out — the same
// stray-background-box bug as the summary line, just on "Analyzing with
// AI…"/"AI Analysis"/"AI analysis failed" instead of the problem count.
func TestRenderProblemInsightFillsFullWidth(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	row := problemRow{id: domain.ResourceID{Host: "local", ID: "1"}, severity: "crit", name: "radarr-1", detail: "unhealthy"}
	const width = 46

	checkLines := func(t *testing.T, label, content string) {
		t.Helper()
		for i, line := range strings.Split(content, "\n") {
			if got := len([]rune(ansi.Strip(line))); got != width {
				t.Fatalf("%s line %d visible width = %d (%q), want exactly %d", label, i, got, ansi.Strip(line), width)
			}
		}
	}

	t.Run("rule-based (no AI)", func(t *testing.T) {
		model := testModel()
		checkLines(t, "rule-based", model.renderProblemInsight(renderer, row, width))
	})

	t.Run("analyzing", func(t *testing.T) {
		model := testModel()
		model.aiAnalysisFor = row.id
		model.aiAnalyzing = true
		checkLines(t, "analyzing", model.renderProblemInsight(renderer, row, width))
	})

	t.Run("analysis failed", func(t *testing.T) {
		model := testModel()
		model.aiAnalysisFor = row.id
		model.aiAnalysisErr = errBoom
		checkLines(t, "failed", model.renderProblemInsight(renderer, row, width))
	})

	t.Run("analysis succeeded", func(t *testing.T) {
		model := testModel()
		model.aiAnalysisFor = row.id
		model.aiAnalysis = "Restart the container and check its logs for the underlying cause."
		checkLines(t, "succeeded", model.renderProblemInsight(renderer, row, width))
	})
}

func TestProblemsKeySwitchesActivityMode(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 30

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	model = updated.(Model)
	if model.mode != activityProblems || model.focus != paneActivity {
		t.Fatalf("mode/focus = %v/%v, want problems/activity", model.mode, model.focus)
	}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "Problems") || !strings.Contains(view, "problem(s) found") {
		t.Fatalf("View() missing problems mode content:\n%s", view)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	model = updated.(Model)
	if model.mode != activityLogs || model.focus != paneActivity {
		t.Fatalf("mode/focus = %v/%v, want logs/activity", model.mode, model.focus)
	}
}

func TestStatsKeySwitchesActivityMode(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 30
	model.rows = model.buildRows()
	for i, row := range model.rows {
		if row.container != nil && row.container.ID.ID == "1" {
			model.cursor = i
			break
		}
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	model = updated.(Model)
	if model.mode != activityStats || model.focus != paneActivity {
		t.Fatalf("mode/focus = %v/%v, want stats/activity", model.mode, model.focus)
	}
	if !model.statsLoading {
		t.Fatal("statsLoading = false, want true after opening stats")
	}
	updated, _ = model.Update(runCmd(t, cmd))
	model = updated.(Model)
	if model.statsLoading {
		t.Fatal("statsLoading = true, want false after stats load")
	}
	if len(model.statsHistory[domain.ResourceID{Host: "local", ID: "1"}].CPU) != 1 {
		t.Fatalf("stats history = %#v, want one CPU sample", model.statsHistory)
	}
	rawView := model.View()
	if !strings.Contains(rawView, "\x1b[") {
		t.Fatalf("View() missing ANSI styling for colored stats graphs:\n%s", rawView)
	}
	view := ansi.Strip(rawView)
	for _, want := range []string{"Stats", "CPU", "Memory", "▓", "░", "41.5%", "384.0 MiB / 2.0 GiB"} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() missing stats content %q:\n%s", want, view)
		}
	}
}

func TestLogsRenderColorCodedTokens(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 30
	model.mode = activityLogs
	ctr := model.provider.(*fakeProvider).containers["1"]
	model.selected = &ctr
	model.logLines = []string{
		"2026-08-12T12:00:00Z [INFO] GET /healthz 200",
		"12:00:01 ERROR request failed 500",
	}

	rawView := model.View()
	view := ansi.Strip(rawView)
	for _, want := range []string{"[INFO]", "GET", "200", "ERROR", "500"} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() missing log token %q:\n%s", want, view)
		}
	}
	if strings.Count(rawView, "\x1b[") < 6 {
		t.Fatalf("View() missing ANSI styling for color-coded logs:\n%s", rawView)
	}
}

func TestFocusedPaneActionStripIsContextual(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.width, model.height = 220, 30
	model.mode = activityLogs
	model.logLines = numberedLogLines(5)

	model.focus = paneTree
	treeRaw := model.View()
	treeView := ansi.Strip(treeRaw)
	if !strings.Contains(treeView, "space fold") || !strings.Contains(treeView, "/ filter") || !strings.Contains(treeView, "r refresh") {
		t.Fatalf("tree action strip missing contextual actions:\n%s", treeView)
	}
	if strings.Contains(treeView, "[space fold]") || !strings.Contains(treeRaw, "\x1b[") {
		t.Fatalf("tree action strip should be styled chips, not bracket text:\n%s", treeView)
	}
	if strings.Contains(treeView, "alt+r restart") {
		t.Fatalf("tree action strip leaked inspector action:\n%s", treeView)
	}

	model.focus = paneInspector
	inspectorView := ansi.Strip(model.View())
	if !strings.Contains(inspectorView, "alt+r restart") || !strings.Contains(inspectorView, "o open") {
		t.Fatalf("inspector action strip missing container actions:\n%s", inspectorView)
	}
	if strings.Contains(inspectorView, "space fold") {
		t.Fatalf("inspector action strip leaked tree action:\n%s", inspectorView)
	}
}

func TestActivityActionStripChangesByMode(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.width, model.height = 220, 30
	model.focus = paneActivity
	model.mode = activityLogs
	model.logLines = numberedLogLines(5)
	model.logFollow = true

	logView := ansi.Strip(model.View())
	for _, want := range []string{"k pause", "/ search", "n/N match", "x clear"} {
		if !strings.Contains(logView, want) {
			t.Fatalf("logs action strip missing %q:\n%s", want, logView)
		}
	}

	model.logFollow = false
	pausedView := ansi.Strip(model.View())
	if !strings.Contains(pausedView, "f live") || strings.Contains(pausedView, "k pause") {
		t.Fatalf("paused logs action strip =\n%s", pausedView)
	}

	model.mode = activityProblems
	problemsView := ansi.Strip(model.View())
	for _, want := range []string{"enter inspect", "r refresh", "l logs", "g stats"} {
		if !strings.Contains(problemsView, want) {
			t.Fatalf("problems action strip missing %q:\n%s", want, problemsView)
		}
	}

	model.mode = activityStats
	statsView := ansi.Strip(model.View())
	for _, want := range []string{"r refresh", "l logs", "p problems"} {
		if !strings.Contains(statsView, want) {
			t.Fatalf("stats action strip missing %q:\n%s", want, statsView)
		}
	}
	if strings.Contains(statsView, "k pause") || strings.Contains(statsView, "f live") {
		t.Fatalf("stats action strip should not advertise log live controls:\n%s", statsView)
	}
}

func TestLogActionStripStaysVisibleWhenLogPaneIsFull(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.width, model.height = 100, 12
	model.focus = paneActivity
	model.mode = activityLogs
	model.logLines = numberedLogLines(50)
	model.logFollow = true

	view := ansi.Strip(model.View())
	for _, want := range []string{"k pause", "/ search", "x clear"} {
		if !strings.Contains(view, want) {
			t.Fatalf("full log pane missing persistent footer chip %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "tail 50/50") || !strings.Contains(view, "line-50") {
		t.Fatalf("full log pane should keep tail content above footer:\n%s", view)
	}
}

func TestRenderLogLinePreservesTextWhenStripped(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	line := "2026-08-12T12:00:00Z [WARN] POST /api 404"
	rendered := renderLogLine(renderer, logColorFull, "", line)
	if got := ansi.Strip(rendered); got != line {
		t.Fatalf("stripped renderLogLine() = %q, want %q", got, line)
	}
}

func TestLogFilterOverlayFiltersVisibleLogsAndHighlightsMatches(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 30
	model.focus = paneActivity
	model.mode = activityLogs
	ctr := model.provider.(*fakeProvider).containers["1"]
	model.selected = &ctr
	model.logLines = []string{
		"2026-08-12T12:00:00Z [INFO] GET /healthz 200",
		"2026-08-12T12:00:01Z [ERROR] POST /api failed 500",
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	model = updated.(Model)
	if model.overlay != overlayLogFilter {
		t.Fatalf("overlay = %v, want log filter", model.overlay)
	}
	for _, char := range []rune("api") {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{char}})
		model = updated.(Model)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.logFilter != "api" || model.overlay != overlayNone {
		t.Fatalf("logFilter/overlay = %q/%v, want api/none", model.logFilter, model.overlay)
	}
	rawView := model.View()
	view := ansi.Strip(rawView)
	if strings.Contains(view, "/healthz") || !strings.Contains(view, "/api failed") {
		t.Fatalf("filtered log view =\n%s", view)
	}
	for _, want := range []string{"tail 1/1", "filter api", "match 1/1", "x", "clear"} {
		if !strings.Contains(view, want) {
			t.Fatalf("filtered log view missing %q:\n%s", want, view)
		}
	}
	if !strings.Contains(rawView, "\x1b[") {
		t.Fatalf("filtered log match should be highlighted:\n%s", rawView)
	}
}

func TestSlashSearchesLogsWhenLogsAreVisibleFromTreeFocus(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 30
	model.focus = paneTree
	model.mode = activityLogs
	ctr := model.provider.(*fakeProvider).containers["1"]
	model.selected = &ctr
	model.selectedID = ctr.ID

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	model = updated.(Model)
	if model.focus != paneActivity || model.overlay != overlayLogFilter {
		t.Fatalf("focus/overlay = %v/%v, want activity/log filter", model.focus, model.overlay)
	}
	if model.overlay == overlayFilter {
		t.Fatal("slash opened project filter while logs were visible")
	}
}

func TestLogSeverityQuickFilters(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 30
	model.focus = paneActivity
	model.mode = activityLogs
	ctr := model.provider.(*fakeProvider).containers["1"]
	model.selected = &ctr
	model.logLines = []string{
		"2026-08-12T12:00:00Z [INFO] GET /healthz 200",
		"2026-08-12T12:00:01Z [WARN] slow request 200",
		"2026-08-12T12:00:02Z [ERROR] POST /api failed 500",
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	model = updated.(Model)
	if model.logLevel != logSeverityErrors {
		t.Fatalf("logLevel = %v, want errors", model.logLevel)
	}
	view := ansi.Strip(model.View())
	if !strings.Contains(view, "failed") || strings.Contains(view, "slow request") || strings.Contains(view, "/healthz") {
		t.Fatalf("error-filtered log view =\n%s", view)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	model = updated.(Model)
	if model.logLevel != logSeverityAll {
		t.Fatalf("logLevel = %v, want all", model.logLevel)
	}
	view = ansi.Strip(model.View())
	for _, want := range []string{"/healthz", "slow", "request", "failed"} {
		if !strings.Contains(view, want) {
			t.Fatalf("all-filtered log view missing %q:\n%s", want, view)
		}
	}
}

func TestLogColorModeControlsTokenStyling(t *testing.T) {
	original := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(original) })

	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	line := "2026-08-12T12:00:00Z [ERROR] POST /api failed 500"
	full := renderLogLine(renderer, logColorFull, "", line)
	mono := renderLogLine(renderer, logColorMono, "", line)

	if !strings.Contains(full, "\x1b[") || strings.Contains(mono, "\x1b[") {
		t.Fatalf("log color ANSI mismatch: full=%q mono=%q", full, mono)
	}
	if ansi.Strip(full) != ansi.Strip(mono) {
		t.Fatalf("log color modes changed visible text:\nfull=%s\nmono=%s", ansi.Strip(full), ansi.Strip(mono))
	}
}

func TestLogNavigationScrollsPausesAndResumesTail(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 12
	model.focus = paneActivity
	model.mode = activityLogs
	ctr := model.provider.(*fakeProvider).containers["1"]
	model.selected = &ctr
	model.selectedID = ctr.ID
	model.logViewID = ctr.ID
	model.logFollow = true
	model.logLines = numberedLogLines(12)

	view := ansi.Strip(model.View())
	if !strings.Contains(view, "tail 12/12") || strings.Contains(view, "line-01") || !strings.Contains(view, "line-12") {
		t.Fatalf("tail log view =\n%s", view)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	model = updated.(Model)
	if model.logFollow {
		t.Fatal("logFollow = true after scrolling up, want paused")
	}
	view = ansi.Strip(model.View())
	if !strings.Contains(view, "paused") || strings.Contains(view, "line-12") {
		t.Fatalf("paused log view =\n%s", view)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnd})
	model = updated.(Model)
	if !model.logFollow {
		t.Fatal("logFollow = false after End, want true")
	}
	view = ansi.Strip(model.View())
	if !strings.Contains(view, "tail 12/12") || !strings.Contains(view, "line-12") {
		t.Fatalf("resumed tail log view =\n%s", view)
	}
}

func TestLogFollowKeyWorksFromAnyFocusWhileInLogs(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 12
	model.focus = paneTree
	model.mode = activityLogs
	ctr := model.provider.(*fakeProvider).containers["1"]
	model.selected = &ctr
	model.selectedID = ctr.ID
	model.logViewID = ctr.ID
	model.logLines = numberedLogLines(12)
	model.logScroll = 1
	model.logFollow = false

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	model = updated.(Model)
	if model.focus != paneActivity || !model.logFollow {
		t.Fatalf("focus/follow = %v/%v, want activity/true", model.focus, model.logFollow)
	}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "tail 12/12") || !strings.Contains(view, "line-12") {
		t.Fatalf("follow from tree focus view =\n%s", view)
	}
}

func TestEscapeClearsStuckLogSearch(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 12
	model.focus = paneActivity
	model.mode = activityLogs
	ctr := model.provider.(*fakeProvider).containers["1"]
	model.selected = &ctr
	model.selectedID = ctr.ID
	model.logViewID = ctr.ID
	model.logLines = numberedLogLines(5)
	model.logFilter = "missing"
	model.logLevel = logSeverityErrors
	model.logFollow = false

	if view := ansi.Strip(model.View()); !strings.Contains(view, `No logs match "missing" · errors · esc`) || !strings.Contains(view, "clear") {
		t.Fatalf("precondition view should show empty filtered state:\n%s", view)
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.logFilter != "" || model.logLevel != logSeverityAll || !model.logFollow {
		t.Fatalf("log state = filter:%q level:%v follow:%v, want cleared/all/follow", model.logFilter, model.logLevel, model.logFollow)
	}
	if view := ansi.Strip(model.View()); strings.Contains(view, "No logs match") || !strings.Contains(view, "line-05") {
		t.Fatalf("cleared search view =\n%s", view)
	}
}

func TestXClearsActiveLogFilterFromAnyFocus(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 12
	model.focus = paneTree
	model.mode = activityLogs
	ctr := model.provider.(*fakeProvider).containers["1"]
	model.selected = &ctr
	model.selectedID = ctr.ID
	model.logViewID = ctr.ID
	model.logLines = numberedLogLines(5)
	model.logFilter = "missing"
	model.logLevel = logSeverityWarnings
	model.logFollow = false

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	model = updated.(Model)
	if model.focus != paneActivity || model.logFilter != "" || model.logLevel != logSeverityAll || !model.logFollow {
		t.Fatalf("log state = focus:%v filter:%q level:%v follow:%v, want activity/empty/all/follow", model.focus, model.logFilter, model.logLevel, model.logFollow)
	}
	if view := ansi.Strip(model.View()); strings.Contains(view, "No logs match") || !strings.Contains(view, "line-05") {
		t.Fatalf("x-cleared log view =\n%s", view)
	}
}

func TestLogSearchNOpensFilterWhenNoTextFilter(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 12
	model.focus = paneActivity
	model.mode = activityLogs
	ctr := model.provider.(*fakeProvider).containers["1"]
	model.selected = &ctr

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	model = updated.(Model)
	if model.focus != paneActivity || model.overlay != overlayLogFilter {
		t.Fatalf("focus/overlay = %v/%v, want activity/log filter", model.focus, model.overlay)
	}
}

func TestNOpensCreateOverlayWhenNotFocusedOnLogs(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 12
	model.focus = paneTree
	model.mode = activityLogs
	ctr := model.provider.(*fakeProvider).containers["1"]
	model.selected = &ctr

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	model = updated.(Model)
	if model.overlay != overlayCreate {
		t.Fatalf("overlay = %v, want overlayCreate ('n' should create when the tree, not the logs pane, is focused)", model.overlay)
	}
}

func TestLogSearchMatchNavigation(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 8
	model.focus = paneActivity
	model.mode = activityLogs
	ctr := model.provider.(*fakeProvider).containers["1"]
	model.selected = &ctr
	model.selectedID = ctr.ID
	model.logViewID = ctr.ID
	model.logFilter = "api"
	model.logFollow = true
	model.logLines = []string{
		"api match 01",
		"api match 02",
		"api match 03",
		"api match 04",
	}

	view := ansi.Strip(model.View())
	if !strings.Contains(view, "match 1/4") {
		t.Fatalf("initial match indicator missing:\n%s", view)
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	model = updated.(Model)
	if model.logMatch != 1 || model.logFollow {
		t.Fatalf("match/follow = %d/%v, want 1/false", model.logMatch, model.logFollow)
	}
	view = ansi.Strip(model.View())
	if !strings.Contains(view, "match 2/4") || strings.Contains(view, "api match 01") {
		t.Fatalf("next match view =\n%s", view)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	model = updated.(Model)
	if model.logMatch != 0 {
		t.Fatalf("logMatch = %d, want 0 after previous", model.logMatch)
	}
	view = ansi.Strip(model.View())
	if !strings.Contains(view, "match 1/4") || !strings.Contains(view, "api match 01") {
		t.Fatalf("previous match view =\n%s", view)
	}
}

func TestLogFollowKeepsTailWhilePausedIgnoresIncomingLines(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 12
	model.focus = paneActivity
	model.mode = activityLogs
	ctr := model.provider.(*fakeProvider).containers["1"]
	model.selected = &ctr
	model.selectedID = ctr.ID
	model.logViewID = ctr.ID
	model.logLines = numberedLogLines(9)
	model.logFollow = true
	model.drainLogLineForTest("line-10")
	if !model.logFollow || model.logScroll != max(0, len(model.visibleLogLines())-model.logVisibleRows()) {
		t.Fatalf("follow state after incoming line = follow:%v scroll:%d", model.logFollow, model.logScroll)
	}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "line-10") {
		t.Fatalf("follow view missing newest line:\n%s", view)
	}

	model.scrollLogs(-2)
	pausedScroll := model.logScroll
	model.drainLogLineForTest("line-11")
	if model.logFollow || model.logScroll != pausedScroll {
		t.Fatalf("paused incoming line changed state = follow:%v scroll:%d want false/%d", model.logFollow, model.logScroll, pausedScroll)
	}
	if view := ansi.Strip(model.View()); strings.Contains(view, "line-11") {
		t.Fatalf("paused view should not jump to newest line:\n%s", view)
	}
}

func TestLogViewStateIsPreservedPerContainer(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 12
	model.mode = activityLogs
	model.logViews = map[domain.ResourceID]logViewState{}
	first := domain.ResourceID{Host: "local", ID: "1"}
	second := domain.ResourceID{Host: "local", ID: "2"}
	model.selectedID = first
	model.logViewID = first
	model.logFilter = "api"
	model.logLevel = logSeverityErrors
	model.logScroll = 3
	model.logFollow = false
	model.saveLogViewState()

	model.restoreLogViewState(second)
	if model.logFilter != "" || model.logLevel != logSeverityAll || !model.logFollow {
		t.Fatalf("new container log state = filter:%q level:%v follow:%v, want defaults", model.logFilter, model.logLevel, model.logFollow)
	}

	model.restoreLogViewState(first)
	if model.logFilter != "api" || model.logLevel != logSeverityErrors || model.logScroll != 3 || model.logFollow {
		t.Fatalf("restored log state = filter:%q level:%v scroll:%d follow:%v", model.logFilter, model.logLevel, model.logScroll, model.logFollow)
	}
}

func TestLogRefreshDoesNotClobberOpenFilterDraft(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.mode = activityLogs
	model.focus = paneActivity
	model.openLogFilter()
	for _, char := range []rune("500") {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{char}})
		model = updated.(Model)
	}
	if model.logDraft != "500" {
		t.Fatalf("logDraft = %q, want typed filter before refresh", model.logDraft)
	}

	container := *model.selected
	updated, _ := model.Update(detailMsg{id: model.selectedID, container: container})
	model = updated.(Model)
	if model.overlay != overlayLogFilter || model.logDraft != "500" {
		t.Fatalf("overlay/logDraft = %v/%q, want open filter draft preserved across refresh", model.overlay, model.logDraft)
	}
}

func TestBackgroundRefreshDoesNotRestartLogStreamForSameContainer(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.mode = activityLogs
	model.logChan = make(chan string)
	model.logLines = []string{"already here"}
	container := *model.selected

	updated, cmd := model.Update(detailMsg{id: model.selectedID, container: container})
	model = updated.(Model)
	if cmd != nil {
		t.Fatalf("cmd = %#v, want nil for same-container background refresh", cmd)
	}
	if model.logChan == nil {
		t.Fatal("logChan was cleared, want the already-running stream left alone")
	}
	if strings.Join(model.logLines, "\n") != "already here" {
		t.Fatalf("logLines = %#v, want existing lines preserved", model.logLines)
	}
}

func TestSelectionChangeStillStartsLogStream(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.mode = activityLogs
	model.logChan = make(chan string)
	other := newFakeProvider().containers["2"]
	model.selectedID = other.ID

	updated, cmd := model.Update(detailMsg{id: other.ID, container: other})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("cmd = nil, want startLogsCmd after a real selection change")
	}
	if model.selected == nil || model.selected.ID != other.ID {
		t.Fatalf("selected = %#v, want %v", model.selected, other.ID)
	}
}

func TestSameContainerLogReconnectDoesNotRenderEmptyFrame(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.mode = activityLogs
	model.width, model.height = 100, 30
	model.logLines = []string{"old redis line"}
	id := model.selectedID
	lines := make(chan string, 1)

	updated, cmd := model.Update(logsStartedMsg{id: id, lines: lines, cancel: func() {}})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("cmd = nil, want log drain tick")
	}
	if len(model.logLines) != 1 || model.logLines[0] != "old redis line" {
		t.Fatalf("logLines after reconnect start = %#v, want old line preserved until first replacement line", model.logLines)
	}
	view := ansi.Strip(model.View())
	if strings.Contains(view, "Waiting for logs") {
		t.Fatalf("view rendered transient waiting state during same-container reconnect:\n%s", view)
	}

	model.logChan <- "new redis line"
	model.drainLogs()
	if len(model.logLines) != 1 || model.logLines[0] != "new redis line" {
		t.Fatalf("logLines after first replacement drain = %#v, want replacement line only", model.logLines)
	}
	close(lines)
}

func TestStaleLogsStartedMessageIsIgnored(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.logLines = []string{"current container line"}
	stale := newFakeProvider().containers["2"].ID

	updated, cmd := model.Update(logsStartedMsg{id: stale, lines: make(chan string), cancel: func() {}})
	model = updated.(Model)
	if cmd != nil {
		t.Fatalf("cmd = %#v, want nil for stale log stream", cmd)
	}
	if len(model.logLines) != 1 || model.logLines[0] != "current container line" {
		t.Fatalf("logLines = %#v, want stale stream ignored", model.logLines)
	}
}

func TestLogTokenColors(t *testing.T) {
	if got := httpStatusColor("200"); got != lipgloss.Color("#80c990") {
		t.Fatalf("httpStatusColor(200) = %q, want green", got)
	}
	if got := httpStatusColor("404"); got != lipgloss.Color("#f5a97f") {
		t.Fatalf("httpStatusColor(404) = %q, want orange", got)
	}
	if got := httpStatusColor("500"); got != lipgloss.Color("#e06c75") {
		t.Fatalf("httpStatusColor(500) = %q, want red", got)
	}
	if got := logSeverityColor("[WARN]"); got != lipgloss.Color("#e8c170") {
		t.Fatalf("logSeverityColor([WARN]) = %q, want yellow", got)
	}
	if got := logSeverityColor("ERROR"); got != lipgloss.Color("#e06c75") {
		t.Fatalf("logSeverityColor(ERROR) = %q, want red", got)
	}
}

// TestRenderLogLineNeverEmitsAbsoluteReset is the regression test for a
// background bug reported live: renderLogLine's colored tokens (via
// renderLogToken/renderLogMatch) used to be built through independent,
// self-contained lipgloss.NewStyle()...Render() calls, each emitting an
// absolute "\x1b[0m"/"\x1b[m" reset at its own end. Since renderLogLine's
// whole output is always embedded inside ONE outer, already-open
// background style (renderActivity's DetailBody.Width(width).Render(...)),
// any reset partway through wiped out that outer background for every
// character after it, until the next colored token re-established one —
// visible as broken/patchy line backgrounds on any log line containing a
// timestamp, HTTP method/status, severity keyword, or search-match
// highlight, which is the common case under the default "full" log color
// mode. renderLogLine's own output must never contain an absolute
// reset — every colored span restores via foregroundSpan/backgroundSpan
// instead, which only ever change foreground/background explicitly and
// never reset (see their own doc comments).
func TestRenderLogLineNeverEmitsAbsoluteReset(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	line := `2024-01-01T12:00:00Z GET /api/v3/queue 200 [ERROR] something failed`

	out := renderLogLine(renderer, logColorFull, "", line)
	if strings.Contains(out, "\x1b[0m") || strings.Contains(out, "\x1b[m") {
		t.Fatalf("renderLogLine output contains an absolute reset, want none:\n%q", out)
	}

	matched := renderLogLine(renderer, logColorFull, "queue", line)
	if strings.Contains(matched, "\x1b[0m") || strings.Contains(matched, "\x1b[m") {
		t.Fatalf("renderLogLine output (with a search-match highlight) contains an absolute reset, want none:\n%q", matched)
	}
}

// TestRenderLogLineKeepsBackgroundAfterColoredToken renders a log line
// through the real activity pane (so it's wrapped in its actual outer
// DetailBody background style) and checks the text following a colored
// token still carries an explicit background SGR rather than falling
// through to whatever the terminal defaults to.
func TestRenderLogLineKeepsBackgroundAfterColoredToken(t *testing.T) {
	original := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(original)

	model := testModelWithSelectedContainer()
	model.width, model.height = 120, 34
	model.mode = activityLogs
	model.logLines = []string{"2024-01-01T12:00:00Z GET /api/v3/queue 200 trailing text after the status code"}

	view := model.View()
	if !strings.Contains(view, "trailing text after the status code") {
		t.Fatalf("view missing the log line:\n%s", ansi.Strip(view))
	}
	idx := strings.Index(view, "trailing")
	if idx < 0 {
		t.Fatal("could not locate \"trailing\" in the view")
	}
	lastBG := strings.LastIndex(view[:idx], "\x1b[48;2;")
	if lastBG < 0 {
		t.Fatal("no explicit background SGR found anywhere before \"trailing\"")
	}
	between := view[lastBG:idx]
	if strings.Contains(between, "\x1b[0m") || strings.Contains(between, "\x1b[m") {
		t.Fatalf("an absolute reset appears between the last background SGR and the text following the status code token — it falls through to the terminal default instead of the pane's own background:\n%q", between)
	}
}

func numberedLogLines(count int) []string {
	lines := make([]string, 0, count)
	for i := 1; i <= count; i++ {
		lines = append(lines, fmt.Sprintf("line-%02d", i))
	}
	return lines
}

func (m *Model) drainLogLineForTest(line string) {
	m.logLines = append(m.logLines, line)
	if m.logFollow {
		m.clampLogScroll()
	}
}

func TestStatsViewShowsHeatSparklineAndDeltas(t *testing.T) {
	model := testModelInStatsMode()
	id := domain.ResourceID{Host: "local", ID: "1"}
	model.appendStats(domain.ContainerStats{
		ID:          id,
		Read:        time.Now(),
		CPUPercent:  10,
		MemoryUsage: 300 * 1024 * 1024,
		MemoryLimit: 2 * 1024 * 1024 * 1024,
		NetworkRx:   32 * 1024 * 1024,
		BlockRead:   60 * 1024 * 1024,
		BlockWrite:  10 * 1024 * 1024,
		PIDs:        10,
	})
	stats := domain.ContainerStats{
		ID:          id,
		Read:        time.Now(),
		CPUPercent:  82,
		MemoryUsage: 360 * 1024 * 1024,
		MemoryLimit: 2 * 1024 * 1024 * 1024,
		NetworkRx:   48 * 1024 * 1024,
		BlockRead:   88 * 1024 * 1024,
		BlockWrite:  22 * 1024 * 1024,
		PIDs:        14,
	}
	model.stats = &stats
	model.appendStats(stats)

	rawView := model.View()
	view := ansi.Strip(rawView)
	for _, want := range []string{"▓▓▓▓░", "▂▃", "↗ 72.0%", "↗ +4"} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() missing hybrid stats treatment %q:\n%s", want, view)
		}
	}
	if strings.Count(rawView, "\x1b[") < 8 {
		t.Fatalf("View() missing heat-colored graph styling:\n%s", rawView)
	}
}

func TestStatsViewOmitsSampledNoticeAfterStatsLoad(t *testing.T) {
	model := testModelInStatsMode()
	stats := newFakeProvider().stats["1"]
	model.stats = &stats
	model.appendStats(stats)

	view := ansi.Strip(model.View())
	for _, unwanted := range []string{"Stats sampled", "Stats will refresh", "Loading Docker stats", "Stats unavailable"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("stats view should keep a quiet spacer row instead of %q:\n%s", unwanted, view)
		}
	}
	model.statsLoading = true
	view = ansi.Strip(model.View())
	if strings.Contains(view, "Loading Docker stats") {
		t.Fatalf("stats loading state should not render transient spacer text:\n%s", view)
	}
	model.statsLoading = false
	model.statsErr = errors.New("boom")
	view = ansi.Strip(model.View())
	if strings.Contains(view, "Stats unavailable") {
		t.Fatalf("stats view should keep a quiet spacer row instead of sampled notice:\n%s", view)
	}
	if !strings.Contains(view, "CPU") {
		t.Fatalf("stats view missing stat rows:\n%s", view)
	}
}

func TestStatsHistoryUsesCounterDeltasForCumulativeIO(t *testing.T) {
	model := testModelInStatsMode()
	id := domain.ResourceID{Host: "local", ID: "1"}

	model.appendStats(domain.ContainerStats{
		ID:        id,
		NetworkRx: 100,
		NetworkTx: 50,
		BlockRead: 200,
	})
	model.appendStats(domain.ContainerStats{
		ID:         id,
		NetworkRx:  160,
		NetworkTx:  90,
		BlockRead:  220,
		BlockWrite: 30,
	})
	history := model.statsHistory[id]

	if got := history.NetworkRx; len(got) != 1 || got[0] != 60 {
		t.Fatalf("NetworkRx history = %#v, want one delta sample 60", got)
	}
	if got := history.NetworkTx; len(got) != 1 || got[0] != 40 {
		t.Fatalf("NetworkTx history = %#v, want one delta sample 40", got)
	}
	if got := history.BlockTotal; len(got) != 1 || got[0] != 50 {
		t.Fatalf("BlockTotal history = %#v, want one delta sample 50", got)
	}
}

func TestHeatSparklineStylesEachGlyph(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	graph := statGraph{values: []float64{0, 50, 100}, maxValue: 100, fallbackLevel: 1}

	raw := renderSparkline(renderer, defaultSettings(), graph, lipgloss.Color("#7dcfff"), 10)
	if view := ansi.Strip(raw); view != "▁▇▇" {
		t.Fatalf("renderSparkline() = %q, want ▁▇▇", view)
	}
}

// TestRenderStatRowFillsGapWithThemedBackground guards against a real bug:
// the space between the graph and its suffix value came from tideui's
// RenderRow/alignRow, which pads with bare, unstyled characters baked
// directly into the string — RenderRow's own outer style only paints
// padding it appends itself via Width(), not padding already present in
// the string it's given, so that gap fell through to the raw terminal
// default the moment the meter/sparkline glyphs before it had already
// emitted their own background and reset.
func TestRenderStatRowFillsGapWithThemedBackground(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	graph := statGraph{values: []float64{1, 1}, maxValue: 100, fallbackLevel: 1}

	row := renderStatRow(renderer, defaultSettings(), 60, "CPU", graph, "22.5%", lipgloss.Color("#7dcfff"))

	stripped := ansi.Strip(row)
	if !strings.Contains(stripped, "CPU") || !strings.Contains(stripped, "22.5%") {
		t.Fatalf("row missing expected content: %q", stripped)
	}
	wantGap := lipgloss.NewStyle().Background(whatthedockTheme().Bg).Render(" ")
	if !strings.Contains(row, wantGap) {
		t.Fatalf("row has no themed-background space run; the gap before the suffix may be unstyled:\nrow=%q\nwant substring=%q", row, wantGap)
	}
}

func TestStatHeatColorFollowsSmoothRamp(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	for _, tc := range []struct {
		name  string
		level int
		want  lipgloss.Color
	}{
		{"bottom", 1, "#80c990"},
		{"low mid", 3, "#bbd36f"},
		{"middle", 5, "#e8c170"},
		{"high mid", 7, "#f29a7a"},
		{"top", 8, "#e06c75"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := statHeatColor(defaultSettings(), tc.level, lipgloss.Color("#7dcfff"), renderer); got != tc.want {
				t.Fatalf("statHeatColor(%d) = %q, want %q", tc.level, got, tc.want)
			}
		})
	}
}

func TestStatGlyphColorFollowsSmoothHeightRamp(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	for _, tc := range []struct {
		glyph string
		want  lipgloss.Color
	}{
		{"▁", "#80c990"},
		{"▂", "#a9d576"},
		{"▃", "#d1cd70"},
		{"▄", "#e8c170"},
		{"▅", "#efaa76"},
		{"▆", "#f28d78"},
		{"▇", "#e97876"},
		{"█", "#e06c75"},
	} {
		t.Run(tc.glyph, func(t *testing.T) {
			if got := statGlyphColor(defaultSettings(), tc.glyph, lipgloss.Color("#7dcfff"), renderer); got != tc.want {
				t.Fatalf("statGlyphColor(%q) = %q, want %q", tc.glyph, got, tc.want)
			}
		})
	}
}

func TestStatsRowFallsBackToValueAtTinyWidth(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	row := renderStatRow(renderer, defaultSettings(), 18, "CPU", statGraph{values: []float64{10, 80}, maxValue: 100, fallbackLevel: 5}, "80.0%", lipgloss.Color("#7dcfff"))
	view := ansi.Strip(row)

	if !strings.Contains(view, "CPU") || !strings.Contains(view, "80.0%") {
		t.Fatalf("tiny stat row missing label/value:\n%s", view)
	}
	if strings.Contains(view, "▓") || strings.Contains(view, "⡀") || strings.Contains(view, "⣷") {
		t.Fatalf("tiny stat row should omit graph treatment:\n%s", view)
	}
}

func TestStatsPollTickReloadsWhileStatsModeActive(t *testing.T) {
	model := testModel()
	model.rows = model.buildRows()
	for i, row := range model.rows {
		if row.container != nil && row.container.ID.ID == "1" {
			model.cursor = i
			model.selectedID = row.container.ID
			break
		}
	}
	model.mode = activityStats
	model.focus = paneActivity

	updated, cmd := model.Update(statsMsg{stats: newFakeProvider().stats["1"]})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("statsMsg returned nil cmd, want next poll tick")
	}

	updated, cmd = model.Update(statsTickMsg{id: domain.ResourceID{Host: "local", ID: "1"}})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("statsTickMsg returned nil cmd, want stats reload")
	}
	if model.statsLoading {
		t.Fatal("statsLoading = true after tick with cached stats, want background refresh without loading state")
	}
}

// TestStatsPollTickDoesNotRearmLoadingWhenStatsKeepFailing guards against
// the bug reported live: a container whose stats fetch keeps failing (so
// m.stats stays nil across every poll) had statsTickMsg set statsLoading
// back to true on every single tick, flipping the "loading stats…" header
// on the poll interval indefinitely — visible as text flashing too fast to
// read rather than a one-time spinner.
func TestStatsPollTickDoesNotRearmLoadingWhenStatsKeepFailing(t *testing.T) {
	model := testModel()
	id := domain.ResourceID{Host: "local", ID: "1"}
	model.selectedID = id
	model.mode = activityStats
	model.focus = paneActivity
	model.statsLoading = false
	model.stats = nil // every fetch so far has failed
	model.statsErr = errors.New("stats unavailable")

	updated, cmd := model.Update(statsTickMsg{id: id})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("statsTickMsg returned nil cmd, want stats reload")
	}
	if model.statsLoading {
		t.Fatal("statsLoading = true after a poll tick with no cached stats, want it to stay false (only the genuine first load should show the spinner)")
	}
}

func TestStatsPollTickStopsOutsideStatsMode(t *testing.T) {
	model := testModel()
	model.selectedID = domain.ResourceID{Host: "local", ID: "1"}
	model.mode = activityLogs

	updated, cmd := model.Update(statsTickMsg{id: domain.ResourceID{Host: "local", ID: "1"}})
	model = updated.(Model)
	if cmd != nil {
		t.Fatalf("statsTickMsg outside stats returned cmd = %#v, want nil", cmd)
	}
	if model.mode != activityLogs {
		t.Fatalf("mode = %v, want logs", model.mode)
	}
}

func TestCommandPaletteCanShowStats(t *testing.T) {
	model := testModel()
	model.rows = model.buildRows()
	for i, row := range model.rows {
		if row.container != nil && row.container.ID.ID == "1" {
			model.cursor = i
			break
		}
	}
	updated, cmd := model.executeCommand("show-stats")
	model = updated.(Model)
	if model.mode != activityStats || model.focus != paneActivity {
		t.Fatalf("mode/focus = %v/%v, want stats/activity", model.mode, model.focus)
	}
	if cmd == nil {
		t.Fatal("show-stats cmd is nil, want stats load")
	}
}

func TestSettingsKeyOpensAndCyclesSettings(t *testing.T) {
	model := testModel()
	// Tall enough that every settings row (including the AI section) is
	// visible without needing to scroll — this test's assertions span the
	// whole list, from Graph style down to Reset defaults.
	model.width, model.height = 100, 45

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{','}})
	model = updated.(Model)
	if cmd != nil {
		t.Fatalf("settings key returned cmd = %#v, want nil", cmd)
	}
	if model.overlay != overlaySettings {
		t.Fatalf("overlay = %v, want settings", model.overlay)
	}
	view := ansi.Strip(model.View())
	for _, want := range []string{"whatthedock · settings", "Stats", "Graph style", "wave", "Logs", "Log color", "full", "Behavior", "Maintenance", "Reset defaults", "ctrl+s save"} {
		if !strings.Contains(view, want) {
			t.Fatalf("settings view missing %q:\n%s", want, view)
		}
	}
	if model.settingsCursor != 1 {
		t.Fatalf("settingsCursor = %d, want first actionable row 1", model.settingsCursor)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.settingsDraft.GraphStyle != graphStyleBlocks {
		t.Fatalf("draft GraphStyle = %v, want blocks", model.settingsDraft.GraphStyle)
	}
	if model.settings.GraphStyle != graphStyleWave {
		t.Fatalf("committed GraphStyle = %v, want unchanged wave", model.settings.GraphStyle)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.settingsDraft.GraphColor != graphColorMetric {
		t.Fatalf("draft GraphColor = %v, want metric", model.settingsDraft.GraphColor)
	}
}

func TestSettingsResetDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	model := testModel()
	model.settingsPath = path
	model.settings.GraphStyle = graphStyleBraille
	model.settings.GraphColor = graphColorMono
	model.settings.LogColor = logColorMono
	model.settings.ShowDeltas = false
	model.settings.StatsRefresh = 5 * time.Second
	model.settings.DefaultActivity = activityStats
	model.openSettingsOverlay()
	rows := model.settingsRows()
	for i, row := range rows {
		if row.label == "Reset defaults" {
			model.settingsCursor = i
		}
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.settingsDraft != defaultSettings() {
		t.Fatalf("settings draft = %#v, want defaults %#v", model.settingsDraft, defaultSettings())
	}
	if model.settings == defaultSettings() {
		t.Fatalf("committed settings reset before ctrl+s: %#v", model.settings)
	}
	if model.status != "settings reset staged" || model.statusErr {
		t.Fatalf("status/statusErr = %q/%v, want staged reset status", model.status, model.statusErr)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)
	if model.settings != defaultSettings() {
		t.Fatalf("settings = %#v, want defaults after ctrl+s", model.settings)
	}
	saved, err := config.LoadSettings(path)
	if err != nil {
		t.Fatalf("LoadSettings() err = %v", err)
	}
	if saved.GraphStyle != "wave" || saved.GraphColor != "gradient" || saved.LogColor != "full" || saved.StatsRefresh != "2s" || saved.DefaultActivity != "problems" {
		t.Fatalf("saved settings = %#v, want defaults", saved)
	}
	if model.status != "settings saved" || model.statusErr {
		t.Fatalf("status/statusErr = %q/%v, want saved status", model.status, model.statusErr)
	}
}

func TestSettingsEscCancelsDraft(t *testing.T) {
	model := testModel()
	model.openSettingsOverlay()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.settingsDraft.GraphStyle != graphStyleBlocks {
		t.Fatalf("draft GraphStyle = %v, want blocks", model.settingsDraft.GraphStyle)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.overlay != overlayNone {
		t.Fatalf("overlay = %v, want none", model.overlay)
	}
	if model.settings.GraphStyle != graphStyleWave || model.settingsDraft.GraphStyle != graphStyleWave {
		t.Fatalf("settings/draft GraphStyle = %v/%v, want canceled wave", model.settings.GraphStyle, model.settingsDraft.GraphStyle)
	}
}

func TestSettingsLoadPersistedValues(t *testing.T) {
	showDeltas := false
	model := NewModelWithSettings(newFakeProvider(), config.Settings{
		GraphStyle:      "braille",
		GraphColor:      "mono",
		LogColor:        "http",
		ShowDeltas:      &showDeltas,
		StatsRefresh:    "5s",
		DefaultActivity: "stats",
	}, "")

	if model.settings.GraphStyle != graphStyleBraille ||
		model.settings.GraphColor != graphColorMono ||
		model.settings.LogColor != logColorHTTP ||
		model.settings.ShowDeltas ||
		model.settings.StatsRefresh != 5*time.Second ||
		model.mode != activityStats {
		t.Fatalf("settings = %#v mode=%v, want persisted values", model.settings, model.mode)
	}
}

// TestNewModelStartsInDashboardWhenSettingEnabled checks Settings > Start
// in dashboard: enabling it must open the Dashboard overlay from the very
// first frame, not just after pressing "d".
func TestNewModelStartsInDashboardWhenSettingEnabled(t *testing.T) {
	enabled := true
	model := NewModelWithSettings(newFakeProvider(), config.Settings{StartInDashboard: &enabled}, "")
	if !model.settings.StartInDashboard {
		t.Fatal("settings.StartInDashboard = false, want true")
	}
	if model.overlay != overlayDashboard {
		t.Fatalf("overlay = %v, want overlayDashboard", model.overlay)
	}
}

// TestNewModelDoesNotStartInDashboardByDefault checks the setting is
// opt-in — an empty/default config must preserve the existing startup
// behavior (no overlay open) rather than surprising an existing user.
func TestNewModelDoesNotStartInDashboardByDefault(t *testing.T) {
	model := NewModelWithSettings(newFakeProvider(), config.Settings{}, "")
	if model.settings.StartInDashboard {
		t.Fatal("settings.StartInDashboard = true, want false by default")
	}
	if model.overlay != overlayNone {
		t.Fatalf("overlay = %v, want overlayNone", model.overlay)
	}
}

// TestInitStartsDashboardStatsPollWhenStartingInDashboard checks that
// opening the Dashboard overlay from Start in dashboard also kicks off
// its stats-polling loop immediately (dashboardRefreshCmd, the same
// command the 'd' key dispatches via openDashboardOverlay) rather than
// showing an empty dashboard until the next unrelated tick.
func TestInitStartsDashboardStatsPollWhenStartingInDashboard(t *testing.T) {
	enabled := true
	model := NewModelWithSettings(newFakeProvider(), config.Settings{StartInDashboard: &enabled}, "")
	model.snapshot = model.provider.(*fakeProvider).snapshot
	model.settings.StatsRefresh = time.Millisecond // keep the re-arm tick's own timer out of this test's runtime

	if !collectMsgTypes(t, model.Init())[dashboardTickMsg{}] {
		t.Fatal("Init()'s batch is missing dashboardRefreshCmd's re-arm tick despite starting in dashboard mode")
	}
}

// TestInitSkipsDashboardStatsPollByDefault is
// TestInitStartsDashboardStatsPollWhenStartingInDashboard's counterpart:
// with the setting off, Init() must not start the Dashboard's polling
// loop for a screen nobody's looking at.
func TestInitSkipsDashboardStatsPollByDefault(t *testing.T) {
	model := testModel()
	if collectMsgTypes(t, model.Init())[dashboardTickMsg{}] {
		t.Fatal("Init()'s batch contains dashboardRefreshCmd's re-arm tick despite not starting in dashboard mode")
	}
}

// collectMsgTypes runs cmd and, recursively for any tea.BatchMsg it
// produces (tea.Batch's own sub-commands are only reachable by invoking
// each of them in turn — dashboardRefreshCmd itself returns tea.Batch(...),
// so a batch built from it nests one level deep inside Init()'s own outer
// batch), returns which dashboardTickMsg{}-shaped messages were seen.
// Keyed by the zero value since dashboardTickMsg carries no fields —
// callers only care whether one showed up at all.
func collectMsgTypes(t *testing.T, cmd tea.Cmd) map[dashboardTickMsg]bool {
	t.Helper()
	seen := map[dashboardTickMsg]bool{}
	var walk func(tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			for _, sub := range msg {
				walk(sub)
			}
		case dashboardTickMsg:
			seen[msg] = true
		}
	}
	walk(cmd)
	return seen
}

// TestSettingsStartInDashboardTogglePersists mirrors
// TestSettingsModalShadowTogglePersistsAndAppliesToView's shape for the
// new Start in dashboard row: toggling it in Settings and saving must
// persist to disk.
func TestSettingsStartInDashboardTogglePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	model := testModel()
	model.settingsPath = path
	if model.settings.StartInDashboard {
		t.Fatal("StartInDashboard should default to false")
	}

	model.openSettingsOverlay()
	rows := model.settingsRows()
	cursor := -1
	for i, row := range rows {
		if row.label == "Start in dashboard" {
			cursor = i
			break
		}
	}
	if cursor == -1 {
		t.Fatal("settings rows missing \"Start in dashboard\"")
	}
	model.settingsCursor = cursor

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if !model.settingsDraft.StartInDashboard {
		t.Fatal("settingsDraft.StartInDashboard = false after toggle, want true")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)
	if !model.settings.StartInDashboard {
		t.Fatal("settings.StartInDashboard = false after save, want true")
	}

	saved, err := config.LoadSettings(path)
	if err != nil {
		t.Fatalf("LoadSettings() err = %v", err)
	}
	if saved.StartInDashboard == nil || !*saved.StartInDashboard {
		t.Fatalf("persisted StartInDashboard = %v, want true", saved.StartInDashboard)
	}
}

// TestNewModelLoadsPersistedTheme is the regression test for themes not
// persisting across restarts: NewModelWithProviderFactory always seeded
// m.theme from whatthedockTheme() regardless of what was saved, so a
// confirmed theme picker choice was silently dropped on the next launch.
func TestNewModelLoadsPersistedTheme(t *testing.T) {
	model := NewModelWithSettings(newFakeProvider(), config.Settings{Theme: "nord"}, "")
	if model.theme.Name != "nord" {
		t.Fatalf("model.theme.Name = %q, want nord", model.theme.Name)
	}
	if got := model.themes.ConfirmedTheme().Name; got != "nord" {
		t.Fatalf("themes.ConfirmedTheme().Name = %q, want nord", got)
	}
}

func TestNewModelDefaultThemeWhenNothingPersisted(t *testing.T) {
	model := NewModelWithSettings(newFakeProvider(), config.Settings{}, "")
	if model.theme.Name != "whatthedock" {
		t.Fatalf("model.theme.Name = %q, want whatthedock default", model.theme.Name)
	}
}

// TestThemePickerConfirmSavesTheme is the other half of the persistence
// regression: confirming a theme in the picker must write it to disk
// immediately (mirroring saveSystemDraft's convention), not just update
// m.theme in memory and wait for a Settings-overlay ctrl+s that theme
// picking never goes through.
func TestThemePickerConfirmSavesTheme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	model := testModel()
	model.settingsPath = path
	model.openThemePicker()
	if model.overlay != overlayThemePicker {
		t.Fatalf("overlay = %v, want theme picker", model.overlay)
	}
	startName := model.theme.Name

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	wantName := model.themes.PreviewTheme().Name
	if wantName == startName {
		t.Fatalf("preview theme = %q after moving down, want it to differ from the starting theme %q", wantName, startName)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.overlay != overlayNone {
		t.Fatalf("overlay = %v, want none after confirm", model.overlay)
	}
	if model.theme.Name != wantName {
		t.Fatalf("model.theme.Name = %q, want %q", model.theme.Name, wantName)
	}
	if model.status != "theme: "+wantName || model.statusErr {
		t.Fatalf("status/statusErr = %q/%v, want \"theme: %s\"/false", model.status, model.statusErr, wantName)
	}

	saved, err := config.LoadSettings(path)
	if err != nil {
		t.Fatalf("LoadSettings() err = %v", err)
	}
	if saved.Theme != wantName {
		t.Fatalf("saved.Theme = %q, want %q", saved.Theme, wantName)
	}
}

func TestSettingsSaveAfterChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	model := testModel()
	model.settingsPath = path
	model.systems = []config.System{{ID: "jarvis", Name: "Jarvis", Kind: "ssh", SSHHost: "allie@jarvis"}}
	model.activeSystem = "jarvis"
	model.openSettingsOverlay()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("settings file exists before ctrl+s: err=%v", err)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)
	saved, err := config.LoadSettings(path)
	if err != nil {
		t.Fatalf("LoadSettings() err = %v", err)
	}
	if saved.GraphStyle != "blocks" {
		t.Fatalf("saved GraphStyle = %q, want blocks", saved.GraphStyle)
	}
	if saved.ActiveSystem != "jarvis" || len(saved.Systems) != 1 || saved.Systems[0].ID != "jarvis" {
		t.Fatalf("saved systems = active:%q systems:%#v, want jarvis preserved", saved.ActiveSystem, saved.Systems)
	}
	if model.status != "settings saved" || model.statusErr {
		t.Fatalf("status/statusErr = %q/%v, want settings saved/false", model.status, model.statusErr)
	}
}

func TestSettingsSaveErrorShowsStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("Mkdir() err = %v", err)
	}
	model := testModel()
	model.settingsPath = path
	model.openSettingsOverlay()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)
	if !model.statusErr || !strings.Contains(model.status, "settings:") {
		t.Fatalf("status/statusErr = %q/%v, want settings save error", model.status, model.statusErr)
	}
}

func TestSettingsVimModeTogglePersistsAndAppliesToEditors(t *testing.T) {
	t.Cleanup(func() { setEditorVimMode(false) })
	path := filepath.Join(t.TempDir(), "settings.json")
	model := testModel()
	model.settingsPath = path
	model.openSettingsOverlay()

	rows := model.settingsRows()
	cursor := -1
	for i, row := range rows {
		if row.label == "Vim mode" {
			cursor = i
			break
		}
	}
	if cursor == -1 {
		t.Fatal("settings rows missing Vim mode")
	}
	model.settingsCursor = cursor

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if !model.settingsDraft.CreateVim {
		t.Fatal("settingsDraft.CreateVim = false after toggle, want true")
	}
	if editorVimMode {
		t.Fatal("editorVimMode applied before save, want unchanged until ctrl+s")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)
	if !model.settings.CreateVim {
		t.Fatal("settings.CreateVim = false after save, want true")
	}
	if !editorVimMode {
		t.Fatal("editorVimMode = false after save, want true")
	}
	if !newEditorArea().vimMode() {
		t.Fatal("editor created after save is not in vim mode")
	}

	saved, err := config.LoadSettings(path)
	if err != nil {
		t.Fatalf("LoadSettings() err = %v", err)
	}
	if saved.CreateVim == nil || !*saved.CreateVim {
		t.Fatalf("persisted CreateVim = %v, want true", saved.CreateVim)
	}
}

func TestSettingsModalShadowTogglePersistsAndAppliesToView(t *testing.T) {
	original := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(original) })

	path := filepath.Join(t.TempDir(), "settings.json")
	model := testModel()
	model.width, model.height = 100, 30
	model.settingsPath = path
	if !model.settings.ModalShadow {
		t.Fatal("ModalShadow should default to true")
	}

	model.overlay = overlayHelp
	viewWithShadow := model.View()

	model.openSettingsOverlay()

	rows := model.settingsRows()
	cursor := -1
	for i, row := range rows {
		if row.label == "Modal shadow" {
			cursor = i
			break
		}
	}
	if cursor == -1 {
		t.Fatal("settings rows missing Modal shadow")
	}
	model.settingsCursor = cursor

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.settingsDraft.ModalShadow {
		t.Fatal("settingsDraft.ModalShadow = true after toggle, want false")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)
	if model.settings.ModalShadow {
		t.Fatal("settings.ModalShadow = true after save, want false")
	}

	saved, err := config.LoadSettings(path)
	if err != nil {
		t.Fatalf("LoadSettings() err = %v", err)
	}
	if saved.ModalShadow == nil || *saved.ModalShadow {
		t.Fatalf("persisted ModalShadow = %v, want false", saved.ModalShadow)
	}

	model.overlay = overlayHelp
	if model.View() == viewWithShadow {
		t.Fatal("expected help overlay to render differently after ModalShadow=false")
	}
}

func TestCommandPaletteCanOpenSettings(t *testing.T) {
	model := testModel()
	updated, cmd := model.executeCommand("open-settings")
	model = updated.(Model)
	if cmd != nil {
		t.Fatalf("open-settings cmd = %#v, want nil", cmd)
	}
	if model.overlay != overlaySettings {
		t.Fatalf("overlay = %v, want settings", model.overlay)
	}
}

func TestSystemsOverlayListsAndEditsSystems(t *testing.T) {
	model := testModel()
	model.width, model.height = 120, 30
	model.systems = []config.System{
		{ID: "local", Name: "local", Kind: "local"},
		{ID: "jarvis", Name: "Jarvis", Kind: "ssh", SSHHost: "jarvis", SSHUser: "allie", RemoteSocket: "/var/run/docker.sock", LocalSocket: "/tmp/jarvis.sock"},
	}
	model.activeSystem = "local"

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	model = updated.(Model)
	if model.overlay != overlaySystems || model.systemMode != systemModeList {
		t.Fatalf("overlay/mode = %v/%v, want systems/list", model.overlay, model.systemMode)
	}
	view := ansi.Strip(model.View())
	for _, want := range []string{"whatthedock · systems", "* local", "Jarvis", "a add", "e edit"} {
		if !strings.Contains(view, want) {
			t.Fatalf("systems overlay missing %q:\n%s", want, view)
		}
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	model = updated.(Model)
	if model.systemMode != systemModeEdit || !model.systemDraftNew {
		t.Fatalf("systemMode/new = %v/%v, want edit/new", model.systemMode, model.systemDraftNew)
	}
	view = ansi.Strip(model.View())
	for _, want := range []string{"whatthedock · add system", "Name", "Kind", "Host", "User", "Port", "Auth", "config/agent", "Remote socket", "Local socket"} {
		if !strings.Contains(view, want) {
			t.Fatalf("add system overlay missing %q:\n%s", want, view)
		}
	}
}

func TestSystemsOverlayCanSelectSSHPasswordPrompt(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 30
	model.openSystemsOverlay()
	model.startAddSystem()
	model.systemField = systemFieldSSHAuth

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)
	if model.systemDraft.SSHAuth != "password" {
		t.Fatalf("SSHAuth = %q, want password", model.systemDraft.SSHAuth)
	}

	view := ansi.Strip(model.View())
	if !strings.Contains(view, "password prompt") {
		t.Fatalf("systems overlay missing password prompt auth:\n%s", view)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = updated.(Model)
	if model.systemDraft.SSHAuth != "config" {
		t.Fatalf("SSHAuth = %q, want config", model.systemDraft.SSHAuth)
	}
}

func TestSystemsOverlayCanTypeUsernameWithNavigationLetters(t *testing.T) {
	model := testModel()
	model.openSystemsOverlay()
	model.startAddSystem()
	model.systemField = systemFieldSSHUser

	for _, char := range "allie" {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{char}})
		model = updated.(Model)
	}

	if model.systemDraft.SSHUser != "allie" {
		t.Fatalf("SSHUser = %q, want allie", model.systemDraft.SSHUser)
	}
}

func TestSystemsOverlayTextEditingUsesCaretAndClear(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 30
	model.openSystemsOverlay()
	model.startAddSystem()
	model.systemField = systemFieldSSHUser

	for _, char := range "alie" {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{char}})
		model = updated.(Model)
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	model = updated.(Model)

	if model.systemDraft.SSHUser != "allie" {
		t.Fatalf("SSHUser = %q, want allie after caret insert", model.systemDraft.SSHUser)
	}
	if model.systemCursor != 3 {
		t.Fatalf("systemCursor = %d, want 3", model.systemCursor)
	}
	view := ansi.Strip(model.View())
	if !strings.Contains(view, "all|ie") {
		t.Fatalf("systems overlay missing visible caret:\n%s", view)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	model = updated.(Model)
	if model.systemDraft.SSHUser != "" || model.systemCursor != 0 {
		t.Fatalf("field/cursor = %q/%d, want cleared", model.systemDraft.SSHUser, model.systemCursor)
	}
}

func TestSystemsOverlayBackspaceAndDeleteEditAroundCaret(t *testing.T) {
	model := testModel()
	model.openSystemsOverlay()
	model.startAddSystem()
	model.systemField = systemFieldSSHHost
	for _, char := range "jxrvis" {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{char}})
		model = updated.(Model)
	}
	for i := 0; i < 5; i++ {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyLeft})
		model = updated.(Model)
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDelete})
	model = updated.(Model)
	if model.systemDraft.SSHHost != "jrvis" {
		t.Fatalf("SSHHost = %q, want jrvis after delete", model.systemDraft.SSHHost)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	model = updated.(Model)
	if model.systemDraft.SSHHost != "rvis" {
		t.Fatalf("SSHHost = %q, want rvis after backspace", model.systemDraft.SSHHost)
	}
}

func TestSystemsOverlayValidationBlocksInvalidSSHSystem(t *testing.T) {
	model := testModel()
	model.openSystemsOverlay()
	model.startAddSystem()
	model.systemDraft.SSHHost = ""

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.statusErr {
		t.Fatalf("enter on text field set error status: %q", model.status)
	}
	if model.systemField != systemFieldKind {
		t.Fatalf("systemField = %v, want next field kind after enter", model.systemField)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)
	if !model.statusErr || !strings.Contains(model.status, "ssh host is required") {
		t.Fatalf("status/statusErr = %q/%v, want ssh host validation", model.status, model.statusErr)
	}
	if model.systemMode != systemModeEdit {
		t.Fatalf("systemMode = %v, want edit after validation failure", model.systemMode)
	}

	model.systemDraft.SSHHost = "jarvis"
	model.systemDraft.SSHPort = "abc"
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)
	if !model.statusErr || !strings.Contains(model.status, "ssh port must be 1-65535") {
		t.Fatalf("status/statusErr = %q/%v, want ssh port validation", model.status, model.statusErr)
	}
}

func TestSystemsOverlaySavesDraftWithCtrlS(t *testing.T) {
	model := testModel()
	model.openSystemsOverlay()
	model.startAddSystem()
	model.systemDraft.SSHHost = "jarvis"
	model.systemDraft.SSHUser = "allie"

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)

	if model.systemMode != systemModeList {
		t.Fatalf("systemMode = %v, want list after ctrl+s", model.systemMode)
	}
	if len(model.systems) != 2 || model.systems[1].SSHHost != "jarvis" || model.systems[1].SSHUser != "allie" {
		t.Fatalf("systems = %#v, want saved ssh system", model.systems)
	}
}

func TestSystemsOverlayTestsProvider(t *testing.T) {
	model := testModel()
	model.systems = []config.System{
		{ID: "local", Name: "local", Kind: "local"},
		{ID: "jarvis", Name: "Jarvis", Kind: "ssh", SSHHost: "jarvis", SSHUser: "allie", RemoteSocket: "/var/run/docker.sock", LocalSocket: "/tmp/jarvis.sock"},
	}
	model.activeSystem = "local"
	model.providerFor = func(_ context.Context, system config.System) (app.Provider, error) {
		provider := newFakeProvider()
		provider.host = domain.Host{ID: domain.HostID(system.ID), Name: system.Name}
		return provider, nil
	}
	model.overlay = overlaySystems
	model.systemsCursor = 1

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("cmd = nil, want test command")
	}
	if model.status != "testing system Jarvis" || model.statusErr {
		t.Fatalf("status/statusErr = %q/%v, want testing status", model.status, model.statusErr)
	}
	msg := runCmd(t, cmd)
	updated, _ = model.Update(msg)
	model = updated.(Model)
	if model.status != "test Jarvis: connected" || model.statusErr {
		t.Fatalf("status/statusErr = %q/%v, want connected", model.status, model.statusErr)
	}
}

func TestSystemsOverlayTestReportsPingError(t *testing.T) {
	model := testModel()
	model.systems = []config.System{{ID: "local", Name: "local", Kind: "local"}}
	model.activeSystem = "local"
	model.providerFor = func(context.Context, config.System) (app.Provider, error) {
		provider := newFakeProvider()
		provider.pingErr = errors.New("no route")
		return provider, nil
	}
	model.overlay = overlaySystems

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	model = updated.(Model)
	msg := runCmd(t, cmd)
	updated, _ = model.Update(msg)
	model = updated.(Model)
	if !model.statusErr || !strings.Contains(model.status, "no route") {
		t.Fatalf("status/statusErr = %q/%v, want ping error", model.status, model.statusErr)
	}
}

func TestSystemsOverlaySwitchesProvider(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.width, model.height = 120, 30
	model.systems = []config.System{
		{ID: "local", Name: "local", Kind: "local"},
		{ID: "jarvis", Name: "Jarvis", Kind: "ssh", SSHHost: "allie@jarvis", RemoteSocket: "/var/run/docker.sock", LocalSocket: "/tmp/jarvis.sock"},
	}
	model.activeSystem = "local"
	var requested config.System
	model.providerFor = func(_ context.Context, system config.System) (app.Provider, error) {
		requested = system
		provider := newFakeProvider()
		provider.host = domain.Host{ID: domain.HostID(system.ID), Name: system.Name}
		return provider, nil
	}
	model.overlay = overlaySystems
	model.systemsCursor = 1

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("cmd = nil, want provider switch command")
	}
	msg := runCmd(t, cmd)
	updated, cmd = model.Update(msg)
	model = updated.(Model)
	if requested.ID != "jarvis" || model.activeSystem != "jarvis" {
		t.Fatalf("requested/active = %#v/%q, want jarvis", requested, model.activeSystem)
	}
	if model.selected != nil || model.selectedID.ID != "" || !model.loading {
		t.Fatalf("state after switch selected=%#v id=%#v loading=%v, want reset/loading", model.selected, model.selectedID, model.loading)
	}
	if cmd == nil {
		t.Fatal("switch update returned nil cmd, want refresh/events batch")
	}
}

func TestSystemsOverlayDoesNotDeleteActiveSystem(t *testing.T) {
	model := testModel()
	model.systems = []config.System{{ID: "local", Name: "local", Kind: "local"}, {ID: "jarvis", Name: "Jarvis", Kind: "ssh"}}
	model.activeSystem = "local"
	model.overlay = overlaySystems
	model.systemsCursor = 0

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(Model)
	if model.systemMode == systemModeDelete {
		t.Fatal("active system entered delete confirmation, want deletion blocked")
	}
}

func TestCopyKeyOpensCopyOverlay(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.width, model.height = 100, 30

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	model = updated.(Model)
	if cmd != nil {
		t.Fatalf("copy key returned cmd = %#v, want nil", cmd)
	}
	if model.overlay != overlayCopy {
		t.Fatalf("overlay = %v, want copy", model.overlay)
	}
	view := ansi.Strip(model.View())
	for _, want := range []string{"whatthedock · copy", "Container ID", "Image", "Port", "Mount", "Label com.docker.compose.project"} {
		if !strings.Contains(view, want) {
			t.Fatalf("copy overlay missing %q:\n%s", want, view)
		}
	}
}

// modelSelectingStandalone builds a model with a selected container that has
// no Compose project — the standalone-path fixture for Delete/Replicate
// tests, alongside modelSelecting (create_test.go) for the Compose path.
func modelSelectingStandalone(name, image string) Model {
	model := modelSelecting("", "", "")
	model.selected.Name = name
	model.selected.Image = image
	model.selectedID = model.selected.ID
	return model
}

func TestPressDOpensDeleteOverlay(t *testing.T) {
	model := testModelWithSelectedContainer()
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	model = updated.(Model)
	if cmd != nil {
		t.Fatalf("D key returned cmd = %#v, want nil (opens a confirmation, doesn't act yet)", cmd)
	}
	if model.overlay != overlayDelete {
		t.Fatalf("overlay = %v, want overlayDelete", model.overlay)
	}
}

func TestPressUOpensReplicateOverlay(t *testing.T) {
	model := testModelWithSelectedContainer()
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	model = updated.(Model)
	if cmd != nil {
		t.Fatalf("u key returned cmd = %#v, want nil (opens a confirmation, doesn't act yet)", cmd)
	}
	if model.overlay != overlayReplicate {
		t.Fatalf("overlay = %v, want overlayReplicate", model.overlay)
	}
}

func TestPressCOpensCloneOverlayPrefilled(t *testing.T) {
	model := testModelWithSelectedContainer() // radarr-1, Compose service "radarr"
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'C'}})
	model = updated.(Model)
	if model.overlay != overlayCreate {
		t.Fatalf("overlay = %v, want overlayCreate (Clone reuses the create overlay)", model.overlay)
	}
	if model.createDraft.Service != "radarr-clone" {
		t.Fatalf("Service = %q, want radarr-clone", model.createDraft.Service)
	}
}

func TestPressMOpensEditOverlayComposePrefilled(t *testing.T) {
	model := testModelWithSelectedContainer() // radarr-1, Compose service "radarr"
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	model = updated.(Model)
	if model.overlay != overlayCreate {
		t.Fatalf("overlay = %v, want overlayCreate (edit reuses the create overlay)", model.overlay)
	}
	if !model.createDraft.Editing {
		t.Fatal("createDraft.Editing = false, want true")
	}
	if model.createDraft.Service != "radarr" {
		t.Fatalf("Service = %q, want radarr (edit keeps the real identity, unlike Clone's -clone suffix)", model.createDraft.Service)
	}
}

func TestPressMOpensEditOverlayStandaloneFullShapePrefilled(t *testing.T) {
	model := modelSelectingStandalone("grafana", "grafana/grafana:latest")
	model.selected.Ports = []domain.Port{{IP: "0.0.0.0", Private: 3000, Public: 3000, Type: "tcp"}}
	model.selected.Env = []string{"FOO=bar"}
	model.selected.RestartPolicy = "always"
	wantID := model.selected.ID

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	model = updated.(Model)
	if model.overlay != overlayCreate {
		t.Fatalf("overlay = %v, want overlayCreate", model.overlay)
	}
	if !model.createDraft.Editing {
		t.Fatal("createDraft.Editing = false, want true")
	}
	if model.createDraft.EditingID != wantID {
		t.Fatalf("EditingID = %v, want %v", model.createDraft.EditingID, wantID)
	}
	if model.createDraft.ContainerName != "grafana" {
		t.Fatalf("ContainerName = %q, want grafana (edit keeps the real name, unlike Clone's -clone suffix)", model.createDraft.ContainerName)
	}
	if model.createDraft.Restart != "always" {
		t.Fatalf("Restart = %q, want always (full shape should carry over like Clone)", model.createDraft.Restart)
	}
	if !strings.Contains(model.createDraft.Ports, "3000") || !strings.Contains(model.createDraft.Env, "FOO=bar") {
		t.Fatalf("Ports/Env = %q/%q, want the selected container's real ports/env carried over", model.createDraft.Ports, model.createDraft.Env)
	}
}

func TestPressMDoesNothingWithNoSelection(t *testing.T) {
	model := testModel()
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	model = updated.(Model)
	if cmd != nil {
		t.Fatalf("m with no selection returned cmd = %#v, want nil", cmd)
	}
	if model.overlay != overlayNone {
		t.Fatalf("overlay = %v, want overlayNone", model.overlay)
	}
}

func TestConfirmEditStandaloneReplacesContainerInPlace(t *testing.T) {
	model := modelSelectingStandalone("grafana", "grafana/grafana:latest")
	model.selected.RestartPolicy = "always"
	oldID := model.selected.ID

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	model = updated.(Model)
	model.createDraft.Restart = "unless-stopped" // the actual edit
	model.createDraft.Confirming = true

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	if !model.busy {
		t.Fatal("busy = false right after confirming edit, want true")
	}
	if cmd == nil {
		t.Fatal("confirming edit returned a nil Cmd")
	}
	msg, ok := runCmd(t, cmd).(createDoneMsg)
	if !ok {
		t.Fatalf("msg = %#v, want createDoneMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("createDoneMsg.err = %v, want nil", msg.err)
	}
	if !msg.edited {
		t.Fatal("createDoneMsg.edited = false, want true")
	}

	updated, _ = model.Update(msg)
	model = updated.(Model)
	if !strings.Contains(model.status, "updated") {
		t.Fatalf("status = %q, want it to say updated (not created)", model.status)
	}

	fp := model.provider.(*fakeProvider)
	if len(fp.removed) != 1 || fp.removed[0] != oldID {
		t.Fatalf("removed = %#v, want a single call for %v", fp.removed, oldID)
	}
	if len(fp.creates) != 1 || fp.creates[0].Name != "grafana" || fp.creates[0].RestartPolicy != "unless-stopped" {
		t.Fatalf("creates = %#v, want one create for grafana with the edited restart policy", fp.creates)
	}
}

func TestHandleDeleteKeyStandaloneCallsRemoveContainer(t *testing.T) {
	model := modelSelectingStandalone("grafana", "grafana/grafana")
	model.overlay = overlayDelete

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	if model.overlay != overlayNone {
		t.Fatalf("overlay = %v, want overlayNone after confirming delete", model.overlay)
	}
	if cmd == nil {
		t.Fatal("y on overlayDelete returned a nil Cmd")
	}
	done, ok := runCmd(t, cmd).(actionDoneMsg)
	if !ok {
		t.Fatalf("msg = %#v, want actionDoneMsg", done)
	}
	if done.err != nil {
		t.Fatalf("actionDoneMsg.err = %v, want nil", done.err)
	}
	fp := model.provider.(*fakeProvider)
	if len(fp.removed) != 1 || fp.removed[0] != model.selected.ID {
		t.Fatalf("removed = %#v, want a single call for %v", fp.removed, model.selected.ID)
	}
	if len(fp.forced) != 1 || !fp.forced[0] {
		t.Fatalf("forced = %#v, want [true]", fp.forced)
	}
}

func TestHandleReplicateKeyStandaloneCallsPullRemoveCreateInOrder(t *testing.T) {
	model := modelSelectingStandalone("grafana", "grafana/grafana:latest")
	model.selected.Ports = []domain.Port{{IP: "0.0.0.0", Private: 3000, Public: 3000, Type: "tcp"}}
	model.overlay = overlayReplicate

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("y on overlayReplicate returned a nil Cmd")
	}
	if !model.busy {
		t.Fatal("busy = false right after dispatching replicate, want true")
	}
	done, ok := runCmd(t, cmd).(actionDoneMsg)
	if !ok {
		t.Fatalf("msg = %#v, want actionDoneMsg", done)
	}
	if done.err != nil {
		t.Fatalf("actionDoneMsg.err = %v, want nil", done.err)
	}
	model.drainReplicateProgress()
	if !strings.Contains(model.status, "grafana/grafana:latest") {
		t.Fatalf("status after draining progress = %q, want it to reflect the pull progress", model.status)
	}
	fp := model.provider.(*fakeProvider)
	if len(fp.pulled) != 1 || fp.pulled[0] != "grafana/grafana:latest" {
		t.Fatalf("pulled = %#v, want a single pull of the original image", fp.pulled)
	}
	if len(fp.removed) != 1 || fp.removed[0] != model.selected.ID {
		t.Fatalf("removed = %#v, want a single call for %v", fp.removed, model.selected.ID)
	}
	if len(fp.creates) != 1 {
		t.Fatalf("creates = %#v, want a single recreate call", fp.creates)
	}
	created := fp.creates[0]
	if created.Name != "grafana" || created.Image != "grafana/grafana:latest" {
		t.Fatalf("recreated spec = %#v, want the same identity as the original, not a -clone name", created)
	}
	if len(created.Ports) != 1 || created.Ports[0].ContainerPort != 3000 {
		t.Fatalf("recreated ports = %#v, want the original 3000 binding carried over", created.Ports)
	}
}

func TestBusySpinnerClearsOnActionDone(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.overlay = overlayDelete

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	if !model.busy {
		t.Fatal("busy = false right after dispatching delete, want true")
	}
	if cmd == nil {
		t.Fatal("y on overlayDelete returned a nil Cmd")
	}
	msg := runCmd(t, cmd)

	updated, _ = model.Update(msg)
	model = updated.(Model)
	if model.busy {
		t.Fatal("busy = true after actionDoneMsg, want false")
	}
}

func TestEventStreamReconnectSetsIndicator(t *testing.T) {
	model := testModel()

	updated, cmd := model.Update(eventStreamClosedMsg{})
	model = updated.(Model)
	if !model.eventsReconnecting {
		t.Fatal("eventsReconnecting = false after eventStreamClosedMsg, want true")
	}
	if cmd == nil {
		t.Fatal("eventStreamClosedMsg returned a nil Cmd, want the reconnect scheduled")
	}

	updated, _ = model.Update(eventsStartedMsg{events: make(chan domain.ContainerEvent)})
	model = updated.(Model)
	if model.eventsReconnecting {
		t.Fatal("eventsReconnecting = true after a successful eventsStartedMsg, want false")
	}
}

func TestHandleDeleteKeyEscCancels(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.overlay = overlayDelete

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.overlay != overlayNone {
		t.Fatalf("overlay = %v, want overlayNone after esc", model.overlay)
	}
	if cmd != nil {
		t.Fatal("esc on overlayDelete returned a non-nil Cmd, want no action taken")
	}
}

func TestHandleReplicateKeyEscCancels(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.overlay = overlayReplicate

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.overlay != overlayNone {
		t.Fatalf("overlay = %v, want overlayNone after esc", model.overlay)
	}
	if cmd != nil {
		t.Fatal("esc on overlayReplicate returned a non-nil Cmd, want no action taken")
	}
}

func TestExecShellCommandUsesDockerHostForSSHSystem(t *testing.T) {
	system := config.System{Kind: "ssh", LocalSocket: "/tmp/wtd-jarvis.sock"}
	id := domain.ResourceID{Host: "jarvis", ID: "abc123"}

	cmd := execShellCommand(system, id)

	wantArgs := []string{"docker", "exec", "-it", "abc123", "sh", "-c", "[ -x /bin/bash ] && exec bash || exec sh"}
	if len(cmd.Args) != len(wantArgs) {
		t.Fatalf("Args = %#v, want %#v", cmd.Args, wantArgs)
	}
	for i, want := range wantArgs {
		if cmd.Args[i] != want {
			t.Fatalf("Args[%d] = %q, want %q", i, cmd.Args[i], want)
		}
	}
	found := false
	for _, kv := range cmd.Env {
		if kv == "DOCKER_HOST=unix:///tmp/wtd-jarvis.sock" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Env = %#v, want DOCKER_HOST pointing at the SSH tunnel socket", cmd.Env)
	}
}

func TestExecShellCommandLeavesEnvUntouchedForDefaultLocalSystem(t *testing.T) {
	system := config.System{Kind: "local"}
	id := domain.ResourceID{Host: "local", ID: "abc123"}

	cmd := execShellCommand(system, id)

	if cmd.Env != nil {
		t.Fatalf("Env = %#v, want nil (inherit the parent process env, no DOCKER_HOST override)", cmd.Env)
	}
}

func TestPressEOpensShellForRunningContainer(t *testing.T) {
	model := testModelWithSelectedContainer() // radarr-1 is StateRunning in the fixture
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if cmd == nil {
		t.Fatal("e on a running selected container returned a nil Cmd, want the exec handoff")
	}
}

func TestPressEDoesNothingForStoppedContainer(t *testing.T) {
	model := modelSelectingStandalone("grafana", "grafana/grafana")
	model.selected.State = domain.StateStopped

	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if cmd != nil {
		t.Fatal("e on a stopped container returned a non-nil Cmd, want no action taken")
	}
}

func TestExecShellDoneMsgSetsStatus(t *testing.T) {
	model := testModel()

	updated, cmd := model.Update(execShellDoneMsg{name: "radarr-1", err: nil})
	model = updated.(Model)
	if model.statusErr || !strings.Contains(model.status, "closed shell in radarr-1") {
		t.Fatalf("status/statusErr = %q/%v, want a clean-close confirmation", model.status, model.statusErr)
	}
	if cmd == nil {
		t.Fatal("execShellDoneMsg returned a nil Cmd, want a refresh")
	}

	updated, _ = model.Update(execShellDoneMsg{name: "radarr-1", err: errors.New("exit status 1")})
	model = updated.(Model)
	if model.statusErr {
		t.Fatalf("statusErr = true for a shell exit status, want false (informational, not an error banner)")
	}
	if !strings.Contains(model.status, "radarr-1") {
		t.Fatalf("status = %q, want it to mention radarr-1", model.status)
	}
}

func TestInspectorShowsContextualCopyOpenHints(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.width, model.height = 120, 30

	view := ansi.Strip(model.View())
	for _, want := range []string{"RUNTIME", "IMAGE", "COMPOSE", "NETWORK", "FILES", "METADATA", "Image", "c", "Ports", "c/o", "Mounts", "Labels"} {
		if !strings.Contains(view, want) {
			t.Fatalf("inspector missing %q:\n%s", want, view)
		}
	}
}

func TestContainerTitleIsConsistentAcrossPanes(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.width, model.height = 140, 30
	model.mode = activityLogs

	view := ansi.Strip(model.View())
	for _, want := range []string{"radarr-1", "Logs: radarr-1", "Inspector: radarr-1"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing consistent container title %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "│  ! radarr ") {
		t.Fatalf("tree rendered compose service as container title, want Docker container name:\n%s", view)
	}
}

func TestInspectorTitleUsesPaneBackgroundInLavenderTheme(t *testing.T) {
	original := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(original) })

	renderer := tideui.NewRenderer(tideui.LavenderFieldsForever, tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	title := renderInspectorTitle(renderer, 44, "radarr-1")
	backgrounds := trueColorBackgrounds(title)
	if len(backgrounds) == 0 {
		t.Fatalf("inspector title has no explicit background styling:\n%q", title)
	}
	want := backgrounds[0]
	for _, bg := range backgrounds {
		if bg != want {
			t.Fatalf("inspector title used mixed backgrounds %s and %s in Lavender theme:\n%q", want, bg, title)
		}
	}
	if strings.Contains(title, "48;2;160;128;225") {
		t.Fatalf("inspector title used Lavender accent background, want pane background:\n%q", title)
	}
}

func TestInspectorUsesColorWithoutChangingVisibleText(t *testing.T) {
	original := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(original) })

	model := testModelWithSelectedContainer()
	model.width, model.height = 120, 30

	rawView := model.View()
	stripped := ansi.Strip(rawView)
	if !strings.Contains(rawView, "\x1b[") {
		t.Fatalf("inspector view missing ANSI color styling:\n%s", rawView)
	}
	for _, want := range []string{"Status", "! running unhealthy", "Image", "radarr", "Ports", "7878 -> 7878/tcp"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("stripped inspector missing %q:\n%s", want, stripped)
		}
	}
}

func TestInspectorFieldRowsUseOnlyPaneBackgroundInLavenderTheme(t *testing.T) {
	original := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(original) })

	renderer := tideui.NewRenderer(tideui.LavenderFieldsForever, tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	rows := renderInspectorField(renderer, 44, "Image", "radarr", "c", "#7dcfff")
	if len(rows) == 0 {
		t.Fatal("renderInspectorField returned no rows")
	}
	backgrounds := trueColorBackgrounds(rows[0])
	if len(backgrounds) == 0 {
		t.Fatalf("inspector row has no explicit background styling:\n%q", rows[0])
	}
	if hasMidRowReset(rows[0]) {
		t.Fatalf("inspector row contains a mid-row full reset that can drop the pane background:\n%q", rows[0])
	}
	want := backgrounds[0]
	for _, bg := range backgrounds {
		if bg != want {
			t.Fatalf("inspector row used mixed backgrounds %s and %s in Lavender theme:\n%q", want, bg, rows[0])
		}
	}
}

func trueColorBackgrounds(value string) []string {
	matches := regexp.MustCompile(`\x1b\[[0-9;]*48;2;([0-9]+;[0-9]+;[0-9]+)[0-9;]*m`).FindAllStringSubmatch(value, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		out = append(out, match[1])
	}
	return out
}

func hasMidRowReset(value string) bool {
	resetPattern := regexp.MustCompile(`\x1b\[(?:0)?m`)
	resets := resetPattern.FindAllStringIndex(value, -1)
	if len(resets) == 0 {
		return false
	}
	for _, reset := range resets {
		suffix := value[reset[0]:]
		if resetPattern.ReplaceAllString(suffix, "") != "" {
			return true
		}
	}
	return false
}

func TestWindowAroundCursor(t *testing.T) {
	tests := []struct {
		name               string
		total, cursor, bu  int
		wantStart, wantEnd int
	}{
		{"fits entirely", 5, 2, 10, 0, 5},
		{"cursor at top", 20, 0, 5, 0, 5},
		{"cursor scrolls window down", 20, 12, 5, 8, 13},
		{"cursor near end clamps to tail", 20, 19, 5, 15, 20},
		{"cursor jumps back up", 20, 0, 5, 0, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end := windowAroundCursor(tt.total, tt.cursor, tt.bu)
			if start != tt.wantStart || end != tt.wantEnd {
				t.Fatalf("windowAroundCursor(%d, %d, %d) = (%d, %d), want (%d, %d)", tt.total, tt.cursor, tt.bu, start, end, tt.wantStart, tt.wantEnd)
			}
			if tt.cursor < start || tt.cursor >= end {
				t.Fatalf("window (%d, %d) does not contain cursor %d", start, end, tt.cursor)
			}
		})
	}
}

func TestCopyOverlayScrollsWithManyRows(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.width, model.height = 100, 16
	ports := make([]domain.Port, 0, 30)
	for i := 0; i < 30; i++ {
		ports = append(ports, domain.Port{IP: "0.0.0.0", Private: uint16(8000 + i), Public: uint16(9000 + i), Type: "tcp"})
	}
	model.selected.Ports = ports
	model.overlay = overlayCopy
	model.copyCursor = 0

	view := ansi.Strip(model.View())
	if !strings.Contains(view, "more") {
		t.Fatalf("copy overlay with %d rows should show a scroll indicator:\n%s", len(model.copyRows()), view)
	}

	rows := model.copyRows()
	target := len(rows) - 2
	for i := 0; i < target; i++ {
		model.moveCopyCursor(1)
	}
	view = ansi.Strip(model.View())
	wantValue := rows[target].value
	if !strings.Contains(view, wantValue) {
		t.Fatalf("copy overlay did not scroll selected row %d (%q) into view:\n%s", target, wantValue, view)
	}
}

func TestCopyOverlayCopiesSelectedRow(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.overlay = overlayCopy
	model.copyCursor = 0
	var out bytes.Buffer
	originalWriter := clipboardWriter
	clipboardWriter = &out
	defer func() { clipboardWriter = originalWriter }()

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.overlay != overlayNone {
		t.Fatalf("overlay = %v, want none", model.overlay)
	}
	if model.status != "copied container id 1" || model.statusErr {
		t.Fatalf("status/statusErr = %q/%v, want copied status", model.status, model.statusErr)
	}
	runCmd(t, cmd)
	got := out.String()
	if !strings.HasPrefix(got, "\x1b]52;c;") || !strings.HasSuffix(got, "\a") {
		t.Fatalf("clipboard output = %q, want OSC52 sequence", got)
	}
}

func TestOpenKeyOpensOpenOverlay(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.width, model.height = 100, 30

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	model = updated.(Model)
	if cmd != nil {
		t.Fatalf("open key returned cmd = %#v, want nil", cmd)
	}
	if model.overlay != overlayOpen {
		t.Fatalf("overlay = %v, want open", model.overlay)
	}
	view := ansi.Strip(model.View())
	for _, want := range []string{"whatthedock · open", "Port", "0.0.0.0:7878 -> 7878/tcp", "http://localhost:7878", "Mount", "/srv/media/radarr -> /config"} {
		if !strings.Contains(view, want) {
			t.Fatalf("open overlay missing %q:\n%s", want, view)
		}
	}
}

func TestOpenOverlayOpensSelectedTarget(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.overlay = overlayOpen
	model.openCursor = 0
	var opened string
	originalOpen := openTarget
	openTarget = func(target string) error {
		opened = target
		return nil
	}
	defer func() { openTarget = originalOpen }()

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.overlay != overlayNone {
		t.Fatalf("overlay = %v, want none", model.overlay)
	}
	if model.status != "opening port 0.0.0.0:7878 -> 7878/tcp" || model.statusErr {
		t.Fatalf("status/statusErr = %q/%v, want opening status", model.status, model.statusErr)
	}
	msg := runCmd(t, cmd)
	if opened != "http://localhost:7878" {
		t.Fatalf("opened = %q, want localhost port URL", opened)
	}
	updated, _ = model.Update(msg)
	model = updated.(Model)
	if model.statusErr {
		t.Fatalf("statusErr = true after successful open, status=%q", model.status)
	}
}

func TestOpenOverlayShowsOpenErrors(t *testing.T) {
	model := testModelWithSelectedContainer()
	originalOpen := openTarget
	openTarget = func(string) error { return errors.New("no opener") }
	defer func() { openTarget = originalOpen }()

	msg := openTargetCmd("Port", "http://localhost:7878")()
	updated, _ := model.Update(msg)
	model = updated.(Model)
	if !model.statusErr || !strings.Contains(model.status, "open port: no opener") {
		t.Fatalf("status/statusErr = %q/%v, want open error", model.status, model.statusErr)
	}
}

func TestCommandPaletteCanOpenCopyOverlay(t *testing.T) {
	model := testModelWithSelectedContainer()
	updated, cmd := model.executeCommand("open-copy")
	model = updated.(Model)
	if cmd != nil {
		t.Fatalf("open-copy cmd = %#v, want nil", cmd)
	}
	if model.overlay != overlayCopy {
		t.Fatalf("overlay = %v, want copy", model.overlay)
	}
}

func TestCommandPaletteCanOpenPortAndMountOverlays(t *testing.T) {
	for _, tc := range []struct {
		name       string
		command    actions.ID
		wantCursor int
		wantLabel  string
	}{
		{"port", actions.OpenPort, 0, "Port"},
		{"mount", actions.OpenMount, 1, "Mount"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := testModelWithSelectedContainer()
			updated, cmd := model.executeCommand(tc.command)
			model = updated.(Model)
			if cmd != nil {
				t.Fatalf("%s cmd = %#v, want nil", tc.command, cmd)
			}
			if model.overlay != overlayOpen {
				t.Fatalf("overlay = %v, want open", model.overlay)
			}
			if model.openCursor != tc.wantCursor {
				t.Fatalf("openCursor = %d, want %d", model.openCursor, tc.wantCursor)
			}
			rows := model.openRows()
			if rows[model.openCursor].label != tc.wantLabel {
				t.Fatalf("selected open row = %q, want %q", rows[model.openCursor].label, tc.wantLabel)
			}
		})
	}
}

func TestSettingsAffectStatsRendering(t *testing.T) {
	model := testModelInStatsMode()
	model.settings.GraphStyle = graphStyleBraille
	model.settings.ShowDeltas = false
	id := domain.ResourceID{Host: "local", ID: "1"}
	model.appendStats(domain.ContainerStats{ID: id, CPUPercent: 10, MemoryUsage: 300 * 1024 * 1024, MemoryLimit: 2 * 1024 * 1024 * 1024})
	stats := domain.ContainerStats{ID: id, Read: time.Now(), CPUPercent: 82, MemoryUsage: 360 * 1024 * 1024, MemoryLimit: 2 * 1024 * 1024 * 1024}
	model.stats = &stats
	model.appendStats(stats)

	view := ansi.Strip(model.View())
	if !strings.Contains(view, "⣀⣶") {
		t.Fatalf("stats view missing braille graph style:\n%s", view)
	}
	if strings.Contains(view, "↗") || strings.Contains(view, "↘") {
		t.Fatalf("stats view rendered deltas despite ShowDeltas=false:\n%s", view)
	}
}

func TestProblemsNavigationEnterJumpsTreeToContainer(t *testing.T) {
	model := testModel()
	model.rows = model.buildRows()
	model.focus = paneActivity
	model.mode = activityProblems

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if cmd != nil {
		t.Fatalf("down returned cmd = %#v, want nil while browsing problems", cmd)
	}
	if model.problemCursor != 1 {
		t.Fatalf("problemCursor = %d, want 1", model.problemCursor)
	}

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.focus != paneTree {
		t.Fatalf("focus = %v, want tree after choosing a problem", model.focus)
	}
	if row := model.currentRow(); row == nil || row.container == nil || row.container.ID.ID != "2" {
		t.Fatalf("currentRow = %#v, want jellyfin problem container", row)
	}
	msg := runCmd(t, cmd).(detailMsg)
	if msg.container.ID.ID != "2" {
		t.Fatalf("loaded container = %q, want 2", msg.container.ID.ID)
	}
}

func TestMouseWheelNoOpWhenInspectorFocused(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.width, model.height = 120, 30
	model.focus = paneInspector
	cursor := model.cursor

	updated, _ := model.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	got := updated.(Model)
	if got.cursor != cursor {
		t.Fatalf("cursor = %d, want unchanged %d after wheel scroll with Inspector focused", got.cursor, cursor)
	}

	updated, _ = got.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	got = updated.(Model)
	if got.cursor != cursor {
		t.Fatalf("cursor = %d, want unchanged %d after wheel scroll up with Inspector focused", got.cursor, cursor)
	}
}

func TestTreeMouseClickUsesScrolledVisibleRows(t *testing.T) {
	model := testModel()
	model.rows = model.buildRows()
	model.width, model.height = 120, 8 // treeVisibleRows() == 2
	model.focus = paneTree
	for i, row := range model.rows {
		if row.container != nil && row.container.ID.ID == "2" {
			model.cursor = i
			break
		}
	}

	if start := model.treeVisibleStart(); start == 0 {
		t.Fatalf("treeVisibleStart = %d, want scrolled tree for this regression", start)
	}

	updated, cmd := model.Update(tea.MouseMsg{
		X:      2,
		Y:      4,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	model = updated.(Model)
	if row := model.currentRow(); row == nil || row.container == nil || row.container.ID.ID != "2" {
		t.Fatalf("currentRow = %#v, want click on second visible scrolled row to select jellyfin", row)
	}
	if cmd == nil {
		t.Fatal("cmd is nil, want selected container load")
	}
	msg := runCmd(t, cmd).(detailMsg)
	if msg.container.ID.ID != "2" {
		t.Fatalf("loaded container = %q, want 2", msg.container.ID.ID)
	}
}

func TestProblemMouseClickJumpsTreeToContainer(t *testing.T) {
	model := testModel()
	model.rows = model.buildRows()
	model.width, model.height = 120, 30
	model.mode = activityProblems

	updated, cmd := model.Update(tea.MouseMsg{
		X:      model.leftPaneWidth() + 2,
		Y:      5,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	model = updated.(Model)
	if model.focus != paneTree {
		t.Fatalf("focus = %v, want tree after mouse choosing a problem", model.focus)
	}
	if row := model.currentRow(); row == nil || row.container == nil || row.container.ID.ID != "2" {
		t.Fatalf("currentRow = %#v, want jellyfin problem container", row)
	}
	if cmd == nil {
		t.Fatal("cmd is nil, want selected container load")
	}
}

func TestResizeViewDoesNotPanic(t *testing.T) {
	model := testModel()
	model.width, model.height = 20, 6
	_ = model.View()
	model.width, model.height = 120, 40
	_ = model.View()
}

func TestSnapshotErrorShowsDockerUnavailableState(t *testing.T) {
	model := NewModel(newFakeProvider())
	updated, _ := model.Update(snapshotMsg{err: errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock")})
	got := updated.(Model)
	if !got.statusErr {
		t.Fatal("statusErr = false, want true")
	}
	if !strings.Contains(got.status, "Docker is unavailable") {
		t.Fatalf("status = %q, want actionable Docker unavailable message", got.status)
	}
	if got.selected != nil {
		t.Fatalf("selected = %#v, want nil after unavailable snapshot", got.selected)
	}
}

// TestSuccessfulSnapshotDoesNotClearAnUnacknowledgedError guards against the
// bug reported live: an action's error status (e.g. Delete failing) was
// visible for well under a second because the routine refresh every action
// triggers (directly via refreshCmd, and again ~250ms later from the
// resulting Docker event — see eventRefreshTickCmd) unconditionally stomped
// it with "Docker connected" the moment the next snapshot came back, often
// faster than a person can read the error.
func TestSuccessfulSnapshotDoesNotClearAnUnacknowledgedError(t *testing.T) {
	model := testModel()
	model.status, model.statusErr = "delete telegraf: compose file not found on jarvis", true

	updated, _ := model.Update(snapshotMsg{snapshot: model.provider.(*fakeProvider).snapshot})
	got := updated.(Model)

	if !got.statusErr || got.status != "delete telegraf: compose file not found on jarvis" {
		t.Fatalf("status/statusErr = %q/%v, want the error preserved across a routine refresh", got.status, got.statusErr)
	}
}

// TestSuccessfulSnapshotStillUpdatesRoutineStatus is
// TestSuccessfulSnapshotDoesNotClearAnUnacknowledgedError's complement: the
// guard must only hold back an active error, not freeze the status line
// forever — a routine refresh with no error showing still updates it as
// before.
func TestSuccessfulSnapshotStillUpdatesRoutineStatus(t *testing.T) {
	model := testModel()
	model.status, model.statusErr = "some earlier notice", false

	updated, _ := model.Update(snapshotMsg{snapshot: model.provider.(*fakeProvider).snapshot})
	got := updated.(Model)

	if got.statusErr || got.status != "Docker connected" {
		t.Fatalf("status/statusErr = %q/%v, want Docker connected/false when nothing was blocking it", got.status, got.statusErr)
	}
}

// TestSuccessfulSnapshotClearsAStaleErrorAfterTheHoldWindow guards against
// overcorrecting TestSuccessfulSnapshotDoesNotClearAnUnacknowledgedError: an
// error must stay legible for a while, but not forever if nothing ever
// triggers a new explicit action to replace it — reported live as "error
// isn't going away" once the hold-forever version of this guard shipped.
func TestSuccessfulSnapshotClearsAStaleErrorAfterTheHoldWindow(t *testing.T) {
	model := testModel()
	model.status, model.statusErr = "delete telegraf: compose file not found on jarvis", true
	model.lastStatusErrText = model.status
	model.statusErrSince = time.Now().Add(-statusErrMinHold - time.Second)

	updated, _ := model.Update(snapshotMsg{snapshot: model.provider.(*fakeProvider).snapshot})
	got := updated.(Model)

	if got.statusErr || got.status != "Docker connected" {
		t.Fatalf("status/statusErr = %q/%v, want Docker connected/false once the hold window has passed", got.status, got.statusErr)
	}
}

func TestThemePickerPreviewsAndCancels(t *testing.T) {
	model := testModel()
	original := model.theme.Name
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'T'}})
	model = updated.(Model)
	if model.overlay != overlayThemePicker {
		t.Fatalf("overlay = %v, want theme picker", model.overlay)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	model = updated.(Model)
	if model.theme.Name == original {
		t.Fatalf("theme after preview = %q, want different from %q", model.theme.Name, original)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.overlay != overlayNone {
		t.Fatalf("overlay after cancel = %v, want none", model.overlay)
	}
	if model.theme.Name != original {
		t.Fatalf("theme after cancel = %q, want restored %q", model.theme.Name, original)
	}
}

func TestOverlaysRenderSoftPanelChrome(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 30
	for _, tc := range []struct {
		name    string
		overlay overlayMode
		want    string
	}{
		{"help", overlayHelp, "whatthedock · help"},
		{"filter", overlayFilter, "whatthedock · filter"},
		{"command", overlayCommandPalette, "whatthedock · command"},
		{"settings", overlaySettings, "whatthedock · settings"},
		{"systems", overlaySystems, "whatthedock · systems"},
		{"copy", overlayCopy, "whatthedock · copy"},
		{"open", overlayOpen, "whatthedock · open"},
		{"create", overlayCreate, "whatthedock · create"},
		{"about", overlayAbout, "whatthedock · about"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model.overlay = tc.overlay
			view := ansi.Strip(model.View())
			if !strings.Contains(view, tc.want) {
				t.Fatalf("View() missing soft panel title %q:\n%s", tc.want, view)
			}
		})
	}
}

func TestHelpMentionsSystemsOverlayCommands(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 30
	model.overlay = overlayHelp

	view := ansi.Strip(model.View())
	for _, want := range []string{"n              create container or Compose service", "e              open shell in selected container"} {
		if !strings.Contains(view, want) {
			t.Fatalf("help view missing %q:\n%s", want, view)
		}
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	model = updated.(Model)
	if model.helpScroll == 0 {
		t.Fatal("helpScroll = 0 after G, want scrolled to bottom")
	}
	view = ansi.Strip(model.View())
	for _, want := range []string{"Ctrl+S         save settings/forms", "S              systems", "Systems: enter switch, t test, a add, e edit, d delete", "A              about screen"} {
		if !strings.Contains(view, want) {
			t.Fatalf("help view missing %q after scrolling to bottom:\n%s", want, view)
		}
	}
}

func TestHelpOverlayShowsStatusLegend(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 30
	model.overlay = overlayHelp

	view := ansi.Strip(model.View())
	for _, want := range []string{
		"Status colors:",
		"healthy / running",
		"restarting",
		"stopped, exited cleanly",
		"dead",
		"unhealthy",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("help view missing status legend entry %q:\n%s", want, view)
		}
	}
}

// TestHelpOverlayLegendStaysVisibleWhileScrolled guards the "always
// visible, not part of the scrollable list" property statusLegendLines is
// meant to have: scrolling the keybinding list must never scroll the
// legend out of view along with it.
func TestHelpOverlayLegendStaysVisibleWhileScrolled(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 20
	model.overlay = overlayHelp

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	model = updated.(Model)

	view := ansi.Strip(model.View())
	if !strings.Contains(view, "Status colors:") {
		t.Fatalf("legend missing after scrolling to bottom:\n%s", view)
	}
}

func TestHelpOverlayScrollsWithJK(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 20
	model.overlay = overlayHelp

	if budget := model.helpBodyBudget(); budget >= len(helpLines) {
		t.Fatalf("helpBodyBudget() = %d, want < %d so this test actually exercises scrolling", budget, len(helpLines))
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	model = updated.(Model)
	if model.helpScroll != 1 {
		t.Fatalf("helpScroll after j = %d, want 1", model.helpScroll)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	model = updated.(Model)
	if model.helpScroll != 0 {
		t.Fatalf("helpScroll after k = %d, want 0", model.helpScroll)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	model = updated.(Model)
	if model.helpScroll != 0 {
		t.Fatalf("helpScroll should not go below 0, got %d", model.helpScroll)
	}
}

func TestAboutOverlayOpensAnimatesAndCloses(t *testing.T) {
	// Spotlights only changes cell color, never the rune itself (unlike the
	// old Burn effect, which changed the glyph during ignition) — under the
	// default no-color test profile every brightness level would render
	// identical plain text, making the animation invisible to this test.
	original := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(original) })

	model := testModel()
	model.width, model.height = 100, 30

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	model = updated.(Model)

	if model.overlay != overlayAbout {
		t.Fatalf("overlay = %v, want about", model.overlay)
	}
	if cmd == nil {
		t.Fatal("about shortcut returned nil cmd, want animation tick")
	}
	firstRaw := model.View()
	first := ansi.Strip(firstRaw)
	if !strings.Contains(first, "whatthedock · about") {
		t.Fatalf("about overlay missing expected copy:\n%s", first)
	}

	firstFrame := model.aboutFrame
	updated, cmd = model.Update(aboutTickMsg{})
	model = updated.(Model)
	if model.aboutFrame != firstFrame+1 {
		t.Fatalf("aboutFrame = %d, want %d", model.aboutFrame, firstFrame+1)
	}
	if cmd == nil {
		t.Fatal("about tick returned nil cmd while overlay is open")
	}
	// Spotlights move at a fraction of a grid cell per frame, so a single
	// tick isn't guaranteed to cross a brightness threshold anywhere on
	// screen — advance several frames instead of asserting on exactly one.
	for range 8 {
		updated, _ = model.Update(aboutTickMsg{})
		model = updated.(Model)
	}
	laterRaw := model.View()
	if firstRaw == laterRaw {
		t.Fatal("about view did not change after several animation ticks")
	}

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.overlay != overlayNone {
		t.Fatalf("overlay after esc = %v, want none", model.overlay)
	}
	if cmd != nil {
		t.Fatalf("esc returned cmd %v, want nil", cmd)
	}
	updated, cmd = model.Update(aboutTickMsg{})
	model = updated.(Model)
	if cmd != nil {
		t.Fatal("about tick returned cmd after overlay closed")
	}
}

// TestSpotlightBrightnessFalloff checks the beam's shape: full brightness
// at the center, zero at and past the radius, and a monotonically
// decreasing value in between rather than an abrupt cutoff.
func TestSpotlightBrightnessFalloff(t *testing.T) {
	if got := spotlightBrightness(0, 2.4, 0.6); got != 1 {
		t.Fatalf("brightness at center = %v, want 1", got)
	}
	if got := spotlightBrightness(2.4, 2.4, 0.6); got != 0 {
		t.Fatalf("brightness at radius = %v, want 0", got)
	}
	if got := spotlightBrightness(3, 2.4, 0.6); got != 0 {
		t.Fatalf("brightness past radius = %v, want 0", got)
	}
	mid := spotlightBrightness(1.8, 2.4, 0.6)
	if mid <= 0 || mid >= 1 {
		t.Fatalf("brightness inside the falloff band = %v, want strictly between 0 and 1", mid)
	}
	closer := spotlightBrightness(1.2, 2.4, 0.6)
	if closer <= mid {
		t.Fatalf("brightness closer to center = %v, want > brightness at 1.8 (%v): falloff should be monotonic", closer, mid)
	}
}

// TestSpotlightRowCellsIlluminatesNearSpotlight checks that a glyph gets a
// lit color when a spotlight sits on top of it during the search phase,
// and an unlit color when no spotlight is anywhere nearby.
func TestSpotlightRowCellsIlluminatesNearSpotlight(t *testing.T) {
	logo := aboutLogo()
	width := 80
	row := 1
	line := logo[row]
	padded := []rune(centerPlainText(line, width))
	col := -1
	for i, r := range padded {
		if r != ' ' {
			col = i
			break
		}
	}
	if col == -1 {
		t.Fatal("expected a non-space column in row 1")
	}

	const unlit = "#4a4550"
	noSpotlights := spotlightRowCells(line, row, len(logo), width, nil, 0)[col]
	if noSpotlights.color != unlit {
		t.Fatalf("cell with no spotlights nearby = %+v, want unlit gray %q", noSpotlights, unlit)
	}

	onTarget := []aboutSpotlight{{row: float64(row), col: float64(col)}}
	lit := spotlightRowCells(line, row, len(logo), width, onTarget, 0)[col]
	if lit.color == unlit {
		t.Fatalf("cell directly under a spotlight still unlit: %+v", lit)
	}
	if lit.r != padded[col] {
		t.Fatalf("lit cell rune = %q, want original glyph %q (spotlights illuminate, they don't replace the glyph)", lit.r, padded[col])
	}
}

// TestSpotlightRowCellsFullyLitAfterExpand checks that, well past the
// search+converge+expand window, every glyph settles at full brightness —
// the reference effect's "converging in the center and expanding" ends
// with the whole text revealed, not just the area near a spotlight.
func TestSpotlightRowCellsFullyLitAfterExpand(t *testing.T) {
	logo := aboutLogo()
	width := 80
	row := 1
	line := logo[row]
	padded := []rune(centerPlainText(line, width))

	doneFrame := aboutSearchFrames + aboutConvergeFrames + aboutExpandFrames + 10
	cells := spotlightRowCells(line, row, len(logo), width, nil, doneFrame)
	for col, want := range padded {
		if want == ' ' {
			continue
		}
		got := cells[col]
		wantColor := spotlightFinalColor(row, len(logo))
		if got.color != wantColor {
			t.Fatalf("cell (%d,%d) after expand = %+v, want fully lit color %q", row, col, got, wantColor)
		}
	}
}

// TestNewAboutSpotlightsSpawnsRequestedCount is a basic sanity check on the
// spawn helper: the right number of spotlights, each starting somewhere
// within the grid and already aimed at a (possibly different) target.
func TestNewAboutSpotlightsSpawnsRequestedCount(t *testing.T) {
	spotlights := newAboutSpotlights(aboutSpotlightCount, 6, 80)
	if len(spotlights) != aboutSpotlightCount {
		t.Fatalf("len(spotlights) = %d, want %d", len(spotlights), aboutSpotlightCount)
	}
	for i, sp := range spotlights {
		if sp.row < 0 || sp.row > 5 || sp.col < 0 || sp.col > 79 {
			t.Fatalf("spotlight %d position = (%v,%v), want within the 6x80 grid", i, sp.row, sp.col)
		}
		if sp.speed <= 0 {
			t.Fatalf("spotlight %d speed = %v, want > 0", i, sp.speed)
		}
	}
}

// TestTickAboutSpotlightsConvergesAfterSearchWindow checks the phase
// transition itself: once the frame count passes aboutSearchFrames, every
// spotlight's target should snap to the grid center rather than another
// random point.
func TestTickAboutSpotlightsConvergesAfterSearchWindow(t *testing.T) {
	model := testModel()
	model.width = 100
	model.aboutFrame = aboutSearchFrames
	model.aboutSpotlights = newAboutSpotlights(aboutSpotlightCount, len(aboutLogo()), aboutContentWidth(model.width))

	model = model.tickAboutSpotlights()

	wantRow := float64(len(aboutLogo())-1) / 2
	wantCol := float64(aboutContentWidth(model.width)-1) / 2
	for i, sp := range model.aboutSpotlights {
		if sp.targetRow != wantRow || sp.targetCol != wantCol {
			t.Fatalf("spotlight %d target = (%v,%v), want center (%v,%v)", i, sp.targetRow, sp.targetCol, wantRow, wantCol)
		}
	}
}

func TestAboutCommandPaletteActionOpensOverlay(t *testing.T) {
	model := testModel()

	updated, cmd := model.executeCommand(actions.OpenAbout)
	model = updated.(Model)

	if model.overlay != overlayAbout {
		t.Fatalf("overlay = %v, want about", model.overlay)
	}
	if cmd == nil {
		t.Fatal("about command returned nil cmd, want animation tick")
	}
}

func testModel() Model {
	provider := newFakeProvider()
	model := NewModel(provider)
	model.snapshot = provider.snapshot
	return model
}

func testModelWithSelectedContainer() Model {
	model := testModel()
	model.rows = model.buildRows()
	for i, row := range model.rows {
		if row.container != nil && row.container.ID.ID == "1" {
			model.cursor = i
			model.selectedID = row.container.ID
			model.selected = row.container
			break
		}
	}
	return model
}

func testModelInStatsMode() Model {
	model := testModelWithSelectedContainer()
	model.width, model.height = 120, 30
	model.mode = activityStats
	model.focus = paneActivity
	return model
}

func newFakeProvider() *fakeProvider {
	host := domain.Host{ID: "local", Name: "local"}
	containers := []domain.Container{
		{
			ID:      domain.ResourceID{Host: "local", ID: "1"},
			Name:    "radarr-1",
			Image:   "radarr",
			ImageID: "sha256:abc123",
			State:   domain.StateRunning,
			Health:  domain.HealthUnhealthy,
			Compose: domain.ComposeRef{
				Project:         "media",
				Service:         "radarr",
				ContainerNumber: "1",
				ConfigFiles:     "/srv/media/compose.yml",
			},
			Ports:  []domain.Port{{IP: "0.0.0.0", Private: 7878, Public: 7878, Type: "tcp"}},
			Mounts: []domain.Mount{{Type: "bind", Source: "/srv/media/radarr", Destination: "/config", ReadWrite: true}},
			Labels: map[string]string{"com.docker.compose.project": "media", "com.docker.compose.service": "radarr"},
		},
		{ID: domain.ResourceID{Host: "local", ID: "2"}, Name: "jellyfin-1", Image: "jellyfin", State: domain.StateStopped, Compose: domain.ComposeRef{Project: "media", Service: "jellyfin"}},
	}
	snapshot := domain.BuildSnapshot(host, containers, time.Unix(1, 0))
	return &fakeProvider{
		host:       host,
		snapshot:   snapshot,
		containers: map[string]domain.Container{"1": containers[0], "2": containers[1]},
		stats: map[string]domain.ContainerStats{
			"1": {
				ID:          containers[0].ID,
				Read:        time.Now(),
				CPUPercent:  41.5,
				MemoryUsage: 384 * 1024 * 1024,
				MemoryLimit: 2 * 1024 * 1024 * 1024,
				NetworkRx:   64 * 1024 * 1024,
				NetworkTx:   18 * 1024 * 1024,
				BlockRead:   128 * 1024 * 1024,
				BlockWrite:  32 * 1024 * 1024,
				PIDs:        12,
			},
			"2": {
				ID:          containers[1].ID,
				Read:        time.Now(),
				CPUPercent:  0,
				MemoryUsage: 96 * 1024 * 1024,
				MemoryLimit: 2 * 1024 * 1024 * 1024,
				PIDs:        0,
			},
		},
	}
}

func rowLabels(rows []treeRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.label)
	}
	return out
}

func runCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("cmd is nil")
	}
	return cmd()
}

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

func TestInitStartsSnapshotAndEventSubscription(t *testing.T) {
	model := testModel()

	msg := runCmd(t, model.Init())
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init() msg = %#v, want tea.BatchMsg", msg)
	}
	// snapshot refresh, event subscription, the status-bar pulse tick, and
	// an update check.
	if len(batch) != 4 {
		t.Fatalf("Init() batch length = %d, want 4", len(batch))
	}
}

// TestInitAlwaysChecksForUpdateRegardlessOfLastCheck is the regression
// test for a live report: the update check used to be throttled to once
// per 24h (via updateLastCheck), which meant relaunching the app later the
// same day silently skipped checking at all — easy to mistake for the
// check being broken. There's no throttle anymore; every launch checks.
func TestInitAlwaysChecksForUpdateRegardlessOfLastCheck(t *testing.T) {
	model := testModel()
	model.updateLastCheck = time.Now()

	msg := runCmd(t, model.Init())
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init() msg = %#v, want tea.BatchMsg", msg)
	}
	if len(batch) != 4 {
		t.Fatalf("Init() batch length = %d, want 4 (an update check even though updateLastCheck was just set)", len(batch))
	}
}

func TestUpdateCheckOpensOverlayForNewerVersion(t *testing.T) {
	model := testModel()
	model.appVersion = "v0.1.3"

	updated, _ := model.Update(updateCheckMsg{latest: "v0.1.4"})
	got := updated.(Model)

	if got.overlay != overlayUpdate {
		t.Fatalf("overlay = %v, want overlayUpdate", got.overlay)
	}
	if got.updateAvailableVersion != "v0.1.4" {
		t.Fatalf("updateAvailableVersion = %q, want v0.1.4", got.updateAvailableVersion)
	}
	if got.updateLastCheck.IsZero() {
		t.Fatal("updateLastCheck = zero, want recorded")
	}
}

// TestUpdateCheckOpensOverlayOverDashboard is the regression test for a
// bug reported live: Settings > Start in dashboard opens the Dashboard
// overlay from the very first frame (see NewModelWithProviderFactory),
// before Init's own autoCheckForUpdateCmd has had a chance to respond —
// so by the time updateCheckMsg arrived, the pre-existing "only open the
// update overlay when nothing else is" check silently downgraded a real
// update to a status-bar hint instead of the interactive install/ignore
// prompt, with no interactive prompt ever shown unless the user happened
// to notice the hint and open Settings themselves.
func TestUpdateCheckOpensOverlayOverDashboard(t *testing.T) {
	model := testModel()
	model.appVersion = "v0.1.3"
	model.overlay = overlayDashboard

	updated, _ := model.Update(updateCheckMsg{latest: "v0.1.4"})
	got := updated.(Model)

	if got.overlay != overlayUpdate {
		t.Fatalf("overlay = %v, want overlayUpdate to take over from overlayDashboard", got.overlay)
	}
	if got.updateAvailableVersion != "v0.1.4" {
		t.Fatalf("updateAvailableVersion = %q, want v0.1.4", got.updateAvailableVersion)
	}
}

func TestUpdateCheckDoesNotStealAnAlreadyOpenOverlay(t *testing.T) {
	model := testModel()
	model.appVersion = "v0.1.3"
	model.overlay = overlaySettings

	updated, _ := model.Update(updateCheckMsg{latest: "v0.1.4"})
	got := updated.(Model)

	if got.overlay != overlaySettings {
		t.Fatalf("overlay = %v, want overlaySettings preserved", got.overlay)
	}
	if got.updateAvailableVersion != "v0.1.4" {
		t.Fatalf("updateAvailableVersion = %q, want v0.1.4 (still recorded for Settings to show)", got.updateAvailableVersion)
	}
	if !strings.Contains(got.status, "v0.1.4") {
		t.Fatalf("status = %q, want it to mention the available version", got.status)
	}
}

// TestUpdateCheckManualFromSettingsShowsInstallPrompt is the regression
// test for a bug reported live: triggering "Check for update" from inside
// Settings (a manual check) left Settings open and only ever showed the
// quiet "update vX available (see Settings)" status-bar hint — never the
// actual install/ignore popup — because Settings itself was the "already
// open overlay" the non-manual deferral logic was protecting. Since the
// user explicitly asked for the check, they should always get the popup.
func TestUpdateCheckManualFromSettingsShowsInstallPrompt(t *testing.T) {
	model := testModel()
	model.appVersion = "v0.1.3"
	model.overlay = overlaySettings

	updated, _ := model.Update(updateCheckMsg{manual: true, latest: "v0.1.4"})
	got := updated.(Model)

	if got.overlay != overlayUpdate {
		t.Fatalf("overlay = %v, want overlayUpdate (a manual check must show the install prompt, not just a status hint)", got.overlay)
	}
	if got.updateAvailableVersion != "v0.1.4" {
		t.Fatalf("updateAvailableVersion = %q, want v0.1.4", got.updateAvailableVersion)
	}
}

func TestUpdateCheckAutoSkipsAnAlreadyIgnoredVersion(t *testing.T) {
	model := testModel()
	model.appVersion = "v0.1.3"
	model.updateIgnoredVersion = "v0.1.4"

	updated, _ := model.Update(updateCheckMsg{manual: false, latest: "v0.1.4"})
	got := updated.(Model)

	if got.overlay == overlayUpdate {
		t.Fatal("overlay = overlayUpdate, want no prompt for a version already dismissed")
	}
}

func TestUpdateCheckManualAlwaysShowsAnIgnoredVersion(t *testing.T) {
	model := testModel()
	model.appVersion = "v0.1.3"
	model.updateIgnoredVersion = "v0.1.4"

	updated, _ := model.Update(updateCheckMsg{manual: true, latest: "v0.1.4"})
	got := updated.(Model)

	if got.overlay != overlayUpdate {
		t.Fatal("overlay != overlayUpdate, want a manual check to show the prompt even for a previously ignored version")
	}
}

func TestUpdateCheckManualReportsUpToDate(t *testing.T) {
	model := testModel()
	model.appVersion = "v0.1.4"

	updated, _ := model.Update(updateCheckMsg{manual: true, latest: "v0.1.4"})
	got := updated.(Model)

	if got.overlay == overlayUpdate {
		t.Fatal("overlay = overlayUpdate, want no prompt when already up to date")
	}
	if got.statusErr || !strings.Contains(got.status, "up to date") {
		t.Fatalf("status/statusErr = %q/%v, want an up-to-date notice", got.status, got.statusErr)
	}
}

func TestUpdateCheckManualErrorShowsStatus(t *testing.T) {
	model := testModel()

	updated, _ := model.Update(updateCheckMsg{manual: true, err: errors.New("network unreachable")})
	got := updated.(Model)

	if !got.statusErr || !strings.Contains(got.status, "network unreachable") {
		t.Fatalf("status/statusErr = %q/%v, want the error surfaced", got.status, got.statusErr)
	}
}

func TestUpdateCheckAutoErrorStaysQuiet(t *testing.T) {
	model := testModel()
	model.status, model.statusErr = "unrelated status", false

	updated, _ := model.Update(updateCheckMsg{manual: false, err: errors.New("network unreachable")})
	got := updated.(Model)

	if got.status != "unrelated status" || got.statusErr {
		t.Fatalf("status/statusErr = %q/%v, want the background check to fail silently", got.status, got.statusErr)
	}
}

func TestUpdateKeyYesInstalls(t *testing.T) {
	model := testModel()
	model.overlay = overlayUpdate
	model.updateAvailableVersion = "v0.1.4"

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	got := updated.(Model)

	if got.overlay != overlayNone {
		t.Fatalf("overlay = %v, want overlayNone", got.overlay)
	}
	if !got.updateInstalling {
		t.Fatal("updateInstalling = false, want true")
	}
	if cmd == nil {
		t.Fatal("cmd = nil, want the install command")
	}
}

func TestUpdateKeyNoIgnoresVersion(t *testing.T) {
	model := testModel()
	model.overlay = overlayUpdate
	model.updateAvailableVersion = "v0.1.4"

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	got := updated.(Model)

	if got.overlay != overlayNone {
		t.Fatalf("overlay = %v, want overlayNone", got.overlay)
	}
	if got.updateIgnoredVersion != "v0.1.4" {
		t.Fatalf("updateIgnoredVersion = %q, want v0.1.4", got.updateIgnoredVersion)
	}
	if got.updateAvailableVersion != "" {
		t.Fatalf("updateAvailableVersion = %q, want cleared", got.updateAvailableVersion)
	}
}

// TestUpdateInstalledSuccessShowsMessageThenQuits is the regression test
// for a live report: a successful install used to return tea.Quit
// immediately, and main.go's restartInto re-execs via syscall.Exec — an
// instant process-image replacement with no visible transition — so the
// whole thing happened "in a flash with no messaging," giving no chance to
// actually read that it worked. A successful install must now show a
// status naming the version before quitting, and quitting itself must wait
// for updateRestartMsg (see tickUpdateRestart's delay) rather than firing
// immediately.
func TestUpdateInstalledSuccessShowsMessageThenQuits(t *testing.T) {
	model := testModel()
	model.updateInstalling = true
	model.updateAvailableVersion = "v0.1.8"

	updated, cmd := model.Update(updateInstalledMsg{exePath: "/usr/local/bin/whatthedock"})
	got := updated.(Model)

	if got.updateInstalling {
		t.Fatal("updateInstalling = true, want false")
	}
	if got.RestartExecPath() != "/usr/local/bin/whatthedock" {
		t.Fatalf("RestartExecPath() = %q, want /usr/local/bin/whatthedock", got.RestartExecPath())
	}
	if got.statusErr || !strings.Contains(got.status, "v0.1.8") {
		t.Fatalf("status/statusErr = %q/%v, want a success message naming v0.1.8", got.status, got.statusErr)
	}
	if cmd == nil {
		t.Fatal("cmd = nil, want the delayed restart tick")
	}

	msg := runCmd(t, cmd)
	if _, ok := msg.(updateRestartMsg); !ok {
		t.Fatalf("msg = %#v, want updateRestartMsg (the delayed tick), not an immediate quit", msg)
	}
	_, quitCmd := got.Update(msg)
	if quitCmd == nil {
		t.Fatal("cmd = nil after updateRestartMsg, want tea.Quit")
	}
	if quitMsg := runCmd(t, quitCmd); quitMsg != (tea.QuitMsg{}) {
		t.Fatalf("msg = %#v, want tea.QuitMsg", quitMsg)
	}
}

func TestUpdateInstalledFailureShowsStatus(t *testing.T) {
	model := testModel()
	model.updateInstalling = true

	updated, _ := model.Update(updateInstalledMsg{err: errors.New("disk full")})
	got := updated.(Model)

	if got.updateInstalling {
		t.Fatal("updateInstalling = true, want false")
	}
	if !got.statusErr || !strings.Contains(got.status, "disk full") {
		t.Fatalf("status/statusErr = %q/%v, want the error surfaced", got.status, got.statusErr)
	}
	if got.RestartExecPath() != "" {
		t.Fatalf("RestartExecPath() = %q, want empty on failure", got.RestartExecPath())
	}
}

func TestSettingsCheckForUpdateRowDispatchesCheck(t *testing.T) {
	model := testModel()
	model.openSettingsOverlay()
	rows := model.settingsRows()
	for i, row := range rows {
		if row.action == settingsActionCheckUpdate {
			model.settingsCursor = i
		}
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)

	if !got.updateChecking {
		t.Fatal("updateChecking = false, want true")
	}
	if cmd == nil {
		t.Fatal("cmd = nil, want the check command")
	}
}

func TestUpdateOverlayRendersVersions(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 30
	model.appVersion = "v0.1.3"
	model.overlay = overlayUpdate
	model.updateAvailableVersion = "v0.1.4"

	view := ansi.Strip(model.View())
	for _, want := range []string{"v0.1.4", "v0.1.3", "update available", "y update", "n/esc ignore"} {
		if !strings.Contains(view, want) {
			t.Fatalf("update overlay missing %q:\n%s", want, view)
		}
	}
}

// TestSettingsCheckForUpdateRowShowsStatus guards against the bug reported
// live: settingsOverlay unconditionally overwrote every settingsRowAction
// row's suffix with the literal "enter", silently discarding
// updateCheckRowValue()'s actual status — so "Check for update" never
// showed anything but "enter" no matter what the last check found,
// leaving an available update visible only in the easy-to-miss main status
// bar.
func TestSettingsCheckForUpdateRowShowsStatus(t *testing.T) {
	model := testModel()
	// Tall enough that Check for update (near the bottom of the list) is
	// visible without scrolling to it first.
	model.width, model.height = 100, 45
	model.updateAvailableVersion = "v0.1.4"
	model.openSettingsOverlay()

	view := ansi.Strip(model.View())
	if !strings.Contains(view, "v0.1.4 available") {
		t.Fatalf("settings view missing update status:\n%s", view)
	}
}

// TestSettingsResetDefaultsRowStillShowsEnter is
// TestSettingsCheckForUpdateRowShowsStatus's complement: only Check for
// update's suffix should change, not every settingsRowAction row.
func TestSettingsResetDefaultsRowStillShowsEnter(t *testing.T) {
	model := testModel()
	// Tall enough that Reset defaults (near the bottom of the list) is
	// visible without scrolling to it first.
	model.width, model.height = 100, 45
	model.openSettingsOverlay()

	view := ansi.Strip(model.View())
	if !strings.Contains(view, "Reset defaults") || !strings.Contains(view, "enter") {
		t.Fatalf("settings view missing Reset defaults' enter hint:\n%s", view)
	}
}

func TestStatusPulseTickIncrementsFrameAndReschedules(t *testing.T) {
	model := testModel()
	firstFrame := model.statusPulseFrame

	updated, cmd := model.Update(statusPulseTickMsg{})
	model = updated.(Model)

	if model.statusPulseFrame != firstFrame+1 {
		t.Fatalf("statusPulseFrame = %d, want %d", model.statusPulseFrame, firstFrame+1)
	}
	if cmd == nil {
		t.Fatal("statusPulseTickMsg returned a nil Cmd, want the pulse rescheduled")
	}
}

func TestStatusLeftShowsPulsingDotWhenConnected(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	model := testModel()
	model.status, model.statusErr = "Docker connected", false

	left := model.statusLeft(renderer)
	stripped := ansi.Strip(left)
	if strings.Contains(stripped, "Docker connected") {
		t.Fatalf("statusLeft() = %q, want the static text replaced by a dot", stripped)
	}
	if !strings.Contains(stripped, "●") {
		t.Fatalf("statusLeft() = %q, want a pulsing dot", stripped)
	}
}

func TestStatusLeftShowsBusySpinnerWithPhaseText(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	model := testModel()
	model.busy = true
	model.status, model.statusErr = "pulling redis:7…", false

	left := ansi.Strip(model.statusLeft(renderer))
	if !strings.Contains(left, "pulling redis:7…") {
		t.Fatalf("statusLeft() = %q, want the busy phase text", left)
	}
	if !strings.Contains(left, spinnerGlyph(model.statusPulseFrame)) {
		t.Fatalf("statusLeft() = %q, want the spinner glyph for frame %d", left, model.statusPulseFrame)
	}
}

func TestStatusLeftShowsReconnectingIndicator(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	model := testModel()
	model.eventsReconnecting = true

	left := ansi.Strip(model.statusLeft(renderer))
	if !strings.Contains(left, "reconnecting to Docker") {
		t.Fatalf("statusLeft() = %q, want a reconnecting indicator", left)
	}
}

func TestStatusLeftShowsTextForNonConnectedStatuses(t *testing.T) {
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	model := testModel()
	model.status, model.statusErr = "restart complete", false

	left := ansi.Strip(model.statusLeft(renderer))
	if !strings.Contains(left, "restart complete") {
		t.Fatalf("statusLeft() = %q, want other status messages left as text, not replaced by the dot", left)
	}
}

func TestPulseDotColorBreathesOverFrames(t *testing.T) {
	bright := lipgloss.Color("#80c990")
	start := pulseDotColor(0, bright)
	quarterCycle := pulseDotColor(6, bright)
	if start == quarterCycle {
		t.Fatalf("pulseDotColor(0) = pulseDotColor(6) = %q, want the color to change over the cycle", start)
	}
}

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

func TestEventRefreshTickRefreshesOnlyWhenDirty(t *testing.T) {
	model := testModel()

	updated, cmd := model.Update(eventRefreshTickMsg{})
	model = updated.(Model)
	if cmd != nil {
		t.Fatalf("clean eventRefreshTickMsg returned cmd = %#v, want nil", cmd)
	}

	model.snapshotDirty = true
	updated, cmd = model.Update(eventRefreshTickMsg{})
	model = updated.(Model)
	if model.snapshotDirty {
		t.Fatal("snapshotDirty = true after eventRefreshTickMsg, want false before refresh starts")
	}
	if cmd == nil {
		t.Fatal("dirty eventRefreshTickMsg returned nil cmd, want snapshot refresh")
	}
	if _, ok := runCmd(t, cmd).(snapshotMsg); !ok {
		t.Fatalf("eventRefreshTickMsg cmd did not return snapshotMsg")
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

func TestEventsStartedErrorAdvancesBackoff(t *testing.T) {
	model := testModel()
	model.eventBackoff = 0

	// First eventsStartedMsg error: backoff should advance to 1s
	updated, cmd := model.Update(eventsStartedMsg{err: errors.New("connection failed")})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("eventsStartedMsg error returned nil cmd, want a reconnect tick scheduled")
	}
	if model.eventBackoff != time.Second {
		t.Fatalf("eventBackoff = %v after first error, want 1s", model.eventBackoff)
	}

	// Second eventsStartedMsg error: backoff should double to 2s
	updated, cmd = model.Update(eventsStartedMsg{err: errors.New("connection failed again")})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("eventsStartedMsg error returned nil cmd, want a reconnect tick scheduled")
	}
	if model.eventBackoff != 2*time.Second {
		t.Fatalf("eventBackoff = %v after second error, want 2s", model.eventBackoff)
	}
}

func TestAppLogModeString(t *testing.T) {
	cases := map[appLogMode]string{appLogOff: "off", appLogOn: "on", appLogSave: "save"}
	for mode, want := range cases {
		if got := mode.String(); got != want {
			t.Fatalf("appLogMode(%d).String() = %q, want %q", mode, got, want)
		}
	}
}

func TestAppLogSettingsRoundTrip(t *testing.T) {
	for _, mode := range []appLogMode{appLogOff, appLogOn, appLogSave} {
		var s appSettings
		s.AppLog = mode
		var restored appSettings
		restored.applyPersisted(s.persisted())
		if restored.AppLog != mode {
			t.Fatalf("mode %v: round-tripped to %v", mode, restored.AppLog)
		}
	}
}

func TestAIProviderSettingsRoundTrip(t *testing.T) {
	for _, provider := range []aiProvider{aiProviderAnthropic, aiProviderOpenAI, aiProviderGemini, aiProviderCustom} {
		var s appSettings
		s.AIProvider = provider
		s.AIModel = "some-model"
		s.AIAPIKey = "sk-test-secret"
		s.AIBaseURL = "http://localhost:11434/v1"
		var restored appSettings
		restored.applyPersisted(s.persisted())
		if restored.AIProvider != provider {
			t.Fatalf("provider %v: round-tripped to %v", provider, restored.AIProvider)
		}
		if restored.AIModel != s.AIModel || restored.AIAPIKey != s.AIAPIKey || restored.AIBaseURL != s.AIBaseURL {
			t.Fatalf("provider %v: fields = %#v, want %#v", provider, restored, s)
		}
	}
}

func TestAIProviderStringAndDefault(t *testing.T) {
	if got := aiProviderAnthropic.String(); got != "anthropic" {
		t.Fatalf("aiProviderAnthropic.String() = %q, want anthropic", got)
	}
	// An unrecognized/empty persisted provider must default to anthropic,
	// not silently become the zero-value-that-happens-to-be-anthropic by
	// accident — applyPersisted's switch has an explicit "anthropic", ""
	// case, this locks that in.
	var s appSettings
	s.applyPersisted(config.Settings{AIProvider: ""})
	if s.AIProvider != aiProviderAnthropic {
		t.Fatalf("AIProvider after empty persisted value = %v, want aiProviderAnthropic", s.AIProvider)
	}
}

func TestCycleSettingAIProviderWrapsThroughAllFour(t *testing.T) {
	model := testModel()
	rows := model.settingsRows()
	index := -1
	for i, row := range rows {
		if row.label == "AI provider" {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatal("settingsRows() has no \"AI provider\" row")
	}
	want := []aiProvider{aiProviderOpenAI, aiProviderGemini, aiProviderCustom, aiProviderAnthropic}
	for _, w := range want {
		model.cycleSetting(index, 1)
		if model.settingsDraft.AIProvider != w {
			t.Fatalf("after cycling, AIProvider = %v, want %v", model.settingsDraft.AIProvider, w)
		}
	}
}

// TestSettingsRowsShowsAIBaseURLOnlyForCustomProvider checks the
// conditional row: "AI base URL" only makes sense (and only appears) once
// the provider is set to custom.
func TestSettingsRowsShowsAIBaseURLOnlyForCustomProvider(t *testing.T) {
	model := testModel()
	model.settings.AIProvider = aiProviderAnthropic
	if hasSettingsRow(model.settingsRows(), "AI base URL") {
		t.Fatal("AI base URL row present for provider anthropic, want it hidden")
	}
	model.settings.AIProvider = aiProviderCustom
	if !hasSettingsRow(model.settingsRows(), "AI base URL") {
		t.Fatal("AI base URL row missing for provider custom, want it shown")
	}
}

func hasSettingsRow(rows []settingsRow, label string) bool {
	for _, row := range rows {
		if row.label == label {
			return true
		}
	}
	return false
}

// TestSettingsTextEditRoundTrip covers the new inline text-editing flow for
// the AI model/API key/base URL rows: entering edit mode, typing, and
// committing with enter actually updates settingsDraft; esc discards.
func TestSettingsTextEditRoundTrip(t *testing.T) {
	model := testModel()
	model.overlay = overlaySettings
	rows := model.settingsRows()
	for i, row := range rows {
		if row.label == "AI API key" {
			model.settingsCursor = i
			break
		}
	}

	updated, _ := model.handleSettingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.settingsEditingField != "AI API key" {
		t.Fatalf("settingsEditingField = %q, want \"AI API key\"", model.settingsEditingField)
	}

	for _, r := range "sk-typed" {
		updated, _ = model.handleSettingsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(Model)
	}
	if model.settingsEditDraft != "sk-typed" {
		t.Fatalf("settingsEditDraft = %q, want sk-typed", model.settingsEditDraft)
	}

	updated, _ = model.handleSettingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.settingsEditingField != "" {
		t.Fatalf("settingsEditingField = %q after enter, want empty (edit committed)", model.settingsEditingField)
	}
	if model.settingsDraft.AIAPIKey != "sk-typed" {
		t.Fatalf("settingsDraft.AIAPIKey = %q, want sk-typed", model.settingsDraft.AIAPIKey)
	}

	// Esc during a second edit must discard, not commit.
	model.settingsEditingField = "AI API key"
	model.settingsEditDraft = "sk-typed"
	model.settingsEditCursor = len("sk-typed")
	updated, _ = model.handleSettingsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	model = updated.(Model)
	updated, _ = model.handleSettingsKey(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.settingsEditingField != "" {
		t.Fatalf("settingsEditingField = %q after esc, want empty", model.settingsEditingField)
	}
	if model.settingsDraft.AIAPIKey != "sk-typed" {
		t.Fatalf("settingsDraft.AIAPIKey = %q after esc, want unchanged sk-typed (edit discarded)", model.settingsDraft.AIAPIKey)
	}
}

// TestSettingsAPIKeyRowIsMaskedNotPlaintext guards the whole point of
// settingsRowSecretText: the stored key must never appear verbatim in the
// rendered Settings overlay, only as a fixed-length mask.
func TestSettingsAPIKeyRowIsMaskedNotPlaintext(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 45
	model.settings.AIAPIKey = "sk-super-secret-value"
	model.openSettingsOverlay()

	view := ansi.Strip(model.View())
	if strings.Contains(view, "sk-super-secret-value") {
		t.Fatalf("settings view leaked the raw API key:\n%s", view)
	}
	if !strings.Contains(view, "••••••••") {
		t.Fatalf("settings view missing the masked API key indicator:\n%s", view)
	}
}

func TestRecordAppLogOffSkipsInfoLines(t *testing.T) {
	model := testModel()
	model.settings.AppLog = appLogOff
	model.status, model.statusErr = "something happened", false
	model.recordAppLog("", false)
	if len(model.appLogLines) != 0 {
		t.Fatalf("appLogLines = %v, want no info-level lines recorded while AppLog is off", model.appLogLines)
	}
}

// TestRecordAppLogOffStillRecordsErrors guards the bug report this fixed:
// an error the user saw on the status page wasn't in the log because AppLog
// was off — you can't turn logging on ahead of an error you don't know is
// coming. Errors must be captured in memory regardless of the setting; off
// only opts out of routine info-level noise, not errors.
func TestRecordAppLogOffStillRecordsErrors(t *testing.T) {
	model := testModel()
	model.settings.AppLog = appLogOff
	model.status, model.statusErr = "create failed: boom", true
	model.recordAppLog("", false)
	if len(model.appLogLines) != 1 {
		t.Fatalf("appLogLines = %v, want the error recorded even while AppLog is off", model.appLogLines)
	}
	if !strings.Contains(model.appLogLines[0], "ERROR") || !strings.Contains(model.appLogLines[0], "boom") {
		t.Fatalf("appLogLines[0] = %q, want it tagged ERROR and containing the message", model.appLogLines[0])
	}
	// Still off, so it must not have been written to disk.
	if model.appLogFile != nil {
		t.Fatal("appLogFile opened while AppLog is off, want save-only")
	}
}

// TestRecordAppLogOnRecordsTransitionsOnly checks a line is appended only
// when the status bar actually changes — not on every call with the same
// status/statusErr repeated (which would otherwise spam a line per tick
// while an error is held on screen).
func TestRecordAppLogOnRecordsTransitionsOnly(t *testing.T) {
	model := testModel()
	model.settings.AppLog = appLogOn

	model.status, model.statusErr = "creating foo", false
	model.recordAppLog("", false)
	if len(model.appLogLines) != 1 {
		t.Fatalf("appLogLines = %v, want exactly 1 entry after the first transition", model.appLogLines)
	}
	if !strings.Contains(model.appLogLines[0], "INFO") || !strings.Contains(model.appLogLines[0], "creating foo") {
		t.Fatalf("appLogLines[0] = %q, want it to contain INFO and the status text", model.appLogLines[0])
	}

	// Same status/statusErr repeated (as if the same message were held
	// across several routine Update calls) must not append again.
	model.recordAppLog(model.status, model.statusErr)
	if len(model.appLogLines) != 1 {
		t.Fatalf("appLogLines grew to %v after a repeated identical status, want still 1", model.appLogLines)
	}

	prevStatus, prevErr := model.status, model.statusErr
	model.status, model.statusErr = "create failed: boom", true
	model.recordAppLog(prevStatus, prevErr)
	if len(model.appLogLines) != 2 {
		t.Fatalf("appLogLines = %v, want a second entry after a real transition", model.appLogLines)
	}
	if !strings.Contains(model.appLogLines[1], "ERROR") {
		t.Fatalf("appLogLines[1] = %q, want it tagged ERROR for an error status", model.appLogLines[1])
	}
}

// TestRecordAppLogSaveWritesFile checks save mode both keeps the in-memory
// buffer (same as on) and appends to whatthedock.log next to settings.json.
func TestRecordAppLogSaveWritesFile(t *testing.T) {
	dir := t.TempDir()
	model := testModel()
	model.settingsPath = filepath.Join(dir, "settings.json")
	model.settings.AppLog = appLogSave

	model.status, model.statusErr = "applying compose service web", false
	model.recordAppLog("", false)

	if len(model.appLogLines) != 1 {
		t.Fatalf("appLogLines = %v, want 1 entry in save mode too", model.appLogLines)
	}
	if model.appLogFile != nil {
		defer model.appLogFile.Close()
	}

	data, err := os.ReadFile(filepath.Join(dir, "whatthedock.log"))
	if err != nil {
		t.Fatalf("reading whatthedock.log: %v", err)
	}
	if !strings.Contains(string(data), "applying compose service web") {
		t.Fatalf("whatthedock.log content = %q, want it to contain the logged status", data)
	}
}

// TestUpdateWrapperRecordsAppLog checks the real Update entry point (not
// recordAppLog called directly) records a line when a message sets an error
// status — this is what every one of Update's existing status-setting call
// sites gets for free from the thin wrapper, without having been touched
// individually.
func TestUpdateWrapperRecordsAppLog(t *testing.T) {
	model := testModel()
	model.settings.AppLog = appLogOn

	updated, _ := model.Update(snapshotMsg{err: errors.New("docker daemon unreachable")})
	next := updated.(Model)
	if len(next.appLogLines) == 0 {
		t.Fatal("appLogLines empty after an error-producing message, want it recorded")
	}
	last := next.appLogLines[len(next.appLogLines)-1]
	if !strings.Contains(last, "ERROR") {
		t.Fatalf("last appLogLines entry = %q, want it tagged ERROR", last)
	}
}

func TestCycleSettingAppLog(t *testing.T) {
	model := testModel()
	rows := model.settingsRows()
	index := -1
	for i, row := range rows {
		if row.label == "App log" {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatal("settingsRows() has no \"App log\" row")
	}
	if model.settingsDraft.AppLog != appLogOff {
		t.Fatalf("initial AppLog = %v, want appLogOff", model.settingsDraft.AppLog)
	}
	model.cycleSetting(index, 1)
	if model.settingsDraft.AppLog != appLogOn {
		t.Fatalf("after one cycle, AppLog = %v, want appLogOn", model.settingsDraft.AppLog)
	}
	model.cycleSetting(index, 1)
	if model.settingsDraft.AppLog != appLogSave {
		t.Fatalf("after two cycles, AppLog = %v, want appLogSave", model.settingsDraft.AppLog)
	}
	model.cycleSetting(index, 1)
	if model.settingsDraft.AppLog != appLogOff {
		t.Fatalf("after three cycles, AppLog = %v, want it wrapping back to appLogOff", model.settingsDraft.AppLog)
	}
}

// TestSettingsViewAppLogOpensOverlay checks selecting the "View app log"
// action row opens overlayAppLog instead of cycling a value (it's an
// action row, not a setting).
func TestSettingsViewAppLogOpensOverlay(t *testing.T) {
	model := testModel()
	model.overlay = overlaySettings
	rows := model.settingsRows()
	for i, row := range rows {
		if row.label == "View app log" {
			model.settingsCursor = i
			break
		}
	}
	updated, _ := model.handleSettingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(Model)
	if next.overlay != overlayAppLog {
		t.Fatalf("overlay = %v after selecting View app log, want overlayAppLog", next.overlay)
	}
}
