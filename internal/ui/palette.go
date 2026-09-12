package ui

import (
	"fmt"
	"math"

	"github.com/allisonhere/tideui"
	"github.com/charmbracelet/lipgloss"
)

// themePalette is the semantic accent set the widgets were originally written
// against as fixed hex literals (#80c990 for healthy, #e06c75 for dead, and so
// on — whatthedock's own default theme, inlined). Deriving the same roles from
// the active theme is what lets container state, log severity, stats graphs and
// the inspector follow whichever theme is selected, instead of leaving ~150
// cells of built-in blue and green painted over every other palette.
type themePalette struct {
	OK    lipgloss.Color // healthy, running, success
	OKAlt lipgloss.Color // a second success-family green, so two "good"
	//                      metrics on one dashboard stay tellable apart
	Warn  lipgloss.Color // restarting, warnings, mid-range graph heat
	Bad   lipgloss.Color // errors, dead, unhealthy
	Info  lipgloss.Color // identifiers: images, networks, keys
	Alt   lipgloss.Color // secondary accent: network in/out, log values
	Alt2  lipgloss.Color // third accent: mounts, uptime, search matches
	Muted lipgloss.Color // labels and de-emphasised text
}

// paletteFor derives the accent set from a theme. Every colour is run through
// accentReadableOn so a desktop palette with a low-contrast accent stays
// legible rather than being snapped to white by tideui's readableText.
func paletteFor(t tideui.Theme) themePalette {
	bg := t.Bg
	ok := accentReadableOn(t.Unread, bg, 3.0)
	bad := accentReadableOn(t.Error, bg, 3.0)
	info := accentReadableOn(t.BorderFocus, bg, 3.0)
	muted := t.Dimmed
	if muted == "" {
		muted = t.Fg
	}
	return themePalette{
		OK:    ok,
		OKAlt: accentReadableOn(mixColors(ok, info, 0.3), bg, 3.0),
		Warn:  accentReadableOn(warnFrom(bad), bg, 3.0),
		Bad:   bad,
		Info:  info,
		Alt:   accentReadableOn(adjustLightness(info, -0.08), bg, 3.0),
		Alt2:  accentReadableOn(rotateHue(info, 60), bg, 3.0),
		Muted: muted,
	}
}

// rotateHue turns a colour around the wheel by deg, keeping its saturation and
// lightness, to mint a third accent that still belongs to the theme. Themes
// carry no "purple" slot, but mounts, uptime and search matches were written
// against one, so it is derived from the primary accent instead of imported.
func rotateHue(c lipgloss.Color, deg float64) lipgloss.Color {
	r, g, b, ok := hexToRGB(c)
	if !ok {
		return c
	}
	h, s, l := rgbToHSL(r, g, b)
	h = math.Mod(h+deg/360.0, 1)
	if h < 0 {
		h++
	}
	nr, ng, nb := hslToRGB(h, s, l)
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x",
		uint8(math.Round(nr*255)), uint8(math.Round(ng*255)), uint8(math.Round(nb*255))))
}

// warnFrom derives an amber warning accent from the theme's error colour by
// rotating hue to ~40 degrees while keeping the theme's own saturation and
// lightness, so warnings read as amber in the theme's voice. Blending error
// with success instead produces a muddy brown, because the two sit on opposite
// sides of the wheel and cancel.
func warnFrom(bad lipgloss.Color) lipgloss.Color {
	r, g, b, ok := hexToRGB(bad)
	if !ok {
		return bad
	}
	_, s, l := rgbToHSL(r, g, b)
	if s < 0.25 {
		s = 0.55 // a desaturated error colour would yield a grey "warning"
	}
	nr, ng, nb := hslToRGB(40.0/360.0, s, l)
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x",
		uint8(math.Round(nr*255)), uint8(math.Round(ng*255)), uint8(math.Round(nb*255))))
}

// activePalette is the accent set for the theme currently being rendered.
// Bubble Tea runs Update and View on a single goroutine, so a package-level
// value is safe here and spares every widget helper a themePalette parameter.
// Model.View refreshes it before painting a frame.
var activePalette = paletteFor(whatthedockTheme())

// setActivePalette refreshes the derived accents for t. Called from View so it
// tracks the live theme, including match-omarchy's 2s desktop-follow poll.
func setActivePalette(t tideui.Theme) {
	activePalette = paletteFor(t)
}

// emphasisColors derives a foreground/background pair for an inline banner
// painted in role's colour — the "N SELECTED", "DELETE N?" and "REMOVING..."
// command strips. The background is a low mix of role into the page so the
// strip reads as part of the active theme rather than a fixed red or amber.
func emphasisColors(renderer tideui.Renderer, role lipgloss.Color) (fg, bg lipgloss.Color) {
	bg = mixColors(renderer.Styles.Theme.Bg, role, 0.22)
	fg = accentReadableOn(role, bg, 4.5)
	return fg, bg
}

// inspectorPlainFg is the default foreground for un-highlighted YAML text in
// the inspector's Compose view, which paints onto composeEditorBG rather than
// the page background.
func inspectorPlainFg() lipgloss.Color {
	return readableText(activePalette.Muted, composeEditorBG, 4.5)
}
