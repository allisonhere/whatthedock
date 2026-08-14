package ui

import (
	"strings"

	"github.com/allisonhere/tideui"
	"github.com/charmbracelet/lipgloss"
)

func (m Model) createOverlay(renderer tideui.Renderer) *tideui.Overlay {
	width := min(112, max(58, m.width-8))
	contentWidth := width - 4
	if m.createDraft.Confirming {
		name := m.createDraft.TargetName()
		prompt := "Create and start standalone container " + name + "?"
		if m.createDraft.Mode == createModeCompose {
			spec, err := m.createDraft.ComposeSpec()
			if err != nil {
				prompt = "Create Compose service " + name + "?"
			} else {
				prompt = "Write " + short(spec.OverrideFile, max(12, contentWidth-10)) + " and run compose up for " + name + "?"
			}
		}
		content := renderer.RenderSoftBody(width,
			renderer.Styles.DetailMeta.Width(contentWidth).Render(prompt)+"\n\n"+
				renderer.Styles.DetailBody.Width(contentWidth).Render(m.createDraft.Preview())+"\n\n"+
				renderer.RenderSoftHints(contentWidth,
					tideui.SoftHint{Key: "y", Label: "create"},
					tideui.SoftHint{Key: "n/esc", Label: "cancel"},
				))
		overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "confirm create", Content: content, Width: width})
		return &overlay
	}
	if m.createEditingCompose {
		return m.createEditorOverlay(renderer)
	}
	if m.createBrowsing {
		return m.createFileBrowserOverlay(renderer, width, contentWidth)
	}
	tabs := renderCreateModeTabs(renderer, contentWidth, m.createDraft.Mode)
	formWidth := max(28, contentWidth/2-1)
	previewWidth := max(24, contentWidth-formWidth-3)
	fields := m.visibleCreateFields()
	formRows := make([]string, 0, len(fields)+4)
	for _, field := range fields {
		value := m.createFieldValueForDisplay(field)
		if field == m.createField && !m.createChoiceField(field) {
			value = m.createFieldValueWithCaret()
		}
		suffix := short(value, max(12, formWidth-18))
		if field == createFieldComposeFile && field == m.createField {
			suffix = short(value, max(12, formWidth-26)) + "  enter/o"
		}
		formRows = append(formRows, renderer.RenderSoftRow(tideui.SoftRow{
			Text:     createFieldLabel(field),
			Suffix:   suffix,
			Selected: field == m.createField,
		}, formWidth))
	}
	previewLines := []string{renderer.Styles.DetailMeta.Render("Preview")}
	for _, line := range strings.Split(m.createDraft.Preview(), "\n") {
		previewLines = append(previewLines, renderer.Styles.DetailBody.Width(previewWidth).Render(line))
	}
	if m.createDraft.Mode == createModeCompose {
		composeFileLine := "Local compose file  " + short(m.createDraft.ComposeFile, max(12, previewWidth-20))
		previewLines = append(previewLines, "", renderer.Styles.DetailMeta.Width(previewWidth).Render(composeFileLine))
		if m.createDraft.OverrideRawSet {
			previewLines = append(previewLines, renderer.Styles.DetailMeta.Width(previewWidth).Render("Override YAML  hand-edited (ctrl+y to re-edit)"))
		} else {
			previewLines = append(previewLines, renderer.Styles.DetailMeta.Width(previewWidth).Render("Override YAML  generated (ctrl+y to edit)"))
		}
	} else {
		previewLines = append(previewLines, "", renderer.Styles.DetailMeta.Width(previewWidth).Render("Docker API create preview"))
	}
	validation := renderer.Styles.DetailMeta.Width(previewWidth).Render("Draft looks good")
	if err := m.createDraft.Validate(m.activeSystemConfig()); err != nil {
		validation = renderer.Styles.StatusError.Width(previewWidth).Render(short(err.Error(), previewWidth))
	}
	previewLines = append(previewLines, "", validation)

	formText := strings.Split(strings.Join(formRows, "\n"), "\n")
	previewText := strings.Split(strings.Join(previewLines, "\n"), "\n")
	rowCount := max(len(formText), len(previewText))
	bodyRows := make([]string, 0, rowCount+4)
	for i := 0; i < rowCount; i++ {
		left, right := "", ""
		if i < len(formText) {
			left = formText[i]
		}
		if i < len(previewText) {
			right = previewText[i]
		}
		bodyRows = append(bodyRows, lipgloss.NewStyle().Width(formWidth).Render(left)+" "+renderer.Styles.DetailMeta.Render("│")+" "+lipgloss.NewStyle().Width(previewWidth).Render(right))
	}
	bodyRows = append(bodyRows, "", renderer.RenderSoftHints(contentWidth,
		tideui.SoftHint{Key: "[/]", Label: "mode"},
		tideui.SoftHint{Key: "tab", Label: "next"},
		tideui.SoftHint{Key: "h/l", Label: "change"},
		tideui.SoftHint{Key: "o/ctrl+o", Label: "browse"},
		tideui.SoftHint{Key: "ctrl+y", Label: "edit yaml"},
		tideui.SoftHint{Key: "ctrl+s", Label: "validate"},
		tideui.SoftHint{Key: "ctrl/alt+enter", Label: "create"},
		tideui.SoftHint{Key: "esc", Label: "cancel"},
	))
	content := renderer.RenderSoftBody(width, tabs+"\n\n"+strings.Join(bodyRows, "\n"))
	overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "create", Content: content, Width: width})
	return &overlay
}

