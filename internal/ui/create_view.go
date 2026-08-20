package ui

import (
	"fmt"
	"strings"

	"github.com/allisonhere/tideui"
	"github.com/charmbracelet/lipgloss"
)

func (m Model) createOverlay(renderer tideui.Renderer) *tideui.Overlay {
	width := min(128, max(70, m.width-8))
	contentWidth := width - 4
	if m.createDraft.Confirming {
		name := m.createDraft.TargetName()
		editing := m.createDraft.Editing
		var prompt string
		confirmLabel := confirmStepLabel(editing)
		switch {
		case editing && m.createDraft.Mode != createModeCompose:
			prompt = "Replace standalone container " + name + " with these changes?"
		case m.createDraft.Mode == createModeCompose && m.createDraft.BaseFileMissing:
			host := "the local filesystem"
			if sys := m.activeSystemConfig(); sys.Kind == "ssh" {
				host = sys.Name
			}
			prompt = "Compose file " + short(m.createDraft.ComposeFile, max(12, contentWidth-10)) +
				" doesn't exist on " + host + " (likely deployed via Portainer or another tool " +
				"that manages its own copy elsewhere).\n\nCreate a NEW compose file here with " +
				name + "'s current configuration? " + name + " will be adopted out of that tool's " +
				"management — future changes go through this file and docker compose directly."
			confirmLabel = "create & adopt"
		case m.createDraft.Mode == createModeCompose:
			spec, err := m.createDraft.ComposeSpec(m.activeSystemConfig())
			if err != nil {
				prompt = "Create Compose service " + name + "?"
			} else {
				prompt = "Write " + short(spec.OverrideFile, max(12, contentWidth-10)) + " and run compose up for " + name + "?"
			}
		default:
			prompt = "Create and start standalone container " + name + "?"
		}
		title := "confirm create"
		if editing {
			title = "confirm edit"
		}
		// Same unbounded-height bug as the form screen's preview column
		// (see the budget comment below in the non-Confirming branch), one
		// level up: this whole prompt+preview+hints block goes through
		// RenderSoftBody with no cap of its own, so a service with enough
		// env/labels grew past softOverlayBodyBudget and the overlay
		// compositor silently dropped the bottom rows (placeBoxAt skips
		// any line landing at/past the terminal's edge once the box can no
		// longer be vertically centered) — cutting off the confirm/cancel
		// hints entirely on a long enough preview. promptRendered's own
		// line count is subtracted first since the confirmation prompt
		// itself can wrap to more than one line (the "adopt" prompt).
		promptRendered := renderer.Styles.DetailMeta.Width(contentWidth).Render(prompt)
		fixedRows := strings.Count(promptRendered, "\n") + 1 /* prompt */ + 1 /* blank above preview */ + 1 /* blank below preview */ + 1 /* hints */
		previewBudget := max(3, m.softOverlayBodyBudget()-fixedRows)
		previewLines := strings.Split(m.createDraft.Preview(), "\n")
		previewText := m.createDraft.Preview()
		if len(previewLines) > previewBudget {
			hidden := len(previewLines) - (previewBudget - 1)
			previewLines = append(previewLines[:previewBudget-1], fmt.Sprintf("… %d more lines — esc back, ctrl+y to review the full file", hidden))
			previewText = strings.Join(previewLines, "\n")
		}
		content := renderer.RenderSoftBody(width,
			promptRendered+"\n\n"+
				renderer.Styles.DetailBody.Width(contentWidth).Render(previewText)+"\n\n"+
				renderer.RenderSoftHints(contentWidth,
					tideui.SoftHint{Key: "y", Label: confirmLabel},
					tideui.SoftHint{Key: "n/esc", Label: "cancel"},
				))
		overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: title, Content: content, Width: width})
		return &overlay
	}
	if m.createEditingCompose {
		return m.createEditorOverlay(renderer)
	}
	if m.createBrowsing {
		return m.createFileBrowserOverlay(renderer, width, contentWidth)
	}
	tabs := renderCreateModeTabs(renderer, contentWidth, m.createDraft.Mode, m.createField == createFieldMode)
	formWidth := max(28, contentWidth/2-1)
	previewWidth := max(24, contentWidth-formWidth-3)
	fields := m.visibleCreateFields()
	formRows := make([]string, 0, len(fields)+4)
	for _, field := range fields {
		if field == createFieldMode {
			continue // shown as the tab pills above, not a row
		}
		value := m.createFieldValueForDisplay(field)
		if field == m.createField && !m.createChoiceField(field) {
			value = m.createFieldValueWithCaret()
		}
		suffix := short(value, max(12, formWidth-18))
		if field == createFieldComposeFile && field == m.createField {
			suffix = short(value, max(12, formWidth-24)) + "  enter/browse"
		}
		formRows = append(formRows, renderer.RenderSoftRow(tideui.SoftRow{
			Text:     createFieldLabel(field),
			Suffix:   suffix,
			Selected: field == m.createField,
		}, formWidth))
	}
	// The preview column is styled to look like a black terminal surface,
	// distinct from the rest of the panel — every line gets an explicit
	// Background(previewBG) rather than inheriting the theme's panel color,
	// since a bare Width-only wrapper leaves ungapped/unstyled runs showing
	// whatever sits behind them instead of a solid fill.
	previewBG := lipgloss.Color("#000000")
	blankPreviewLine := lipgloss.NewStyle().Width(previewWidth).Background(previewBG).Render("")
	previewLines := []string{renderer.Styles.DetailMeta.Background(previewBG).Width(previewWidth).Render("Preview")}
	if m.createDraft.Mode == createModeCompose {
		// Syntax-highlighted lines carry their own foreground color codes
		// via foregroundSpanDefault, never a background (see
		// highlightComposeYAML's own doc comment for why that specifically
		// matters here) — so wrapping them in a Background-only style below
		// paints the black surface behind them, including the plain
		// punctuation/whitespace runs between highlighted tokens, without
		// clobbering the highlight colors.
		for _, line := range highlightComposeYAML(m.createDraft.Preview()) {
			previewLines = append(previewLines, lipgloss.NewStyle().Width(previewWidth).Background(previewBG).Render(line))
		}
	} else {
		for _, line := range strings.Split(m.createDraft.Preview(), "\n") {
			previewLines = append(previewLines, renderer.Styles.DetailBody.Background(previewBG).Width(previewWidth).Render(line))
		}
	}
	if m.createDraft.Mode == createModeCompose {
		composeFileLine := "Local compose file  " + short(m.createDraft.ComposeFile, max(12, previewWidth-20))
		previewLines = append(previewLines, blankPreviewLine, renderer.Styles.DetailMeta.Background(previewBG).Width(previewWidth).Render(composeFileLine))
		switch {
		case m.createDraft.OverrideLoaded:
			previewLines = append(previewLines, renderer.Styles.DetailMeta.Background(previewBG).Width(previewWidth).Render("Override YAML  existing file loaded (ctrl+y to edit)"))
		case m.createDraft.OverrideRawSet:
			previewLines = append(previewLines, renderer.Styles.DetailMeta.Background(previewBG).Width(previewWidth).Render("Override YAML  hand-edited (ctrl+y to re-edit)"))
		default:
			previewLines = append(previewLines, renderer.Styles.DetailMeta.Background(previewBG).Width(previewWidth).Render("Override YAML  generated (ctrl+y to edit)"))
		}
	} else {
		previewLines = append(previewLines, blankPreviewLine, renderer.Styles.DetailMeta.Background(previewBG).Width(previewWidth).Render("Docker API create preview"))
	}
	validation := renderer.Styles.DetailMeta.Background(previewBG).Width(previewWidth).Render("Draft looks good")
	if err := m.createDraft.Validate(); err != nil {
		validation = renderer.Styles.StatusError.Background(previewBG).Width(previewWidth).Render(short(err.Error(), previewWidth))
	}
	previewLines = append(previewLines, blankPreviewLine, validation)

	// A row past the shorter column's line count (the preview is usually
	// taller than the field list) leaves that side an empty string with no
	// background of its own — Width-padding it without a style falls
	// through to the raw terminal default (glaring black in a light theme)
	// instead of blending into the panel like every populated row does.
	panelBG := renderer.Styles.OverlayBody.GetBackground()
	formText := strings.Split(strings.Join(formRows, "\n"), "\n")
	previewText := strings.Split(strings.Join(previewLines, "\n"), "\n")
	rowCount := max(len(formText), len(previewText))
	// The preview column scales with the service's own real content
	// (labels, env, volumes, ...), completely unbounded — a service with
	// enough of that clipped the whole overlay off the top and bottom of
	// the terminal, reported live. Cap the shared row count to what
	// actually fits (mirroring softOverlayBodyBudget's own allowance,
	// minus this overlay's own always-present rows: the mode tabs + blank
	// above this split, the blank + two hint rows below it, plus one row
	// of slack verified empirically against this overlay's actual layout
	// rather than derived exactly) and point at ctrl+y — the full-height
	// override-YAML editor already exists for when the whole file needs
	// reviewing — instead of ever overflowing.
	budget := max(5, m.softOverlayBodyBudget()-6)
	truncated := rowCount > budget
	if truncated {
		rowCount = budget - 1 // reserve the last row for the "N more" notice
	}
	bodyRows := make([]string, 0, rowCount+5)
	for i := 0; i < rowCount; i++ {
		left, right := "", ""
		if i < len(formText) {
			left = formText[i]
		}
		if i < len(previewText) {
			right = previewText[i]
		}
		bodyRows = append(bodyRows, lipgloss.NewStyle().Width(formWidth).Background(panelBG).Render(left)+lipgloss.NewStyle().Background(panelBG).Render(" ")+renderer.Styles.DetailMeta.Background(panelBG).Render("│")+lipgloss.NewStyle().Background(previewBG).Render(" ")+lipgloss.NewStyle().Width(previewWidth).Render(right))
	}
	if truncated {
		hiddenPreview := len(previewText) - rowCount
		notice := lipgloss.NewStyle().Width(formWidth).Background(panelBG).Render("") +
			lipgloss.NewStyle().Background(panelBG).Render(" ") +
			renderer.Styles.DetailMeta.Background(panelBG).Render("│") +
			lipgloss.NewStyle().Background(previewBG).Render(" ") +
			renderer.Styles.DetailMeta.Background(previewBG).Width(previewWidth).Render(fmt.Sprintf("▼ %d more — ctrl+y for the full file", hiddenPreview))
		bodyRows = append(bodyRows, notice)
	}
	bodyRows = append(bodyRows,
		lipgloss.NewStyle().Width(contentWidth).Background(panelBG).Render(""),
		renderer.RenderSoftHints(contentWidth,
			tideui.SoftHint{Key: "[/]", Label: "mode"},
			tideui.SoftHint{Key: "tab", Label: "next"},
			tideui.SoftHint{Key: "h/l", Label: "change"},
			tideui.SoftHint{Key: "o/ctrl+o", Label: "browse"},
		),
		renderer.RenderSoftHints(contentWidth,
			tideui.SoftHint{Key: "ctrl+y", Label: "edit yaml"},
			tideui.SoftHint{Key: "ctrl+s", Label: "validate"},
			tideui.SoftHint{Key: "ctrl/alt+enter", Label: confirmStepLabel(m.createDraft.Editing)},
			tideui.SoftHint{Key: "esc", Label: "cancel"},
		),
	)
	content := renderer.RenderSoftBody(width, tabs+"\n\n"+strings.Join(bodyRows, "\n"))
	title := "create"
	if m.createDraft.Editing {
		title = "edit"
	}
	overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: title, Content: content, Width: width})
	return &overlay
}

