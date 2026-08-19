package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/allisonhere/whatthedock/internal/config"
	"github.com/allisonhere/whatthedock/internal/systems"
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

// TestProviderForModeSurfacesErrorAndSystemName is the regression test for
// a live report: a configured active system (a password-auth SSH host
// named "jarvis") that couldn't connect used to make the whole app refuse
// to launch. main() now falls back to local Docker on this error instead —
// providerForMode's job in that fix is just to fail cleanly and say which
// system it was trying to reach, so main() can name it in the fallback
// status. This only checks providerForMode itself; the actual fallback-to-
// local behavior lives in main() and isn't unit tested (main() isn't
// structured to make that convenient — see TestWithStatusOverrides
// InitialConnectingMessage in internal/ui for the model-side half).
func TestProviderForModeSurfacesErrorAndSystemName(t *testing.T) {
	settings := config.Settings{
		ActiveSystem: "jarvis",
		Systems: []config.System{{
			ID:           "jarvis",
			Name:         "jarvis",
			Kind:         "ssh",
			SSHHost:      "192.168.86.74",
			SSHUser:      "allie",
			SSHAuth:      "password",
			RemoteSocket: "/var/run/docker.sock",
			LocalSocket:  filepath.Join(t.TempDir(), "jarvis.sock"),
		}},
	}
	boom := errors.New("boom")
	factory := systems.Factory{Runner: func(context.Context, string, ...string) error { return boom }}

	provider, systemName, err := providerForMode(context.Background(), false, settings, factory)

	if provider != nil {
		t.Fatalf("provider = %v, want nil on connect failure", provider)
	}
	if systemName != "jarvis" {
		t.Fatalf("systemName = %q, want jarvis (even on error, so a caller can name it)", systemName)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap the runner's error", err)
	}
}

func TestProviderForModeReturnsLocalOnDefaultSettings(t *testing.T) {
	factory := systems.NewFactory()

	provider, systemName, err := providerForMode(context.Background(), false, config.Settings{}, factory)

	if err != nil {
		t.Fatalf("providerForMode() err = %v, want nil for the default local system", err)
	}
	if provider == nil {
		t.Fatal("provider = nil, want a local provider")
	}
	if systemName != "local" {
		t.Fatalf("systemName = %q, want local", systemName)
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
