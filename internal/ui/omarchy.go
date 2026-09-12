package ui

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/allisonhere/tideui"
	"github.com/charmbracelet/lipgloss"

	"github.com/allisonhere/whatthedock/internal/omarchy"
)

// ThemeNameMatchOmarchy is the pseudo-theme that follows the current Omarchy
// desktop theme, contrast-corrected. It is not a member of
// tideui.BuiltinThemes — its colours are resolved at runtime — but it is
// offered by pickableThemes().
const ThemeNameMatchOmarchy = "match-omarchy"

// omarchyThemeName is the spelling the rest of this package and the persisted
// settings use.
const omarchyThemeName = ThemeNameMatchOmarchy

// omarchyPlaceholderTheme is the theme picker row and the preview/fallback base
// before (or when) the live Omarchy palette resolves. It reuses a known-good
// built-in palette rather than whatthedock's own default, so an unresolved
// desktop theme is visibly distinct from "the app just kept its usual colours".
var omarchyPlaceholderTheme = func() tideui.Theme {
	t, ok := tideui.ThemeByName("catppuccin-mocha")
	if !ok {
		t = whatthedockTheme()
	}
	t.Name = ThemeNameMatchOmarchy
	return t
}()

// isMatchOmarchy reports whether name selects the Omarchy-following theme.
func isMatchOmarchy(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), ThemeNameMatchOmarchy)
}

func pickableThemes() []tideui.Theme {
	themes := append([]tideui.Theme{whatthedockTheme()}, tideui.BuiltinThemes...)
	live, ok := resolveOmarchyTheme()
	if !ok {
		live = omarchyPlaceholderTheme
	}
	return append(themes, live)
}

// omarchyTheme maps a raw Omarchy palette onto the tideui Theme struct. The
// result still needs contrastCorrectTheme before use.
func omarchyTheme(p omarchy.Palette) tideui.Theme {
	c := func(s string) lipgloss.Color { return lipgloss.Color(s) }

	bg := c(p.Background)
	fg := c(p.Foreground)

	border := c(p.Muted)
	if p.Muted == "" {
		border = mixColors(fg, bg, 0.5)
	}
	accent := c(p.Accent)
	if p.Accent == "" {
		accent = fg
	}
	statusBg := c(p.StatusBg)
	if p.StatusBg == "" {
		if isDark(bg) {
			statusBg = adjustLightness(bg, 0.05)
		} else {
			statusBg = adjustLightness(bg, -0.05)
		}
	}
	unread := c(p.Ok)
	if p.Ok == "" {
		unread = accent
	}
	errColor := c(p.Error)
	if p.Error == "" {
		errColor = accent
	}

	return tideui.Theme{
		Name:          ThemeNameMatchOmarchy,
		Bg:            bg,
		Fg:            fg,
		Border:        border,
		BorderFocus:   accent,
		Selected:      accent,
		Unread:        unread,
		Dimmed:        border,
		StatusBar:     statusBg,
		StatusFg:      fg,
		Error:         errColor,
		Overlay:       statusBg,
		OverlayBorder: accent,
	}
}

// omarchyFocusMinContrast is the contrast floor for the focused-pane border —
// higher than the text bar so the focus highlight stays the strongest element.
// It must not drop below tideui's own unexported paneFocusMinContrast: tideui
// grades that border with a readableText that does not nudge, so an accent left
// under its floor is replaced outright by #ffffff.
const omarchyFocusMinContrast = 7.0

// nudgeSurfaceForRatio lightens/darkens a surface (starting from bg) until it
// clears minRatio against bg itself, so a status-bar surface that matches the
// page background still reads as distinct.
func nudgeSurfaceForRatio(bg lipgloss.Color, minRatio float64) lipgloss.Color {
	const step = 0.03
	dir := step
	if !isDark(bg) {
		dir = -step
	}
	cur := bg
	for range 30 {
		next := adjustLightness(cur, dir)
		if next == cur {
			break
		}
		cur = next
		if contrastRatio(cur, bg) >= minRatio {
			return cur
		}
	}
	return cur
}