// createEditorOverlay renders the raw override-YAML editor. Unlike the rest
// of the create overlay, this is sized close to the full terminal — "the
// window large enough there is a quality compose editor" — while staying a
// SoftPanel like every other overlay, just a much bigger one, since editing
// multi-line text genuinely needs the room a field list doesn't.
func (m Model) createEditorOverlay(renderer tideui.Renderer) *tideui.Overlay {
	width := max(60, m.width-6)
	contentWidth := width - 4
	editorHeight := max(6, m.height-12)

	editor := m.createEditor
	editor.SetSize(contentWidth, editorHeight)

	status := "standard editing"
	if editor.vimMode() {
		status = "vim"
		if cl := editor.CommandLine(); cl != "" {
			status = cl
		} else if mode := editor.Mode(); mode != "" {
			status = "vim · " + mode
		}
	}
	lint := renderer.Styles.DetailMeta.Render("valid YAML")
	if err := lintComposeYAML(editor.Value()); err != nil {
		lint = renderer.Styles.StatusError.Render(short(err.Error(), max(20, contentWidth-50)))
	}
	header := lipgloss.NewStyle().Width(contentWidth).Render(
		renderer.Styles.DetailMeta.Render("Editing override YAML  ·  "+status+"  ·  ") + lint)

	body := strings.Join([]string{
		header,
		"",
		editor.View(),
		"",
		renderer.RenderSoftHints(contentWidth,
			tideui.SoftHint{Key: "ctrl+s", Label: "save"},
			tideui.SoftHint{Key: "esc", Label: "cancel"},
		),
	}, "\n")
	content := renderer.RenderSoftBody(width, body)
	overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "edit override yaml", Content: content, Width: width})
	return &overlay
}

// renderCreateModeTabs draws the Compose/standalone mode switch as a
// dedicated tab row above the form, rather than as a cycled field mixed in
// with the rest — cycled with "[" and "]" (handleCreateKey), reachable from
// any field since it isn't part of the focusable field list.
func renderCreateModeTabs(renderer tideui.Renderer, width int, active createMode) string {
	compose := " Compose service "
	standalone := " Standalone container "
	if active == createModeCompose {
		compose = renderer.Styles.OverlayTitle.Render(compose)
		standalone = renderer.Styles.StatusHint.Render(standalone)
	} else {
		compose = renderer.Styles.StatusHint.Render(compose)
		standalone = renderer.Styles.OverlayTitle.Render(standalone)
	}
	return lipgloss.NewStyle().Width(width).Render(compose + " " + standalone)
}

func (m Model) createFileBrowserOverlay(renderer tideui.Renderer, width int, contentWidth int) *tideui.Overlay {
	rows := []string{
		renderer.Styles.DetailMeta.Width(contentWidth).Render("Directory  " + short(m.createBrowseDir, max(12, contentWidth-12))),
	}
	if m.createFileErr != "" {
		rows = append(rows, renderer.Styles.StatusError.Width(contentWidth).Render(m.createFileErr))
	} else if len(m.createFiles) == 0 {
		rows = append(rows, renderer.Styles.DetailMeta.Width(contentWidth).Render("No Compose files found here."))
	} else {
		limit := max(4, min(len(m.createFiles), m.height-12))
		start := 0
		if m.createFileCursor >= limit {
			start = m.createFileCursor - limit + 1
		}
		end := min(len(m.createFiles), start+limit)
		for i := start; i < end; i++ {
			entry := m.createFiles[i]
			text := entry.Name
			prefix := "  "
			suffix := ""
			if entry.Dir {
				prefix = "▸ "
				suffix = "dir"
			}
			if entry.Parent {
				prefix = "↰ "
				suffix = "up"
			}
			if entry.Selected {
				suffix = "selected"
			}
			rows = append(rows, renderer.RenderSoftRow(tideui.SoftRow{
				Prefix:   prefix,
				Text:     text,
				Suffix:   suffix,
				Selected: i == m.createFileCursor,
			}, contentWidth))
		}
	}
	rows = append(rows, "", renderer.RenderSoftHints(contentWidth,
		tideui.SoftHint{Key: "enter/l", Label: "open/select"},
		tideui.SoftHint{Key: "h/backspace", Label: "up"},
		tideui.SoftHint{Key: "esc", Label: "back"},
	))
	content := renderer.RenderSoftBody(width, strings.Join(rows, "\n"))
	overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "compose file", Content: content, Width: width})
	return &overlay
}

func (m Model) createFieldValueForDisplay(field createField) string {
	m.createField = field
	return m.createFieldValue()
}

func (m Model) createChoiceField(field createField) bool {
	return field == createFieldRestart
}
