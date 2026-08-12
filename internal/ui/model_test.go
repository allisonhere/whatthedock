package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	starts     int
	stops      int
	restarts   int
}

func (f *fakeProvider) Host() domain.Host          { return f.host }
func (f *fakeProvider) Ping(context.Context) error { return nil }
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
	if view := ansi.Strip(model.View()); strings.Contains(view, "x clear") || !strings.Contains(view, "line-05") {
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
	for _, want := range []string{"▓▓▓▓░", "▂▃", "↗ 72.0%", "↗ +16.0 MiB", "↗ +4"} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() missing hybrid stats treatment %q:\n%s", want, view)
		}
	}
	if strings.Count(rawView, "\x1b[") < 8 {
		t.Fatalf("View() missing heat-colored graph styling:\n%s", rawView)
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
	for _, want := range []string{"whatthedock · settings", "Stats", "Graph style", "wave", "Logs", "Log color", "full", "Behavior", "Maintenance", "Reset defaults"} {
		if !strings.Contains(view, want) {
			t.Fatalf("settings view missing %q:\n%s", want, view)
		}
	}
	if model.settingsCursor != 1 {
		t.Fatalf("settingsCursor = %d, want first actionable row 1", model.settingsCursor)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.settings.GraphStyle != graphStyleBlocks {
		t.Fatalf("GraphStyle = %v, want blocks", model.settings.GraphStyle)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.settings.GraphColor != graphColorMetric {
		t.Fatalf("GraphColor = %v, want metric", model.settings.GraphColor)
	}
}

func TestSettingsResetDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	model := testModel()
	model.settingsPath = path
	model.overlay = overlaySettings
	model.settingsCursor = len(model.settingsRows()) - 1
	model.settings.GraphStyle = graphStyleBraille
	model.settings.GraphColor = graphColorMono
	model.settings.LogColor = logColorMono
	model.settings.ShowDeltas = false
	model.settings.StatsRefresh = 5 * time.Second
	model.settings.DefaultActivity = activityStats

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.settings != defaultSettings() {
		t.Fatalf("settings = %#v, want defaults %#v", model.settings, defaultSettings())
	}
	saved, err := config.LoadSettings(path)
	if err != nil {
		t.Fatalf("LoadSettings() err = %v", err)
	}
	if saved.GraphStyle != "wave" || saved.GraphColor != "gradient" || saved.LogColor != "full" || saved.StatsRefresh != "2s" || saved.DefaultActivity != "problems" {
		t.Fatalf("saved settings = %#v, want defaults", saved)
	}
	if model.status != "settings reset to defaults" || model.statusErr {
		t.Fatalf("status/statusErr = %q/%v, want reset status", model.status, model.statusErr)
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
	model.overlay = overlaySettings
	model.settingsCursor = model.firstSettingsRow()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	saved, err := config.LoadSettings(path)
	if err != nil {
		t.Fatalf("LoadSettings() err = %v", err)
	}
	if saved.GraphStyle != "blocks" {
		t.Fatalf("saved GraphStyle = %q, want blocks", saved.GraphStyle)
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
	model.overlay = overlaySettings

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
