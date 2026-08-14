package main

import (
	"fmt"
	"strings"

	"github.com/allisonhere/tideui"
)

var renderer = tideui.NewRenderer(tideui.CatppuccinMocha, tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})

func (m model) View() string {
	if m.quitting {
		return ""
	}
	width := 70
	if m.width > 0 {
		width = min(78, max(50, m.width-8))
	}
	var body string
	switch m.screen {
	case screenVersion:
		body = m.versionView(width)
	case screenConfirm:
		body = m.confirmView(width)
	case screenRunning, screenDone:
		body = m.runningView(width)
	}
	panel := renderer.RenderSoftPanel(tideui.SoftPanel{Prefix: "whatthedock", Title: "release", Content: body, Width: width})
	return panel + "\n"
}

func (m model) versionView(width int) string {
	meta := renderer.Styles.DetailMeta
	lines := []string{
		meta.Render("branch   " + m.branch),
		meta.Render("latest   " + orDash(m.latestTag)),
	}
	if m.diffStat != "" {
		lines = append(lines, meta.Render("since    "+m.diffStat))
	}
	if m.dirty {
		lines = append(lines, renderer.Styles.StatusError.Render(" working tree has uncommitted changes "))
	}
	if m.dryRun {
		lines = append(lines, renderer.Styles.StatusNotice.Render(" dry run — no tag, push, or release will actually happen "))
	}

	if len(m.commits) > 0 {
		header := "Commits since " + m.latestTag + ":"
		if m.latestTag == "" {
			header = "Recent commits:"
		}
		lines = append(lines, "", meta.Render(header))
		for _, c := range m.commits {
			lines = append(lines, "  "+commitLine(c, width-6))
		}
	} else {
		lines = append(lines, "", meta.Render("No commits since "+m.latestTag+" — nothing new to release."))
	}

	lines = append(lines, "", "Version to release:", renderer.Styles.InputFocused.Width(max(20, width-12)).Render(m.versionInput+"█"))
	lines = append(lines, "", renderer.RenderSoftHints(width-4,
		tideui.SoftHint{Key: "enter", Label: "continue"},
		tideui.SoftHint{Key: "esc", Label: "quit"},
	))
	return renderer.RenderSoftBody(width, strings.Join(lines, "\n"))
}

func (m model) confirmView(width int) string {
	meta := renderer.Styles.DetailMeta
	lines := []string{meta.Render("This will:")}
	for _, s := range m.steps {
		lines = append(lines, "  "+s.label)
	}
	if m.dirty {
		lines = append(lines, "", renderer.Styles.StatusError.Render(" working tree has uncommitted changes — the tag will still point at HEAD "))
	}
	lines = append(lines, "", renderer.RenderSoftHints(width-4,
		tideui.SoftHint{Key: "y/enter", Label: "go"},
		tideui.SoftHint{Key: "n/esc", Label: "back"},
	))
	return renderer.RenderSoftBody(width, strings.Join(lines, "\n"))
}

func (m model) runningView(width int) string {
	var lines []string
	for i, s := range m.steps {
		lines = append(lines, stepLine(s, i == m.current && m.screen == screenRunning && !s.done))
	}
	lines = append(lines, "")
	budget := 12
	logLines := m.log
	if len(logLines) > budget {
		logLines = logLines[len(logLines)-budget:]
	}
	for _, l := range logLines {
		lines = append(lines, renderer.Styles.DetailMeta.Render(l))
	}
	if m.screen == screenDone {
		lines = append(lines, "")
		if m.failed {
			lines = append(lines, renderer.Styles.StatusError.Render(" release failed — see log above "))
		} else {
			lines = append(lines, renderer.Styles.StatusSuccess.Render(" release complete "))
		}
		lines = append(lines, "", renderer.RenderSoftHints(width-4, tideui.SoftHint{Key: "q/enter", Label: "quit"}))
	}
	return renderer.RenderSoftBody(width, strings.Join(lines, "\n"))
}

func stepLine(s stepStatus, active bool) string {
	glyph := "○"
	style := renderer.Styles.DetailMeta
	switch {
	case s.err != nil:
		glyph = "✗"
		style = renderer.Styles.StatusError
	case s.done:
		glyph = "✓"
		style = renderer.Styles.StatusSuccess
	case active:
		glyph = "●"
	}
	return style.Render(fmt.Sprintf(" %s %s", glyph, s.label))
}

func orDash(s string) string {
	if s == "" {
		return "(none yet)"
	}
	return s
}

// commitLine renders one "<sha> <subject>" log line with the sha in the
// theme's accent color and the subject in muted text, truncating the
// subject (not the sha) if the line is too wide for the panel.
func commitLine(line string, width int) string {
	sha, subject, ok := strings.Cut(line, " ")
	if !ok {
		return renderer.Styles.DetailMeta.Render(shortenText(line, width))
	}
	shaWidth := len(sha) + 1
	subject = shortenText(subject, max(1, width-shaWidth))
	return renderer.Styles.Badge.Render(sha) + " " + renderer.Styles.DetailMeta.Render(subject)
}

func shortenText(s string, width int) string {
	if width <= 0 || len(s) <= width {
		return s
	}
	if width <= 1 {
		return s[:width]
	}
	return s[:width-1] + "…"
}
