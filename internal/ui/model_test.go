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
	host       domain.Host
	snapshot   domain.Snapshot
	containers map[string]domain.Container
	stats      map[string]domain.ContainerStats
	pingErr    error
	starts     int
	stops      int
	restarts   int
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
	model.focus = paneTree
	model.mode = activityLogs
	ctr := model.provider.(*fakeProvider).containers["1"]
	model.selected = &ctr

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	model = updated.(Model)
	if model.focus != paneActivity || model.overlay != overlayLogFilter {
		t.Fatalf("focus/overlay = %v/%v, want activity/log filter", model.focus, model.overlay)
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
	model.width, model.height = 100, 30

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
	model.settingsCursor = len(model.settingsRows()) - 1

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
	for _, want := range []string{"Ctrl+S         save settings/forms", "S              systems", "Systems: enter switch, t test, a add, e edit, d delete"} {
		if !strings.Contains(view, want) {
			t.Fatalf("help view missing %q:\n%s", want, view)
		}
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
	if len(batch) != 2 {
		t.Fatalf("Init() batch length = %d, want snapshot refresh and event subscription", len(batch))
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
