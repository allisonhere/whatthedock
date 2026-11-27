package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	whatthedock "github.com/allisonhere/whatthedock"
)

const sampleChangelog = `# Changelog

Intro paragraph that the overlay skips.

## [Unreleased]

### Added
- Short item.
- A longer item that is hard-wrapped in the source
  and continues on a second source line with ` + "`code`" + `.

### Fixed
- Bug.

## [0.1.2] - 2026-01-02

### Fixed
- Old bug.
`

func TestChangelogLayoutSkipsIntroAndJoinsWrappedItems(t *testing.T) {
	lines := changelogLayout(sampleChangelog, 200)

	var kinds, texts []string
	for _, l := range lines {
		kinds = append(kinds, map[changelogKind]string{
			changelogBlank: "blank", changelogRelease: "release", changelogSection: "section",
			changelogBullet: "bullet", changelogMore: "more",
		}[l.kind])
		texts = append(texts, l.text)
	}
	if strings.Contains(strings.Join(texts, "\n"), "Intro paragraph") || strings.Contains(strings.Join(texts, "\n"), "# Changelog") {
		t.Fatalf("title/intro leaked into the layout: %q", texts)
	}
	wantKinds := "release,blank,section,bullet,bullet,blank,section,bullet,blank,release,blank,section,bullet"
	if got := strings.Join(kinds, ","); got != wantKinds {
		t.Fatalf("kinds = %s\nwant    %s", got, wantKinds)
	}
	if texts[0] != "Unreleased" || texts[9] != "0.1.2  ·  2026-01-02" {
		t.Fatalf("release headings = %q / %q, want \"Unreleased\" and \"0.1.2  ·  2026-01-02\"", texts[0], texts[9])
	}
	if want := "A longer item that is hard-wrapped in the source and continues on a second source line with `code`."; texts[4] != want {
		t.Fatalf("joined item = %q, want %q", texts[4], want)
	}
}

func TestChangelogLayoutRewrapsToTheWidth(t *testing.T) {
	lines := changelogLayout(sampleChangelog, 30)
	var item []string
	for _, l := range lines {
		if l.kind == changelogMore {
			item = append(item, l.text)
		}
		if lipgloss.Width(l.text) > 30 {
			t.Fatalf("line %q is wider than the 30-column wrap width", l.text)
		}
	}
	if len(item) == 0 {
		t.Fatal("a long item at 30 columns should wrap onto continuation lines")
	}
}

func TestEmbeddedChangelogIsBundledAndRenders(t *testing.T) {
	if strings.TrimSpace(whatthedock.Changelog) == "" {
		t.Fatal("embedded CHANGELOG.md is empty")
	}
	lines := changelogLayout(whatthedock.Changelog, 80)
	releases := 0
	for _, l := range lines {
		if l.kind == changelogRelease {
			releases++
		}
	}
	if releases < 2 {
		t.Fatalf("embedded changelog produced %d release headings, want several", releases)
	}
}

func openChangelogFromSettings(t *testing.T) Model {
	t.Helper()
	model := testModel()
	model.width, model.height = 100, 30
	model.overlay = overlaySettings
	model.settingsPaneFocus = true
	for i, row := range model.settingsRows() {
		if row.label == "View changelog" {
			model.settingsCursor = i
		}
	}
	updated, _ := model.handleSettingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	return updated.(Model)
}

func TestSettingsViewChangelogOpensAndEscReturnsToSettings(t *testing.T) {
	model := openChangelogFromSettings(t)
	if model.overlay != overlayChangelog {
		t.Fatalf("overlay = %v after selecting View changelog, want overlayChangelog", model.overlay)
	}
	view := ansi.Strip(model.View())
	if !strings.Contains(view, "whatthedock · changelog") || !strings.Contains(view, "Unreleased") {
		t.Fatalf("changelog view missing its title or the Unreleased heading:\n%s", view)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got := updated.(Model).overlay; got != overlaySettings {
		t.Fatalf("overlay after esc = %v, want it back on settings", got)
	}
}

func TestChangelogOverlayScrollsAndClamps(t *testing.T) {
	model := openChangelogFromSettings(t)
	total, budget := model.changelogLineCount(), model.changelogBodyBudget()
	if total <= budget {
		t.Fatalf("test needs a changelog taller than the panel (%d lines, budget %d)", total, budget)
	}

	press := func(m Model, key tea.KeyMsg) Model {
		updated, _ := m.Update(key)
		return updated.(Model)
	}
	model = press(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if model.changelogScroll != 1 {
		t.Fatalf("scroll after j = %d, want 1", model.changelogScroll)
	}
	model = press(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	model = press(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if model.changelogScroll != 0 {
		t.Fatalf("scroll above the top = %d, want clamped to 0", model.changelogScroll)
	}
	model = press(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	if want := total - budget; model.changelogScroll != want {
		t.Fatalf("scroll after G = %d, want the last page (%d)", model.changelogScroll, want)
	}
	model = press(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if model.changelogScroll != 0 {
		t.Fatalf("scroll after g = %d, want 0", model.changelogScroll)
	}
}
