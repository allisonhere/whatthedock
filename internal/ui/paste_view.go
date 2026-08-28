package ui

import (
	"strings"

	"github.com/allisonhere/tideui"
	"github.com/charmbracelet/lipgloss"

	"github.com/allisonhere/whatthedock/internal/clipboard"
)

// pasteConflictOK/Warn are the same "healthy"/"restarting" greens and
// ambers inspectorStatusColor already uses elsewhere in the app, reused
// here rather than inventing a second color vocabulary for "this is fine"
// vs. "this needs a look."
var (
	pasteConflictOK   = lipgloss.Color("#80c990")
	pasteConflictWarn = lipgloss.Color("#e5c07b")
)

// pasteOverlay is the review/conflict screen shown after "P" — see
// handlePasteKey. Its shape follows the request's own mockup: a checklist
// of what Plan found, then Enter/d/Esc.
func (m Model) pasteOverlay(renderer tideui.Renderer) *tideui.Overlay {
	width := min(84, max(48, m.width-8))
	contentWidth := width - 4
	if m.pastePlan == nil {
		content := renderer.RenderSoftBody(width, renderer.Styles.DetailMeta.Width(contentWidth).Render("No paste in progress."))
		overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "paste", Content: content, Width: width})
		return &overlay
	}
	plan := *m.pastePlan

	lines := []string{
		renderer.Styles.DetailMeta.Width(contentWidth).Render(
			"Paste " + short(plan.Source.Name, contentWidth/3) + " -> " + short(plan.TargetHost.Name, contentWidth/3)),
		"",
	}
	for _, c := range plan.Conflicts {
		lines = append(lines, pasteConflictLine(renderer, c))
	}
	lines = append(lines, "")
	if plan.Blocked() {
		lines = append(lines, renderer.Styles.StatusError.Width(contentWidth).Render("blocking conflict(s) above must be fixed before deploying"))
		lines = append(lines, "")
	}
	hints := []tideui.SoftHint{
		{Key: "enter", Label: "review / fix"},
		{Key: "d", Label: "deploy"},
	}
	if hasBlockingBindPathConflict(plan) {
		hints = append(hints, tideui.SoftHint{Key: "t", Label: "redirect missing paths to a placeholder"})
	}
	hints = append(hints, tideui.SoftHint{Key: "esc", Label: "cancel"})
	lines = append(lines,
		renderer.Styles.DetailMeta.Width(contentWidth).Render("configuration clone — volume/bind-mount data is not copied"),
		"",
		renderer.RenderSoftHints(contentWidth, hints...),
	)

	content := renderer.RenderSoftBody(width, strings.Join(lines, "\n"))
	overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "paste", Content: content, Width: width})
	return &overlay
}

func pasteConflictLine(renderer tideui.Renderer, c clipboard.PasteConflict) string {
	switch c.Severity {
	case clipboard.SeverityOK:
		return lipgloss.NewStyle().Foreground(pasteConflictOK).Render("✓ " + c.Message)
	case clipboard.SeverityBlock:
		return renderer.Styles.StatusError.Render("! " + c.Message)
	default:
		return lipgloss.NewStyle().Foreground(pasteConflictWarn).Render("! " + c.Message)
	}
}
