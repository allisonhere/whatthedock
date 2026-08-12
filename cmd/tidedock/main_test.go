package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSettingsIgnoresInvalidConfig(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	path := filepath.Join(configDir, "tidedock", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() err = %v", err)
	}
	if err := os.WriteFile(path, []byte("{bad json"), 0o644); err != nil {
		t.Fatalf("WriteFile() err = %v", err)
	}

	gotPath, settings, err := loadSettings()
	if err != nil {
		t.Fatalf("loadSettings() err = %v", err)
	}
	if gotPath != path {
		t.Fatalf("path = %q, want %q", gotPath, path)
	}
	if settings.GraphStyle != "" || settings.GraphColor != "" || settings.ShowDeltas != nil {
		t.Fatalf("settings = %#v, want defaults after invalid config", settings)
	}
}
