package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// tokensAt renders content's classified kinds back as text, one line per
// input line, with each rune replaced by a single-letter code, so test
// expectations read like a small diagram of the input.
func tokensAt(content string) string {
	kinds := composeYAMLTokens(content)
	var out strings.Builder
	for _, k := range kinds {
		switch k {
		case "key":
			out.WriteByte('K')
		case "string":
			out.WriteByte('S')
		case "comment":
			out.WriteByte('C')
		case "":
			out.WriteByte('.')
		default:
			out.WriteByte('?')
		}
	}
	return out.String()
}

func TestComposeYAMLTokensSimpleMapping(t *testing.T) {
	content := "image: redis:7"
	want := "KKKKKK........" // "image:" is the key; " redis:7" is plain value
	if len(want) != len([]rune(content)) {
		t.Fatalf("test fixture bug: want len %d != content len %d", len(want), len([]rune(content)))
	}
	if got := tokensAt(content); got != want {
		t.Fatalf("tokensAt(%q) = %q, want %q", content, got, want)
	}
}

func TestComposeYAMLTokensQuotedKeyAndStringValue(t *testing.T) {
	content := `"new-service": "redis:7"`
	kinds := composeYAMLTokens(content)
	runes := []rune(content)

	keyEnd := len(`"new-service":`)
	for i := 0; i < keyEnd; i++ {
		if kinds[i] != "key" {
			t.Fatalf("offset %d (%q) kind = %q, want key", i, string(runes[i]), kinds[i])
		}
	}
	valueStart := len(`"new-service": `)
	for i := valueStart; i < len(runes); i++ {
		if kinds[i] != "string" {
			t.Fatalf("offset %d (%q) kind = %q, want string", i, string(runes[i]), kinds[i])
		}
	}
}

func TestComposeYAMLTokensComment(t *testing.T) {
	content := "  # a note\nimage: redis"
	kinds := composeYAMLTokens(content)
	// Leading indentation before "#" isn't styled (nothing visually differs
	// for whitespace); the "#" itself through end of line is the comment.
	hashOffset := len("  ")
	for i := hashOffset; i < len("  # a note"); i++ {
		if kinds[i] != "comment" {
			t.Fatalf("offset %d kind = %q, want comment", i, kinds[i])
		}
	}
	lineTwoStart := len("  # a note\n")
	if kinds[lineTwoStart] != "key" {
		t.Fatalf("second line's key offset kind = %q, want key", kinds[lineTwoStart])
	}
}

func TestComposeYAMLTokensListItem(t *testing.T) {
	content := `  - "6379:6379"`
	kinds := composeYAMLTokens(content)
	runes := []rune(content)
	dashEnd := len(`  - `)
	for i := 0; i < dashEnd; i++ {
		if kinds[i] != "" {
			t.Fatalf("list marker offset %d kind = %q, want plain", i, kinds[i])
		}
	}
	for i := dashEnd; i < len(runes); i++ {
		if kinds[i] != "string" {
			t.Fatalf("offset %d (%q) kind = %q, want string", i, string(runes[i]), kinds[i])
		}
	}
}

func TestComposeYAMLTokensUnquotedPortIsNotMistakenForKey(t *testing.T) {
	// No space after the colon inside "8080:80" — must not be classified as
	// a mapping key, unlike a real "key: value" line.
	content := `  - 8080:80`
	kinds := composeYAMLTokens(content)
	for i, k := range kinds {
		if k == "key" {
			t.Fatalf("offset %d classified as key in unquoted port pair %q: %v", i, content, kinds)
		}
	}
}

func TestComposeYAMLTokensEmptyContent(t *testing.T) {
	if kinds := composeYAMLTokens(""); len(kinds) != 0 {
		t.Fatalf("composeYAMLTokens(\"\") = %v, want empty", kinds)
	}
}

func TestEditorAreaViewAppliesHighlighting(t *testing.T) {
	original := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(original) })

	e := newEditorArea()
	e.SetSize(40, 3)
	e.SetValue("image: redis:7\n# a note")
	e.Focus()

	view := e.View()
	if !strings.Contains(view, "\x1b[") {
		t.Fatalf("expected styled (ANSI) output for highlighted YAML, got plain: %q", view)
	}
}

// TestEditorAreaViewUsesBlackBackground guards against a real bug: the
// syntax styles only set Foreground, and the Style callback's default case
// (plain runs — indentation, punctuation, unquoted scalars) returned text
// completely unstyled, so once any styled run reset, everything after it
// fell through to whatever sat behind the editor instead of a solid black
// terminal surface.
func TestEditorAreaViewUsesBlackBackground(t *testing.T) {
	original := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(original) })

	e := newEditorArea()
	e.SetSize(40, 3)
	e.SetValue("image: redis:7")
	e.Focus()

	view := e.View()
	if !strings.Contains(view, "48;2;0;0;0") {
		t.Fatalf("expected a black (#000000) background SGR in the rendered view, got: %q", view)
	}
}

func TestHighlightComposeYAML(t *testing.T) {
	original := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(original) })

	content := "services:\n  demo:\n    image: redis:7\n    # a note"
	lines := highlightComposeYAML(content)

	if len(lines) != 4 {
		t.Fatalf("highlightComposeYAML() returned %d lines, want 4: %#v", len(lines), lines)
	}
	for i, want := range []string{"services", "demo", "image", "# a note"} {
		if !strings.Contains(ansi.Strip(lines[i]), want) {
			t.Fatalf("line %d = %q, want it to contain %q", i, lines[i], want)
		}
	}
	if !strings.Contains(lines[0], "\x1b[") {
		t.Fatalf("line 0 (a key) has no ANSI styling: %q", lines[0])
	}
	if !strings.Contains(lines[3], "\x1b[") {
		t.Fatalf("line 3 (a comment) has no ANSI styling: %q", lines[3])
	}
}