// surfaceFor picks the status-bar / modal surface. Nudging purely for
// separation from the page background overshoots: Omarchy's own surface sits at
// a 1.17 ratio against its background, a hair under a 1.2 cutoff, and replacing
// it with a 2.0-ratio surface leaves the theme's foreground at 3.94 against it
// — under tideui's 4.5 floor, so readableText swaps every modal row, picker
// entry and status label for pure #ffffff. Keeping a surface the theme already
// chose is worth more than a marginal gain in background separation, so a
// usable surface is kept and any nudge stops before the foreground stops
// clearing 4.5.
func surfaceFor(bg, fg, surface lipgloss.Color) lipgloss.Color {
	const minSeparation = 1.08
	const textFloor = 4.5
	usable := func(c lipgloss.Color) bool {
		return contrastRatio(c, bg) >= minSeparation && contrastRatio(fg, c) >= textFloor
	}
	if surface != "" && usable(surface) {
		return surface
	}
	step := 0.03
	if !isDark(bg) {
		step = -step
	}
	cur := bg
	var best lipgloss.Color
	for range 30 {
		next := adjustLightness(cur, step)
		if next == cur {
			break
		}
		cur = next
		if contrastRatio(fg, cur) < textFloor {
			break // any further and the foreground gets replaced by white
		}
		best = cur
		if contrastRatio(cur, bg) >= 1.3 {
			return cur
		}
	}
	if best != "" {
		return best
	}
	if surface != "" {
		return surface
	}
	return nudgeSurfaceForRatio(bg, 1.3)
}

// contrastCorrectTheme nudges each directly-consumed Theme field until it
// clears the readability floors against the theme background, so an arbitrary
// desktop palette can't produce an unreadable UI. Light/dark-agnostic — the
// helpers branch on isDark(t.Bg) internally.
func contrastCorrectTheme(t tideui.Theme) tideui.Theme {
	out := t

	out.Fg = readableText(t.Fg, t.Bg, 4.5)
	out.Dimmed = mutedText(out.Fg, t.Bg)
	out.BorderFocus = accentReadableOn(t.BorderFocus, t.Bg, omarchyFocusMinContrast)
	out.Selected = accentReadableOn(t.Selected, t.Bg, 4.5)
	out.OverlayBorder = accentReadableOn(t.OverlayBorder, t.Bg, 4.5)
	out.Border = accentReadableOn(t.Border, t.Bg, 3.0)
	out.Unread = accentReadableOn(t.Unread, t.Bg, 3.0)
	out.Error = accentReadableOn(t.Error, t.Bg, 4.5)

	out.StatusBar = surfaceFor(t.Bg, out.Fg, out.StatusBar)
	out.Overlay = out.StatusBar
	out.StatusFg = readableText(t.StatusFg, out.StatusBar, 4.5)

	return out
}

// resolveOmarchyTheme reads the live Omarchy palette and returns a
// contrast-corrected Theme. ok is false when Omarchy isn't available.
func resolveOmarchyTheme() (tideui.Theme, bool) {
	p, ok := omarchy.CurrentPalette()
	if !ok {
		return tideui.Theme{}, false
	}
	return contrastCorrectTheme(omarchyTheme(p)), true
}

// loadOmarchyTheme is the name the model uses for resolveOmarchyTheme.
func loadOmarchyTheme() (tideui.Theme, bool) { return resolveOmarchyTheme() }

// currentOmarchyThemeName returns the active Omarchy theme slug for a settings
// hint, or "" when Omarchy isn't available.
func currentOmarchyThemeName() string {
	if p, ok := omarchy.CurrentPalette(); ok {
		return p.Name
	}
	return ""
}

func omarchySignature() string { return omarchy.CurrentSignature() }

// ── colour helpers ──────────────────────────────────────────────────────────

// hexToRGB parses a #rrggbb color into [0,1] float components.
func hexToRGB(c lipgloss.Color) (r, g, b float64, ok bool) {
	s := strings.TrimPrefix(string(c), "#")
	if len(s) != 6 {
		return
	}
	ri, e1 := strconv.ParseUint(s[0:2], 16, 8)
	gi, e2 := strconv.ParseUint(s[2:4], 16, 8)
	bi, e3 := strconv.ParseUint(s[4:6], 16, 8)
	if e1 != nil || e2 != nil || e3 != nil {
		return
	}
	return float64(ri) / 255, float64(gi) / 255, float64(bi) / 255, true
}

// mixColors blends tint into base by amount in sRGB space.
func mixColors(base, tint lipgloss.Color, amount float64) lipgloss.Color {
	br, bg, bb, baseOK := hexToRGB(base)
	tr, tg, tb, tintOK := hexToRGB(tint)
	if !baseOK || !tintOK {
		return base
	}
	amount = math.Max(0, math.Min(1, amount))
	mix := func(a, b float64) int {
		return int(math.Round((a*(1-amount) + b*amount) * 255))
	}
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", mix(br, tr), mix(bg, tg), mix(bb, tb)))
}

