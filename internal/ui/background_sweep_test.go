package ui

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/allisonhere/tideui"
	"github.com/allisonhere/whatthedock/internal/domain"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func themeByNameForTest(t *testing.T, name string) tideui.Theme {
	t.Helper()
	if name == "whatthedock" {
		return whatthedockTheme()
	}
	for _, th := range tideui.BuiltinThemes {
		if th.Name == name {
			return th
		}
	}
	t.Fatalf("no builtin theme named %q", name)
	return tideui.Theme{}
}

// bareResetThenText matches an SGR reset immediately followed by a
// printable, non-escape character — text with no active style at all
// right after a reset. This is the exact shape of the "stray background"
// bug class this app has hit repeatedly: text built by concatenating
// several independently-`.Render()`ed segments (each emitting its own
// absolute reset) with plain, unstyled runs in between, all embedded
// inside one outer already-open background style. The reset wipes out
// that outer background for everything after it, until the next colored
// segment re-establishes one — so any plain run between two colored
// segments, or after the last one, falls through to the terminal's raw
// default instead of the theme's own color. Confirmed and fixed twice
// this session (renderProblemsSplit/renderProblemInsight via
// lipgloss.JoinVertical's own unstyled re-padding; renderLogLine and
// highlightComposeYAML via self-resetting per-token Render() calls) — see
// dashboardWarnPct and foregroundSpan/backgroundSpan/foregroundSpanDefault
// for the established fix (restore explicitly, never reset).
var bareResetThenText = regexp.MustCompile(`\x1b\[0?m([^\x1b\n]+)`)

func scanForBareReset(t *testing.T, label, s string) {
	t.Helper()
	locs := bareResetThenText.FindAllStringSubmatchIndex(s, -1)
	for _, loc := range locs {
		start := max(0, loc[0]-40)
		end := min(len(s), loc[1]+40)
		t.Errorf("[%s] bare reset immediately followed by unstyled text at byte %d — falls through to the terminal's raw default background instead of the theme's own: ...%q...", label, loc[0], s[start:end])
	}
}

// TestBackgroundStaysExplicitAcrossScreensAndThemes renders every overlay/
// mode this app has, under both dark and light themes, and fails if any
// rendered line shows the "stray background" bug bareResetThenText
// detects (see its own doc comment). Light themes are included
// deliberately: a gap that falls through to a near-black terminal default
// is invisible against this app's own dark themes but glaringly obvious
// against a light one, which is how several real instances of this bug
// were first spotted.
func TestBackgroundStaysExplicitAcrossScreensAndThemes(t *testing.T) {
	original := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(original)

	base := testModelWithSelectedContainer()
	base.width, base.height = 130, 36

	scenarios := []struct {
		label string
		setup func(*Model)
	}{
		{"tree/logs default", func(m *Model) {}},
		{"logs no selection", func(m *Model) { m.selected = nil; m.mode = activityLogs; m.focus = paneActivity }},
		{"logs with lines", func(m *Model) {
			m.mode = activityLogs
			m.focus = paneActivity
			m.logLines = []string{"2024-01-01T12:00:00Z GET /api/v3/queue 200 trailing plain text here"}
		}},
		{"problems", func(m *Model) { m.mode = activityProblems; m.focus = paneActivity }},
		{"problems AI analyzing", func(m *Model) {
			m.mode = activityProblems
			m.focus = paneActivity
			m.aiAnalysisFor = domain.ResourceID{Host: "local", ID: "1"}
			m.aiAnalyzing = true
		}},
		{"stats", func(m *Model) { m.mode = activityStats; m.focus = paneActivity }},
		{"inspector", func(m *Model) { m.focus = paneInspector }},
		{"help overlay", func(m *Model) { m.overlay = overlayHelp }},
		{"filter overlay", func(m *Model) { m.overlay = overlayFilter }},
		{"log filter overlay", func(m *Model) { m.mode = activityLogs; m.overlay = overlayLogFilter }},
		{"settings overlay", func(m *Model) { m.overlay = overlaySettings }},
		{"command palette", func(m *Model) { m.overlay = overlayCommandPalette }},
		{"theme picker", func(m *Model) { m.overlay = overlayThemePicker }},
		{"copy overlay", func(m *Model) { m.overlay = overlayCopy }},
		{"open overlay", func(m *Model) { m.overlay = overlayOpen }},
		{"systems overlay", func(m *Model) { m.overlay = overlaySystems }},
		{"create overlay", func(m *Model) { m.overlay = overlayCreate }},
		{"create overlay compose populated", func(m *Model) {
			m.overlay = overlayCreate
			m.createDraft.Mode = createModeCompose
			m.createDraft.Project = "media"
			m.createDraft.Service = "radarr"
			m.createDraft.Image = "lscr.io/linuxserver/radarr:latest"
			m.createDraft.Ports = "7878:7878"
			m.createDraft.Mounts = "/srv/radarr:/config"
			m.createDraft.Env = "PUID=1000\nPGID=1000\nTZ=Etc/UTC"
			m.createDraft.Restart = "unless-stopped"
			m.createDraft.OverrideRaw = "# override\nservices:\n  radarr:\n    environment:\n      - \"FOO=bar\"\n"
			m.createDraft.OverrideRawSet = true
		}},
		{"about", func(m *Model) { m.overlay = overlayAbout }},
		{"delete overlay", func(m *Model) { m.overlay = overlayDelete }},
		{"replicate overlay", func(m *Model) { m.overlay = overlayReplicate }},
		{"app log overlay", func(m *Model) { m.overlay = overlayAppLog }},
		{"dashboard", func(m *Model) { m.overlay = overlayDashboard }},
		{"dashboard many containers", func(m *Model) {
			m.overlay = overlayDashboard
			for i := 0; i < 15; i++ {
				id := domain.ResourceID{Host: "local", ID: fmt.Sprintf("extra-%d", i)}
				m.snapshot.Standalone = append(m.snapshot.Standalone, domain.Container{
					ID: id, Name: fmt.Sprintf("svc-%d", i), State: domain.StateRunning,
				})
			}
		}},
	}

	for _, theme := range []string{"whatthedock", "catppuccin-latte", "gruvbox-light"} {
		for _, sc := range scenarios {
			m := base
			m.theme = themeByNameForTest(t, theme)
			sc.setup(&m)
			scanForBareReset(t, theme+" / "+sc.label, m.View())
		}
	}
}
