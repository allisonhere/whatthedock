package ui

import (
	"strings"

	"github.com/allisonhere/ripple"
	"github.com/allisonhere/tideui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// openInspectorDetail snapshots the Inspector row currently under
// inspectorCursor into a fresh, read-only ripple.Model and opens
// overlayInspectorDetail — the one overlay that answers both "show me the
// full value" (anything the pane truncates) and "let me select part of it
// to copy". Reuses the exact same editor component, clipboard bridge
// (editorClipboard, editor_area.go — the OSC52 path copyTextCmd also
// uses), and vim-mode toggle (editorVimMode) the Compose YAML editor
// already wires up, so this overlay gets real vim motions/visual
// selection or plain arrow+shift-arrow selection for free, matching
// whichever the user already has set. Value receiver, mutating and
// returning its own copy of m — matching openNetworkCuration/
// openVolumeCuration's convention.
func (m Model) openInspectorDetail() (tea.Model, tea.Cmd) {
	row, ok := m.currentInspectorFieldRow()
	if !ok {
		return m, nil
	}
	ed := ripple.New()
	ed.SetClipboard(editorClipboard)
	if editorVimMode {
		ed.SetInputMode(ripple.ModeVim)
	}
	ed.SetValue(row.value)
	ed.Focus()
	m.inspectorDetailLabel = row.label
	m.inspectorDetailKind = row.valueKind
	m.inspectorDetailEditor = ed
	m.overlay = overlayInspectorDetail
	return m, nil
}

// handleInspectorDetailKey forwards every key to the embedded ripple.Model
// — real vim or plain-mode motions, visual/shift selection, and clipboard
// copy — but vetoes anything that would actually change the document: the
// Inspector shows read-only container/host metadata, and nothing here
// should be able to edit it. A veto discards the post-keystroke editor
// state and keeps the pre-keystroke one instead. This still "copies" a
// vim y/d/c/x on a selection even though the document itself doesn't
// visibly change: ripple's vim mode writes the affected text to the
// clipboard as a synchronous side effect of processing the keystroke
// (setRegister, vim.go) — before this function ever decides whether to
// keep or discard the result — so cutting/changing/yanking still reaches
// the system clipboard, a fitting reading of "delete" in a view you can't
// actually edit.
func (m Model) handleInspectorDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	before := m.inspectorDetailEditor
	updated, cmd := before.Update(msg)

	if msg.String() == "esc" {
		// ripple has no notion of "cancel" outside vim mode — a plain-mode
		// Esc is always a no-op inside the editor itself (handlePlainKey
		// matches no keymap entry for it, and the default case swallows
		// it), so it's unambiguously this overlay's own close key.
		if before.InputMode() != ripple.ModeVim {
			m.overlay = overlayNone
			return m, nil
		}
		// In vim mode, whether this Esc had "nothing to cancel" can't be
		// read off Mode()/CursorIndex()/SelectedText(): Mode() reports
		// "NORMAL" whether or not a count/operator is pending, so a
		// pending "d" (say) cleared by this Esc looks identical, by those
		// three, to an already-idle Esc — comparing them (the previous
		// version of this check) closed the overlay in both cases, when
		// only the idle one should. ripple itself already tracks the
		// distinction: a truly clean Esc (no pending count/operator, not
		// in Visual) is the only case where it returns a cmd producing
		// ripple.CancelMsg (vimNormalKey) — clearing a pending
		// count/operator, or leaving Visual mode, both return a nil cmd
		// instead. Calling cmd here to check is safe: for the Esc key
		// ripple only ever returns nil or that one pure message
		// constructor, never one with a side effect like a clipboard
		// write.
		if cmd != nil {
			if _, cancel := cmd().(ripple.CancelMsg); cancel {
				m.overlay = overlayNone
				return m, nil
			}
		}
		m.inspectorDetailEditor = updated
		return m, nil
	}

	if updated.Value() != before.Value() || updated.Mode() == "INSERT" || updated.Mode() == "COMMAND" {
		return m, cmd
	}

	m.inspectorDetailEditor = updated
	return m, cmd
}

// inspectorDetailBoxWidth/Height are a fixed, equal-sided box for the
// editor surface — not sized to content, so a one-word value and a
// multi-line one get the identical rectangle (Ripple pads short content
// with blank rows, ripple.go's View: "for row := 0; row < m.height;
// row++"). 56x28 reads as roughly square on a typical monospace terminal
// font, whose character cells are about twice as tall as they are wide.
const (
	inspectorDetailBoxWidth  = 56
	inspectorDetailBoxHeight = 28
)

