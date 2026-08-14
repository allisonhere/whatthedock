package ui

import (
	"errors"
	"io"

	"github.com/allisonhere/ripple"
	osc52 "github.com/aymanbagabas/go-osc52/v2"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// osc52Clipboard adapts whatthedock's existing OSC52-based clipboard write
// path (see copyTextCmd) to ripple.Clipboard. Unlike tidemail's editor
// adapter, whatthedock has no OS clipboard read integration — it only ever
// writes OSC52 sequences for the terminal emulator to pick up. Read is
// intentionally unsupported rather than shelling out to xclip/wl-copy/
// pbpaste, which would be new scope beyond wiring up an editor. Terminal-
// native bracketed paste still works independent of this: ripple handles
// that as ordinary input, not through Clipboard.Read.
type osc52Clipboard struct{}

func (osc52Clipboard) Read() (string, error) {
	return "", errors.New("clipboard read is not supported here; use terminal paste")
}

func (osc52Clipboard) Write(text string) error {
	_, err := io.WriteString(clipboardWriter, osc52.New(text).String())
	return err
}

var editorClipboard ripple.Clipboard = osc52Clipboard{}

// editorVimMode, when true, starts every Ripple editor in vim mode. It is a
// package var (set once from the persisted vim-mode setting) so every editor
// instance picks it up through newEditorArea without threading settings
// through each call site — mirrors tidemail's internal/ui/editor_area.go.
var editorVimMode bool

// setEditorVimMode sets whether new editors use vim modal editing.
func setEditorVimMode(on bool) { editorVimMode = on }

// editorArea adapts ripple.Model to the subset of behavior whatthedock's
// overlays need: fixed clipboard wiring, vim-mode setup, remembered size,
// and cursor rendering that differs by vim sub-mode.
type editorArea struct {
	ed   ripple.Model
	w, h int

	// Styling, configurable by the consumer (e.g. from the active theme).
	CursorStyle      lipgloss.Style
	SelectedStyle    lipgloss.Style
	PlaceholderStyle lipgloss.Style
	KeyStyle         lipgloss.Style
	StringStyle      lipgloss.Style
	CommentStyle     lipgloss.Style
}

func newEditorArea() editorArea {
	ed := ripple.New()
	ed.SetClipboard(editorClipboard)
	if editorVimMode {
		ed.SetInputMode(ripple.ModeVim)
	}
	return editorArea{
		ed:               ed,
		CursorStyle:      lipgloss.NewStyle().Reverse(true),
		SelectedStyle:    lipgloss.NewStyle().Reverse(true),
		PlaceholderStyle: lipgloss.NewStyle().Faint(true),
		KeyStyle:         lipgloss.NewStyle().Foreground(lipgloss.Color("#7dcfff")).Bold(true),
		StringStyle:      lipgloss.NewStyle().Foreground(lipgloss.Color("#e0af68")),
		CommentStyle:     lipgloss.NewStyle().Foreground(lipgloss.Color("#565f89")).Italic(true),
	}
}

func (e editorArea) Value() string { return e.ed.Value() }

func (e *editorArea) SetValue(s string) { e.ed.SetValue(s) }

func (e *editorArea) SetPlaceholder(s string) { e.ed.SetPlaceholder(s) }

func (e *editorArea) Focus() { e.ed.Focus() }

func (e *editorArea) Blur() { e.ed.Blur() }

// SetSize sets width and height together so the editor's viewport is
// computed once against both real dimensions.
func (e *editorArea) SetSize(w, h int) { e.w, e.h = w, h; e.ed.SetSize(w, h) }

// vimMode reports whether this editor is in vim input mode.
func (e editorArea) vimMode() bool { return e.ed.InputMode() == ripple.ModeVim }

// EnterInsert puts a vim editor into Insert mode (a no-op otherwise), so
// opening the editor lets the user type immediately and Esc returns to
// Normal, instead of the first keystrokes (and first Esc) being swallowed
// by Normal-mode motions.
func (e *editorArea) EnterInsert() { e.ed.StartInsert() }

// Mode returns the vim sub-mode label ("NORMAL", "INSERT", …) for a status
// indicator, or "" when not in vim mode.
func (e editorArea) Mode() string { return e.ed.Mode() }

// CommandLine returns the ":" command line being typed, or "" when inactive.
func (e editorArea) CommandLine() string { return e.ed.CommandLine() }

// Update forwards every message to the editor and propagates the command it
// returns — clipboard side effects, and in vim mode ripple.SubmitMsg
// (":w"/":wq"/":x") and ripple.CancelMsg (":q" or a second Esc) for the host
// to act on.
func (e editorArea) Update(msg tea.Msg) (editorArea, tea.Cmd) {
	var cmd tea.Cmd
	e.ed, cmd = e.ed.Update(msg)
	return e, cmd
}

func (e editorArea) View() string {
	tokens := composeYAMLTokens(e.ed.Value())
	opts := ripple.Options{
		Selected:    func(s string) string { return e.SelectedStyle.Render(s) },
		Placeholder: func(s string) string { return e.PlaceholderStyle.Render(s) },
		StyleKey: func(offset int) string {
			if offset < 0 || offset >= len(tokens) {
				return ""
			}
			return tokens[offset]
		},
		Style: func(key, text string) string {
			switch key {
			case "key":
				return e.KeyStyle.Render(text)
			case "string":
				return e.StringStyle.Render(text)
			case "comment":
				return e.CommentStyle.Render(text)
			default:
				return text
			}
		},
	}
	switch e.ed.Mode() {
	case "INSERT":
		// A thin bar marks Insert mode, distinct from the Normal block.
		opts.Cursor = e.barCursor()
	case "":
		// Plain (non-vim) editing: the conventional block cursor.
		opts.Cursor = e.CursorStyle.Render(" ")
	default:
		// vim Normal/Visual/Command: the caret rests on a character, so render a
		// block that still shows the glyph underneath.
		opts.CursorRune = func(s string) string { return e.CursorStyle.Render(s) }
	}
	return e.ed.View(opts)
}

// barCursor renders a thin vertical bar for Insert mode by reusing the block
// cursor's colors swapped — a foreground bar on the body background, versus
// the Normal block's filled cell.
func (e editorArea) barCursor() string {
	return lipgloss.NewStyle().
		Foreground(e.CursorStyle.GetBackground()).
		Background(e.CursorStyle.GetForeground()).
		Render("▏")
}

// composeYAMLTokens classifies each rune offset in content by syntax kind
// for highlighting via ripple.Options.StyleKey/Style: "comment", "key",
// "string", or "" for everything else. It's a pragmatic line-oriented
// scanner for typical Compose override YAML (key: value mappings, "-" list
// items, quoted scalars, "#" comments) — not a full YAML grammar. Deciding
// whether the document is actually valid is lintComposeYAML's job; this only
// decides what looks like what.
func composeYAMLTokens(content string) []string {
	runes := []rune(content)
	kinds := make([]string, len(runes))
	for lineStart := 0; lineStart <= len(runes); {
		lineEnd := lineStart
		for lineEnd < len(runes) && runes[lineEnd] != '\n' {
			lineEnd++
		}
		tokenizeComposeYAMLLine(runes, kinds, lineStart, lineEnd)
		lineStart = lineEnd + 1
	}
	return kinds
}

func tokenizeComposeYAMLLine(runes []rune, kinds []string, start, end int) {
	i := start
	for i < end && (runes[i] == ' ' || runes[i] == '\t') {
		i++
	}
	// A "- " list marker stays unstyled; the entry itself is tokenized the
	// same as any other line from here.
	for i < end && runes[i] == '-' && i+1 < end && runes[i+1] == ' ' {
		i += 2
		for i < end && (runes[i] == ' ' || runes[i] == '\t') {
			i++
		}
	}
	contentStart := i

	if i < end && runes[i] == '#' {
		for j := i; j < end; j++ {
			kinds[j] = "comment"
		}
		return
	}

	if colon := findMappingColon(runes, contentStart, end); colon >= 0 {
		for j := contentStart; j <= colon; j++ {
			kinds[j] = "key"
		}
		i = colon + 1
	}

	inQuote := rune(0)
	for i < end {
		r := runes[i]
		switch {
		case inQuote != 0:
			kinds[i] = "string"
			if r == inQuote {
				inQuote = 0
			}
			i++
		case r == '"' || r == '\'':
			inQuote = r
			kinds[i] = "string"
			i++
		case r == '#':
			for j := i; j < end; j++ {
				kinds[j] = "comment"
			}
			return
		default:
			i++
		}
	}
}

// findMappingColon returns the offset of the ":" separating a YAML mapping
// key from its value on this line, or -1 if the line doesn't look like a
// mapping entry. A colon only counts as the key/value separator when it's
// followed by whitespace or end of line — distinguishing "image:" from the
// colon inside an unquoted "8080:80" port pair. It skips over a quoted key
// so `"new-service":` still finds the colon right after the closing quote.
func findMappingColon(runes []rune, start, end int) int {
	i := start
	if i < end && (runes[i] == '"' || runes[i] == '\'') {
		quote := runes[i]
		i++
		for i < end && runes[i] != quote {
			i++
		}
		if i < end {
			i++
		}
		if i < end && runes[i] == ':' && (i+1 >= end || runes[i+1] == ' ' || runes[i+1] == '\t') {
			return i
		}
		return -1
	}
	for i < end {
		if runes[i] == ':' && (i+1 >= end || runes[i+1] == ' ' || runes[i+1] == '\t') {
			return i
		}
		if runes[i] == '#' {
			return -1
		}
		i++
	}
	return -1
}
