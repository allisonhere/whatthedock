package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSettingsMissingFileReturnsDefaults(t *testing.T) {
	settings, err := LoadSettings(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("LoadSettings() err = %v, want nil for missing file", err)
	}
	if settings != (Settings{}) {
		t.Fatalf("settings = %#v, want zero settings", settings)
	}
}

func TestLoadSettingsInvalidJSONReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{bad json"), 0o644); err != nil {
		t.Fatalf("WriteFile() err = %v", err)
	}
	if _, err := LoadSettings(path); err == nil {
		t.Fatal("LoadSettings() err = nil, want invalid JSON error")
	}
}

func TestSaveAndLoadSettingsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.json")
	showDeltas := false
	want := Settings{
		GraphStyle:      "braille",
		GraphColor:      "mono",
		LogColor:        "severity",
		ShowDeltas:      &showDeltas,
		StatsRefresh:    "5s",
		DefaultActivity: "stats",
	}
	if err := SaveSettings(path, want); err != nil {
		t.Fatalf("SaveSettings() err = %v", err)
	}
	got, err := LoadSettings(path)
	if err != nil {
		t.Fatalf("LoadSettings() err = %v", err)
	}
	if got.GraphStyle != want.GraphStyle ||
		got.GraphColor != want.GraphColor ||
		got.LogColor != want.LogColor ||
		got.ShowDeltas == nil ||
		*got.ShowDeltas != showDeltas ||
		got.StatsRefresh != want.StatsRefresh ||
		got.DefaultActivity != want.DefaultActivity {
		t.Fatalf("settings = %#v, want %#v", got, want)
	}
}

func TestSettingsPathUsesUserConfigDir(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	path, err := SettingsPath()
	if err != nil {
		t.Fatalf("SettingsPath() err = %v", err)
	}
	want := filepath.Join(configDir, "whatthedock", settingsFileName)
	if path != want {
		t.Fatalf("SettingsPath() = %q, want %q", path, want)
	}
}
