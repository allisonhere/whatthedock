package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/allisonhere/whatthedock/internal/config"
	"github.com/allisonhere/whatthedock/internal/omarchy"
)

func TestOmarchyRecoveryAndPickerRenderCurrentPalette(t *testing.T) {
	withTrueColorProfile(t)
	root := t.TempDir()
	t.Setenv("OMARCHY_DIR", root)
	m := NewModelWithSettings(newFakeProvider(), config.Settings{Theme: omarchyThemeName}, "")
	m.width, m.height = 100, 30
	if m.theme.Name != omarchyThemeName {
		t.Fatal("unavailable palette silently discarded persisted match-omarchy")
	}
	dir := filepath.Join(root, "current", "theme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(bg, accent string) {
		t.Helper()
		body := "background = \"" + bg + "\" # live desktop\nforeground = \"#eeeeee\"\naccent = \"" + accent + "\"\n"
		if err := os.WriteFile(filepath.Join(dir, "colors.toml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("#123456", "#aaffcc")
	next, _ := m.updateStep(omarchyThemeTickMsg{})
	m = next.(Model)
	if !strings.Contains(m.View(), "48;2;18;52;86") {
		t.Fatal("rendered frame does not contain recovered Omarchy background RGB 18,52,86")
	}
	// A desktop switch while a built-in theme is active must be reflected
	// when opening and confirming Omarchy, without restarting the app.
	m.theme = whatthedockTheme()
	write("#452311", "#ffccaa")
	m.openThemePicker()
	for range len(pickableThemes()) {
		next, _ = m.handleOverlayKey(tea.KeyMsg{Type: tea.KeyDown})
		m = next.(Model)
	}
	next, _ = m.handleOverlayKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.theme.Name != omarchyThemeName || !strings.Contains(m.View(), "48;2;69;35;17") {
		t.Fatal("confirmed Omarchy frame did not use the new desktop palette")
	}
}

func TestOmarchyThemeFromPaletteMapsTideUIRoles(t *testing.T) {
	theme := contrastCorrectTheme(omarchyTheme(omarchy.Palette{
		Background: "#1a1b26", Foreground: "#a9b1d6", Accent: "#7aa2f7",
		Selection: "#272833", Muted: "#686d86", StatusBg: "#272833",
		Error: "#f7768e", Ok: "#9ece6a",
	}))
	wants := map[string]string{
		"name": omarchyThemeName, "bg": "#1a1b26", "fg": "#a9b1d6",
		// focus: the accent is lifted off #7aa2f7 (6.79 on this bg) to clear
		// omarchyFocusMinContrast; left alone, tideui's readableText replaces
		// the focused border outright with #ffffff.
		// selected: comes from the accent, not Palette.Selection.
		// status: Palette.StatusBg is kept as-is. It only reaches a 1.17 ratio
		// against the page background, but nudging it brighter for separation
		// drops the foreground under tideui's 4.5 floor, which replaces every
		// modal row and picker entry with #ffffff. See surfaceFor.
		"border": "#686d86", "focus": "#97b6f9", "selected": "#7aa2f7",
		"status": "#272833", "error": "#f7768e", "ok": "#9ece6a",
	}
	gots := map[string]string{
		"name": theme.Name, "bg": string(theme.Bg), "fg": string(theme.Fg),
		"border": string(theme.Border), "focus": string(theme.BorderFocus), "selected": string(theme.Selected),
		"status": string(theme.StatusBar), "error": string(theme.Error), "ok": string(theme.Unread),
	}
	for name, want := range wants {
		if got := gots[name]; got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestOmarchyThemeSeparatesMissingStatusSurface(t *testing.T) {
	theme := contrastCorrectTheme(omarchyTheme(omarchy.Palette{
		Background: "#111111", Foreground: "#eeeeee", Accent: "#7799cc",
	}))
	if theme.StatusBar == theme.Bg {
		t.Fatal("status bar must not disappear into the page background")
	}
	if theme.Border == theme.Bg {
		t.Fatal("border must not disappear into the page background")
	}
}

func TestMatchOmarchyLoadsAndReloadsLegacyCurrentTheme(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "current")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTheme := func(name, bg string) string {
		dir := filepath.Join(root, "themes", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "[palette]\nbg = \"" + bg + "\"\nsurface = \"#222222\"\ntext = \"#eeeeee\"\nmuted = \"#777777\"\naccent = \"#7799cc\"\n"
		if err := os.WriteFile(filepath.Join(dir, "colors.toml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	first := writeTheme("first", "#111111")
	second := writeTheme("second", "#121212")
	link := filepath.Join(current, "theme")
	if err := os.Symlink(first, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Setenv("OMARCHY_DIR", root)

	model := NewModelWithSettings(newFakeProvider(), config.Settings{Theme: omarchyThemeName}, "")
	if got := string(model.theme.Bg); got != "#111111" {
		t.Fatalf("initial background = %q, want #111111", got)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, link); err != nil {
		t.Fatal(err)
	}
	next, _ := model.updateStep(omarchyThemeTickMsg{})
	model = next.(Model)
	if got := string(model.theme.Bg); got != "#121212" {
		t.Fatalf("reloaded background = %q, want #121212", got)
	}
}

func TestMatchOmarchyProvidesTerminalColorSequences(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "current", "theme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "bg = \"#1a1b26\"\ntext = \"#a9b1d6\"\naccent = \"#7aa2f7\"\n"
	if err := os.WriteFile(filepath.Join(dir, "colors.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OMARCHY_DIR", root)
	model := NewModelWithSettings(newFakeProvider(), config.Settings{Theme: omarchyThemeName}, "")
	set, reset := model.TerminalColorSequences()
	if want := "\x1b]10;#a9b1d6\x07\x1b]11;#1a1b26\x07"; set != want {
		t.Fatalf("set sequence = %q, want %q", set, want)
	}
	if want := "\x1b]110\x07\x1b]111\x07"; reset != want {
		t.Fatalf("reset sequence = %q, want %q", reset, want)
	}
}

// The nudge must preserve hue rather than washing the accent out: a corrected
// accent that is merely "not white" but grey would be just as wrong.
func TestAccentCorrectionKeepsHue(t *testing.T) {
	got := accentReadableOn("#7aa2f7", "#1a1b26", omarchyFocusMinContrast)
	if got != "#97b6f9" {
		t.Fatalf("corrected accent = %q, want %q", got, "#97b6f9")
	}
	r, g, b, ok := hexToRGB(got)
	if !ok || !(b > g && g > r) {
		t.Fatalf("corrected accent %q lost its blue bias (r=%.2f g=%.2f b=%.2f)", got, r, g, b)
	}
}
