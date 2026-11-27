package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/allisonhere/tideui"

	whatthedock "github.com/allisonhere/whatthedock"
)

// The changelog overlay (Settings > View changelog) shows the CHANGELOG.md
// embedded in the binary: release headings, section headings, and bullets
// re-wrapped to the panel so a narrow terminal never clips a line.

type changelogKind int

const (
	changelogBlank changelogKind = iota
	changelogRelease
	changelogSection
	changelogBullet // first line of an item
	changelogMore   // wrapped continuation of an item
)

type changelogLine struct {
	kind changelogKind
	text string
}

// changelogPanelWidth is the overlay's width; changelogWrapWidth is the width
// its text is wrapped to (the soft panel's padding and bullet gutter come off).
func (m Model) changelogPanelWidth() int { return min(100, max(44, m.width-8)) }
func (m Model) changelogWrapWidth() int  { return m.changelogPanelWidth() - 8 }

func (m Model) changelogBodyBudget() int { return m.softOverlayBodyBudget() }

// changelogLayout flattens CHANGELOG.md into display lines wrapped to width.
// The file's title and intro paragraph are skipped — the panel title already
// says what this is — and each bullet's hard-wrapped source lines are joined
// and wrapped afresh, so the layout follows the panel rather than the file.
func changelogLayout(text string, width int) []changelogLine {
	width = max(10, width)
	var out []changelogLine
	var item []string
	flush := func() {
		if len(item) == 0 {
			return
		}
		wrapped := strings.Split(ansi.Wrap(strings.Join(item, " "), width, " "), "\n")
		for i, line := range wrapped {
			kind := changelogMore
			if i == 0 {
				kind = changelogBullet
			}
			out = append(out, changelogLine{kind: kind, text: line})
		}
		item = nil
	}

	started := false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, " \t")
		switch {
		case strings.HasPrefix(line, "## "):
			flush()
			started = true
			if len(out) > 0 {
				out = append(out, changelogLine{kind: changelogBlank})
			}
			out = append(out, changelogLine{kind: changelogRelease, text: releaseHeading(strings.TrimPrefix(line, "## "))})
		case !started:
			// Title and intro before the first release.
		case strings.HasPrefix(line, "### "):
			flush()
			out = append(out, changelogLine{kind: changelogBlank}, changelogLine{kind: changelogSection, text: strings.TrimPrefix(line, "### ")})
		case strings.HasPrefix(line, "- "):
			flush()
			item = []string{strings.TrimPrefix(line, "- ")}
		case strings.HasPrefix(line, " ") && len(item) > 0:
			item = append(item, strings.TrimSpace(line))
		case strings.TrimSpace(line) == "":
			flush()
		}
	}
	flush()
	return out
}

// releaseHeading turns "[0.1.29] - 2026-10-18" into "0.1.29  ·  2026-10-18"
// and "[Unreleased]" into "Unreleased".
func releaseHeading(heading string) string {
	version, date, found := strings.Cut(heading, "] - ")
	version = strings.TrimPrefix(version, "[")
	version = strings.TrimSuffix(version, "]")
	if !found {
		return version
	}
	return version + "  ·  " + date
}

func (m Model) changelogLines() []changelogLine {
	return changelogLayout(whatthedock.Changelog, m.changelogWrapWidth())
}

func (m Model) changelogLineCount() int { return len(m.changelogLines()) }

func (m *Model) scrollChangelog(delta int) {
	maxScroll := max(0, m.changelogLineCount()-m.changelogBodyBudget())
	m.changelogScroll = clamp(m.changelogScroll+delta, 0, maxScroll)
}

// renderChangelogLine styles one display line. inCode carries the inline-code
// state across a wrapped item's lines, since a `code span` can break across
// them. Every segment sets the panel background itself: RenderSoftBody leaves
// any line that already contains an escape code unstyled.
func renderChangelogLine(renderer tideui.Renderer, line changelogLine, inCode *bool) string {
	panelBG := renderer.Styles.OverlayBody.GetBackground()
	textFg := styleForeground(renderer.Styles.OverlayBody, renderer.Styles.Theme.Fg)
	panel := lipgloss.NewStyle().Background(panelBG).Foreground(textFg)
	accent := renderer.Styles.Theme.Selected
	if accent == "" {
		accent = renderer.Styles.Theme.BorderFocus
	}

	switch line.kind {
	case changelogBlank:
		return panel.Render("")
	case changelogRelease:
		return lipgloss.NewStyle().Background(panelBG).Foreground(accent).Bold(true).Render(line.text)
	case changelogSection:
		return lipgloss.NewStyle().Background(panelBG).Foreground(textFg).Bold(true).Render(line.text)
	}

	gutter := panel.Render("  ")
	if line.kind == changelogBullet {
		gutter = lipgloss.NewStyle().Background(panelBG).Foreground(accent).Render("• ")
	}
	code := lipgloss.NewStyle().Background(panelBG).Foreground(activePalette.Info)
	var b strings.Builder
	b.WriteString(gutter)
	for i, part := range strings.Split(line.text, "`") {
		if i > 0 {
			*inCode = !*inCode
		}
		if part == "" {
			continue
		}
		if *inCode {
			b.WriteString(code.Render(part))
		} else {
			b.WriteString(panel.Render(part))
		}
	}
	return b.String()
}

// changelogOverlay renders the scrollable changelog panel.
func (m Model) changelogOverlay(renderer tideui.Renderer) *tideui.Overlay {
	width := m.changelogPanelWidth()
	lines := m.changelogLines()
	budget := m.changelogBodyBudget()
	scroll := clamp(m.changelogScroll, 0, max(0, len(lines)-budget))
	end := min(len(lines), scroll+budget)

	// Inline-code state depends on everything above the window, so walk from
	// the top and keep only the visible lines.
	var visible []string
	inCode := false
	for i, line := range lines[:end] {
		rendered := renderChangelogLine(renderer, line, &inCode)
		if i >= scroll {
			visible = append(visible, rendered)
		}
	}
	if len(lines) == 0 {
		visible = []string{"(no changelog is bundled with this build)"}
	}

	hints := []tideui.SoftHint{{Key: "esc/q", Label: "back"}}
	if len(lines) > budget {
		hints = append(hints, tideui.SoftHint{Key: "j/k", Label: fmt.Sprintf("scroll (%d/%d)", end, len(lines))})
		hints = append(hints, tideui.SoftHint{Key: "home/end", Label: "top/bottom"})
	}
	content := renderer.RenderSoftBody(width, strings.Join(visible, "\n")+"\n\n"+renderer.RenderSoftHints(width-4, hints...))
	overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "changelog", Content: content, Width: width})
	return &overlay
}