func confirmStepLabel(editing bool) string {
	if editing {
		return "update"
	}
	return "create"
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
	// Every segment gets the panel's own background explicitly — the outer
	// Width wrap only paints padding it appends itself, not the gap after
	// whatever segment happens to render last, so without this the trailing
	// space past the lint message fell through to the raw terminal default
	// instead of matching the panel.
	panelBG := renderer.Styles.OverlayBody.GetBackground()
	lint := renderer.Styles.DetailMeta.Background(panelBG).Render("valid YAML")
	if err := lintComposeYAML(editor.Value()); err != nil {
		lint = renderer.Styles.StatusError.Render(short(err.Error(), max(20, contentWidth-50)))
	}
	header := lipgloss.NewStyle().Width(contentWidth).Background(panelBG).Render(
		renderer.Styles.DetailMeta.Background(panelBG).Render("Editing override YAML  ·  "+status+"  ·  ") + lint)

	// The editor's own styling (see editor_area.go) covers every styled
	// character run, but ripple pads unfilled rows below short documents
	// with bare empty strings and doesn't right-pad short lines itself —
	// wrapping the whole block here fills both with black instead of the
	// panel's normal background.
	editorBody := lipgloss.NewStyle().Width(contentWidth).Background(composeEditorBG).Render(editor.View())

	body := strings.Join([]string{
		header,
		"",
		editorBody,
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
func renderCreateModeTabs(renderer tideui.Renderer, width int, active createMode, focused bool) string {
	// OverlayTitle already carries Padding(0, 1) (see tideui's soft-modal
	// title style) — don't also wrap its content in manual spaces, or the
	// padding stacks into an extra highlighted blank column on each side
	// that isn't part of the label. StatusHint has no built-in padding, so
	// the inactive pill still adds its own spaces to match width.
	// Every non-pill segment of the row (gaps, the unfocused rail, the
	// inactive tab) gets the panel body's own background explicitly — a
	// bare unstyled space here would otherwise show whatever's behind the
	// row instead of blending into the panel, and StatusHint's own
	// background is tuned for the status bar, not this row, so it needs the
	// same override.
	panelBG := renderer.Styles.OverlayBody.GetBackground()
	activeStyle := renderer.Styles.OverlayTitle
	inactiveStyle := renderer.Styles.StatusHint.Background(panelBG)
	railStyle := renderer.Styles.OverlayTitle.Padding(0)
	fill := lipgloss.NewStyle().Background(panelBG)

	rail := fill.Render("  ")
	if focused {
		rail = railStyle.Render("▌") + fill.Render(" ")
	}
	blank := fill.Render("  ")

	compose := "Compose service"
	standalone := "Standalone container"

	// The focus rail sits directly before whichever pill is active, not
	// always before "Compose service" — so it tracks the highlighted tab
	// instead of pointing at the wrong one when Standalone is selected.
	var out string
	if active == createModeCompose {
		out = rail + activeStyle.Render(compose) + fill.Render(" ") + blank + inactiveStyle.Render(" "+standalone+" ")
	} else {
		out = blank + inactiveStyle.Render(" "+compose+" ") + fill.Render(" ") + rail + activeStyle.Render(standalone)
	}
	return lipgloss.NewStyle().Width(width).Background(panelBG).Render(fill.Render(" ") + out)
}

func (m Model) createFileBrowserOverlay(renderer tideui.Renderer, width int, contentWidth int) *tideui.Overlay {
	dirLabel := "Directory  " + short(m.createBrowseDir, max(12, contentWidth-12))
	if system := m.activeSystemConfig(); system.Kind == "ssh" {
		dirLabel = "Directory (" + system.Name + ")  " + short(m.createBrowseDir, max(12, contentWidth-len(system.Name)-14))
	}
	rows := []string{
		renderer.Styles.DetailMeta.Width(contentWidth).Render(dirLabel),
	}
	if m.createFileLoading {
		rows = append(rows, renderer.Styles.DetailMeta.Width(contentWidth).Render("Listing remote directory…"))
	} else if m.createFileErr != "" {
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
