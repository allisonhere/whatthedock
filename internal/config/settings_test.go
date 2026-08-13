package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadSettingsMissingFileReturnsDefaults(t *testing.T) {
	settings, err := LoadSettings(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("LoadSettings() err = %v, want nil for missing file", err)
	}
	if !reflect.DeepEqual(settings, Settings{}) {
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
		ActiveSystem:    "jarvis",
		Systems: []System{{
			ID:           "jarvis",
			Name:         "Jarvis",
			Kind:         "ssh",
			SSHHost:      "allie@jarvis",
			RemoteSocket: "/var/run/docker.sock",
			LocalSocket:  "/tmp/jarvis.sock",
		}},
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
		got.DefaultActivity != want.DefaultActivity ||
		!reflect.DeepEqual(got.Systems, want.Systems) ||
		got.ActiveSystem != want.ActiveSystem {
		t.Fatalf("settings = %#v, want %#v", got, want)
	}
}

func TestNormalizeSystemsCreatesLocalDefault(t *testing.T) {
	settings := NormalizeSystems(Settings{})
	if len(settings.Systems) != 1 {
		t.Fatalf("systems = %#v, want one default", settings.Systems)
	}
	if settings.ActiveSystem != "local" || settings.Systems[0] != DefaultSystem() {
		t.Fatalf("settings = %#v, want active local default", settings)
	}
}

func TestNormalizeSystemsDefaultsSSHFields(t *testing.T) {
	settings := NormalizeSystems(Settings{
		Systems: []System{{ID: "jarvis", Name: "Jarvis", Kind: "ssh", SSHHost: "allie@jarvis"}},
	})
	got := settings.Systems[0]
	if got.SSHHost != "jarvis" || got.SSHUser != "allie" {
		t.Fatalf("ssh target = user:%q host:%q, want allie/jarvis", got.SSHUser, got.SSHHost)
	}
	if got.SSHAuth != "config" {
		t.Fatalf("SSHAuth = %q, want config default", got.SSHAuth)
	}
	if got.RemoteSocket != "/var/run/docker.sock" {
		t.Fatalf("RemoteSocket = %q, want Docker socket default", got.RemoteSocket)
	}
	if got.LocalSocket == "" {
		t.Fatal("LocalSocket is empty, want generated temp socket")
	}
	if settings.ActiveSystem != "jarvis" {
		t.Fatalf("ActiveSystem = %q, want jarvis", settings.ActiveSystem)
	}
}

func TestNormalizeSystemsPreservesSeparateSSHUserHostPort(t *testing.T) {
	settings := NormalizeSystems(Settings{
		Systems: []System{{ID: "jarvis", Name: "Jarvis", Kind: "ssh", SSHHost: "jarvis.lan", SSHUser: "allie", SSHPort: "2222"}},
	})
	got := settings.Systems[0]
	if got.SSHHost != "jarvis.lan" || got.SSHUser != "allie" || got.SSHPort != "2222" {
		t.Fatalf("ssh fields = user:%q host:%q port:%q, want allie/jarvis.lan/2222", got.SSHUser, got.SSHHost, got.SSHPort)
	}
}

func TestNormalizeSystemsPreservesSSHPasswordPrompt(t *testing.T) {
	settings := NormalizeSystems(Settings{
		Systems: []System{{ID: "jarvis", Name: "Jarvis", Kind: "ssh", SSHHost: "allie@jarvis", SSHAuth: "password"}},
	})
	if got := settings.Systems[0].SSHAuth; got != "password" {
		t.Fatalf("SSHAuth = %q, want password", got)
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
