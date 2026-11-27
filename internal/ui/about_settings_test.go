package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSettingsAboutOpensAboutAndEscReturnsToSettings(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 30
	model.overlay = overlaySettings
	model.settingsPaneFocus = true
	found := false
	for i, row := range model.settingsRows() {
		if row.label == "About WhatTheDock" {
			model.settingsCursor, found = i, true
		}
	}
	if !found {
		t.Fatal("settings has no \"About WhatTheDock\" row")
	}

	updated, cmd := model.handleSettingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	opened := updated.(Model)
	if opened.overlay != overlayAbout {
		t.Fatalf("overlay = %v after selecting About, want overlayAbout", opened.overlay)
	}
	if cmd == nil {
		t.Fatal("opening About from settings returned no tick cmd; its animation would never start")
	}

	closed, _ := opened.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got := closed.(Model); got.overlay != overlaySettings || got.aboutFromSettings {
		t.Fatalf("after esc: overlay = %v aboutFromSettings = %v, want settings and the flag cleared", got.overlay, got.aboutFromSettings)
	}
}

// Opened the usual way (the A key), About still closes to the main view —
// the return-to-settings behaviour belongs to Settings > About only.
func TestAboutKeyStillClosesToMainView(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 30
	// A stale flag from an earlier visit must not redirect this close.
	model.aboutFromSettings = true

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	opened := updated.(Model)
	if opened.overlay != overlayAbout {
		t.Fatalf("overlay = %v after A, want overlayAbout", opened.overlay)
	}
	closed, _ := opened.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got := closed.(Model).overlay; got != overlayNone {
		t.Fatalf("overlay after esc = %v, want overlayNone", got)
	}
}
