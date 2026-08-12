package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/allisonhere/tideui"

	"github.com/allisonhere/tidedock/internal/domain"
)

func (m Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	renderer := tideui.NewRenderer(m.theme, tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	panes := [3]tideui.Pane{
		{Title: "Projects", Hint: "space collapse  / filter", Content: m.renderTree(renderer), Focused: m.focus == paneTree},
		{Title: "Activity", Hint: "logs", Content: m.renderActivity(renderer), Focused: m.focus == paneActivity},
		{Title: "Inspector", Hint: "s start/stop  alt+r restart", Content: m.renderInspector(renderer), Focused: m.focus == paneInspector},
	}
	modal := m.renderOverlay(renderer)
	status := &tideui.StatusBar{
		Left:  m.statusLeft(renderer),
		Right: "j/k move  space expand  / filter  T themes  ctrl+k commands  ? help  q quit",
	}
	return renderer.Render(tideui.Layout{
		Width:        m.width,
		Height:       m.height,
		Mode:         tideui.ThreeColumn,
		Panes:        panes,
		Status:       status,
		Modal:        modal,
		ColumnRatios: [3]float64{3, 5, 4},
	})
}

func (m Model) renderTree(renderer tideui.Renderer) string {
	if m.statusErr && len(m.rows) == 0 {
		return renderer.Styles.DetailMeta.Render("Docker state is unavailable.\n\n" + m.status)
	}
	if len(m.rows) == 0 {
		if strings.TrimSpace(m.filter) != "" {
			return renderer.Styles.DetailMeta.Render("No projects or containers match " + m.filter + ".")
		}
		return renderer.Styles.DetailMeta.Render("No containers found on this host.")
	}
	width := m.leftPaneWidth() - 4
	if width < 12 {
		width = 12
	}
	lines := make([]string, 0, len(m.rows))
	for i, row := range m.rows {
		selected := i == m.cursor
		prefix := strings.Repeat("  ", row.depth)
		suffix := ""
		text := row.label
		switch row.kind {
		case rowProject:
			if m.collapsed[row.project] {
				prefix += "▸ "
			} else {
				prefix += "▾ "
			}
		case rowService:
			prefix += "  "
		case rowContainer:
			prefix += statusGlyph(*row.container) + " "
			suffix = statusText(*row.container)
			if row.container.Compose.Service != "" {
				text = row.container.Compose.Service
			}
		case rowSection:
			prefix += ""
		}
		lines = append(lines, renderer.RenderRow(tideui.Row{Prefix: prefix, Text: text, Suffix: suffix, Selected: selected, Muted: row.muted}, width))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderActivity(renderer tideui.Renderer) string {
	if m.selected == nil {
		return renderer.Styles.DetailMeta.Render("Select a container to view live logs.")
	}
	if m.logErr != nil {
		return renderer.Styles.StatusError.Render(friendlyDockerError(m.logErr))
	}
	if len(m.logLines) == 0 {
		return renderer.Styles.DetailMeta.Render("Waiting for logs from " + m.selected.DisplayName() + "...")
	}
	width := m.centerPaneWidth() - 4
	if width < 20 {
		width = 20
	}
	start := 0
	visible := max(1, m.height-5)
	if len(m.logLines) > visible {
		start = len(m.logLines) - visible
	}
	lines := make([]string, 0, len(m.logLines)-start)
	for _, line := range m.logLines[start:] {
		lines = append(lines, renderer.Styles.DetailBody.Width(width).Render(line))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderInspector(renderer tideui.Renderer) string {
	if m.selected == nil {
		return renderer.Styles.DetailMeta.Render("No container selected.")
	}
	ctr := *m.selected
	var lines []string
	add := func(label, value string) {
		if strings.TrimSpace(value) == "" {
			value = "none"
		}
		lines = append(lines, renderer.RenderRow(tideui.Row{Prefix: label + "  ", Text: value}, max(12, m.rightPaneWidth()-4)))
	}
	lines = append(lines, renderer.Styles.DetailTitle.Render(ctr.DisplayName()))
	add("Status", strings.TrimSpace(strings.Join([]string{string(ctr.State), string(ctr.Health)}, " ")))
	add("Uptime", formatDuration(ctr.Created))
	add("Image", ctr.Image)
	add("Image ID", short(ctr.ImageID, 20))
	add("Restart", ctr.RestartPolicy)
	add("Restarts", fmt.Sprintf("%d", ctr.RestartCount))
	if ctr.Compose.Project != "" {
		add("Project", ctr.Compose.Project)
		add("Service", ctr.Compose.Service)
	}
	add("Ports", formatPorts(ctr.Ports))
	add("Mounts", formatMounts(ctr.Mounts))
	add("Networks", strings.Join(ctr.Networks, ", "))
	add("Env", formatList(ctr.Env, 8))
	add("Labels", formatMap(ctr.Labels, 8))
	if ctr.HealthCheck != nil {
		add("Health", strings.Join(ctr.HealthCheck.Test, " "))
	}
	lines = append(lines, "")
	lines = append(lines, renderer.Styles.DetailMeta.Render("Actions: s start/stop  alt+r restart  l logs  ctrl+k more"))
	return strings.Join(lines, "\n")
}

func (m Model) renderOverlay(renderer tideui.Renderer) *tideui.Overlay {
	switch m.overlay {
	case overlayHelp:
		width := min(72, max(40, m.width-8))
		content := renderer.RenderSoftBody(width, helpText()+"\n\n"+
			renderer.RenderSoftHints(width-4, tideui.SoftHint{Key: "esc/?/q", Label: "close"}))
		overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "tidedock", Title: "help", Content: content, Width: width})
		return &overlay
	case overlayFilter:
		width := min(72, max(40, m.width-8))
		input := renderer.Styles.InputFocused.Width(max(20, width-8)).Render(m.filterDraft)
		content := renderer.RenderSoftBody(width, input+"\n\n"+
			renderer.RenderSoftHints(width-4,
				tideui.SoftHint{Key: "enter", Label: "apply"},
				tideui.SoftHint{Key: "esc", Label: "cancel"},
			))
		overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "tidedock", Title: "filter", Content: content, Width: width})
		return &overlay
	case overlayCommandPalette:
		return m.commandPaletteOverlay(renderer)
	case overlayThemePicker:
		overlay := m.themes.SoftModal(renderer, min(72, max(40, m.width-8)), max(8, m.height-4), "tidedock")
		return &overlay
	default:
		return nil
	}
}

func (m Model) commandPaletteOverlay(renderer tideui.Renderer) *tideui.Overlay {
	items := m.filteredCommands()
	width := min(76, max(44, m.width-8))
	contentWidth := width - 4
	input := renderer.Styles.InputFocused.Width(contentWidth - 4).Render(m.commandFilter)
	var rows []string
	rows = append(rows, input)
	for i, item := range items {
		rows = append(rows, renderer.RenderSoftRow(tideui.SoftRow{
			Text:     item.Name,
			Suffix:   item.Shortcut,
			Selected: i == m.commandCursor,
			Muted:    !item.Enabled,
		}, contentWidth))
	}
	if len(items) == 0 {
		rows = append(rows, renderer.Styles.DetailMeta.Render("No matching commands."))
	}
	rows = append(rows, "", renderer.RenderSoftHints(contentWidth,
		tideui.SoftHint{Key: "enter", Label: "run"},
		tideui.SoftHint{Key: "esc", Label: "close"},
	))
	content := renderer.RenderSoftBody(width, strings.Join(rows, "\n"))
	overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "tidedock", Title: "command", Content: content, Width: width})
	return &overlay
}