func (m Model) inspectorDetailOverlay(renderer tideui.Renderer) *tideui.Overlay {
	innerWidth := min(inspectorDetailBoxWidth, max(20, m.width-8))
	innerHeight := min(inspectorDetailBoxHeight, max(6, m.height-10))
	width := innerWidth + 4

	ed := m.inspectorDetailEditor
	ed.SetSize(innerWidth, innerHeight)

	cursorStyle := lipgloss.NewStyle().Background(renderer.Styles.Theme.BorderFocus).Foreground(composeEditorBG)
	selBg, selFg := renderer.Styles.Theme.Selected, lipgloss.Color("#c0caf5")
	if bg, ok := renderer.Styles.ItemSelected.GetBackground().(lipgloss.Color); ok {
		selBg = bg
	}
	if fg, ok := renderer.Styles.ItemSelected.GetForeground().(lipgloss.Color); ok {
		selFg = fg
	}
	selectedStyle := lipgloss.NewStyle().Background(selBg).Foreground(selFg)

	kinds := inspectorValueTokens(m.inspectorDetailKind, ed.Value())
	opts := ripple.Options{
		Selected: func(s string) string { return selectedStyle.Render(s) },
		StyleKey: func(offset int) string {
			if offset < 0 || offset >= len(kinds) {
				return ""
			}
			return kinds[offset]
		},
		Style: func(key, text string) string { return inspectorTokenStyle(key).Render(text) },
	}
	if ed.Mode() == "" {
		opts.Cursor = cursorStyle.Render(" ")
	} else {
		opts.CursorRune = func(s string) string { return cursorStyle.Render(s) }
	}

	// Editable fields render on a solid black surface — the same
	// composeEditorBG the Compose YAML editor uses (editor_area.go), so
	// every "you can put a cursor in this" surface in the app looks like
	// the same kind of thing. inspectorTokenStyle already backgrounds
	// every styled rune; this second pass backgrounds Ripple's blank
	// padding rows too (ripple.go's View emits a literal "" for rows past
	// the content, with no Style call to hook), so the box is solid black
	// on every row, not just the ones with text — the "equal-sided
	// rectangle" the fixed SetSize above establishes actually reads as
	// one continuous black rectangle instead of text-height black strips.
	blackRow := lipgloss.NewStyle().Background(composeEditorBG).Width(innerWidth)
	rows := strings.Split(ed.View(opts), "\n")
	for i, row := range rows {
		rows[i] = blackRow.Render(row)
	}
	body := strings.Join(rows, "\n")

	hints := renderer.RenderSoftHints(innerWidth, inspectorDetailHints(ed)...)
	content := renderer.RenderSoftBody(width, body+"\n\n"+hints)
	overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: m.inspectorDetailLabel, Content: content, Width: width})
	return &overlay
}

// inspectorTokenStyle is inspectorTokenColor (view.go) plus the black
// composeEditorBG every run in this overlay needs, matching
// editorArea.View()'s Compose YAML styles (composeKeyStyle et al.) —
// same reasoning: an unstyled span with no explicit background falls
// through to whatever's behind it the moment an adjacent styled span's
// own render resets, corrupting the black surface with gaps.
func inspectorTokenStyle(kind string) lipgloss.Style {
	style := lipgloss.NewStyle().Background(composeEditorBG)
	switch kind {
	case "key":
		return style.Foreground(inspectorTokenColor("key")).Bold(true)
	case "comment":
		return style.Foreground(inspectorTokenColor("comment")).Italic(true)
	case "":
		return style.Foreground(lipgloss.Color("#c0caf5"))
	default:
		return style.Foreground(inspectorTokenColor(kind))
	}
}

// inspectorDetailHints shows plain-mode or vim-mode hints depending on
// which one this editor is actually running — vim's h/j/k/l and visual
// mode aren't the same gesture as plain-mode arrows/shift-arrows, so
// showing the wrong set would be actively misleading.
func inspectorDetailHints(ed ripple.Model) []tideui.SoftHint {
	if ed.InputMode() != ripple.ModeVim {
		return []tideui.SoftHint{
			{Key: "←/→", Label: "move"},
			{Key: "shift+←/→", Label: "select"},
			{Key: "ctrl+c", Label: "copy"},
			{Key: "esc", Label: "close"},
		}
	}
	return []tideui.SoftHint{
		{Key: "hjkl/w/b", Label: "move"},
		{Key: "v", Label: "select"},
		{Key: "y", Label: "copy"},
		{Key: "esc esc", Label: "close"},
	}
}
