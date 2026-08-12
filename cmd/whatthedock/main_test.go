package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVersionStringDefaultsToDev(t *testing.T) {
	t.Setenv("WHATTHEDOCK_PROVIDER", "")
	oldVersion, oldCommit, oldDate := version, commit, date
	t.Cleanup(func() {
		version, commit, date = oldVersion, oldCommit, oldDate
	})
	version, commit, date = "dev", "", ""

	if got := versionString(); got != "whatthedock dev" {
		t.Fatalf("versionString() = %q, want dev version", got)
	}
}

func TestVersionStringIncludesBuildMetadata(t *testing.T) {
	oldVersion, oldCommit, oldDate := version, commit, date
	t.Cleanup(func() {
		version, commit, date = oldVersion, oldCommit, oldDate
	})
	version, commit, date = "v1.2.3", "abc1234", "2026-08-12"

	want := "whatthedock v1.2.3 commit abc1234 built 2026-08-12"
	if got := versionString(); got != want {
		t.Fatalf("versionString() = %q, want %q", got, want)
	}
}

func TestLoadSettingsIgnoresInvalidConfig(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	path := filepath.Join(configDir, "whatthedock", "settings.json")
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