func (m Model) statusLeft(renderer tideui.Renderer) string {
	host := m.provider.Host().Name
	prefix := " " + host + " "
	if m.statusErr {
		return renderer.Styles.StatusError.Render(prefix + m.status)
	}
	if strings.TrimSpace(m.filter) != "" {
		return renderer.Styles.StatusNotice.Render(prefix+"filter: "+m.filter) + renderer.Styles.StatusBar.Render(" "+m.status)
	}
	return renderer.Styles.StatusBar.Render(prefix + m.status)
}

func helpText() string {
	return strings.Join([]string{
		"j / Down       next",
		"k / Up         previous",
		"Enter          select/open",
		"Space          expand/collapse project",
		"/              filter projects, services, containers",
		"s              start/stop selected container",
		"r              refresh",
		"Alt+r          restart selected container",
		"l              logs",
		"T              theme picker",
		"Ctrl+K         command palette",
		"?              keyboard help",
		"q              quit",
	}, "\n")
}

func formatPorts(ports []domain.Port) string {
	if len(ports) == 0 {
		return ""
	}
	out := make([]string, 0, len(ports))
	for _, port := range ports {
		private := fmt.Sprintf("%d/%s", port.Private, port.Type)
		if port.Public > 0 {
			out = append(out, fmt.Sprintf("%d -> %s", port.Public, private))
		} else {
			out = append(out, private)
		}
	}
	return strings.Join(out, ", ")
}

func formatMounts(mounts []domain.Mount) string {
	if len(mounts) == 0 {
		return ""
	}
	out := make([]string, 0, len(mounts))
	for _, mount := range mounts {
		out = append(out, mount.Source+" -> "+mount.Destination)
	}
	return strings.Join(out, "\n")
}

func formatList(values []string, limit int) string {
	if len(values) == 0 {
		return ""
	}
	values = append([]string(nil), values...)
	sort.Strings(values)
	if len(values) > limit {
		values = append(values[:limit], fmt.Sprintf("... %d more", len(values)-limit))
	}
	return strings.Join(values, "\n")
}

func formatMap(values map[string]string, limit int) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = append(keys[:limit], fmt.Sprintf("... %d more", len(keys)-limit))
	}
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		if strings.HasPrefix(key, "... ") {
			lines = append(lines, key)
			continue
		}
		lines = append(lines, key+"="+values[key])
	}
	return strings.Join(lines, "\n")
}

func short(value string, n int) string {
	if len(value) <= n {
		return value
	}
	return value[:n]
}

func (m Model) leftPaneWidth() int {
	return max(1, m.width*3/12)
}

func (m Model) centerPaneWidth() int {
	return max(1, m.width*5/12)
}

func (m Model) rightPaneWidth() int {
	return max(1, m.width-m.leftPaneWidth()-m.centerPaneWidth())
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
