package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/allisonhere/tideui"

	"github.com/allisonhere/whatthedock/internal/domain"
)

func dashRenderer() tideui.Renderer {
	return tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
}

// stackedModel is a model whose running containers belong to stacks:
// media/{radarr,sonarr}, apps/{grafana}, and one standalone proxy.
func stackedModel(t *testing.T) Model {
	t.Helper()
	model := testModel()
	model.snapshot.Standalone = nil
	model.snapshot.Projects = nil
	add := func(name, project string, cpu float64) {
		id := domain.ResourceID{Host: "local", ID: name}
		model.snapshot.Standalone = append(model.snapshot.Standalone, domain.Container{
			ID: id, Name: name, State: domain.StateRunning, Status: "Up 3 days",
			Compose: domain.ComposeRef{Project: project},
		})
		model.appendStats(domain.ContainerStats{ID: id, CPUPercent: cpu, MemoryUsage: 10, MemoryLimit: 100})
	}
	add("sonarr", "media", 5)
	add("radarr", "media", 50)
	add("grafana", "apps", 20)
	add("proxy", "", 70)
	return model
}

func TestCompactUptime(t *testing.T) {
	cases := map[string]string{
		"Up 3 days":                       "3d",
		"Up 36 hours (healthy)":           "36h",
		"Up About an hour":                "1h",
		"Up About a minute":               "1m",
		"Up 12 seconds":                   "12s",
		"Up Less than a second":           "<1s",
		"Up 2 weeks":                      "2w",
		"Up 11 months":                    "11mo",
		"Up 3 years":                      "3y",
		"Up 5 hours (Paused)":             "5h",
		"Up 7 minutes (health: starting)": "7m",
		"Exited (0) 2 hours ago":          "",
		"Restarting (1) 3 seconds ago":    "",
		"Created":                         "",
		"Up soon":                         "",
	}
	for status, want := range cases {
		if got := compactUptime(status); got != want {
			t.Errorf("compactUptime(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestDashboardGroupedOrderIsStacksAlphabeticalStandaloneLast(t *testing.T) {
	model := stackedModel(t)
	model.dashboardGrouped = true
	model.dashboardSort = dashboardSortCPU // within a stack: heaviest first

	if got := strings.Join(runningNames(model), ","); got != "grafana,radarr,sonarr,proxy" {
		t.Fatalf("grouped order = %s, want apps, media (heaviest first), then standalone", got)
	}
	model.dashboardGrouped = false
	if got := strings.Join(runningNames(model), ","); got != "proxy,radarr,grafana,sonarr" {
		t.Fatalf("flat order = %s, want a plain CPU sort", got)
	}
}

func TestDashboardListLinesInsertsOneHeaderPerStack(t *testing.T) {
	model := stackedModel(t)
	model.dashboardGrouped = true
	running, _ := model.dashboardRunningContainers()

	lines := model.dashboardListLines(running)
	var kinds []string
	for _, line := range lines {
		if line.isHeader() {
			kinds = append(kinds, "H:"+line.group)
		} else {
			kinds = append(kinds, running[line.index].DisplayName())
		}
	}
	want := "H:apps,grafana,H:media,radarr,sonarr,H:,proxy"
	if got := strings.Join(kinds, ","); got != want {
		t.Fatalf("lines = %s, want %s", got, want)
	}

	model.dashboardGrouped = false
	if got := len(model.dashboardListLines(running)); got != len(running) {
		t.Fatalf("ungrouped lines = %d, want one per container (%d)", got, len(running))
	}
}

// Headers cost a line each, and a header must never be left at the bottom
// with no container under it.
func TestDashboardVisiblePlanCountsHeadersAndNeverStrandsOne(t *testing.T) {
	model := stackedModel(t)
	model.dashboardGrouped = true
	running, _ := model.dashboardRunningContainers() // 3 stacks + 4 containers = 7 lines

	shown, more := dashboardVisiblePlan(running, 7, true)
	if len(shown) != 4 || more != 0 {
		t.Fatalf("budget 7: shown=%d more=%d, want everything", len(shown), more)
	}
	// Budget 6: reserve a line for "N more", leaving 5 — apps(2) + media(3)
	// fit as grafana, header, radarr, sonarr... only 2+3=5 lines: 3 containers.
	shown, more = dashboardVisiblePlan(running, 6, true)
	if len(shown) != 3 || more != 1 {
		t.Fatalf("budget 6: shown=%d more=%d, want 3 shown and 1 more", len(shown), more)
	}
	// Budget 5 leaves 4 lines: apps+grafana (2) + media header (1) +
	// radarr (1). Taking sonarr would overflow; the standalone header must
	// not be shown without its container.
	shown, more = dashboardVisiblePlan(running, 5, true)
	if len(shown) != 2 || more != 2 {
		t.Fatalf("budget 5: shown=%d more=%d, want 2 shown and 2 more", len(shown), more)
	}
	lines := model.dashboardListLines(shown)
	if last := lines[len(lines)-1]; last.isHeader() {
		t.Fatalf("plan ends on a stranded stack header: %#v", lines)
	}

	// Ungrouped, a plan is just the old one-line-per-container budget.
	flat, flatMore := dashboardVisiblePlan(running, 3, false)
	if len(flat) != 2 || flatMore != 2 {
		t.Fatalf("ungrouped budget 3: shown=%d more=%d, want 2 shown and 2 more", len(flat), flatMore)
	}
}

// The header block's height is a contract: dashboardBodyPlan and the mouse
// hit test place the list and the bottom row by it, so it must equal what is
// actually rendered — in both header layouts, grouped or not.
func TestDashboardStatusRowLandsWhereThePlanSaysItDoes(t *testing.T) {
	cases := []struct {
		name          string
		width, height int
		grouped       bool
		cards         bool
	}{
		{"cards", 140, 40, false, true},
		{"cards grouped", 140, 40, true, true},
		{"compact (short)", 140, 14, false, false},
		{"compact (narrow)", 80, 40, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := stackedModel(t)
			model.width, model.height = tc.width, tc.height
			model.dashboardGrouped = tc.grouped
			model.overlay = overlayDashboard
			if model.dashboardUsesCards() != tc.cards {
				t.Fatalf("dashboardUsesCards() = %v, want %v", model.dashboardUsesCards(), tc.cards)
			}

			_, _, _, statusIdx, _ := model.dashboardBodyPlan()
			boxTop, statusLine := -1, -1
			for y, line := range strings.Split(ansi.Strip(model.View()), "\n") {
				switch {
				case boxTop < 0 && strings.Contains(line, "whatthedock · dashboard"):
					boxTop = y
				case boxTop >= 0 && statusLine < 0 && (strings.Contains(line, "need attention") || strings.Contains(line, "needs attention") || strings.Contains(line, "All monitored")):
					statusLine = y
				}
			}
			if boxTop < 0 || statusLine < 0 {
				t.Fatalf("couldn't find the box top / status row in the view")
			}
			if got := statusLine - (boxTop + 2); got != statusIdx {
				t.Fatalf("status row renders at body line %d, plan says %d", got, statusIdx)
			}
		})
	}
}

func TestDashboardCardsAreExactlyTheRequestedWidth(t *testing.T) {
	renderer := dashRenderer()
	model := stackedModel(t)
	model.fleetCPUHistory = []float64{10, 40, 90}
	model.fleetNetHistory = []uint64{0, 1 << 20, 4 << 20}
	summary := model.fleetSummary()
	summary.cpuCores = 8
	summary.memUsed, summary.memLimit = 3<<30, 8<<30
	attention := []attentionItem{{name: "pg", reason: "unhealthy", rank: 1}}

	for _, width := range []int{92, 93, 100, 111, 156} {
		lines := model.dashboardCards(renderer, summary, attention, 0, width)
		if len(lines) != dashboardCardRows {
			t.Fatalf("width %d: %d card lines, want %d", width, len(lines), dashboardCardRows)
		}
		for i, line := range lines {
			if got := lipgloss.Width(line); got != width {
				t.Fatalf("width %d line %d: width = %d, want exactly %d\n%q", width, i, got, width, ansi.Strip(line))
			}
		}
	}

	plain := ansi.Strip(strings.Join(model.dashboardCards(renderer, summary, attention, 0, 120), "\n"))
	for _, want := range []string{"╭─ CPU", "╭─ MEM", "╭─ NET", "╭─ FLEET", "of 8 cores", "3.0 GiB / 8.0 GiB  38%"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("cards missing %q:\n%s", want, plain)
		}
	}
}

func TestDashboardStatusBarProportionsAndFloor(t *testing.T) {
	original := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(original)

	renderer := dashRenderer()
	const width = 40
	summary := dashboardSummary{counts: map[string]int{"●": 97, "!": 1, "○": 2}}

	bar := ansi.Strip(dashboardStatusBar(renderer, summary, width, "#000000"))
	if got := lipgloss.Width(bar); got != width {
		t.Fatalf("bar width = %d, want %d", got, width)
	}
	// A lone unhealthy container among 100 must still get a cell of its
	// own: one run per present status, each in its own colour.
	only := dashboardSummary{counts: map[string]int{"●": 99, "!": 1}}
	raw := dashboardStatusBar(renderer, only, width, "#000000")
	if strings.Count(raw, "━") != width {
		t.Fatalf("bar cells = %d, want %d", strings.Count(raw, "━"), width)
	}
	if segments := strings.Count(raw, "\x1b[0m"); segments != 2 {
		t.Fatalf("styled segments = %d, want 2 (running + unhealthy): %q", segments, raw)
	}
	if !strings.HasSuffix(ansi.Strip(raw), "━") || strings.Count(strings.SplitAfter(raw, "\x1b[0m")[1], "━") != 1 {
		t.Fatalf("the unhealthy segment should be exactly one cell: %q", raw)
	}

	empty := ansi.Strip(dashboardStatusBar(renderer, dashboardSummary{counts: map[string]int{}}, 10, "#000000"))
	if strings.Trim(empty, "─") != "" {
		t.Fatalf("empty fleet should draw a dim track, got %q", empty)
	}
}

func TestDashboardRowShowsUptimeAndRestartsWhenThereIsRoom(t *testing.T) {
	renderer := dashRenderer()
	model := stackedModel(t)
	var ctr domain.Container
	for _, c := range model.snapshotContainers() {
		if c.DisplayName() == "radarr" {
			ctr = c
		}
	}
	model.dashboardMeta = map[domain.ResourceID]dashboardMeta{ctr.ID: {restarts: 3}}

	wide := ansi.Strip(model.dashboardRow(renderer, ctr, 140, false, false))
	if !strings.Contains(wide, "3d") || !strings.Contains(wide, "↻3") {
		t.Fatalf("wide row = %q, want uptime 3d and ↻3", wide)
	}
	header := ansi.Strip(model.dashboardHeaderRow(renderer, 140))
	if !strings.Contains(header, "UP") || !strings.Contains(header, "RST") {
		t.Fatalf("wide header = %q, want UP and RST labels", header)
	}

	// Header and row agree on where the columns are.
	column := func(s, needle string) int { return lipgloss.Width(s[:strings.Index(s, needle)]) }
	if column(header, "RST")+len("RST") != column(wide, "↻3")+lipgloss.Width("↻3") {
		t.Fatalf("RST header and ↻3 data misaligned:\n%q\n%q", header, wide)
	}

	narrow := ansi.Strip(model.dashboardRow(renderer, ctr, 80, false, false))
	if strings.Contains(narrow, "↻") || strings.Contains(narrow, "3d") {
		t.Fatalf("narrow row = %q, want no uptime/restart columns", narrow)
	}

	// Unknown restarts render blank, zero as a dash, never a fake number.
	model.dashboardMeta = nil
	unknown := ansi.Strip(model.dashboardRow(renderer, ctr, 140, false, false))
	if strings.Contains(unknown, "↻") || strings.Contains(unknown, "–") {
		t.Fatalf("row with unknown restarts = %q, want a blank restart cell", unknown)
	}
	model.dashboardMeta = map[domain.ResourceID]dashboardMeta{ctr.ID: {restarts: 0}}
	if zero := ansi.Strip(model.dashboardRow(renderer, ctr, 140, false, false)); !strings.Contains(zero, "–") {
		t.Fatalf("row with zero restarts = %q, want a dash", zero)
	}
}

func TestDashboardRowStripeAndSelectionChangeOnlyBackground(t *testing.T) {
	original := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(original)

	renderer := dashRenderer()
	model := stackedModel(t)
	ctr := model.snapshotContainers()[0]

	plain := model.dashboardRow(renderer, ctr, 120, false, false)
	striped := model.dashboardRow(renderer, ctr, 120, false, true)
	selected := model.dashboardRow(renderer, ctr, 120, true, false)
	if plain == striped {
		t.Fatal("striped row is byte-identical to the plain row; the band has no effect")
	}
	if plain == selected {
		t.Fatal("selected row is byte-identical to the plain row")
	}
	if ansi.Strip(plain) != ansi.Strip(striped) {
		t.Fatalf("striping changed the text:\n%q\n%q", ansi.Strip(plain), ansi.Strip(striped))
	}
}

func TestDashboardGroupRowRollsUpTheStack(t *testing.T) {
	renderer := dashRenderer()
	model := stackedModel(t)
	// A stopped member counts toward the stack's rollup and attention badge.
	model.snapshot.Standalone = append(model.snapshot.Standalone, domain.Container{
		ID: domain.ResourceID{Host: "local", ID: "old"}, Name: "old", State: domain.StateExited,
		Compose: domain.ComposeRef{Project: "media"},
	})

	row := ansi.Strip(model.dashboardGroupRow(renderer, "media", 110))
	for _, want := range []string{"▾ media", "2 running", "1 stopped", "CPU 55.0%", "⚠ 1"} {
		if !strings.Contains(row, want) {
			t.Fatalf("group row = %q, missing %q", row, want)
		}
	}
	if got := lipgloss.Width(row); got != 110 {
		t.Fatalf("group row width = %d, want exactly 110", got)
	}
	if standalone := ansi.Strip(model.dashboardGroupRow(renderer, "", 110)); !strings.Contains(standalone, "▾ standalone") {
		t.Fatalf("standalone header = %q", standalone)
	}
	if tight := ansi.Strip(model.dashboardGroupRow(renderer, "media", 20)); lipgloss.Width(tight) > 20 {
		t.Fatalf("tight group row overflows: %q", tight)
	}
}

func TestDashboardGKeyTogglesGroupingAndCursorFollows(t *testing.T) {
	model := stackedModel(t)
	model.width, model.height = 140, 40
	model.overlay = overlayDashboard
	model.dashboardMoveCursor(1)
	shown, _, _, _, _ := model.dashboardBodyPlan()
	before := shown[model.dashboardSelectedIndex(shown)].DisplayName()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	next := updated.(Model)
	if !next.dashboardGrouped || next.overlay != overlayDashboard {
		t.Fatalf("after g: grouped=%v overlay=%v, want grouped and still open", next.dashboardGrouped, next.overlay)
	}
	shown, _, _, _, _ = next.dashboardBodyPlan()
	if got := shown[next.dashboardSelectedIndex(shown)].DisplayName(); got != before {
		t.Fatalf("cursor moved from %q to %q when grouping changed the order", before, got)
	}

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if updated.(Model).dashboardGrouped {
		t.Fatal("second g should switch grouping back off")
	}
}

// Clicking a stack header does nothing, and the rows below it map to the
// right container despite the extra lines.
func TestDashboardHitTestSkipsStackHeaders(t *testing.T) {
	model := stackedModel(t)
	model.width, model.height = 140, 40
	model.overlay = overlayDashboard
	model.dashboardGrouped = true

	boxTop, boxLeft := -1, -1
	for y, line := range strings.Split(ansi.Strip(model.View()), "\n") {
		if strings.Contains(line, "whatthedock · dashboard") {
			boxTop, boxLeft = y, strings.Index(line, "╭")
			break
		}
	}
	if boxTop < 0 {
		t.Fatal("dashboard box not found")
	}
	bodyTop := boxTop + 2
	header := model.dashboardHeaderLineCount()
	click := func(line int) (int, bool, bool) {
		return model.dashboardHitTest(tea.MouseMsg{X: boxLeft + 6, Y: bodyTop + header + line, Button: tea.MouseButtonLeft})
	}

	if _, _, ok := click(0); ok {
		t.Fatal("clicking the first stack header was treated as a row")
	}
	row, isStatus, ok := click(1) // grafana, first container under "apps"
	shown, _, _, _, _ := model.dashboardBodyPlan()
	if !ok || isStatus || shown[row].DisplayName() != "grafana" {
		t.Fatalf("click on line 1: row=%d ok=%v status=%v, want grafana", row, ok, isStatus)
	}
	row, _, ok = click(4) // header(0) grafana(1) header(2) radarr(3) sonarr(4)
	if !ok || shown[row].DisplayName() != "sonarr" {
		t.Fatalf("click on line 4: row=%d ok=%v, want sonarr", row, ok)
	}
}

// The restart-count inspect is slow, so a poll only repeats it once the
// cached value is older than dashboardMetaTTL.
func TestDashboardRefreshInspectsOnlyWhenMetaIsMissingOrStale(t *testing.T) {
	model := testModel()
	model.overlay = overlayDashboard
	model.settings.StatsRefresh = time.Millisecond
	radarr := domain.ResourceID{Host: "local", ID: "1"}

	hasMeta := func(m Model) bool {
		t.Helper()
		batch, ok := runCmd(t, m.dashboardRefreshCmd()).(tea.BatchMsg)
		if !ok {
			t.Fatal("refresh cmd is not a batch")
		}
		for _, c := range batch {
			if msg, ok := c().(dashboardStatsMsg); ok {
				return msg.hasMeta
			}
		}
		t.Fatal("no dashboardStatsMsg in the batch")
		return false
	}

	if !hasMeta(model) {
		t.Fatal("first poll should inspect (no meta cached yet)")
	}
	model.dashboardMeta = map[domain.ResourceID]dashboardMeta{radarr: {restarts: 2, fetched: time.Now()}}
	if hasMeta(model) {
		t.Fatal("poll with fresh meta should not inspect again")
	}
	model.dashboardMeta[radarr] = dashboardMeta{restarts: 2, fetched: time.Now().Add(-2 * dashboardMetaTTL)}
	if !hasMeta(model) {
		t.Fatal("poll with stale meta should inspect again")
	}
}

func TestDashboardStatsMsgStoresRestartMeta(t *testing.T) {
	model := testModel()
	model.overlay = overlayDashboard
	id := domain.ResourceID{Host: "local", ID: "1"}

	updated, _ := model.Update(dashboardStatsMsg{stats: domain.ContainerStats{ID: id}, hasMeta: true, restarts: 4})
	got := updated.(Model)
	if count, known := got.dashboardRestartCount(domain.Container{ID: id}); !known || count != 4 {
		t.Fatalf("restart count = %d known=%v, want 4 known", count, known)
	}
	if _, known := got.dashboardRestartCount(domain.Container{ID: domain.ResourceID{Host: "local", ID: "other"}}); known {
		t.Fatal("a container with no meta and no restarts must read as unknown, not zero")
	}
}

// A row must span the panel: whatever width the fixed columns leave over
// goes to the bars and trends (or, in the text-only layouts, becomes padding
// before a right-aligned net figure), never blank space at the right edge.
func TestDashboardRowsSpanTheFullWidth(t *testing.T) {
	renderer := dashRenderer()
	model := stackedModel(t)
	ctr := model.snapshotContainers()[0]

	for _, width := range []int{66, 86, 92, 100, 112, 128, 130, 140, 154} {
		row := ansi.Strip(model.dashboardRow(renderer, ctr, width, false, false))
		if got := lipgloss.Width(row); got != width {
			t.Errorf("width %d: row is %d columns wide, want exactly %d", width, got, width)
		}
		if used := lipgloss.Width(strings.TrimRight(row, " ")); used < width-3 {
			t.Errorf("width %d: row's content ends at column %d, leaving %d blank columns on the right:\n%q", width, used, width-used, row)
		}
		header := ansi.Strip(model.dashboardHeaderRow(renderer, width))
		if lipgloss.Width(header) > width {
			t.Errorf("width %d: header is %d columns wide", width, lipgloss.Width(header))
		}
	}
}