func srgbLinearize(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

// colorLuminance returns the WCAG relative luminance of a hex color.
func colorLuminance(c lipgloss.Color) float64 {
	r, g, b, ok := hexToRGB(c)
	if !ok {
		return 0
	}
	return 0.2126*srgbLinearize(r) + 0.7152*srgbLinearize(g) + 0.0722*srgbLinearize(b)
}

// isDark reports whether a color is perceptually dark.
func isDark(c lipgloss.Color) bool { return colorLuminance(c) < 0.179 }

// contrastFg returns a light or dark foreground color for legible text on bg.
func contrastFg(bg lipgloss.Color) lipgloss.Color {
	if isDark(bg) {
		return lipgloss.Color("#ffffff")
	}
	return lipgloss.Color("#000000")
}

func contrastRatio(a, b lipgloss.Color) float64 {
	la, lb := colorLuminance(a), colorLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func readableText(preferred, bg lipgloss.Color, minRatio float64) lipgloss.Color {
	if preferred != "" && contrastRatio(preferred, bg) >= minRatio {
		return preferred
	}
	return contrastFg(bg)
}

// accentReadableOn returns an accent that meets minRatio against bg, nudging
// HSL lightness (same hue) before falling back to readableText/contrastFg.
func accentReadableOn(accent, bg lipgloss.Color, minRatio float64) lipgloss.Color {
	if accent == "" {
		return ""
	}
	if _, _, _, ok := hexToRGB(accent); !ok {
		return accent
	}
	if contrastRatio(accent, bg) >= minRatio {
		return accent
	}
	const step = 0.06
	const maxSteps = 12
	cur := accent
	for range maxSteps {
		if isDark(bg) {
			cur = adjustLightness(cur, step)
		} else {
			cur = adjustLightness(cur, -step)
		}
		if contrastRatio(cur, bg) >= minRatio {
			return cur
		}
	}
	return readableText(accent, bg, minRatio)
}

func mutedText(text, bg lipgloss.Color) lipgloss.Color {
	if isDark(bg) {
		candidate := adjustLightness(text, -0.20)
		if contrastRatio(candidate, bg) >= 3 {
			return candidate
		}
		return adjustLightness(text, -0.12)
	}
	candidate := adjustLightness(text, 0.20)
	if contrastRatio(candidate, bg) >= 3 {
		return candidate
	}
	return adjustLightness(text, 0.12)
}

func rgbToHSL(r, g, b float64) (h, s, l float64) {
	hi := math.Max(r, math.Max(g, b))
	lo := math.Min(r, math.Min(g, b))
	l = (hi + lo) / 2
	if hi == lo {
		return 0, 0, l
	}
	d := hi - lo
	if l > 0.5 {
		s = d / (2 - hi - lo)
	} else {
		s = d / (hi + lo)
	}
	switch hi {
	case r:
		h = (g - b) / d
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	case b:
		h = (r-g)/d + 4
	}
	h /= 6
	return
}

func hue2rgb(p, q, t float64) float64 {
	if t < 0 {
		t++
	}
	if t > 1 {
		t--
	}
	switch {
	case t < 1.0/6:
		return p + (q-p)*6*t
	case t < 0.5:
		return q
	case t < 2.0/3:
		return p + (q-p)*(2.0/3-t)*6
	default:
		return p
	}
}

func hslToRGB(h, s, l float64) (r, g, b float64) {
	if s == 0 {
		return l, l, l
	}
	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q
	return hue2rgb(p, q, h+1.0/3), hue2rgb(p, q, h), hue2rgb(p, q, h-1.0/3)
}

// adjustLightness nudges a color's HSL lightness by delta (positive = lighter).
// Returns the original color unchanged on parse failure.
func adjustLightness(c lipgloss.Color, delta float64) lipgloss.Color {
	r, g, b, ok := hexToRGB(c)
	if !ok {
		return c
	}
	h, s, l := rgbToHSL(r, g, b)
	l = math.Max(0, math.Min(1, l+delta))
	nr, ng, nb := hslToRGB(h, s, l)
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x",
		uint8(math.Round(nr*255)),
		uint8(math.Round(ng*255)),
		uint8(math.Round(nb*255)),
	))
}
