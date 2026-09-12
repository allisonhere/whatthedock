package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/allisonhere/whatthedock/internal/config"
	"github.com/allisonhere/whatthedock/internal/domain"
)

// fakeDockerChecker is dockerChecker's test double — no real daemon
// involved, every response fully controlled per test.
type fakeDockerChecker struct {
	pingErr             error
	version, apiVersion string
	versionErr          error
	snapshot            domain.Snapshot
	snapshotErr         error
}

func (f *fakeDockerChecker) Ping(context.Context) error { return f.pingErr }
func (f *fakeDockerChecker) ServerVersion(context.Context) (string, string, error) {
	return f.version, f.apiVersion, f.versionErr
}
func (f *fakeDockerChecker) Snapshot(context.Context) (domain.Snapshot, error) {
	return f.snapshot, f.snapshotErr
}

// baseDoctorDeps returns a doctorDeps with every seam pointed at a
// deterministic, no-I/O fake — a single healthy local system, a reachable
// Docker daemon reporting 3 containers, compose available, no keychain
// lookups. Individual tests override just the field(s) they care about.
func baseDoctorDeps(t *testing.T) doctorDeps {
	t.Helper()
	settingsDir := t.TempDir()
	return doctorDeps{
		version:      "v1.2.3",
		commit:       "abc1234",
		date:         "2026-09-11",
		settingsPath: filepath.Join(settingsDir, "settings.json"),
		loadRawSettings: func(string) (config.Settings, error) {
			return config.Settings{}, nil
		},
		newDockerChecker: func(string) (dockerChecker, error) {
			return &fakeDockerChecker{
				version:    "27.0.0",
				apiVersion: "1.47",
				snapshot:   domain.Snapshot{Standalone: make([]domain.Container, 3)},
			}, nil
		},
		runComposeVersion: func(context.Context) (string, error) { return "2.24.0", nil },
		currentUser:       func() (string, error) { return "testuser", nil },
		passwordFor:       func(string) (string, error) { return "", errors.New("no password stored") },
	}
}

func findCheck(report doctorReport, section, name string) (checkResult, bool) {
	for _, c := range report.Checks {
		if c.Section == section && c.Name == name {
			return c, true
		}
	}
	return checkResult{}, false
}

// TestBuildDoctorReportHealthyLocalConfig is the "everything is fine"
// baseline: default (local-only) config, reachable Docker, compose
// available — no warnings, no failures, exit code 0.
func TestBuildDoctorReportHealthyLocalConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // deterministic clipboard-dir check
	deps := baseDoctorDeps(t)

	report := buildDoctorReport(context.Background(), deps)

	if report.Failed != 0 || report.Warned != 0 {
		t.Fatalf("report has %d warning(s)/%d failure(s), want none:\n%s", report.Warned, report.Failed, renderDoctorText(report))
	}
	if report.ExitCode() != 0 {
		t.Fatalf("ExitCode() = %d, want 0", report.ExitCode())
	}
	if c, ok := findCheck(report, sectionDocker, "Containers"); !ok || c.Message != "3 visible" {
		t.Fatalf("Docker/Containers = %#v, want \"3 visible\"", c)
	}
	if c, ok := findCheck(report, sectionCompose, "Local compose"); !ok || c.Message != "2.24.0" {
		t.Fatalf("Compose/Local compose = %#v, want 2.24.0", c)
	}
}

// TestBuildDoctorReportDockerUnavailable checks an unreachable daemon
// produces a FAIL (not a panic or a silently-ignored error) and stops
// short of the Containers check, which would only fail again redundantly.
func TestBuildDoctorReportDockerUnavailable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	deps := baseDoctorDeps(t)
	deps.newDockerChecker = func(string) (dockerChecker, error) {
		return &fakeDockerChecker{pingErr: errors.New("connection refused")}, nil
	}

	report := buildDoctorReport(context.Background(), deps)

	c, ok := findCheck(report, sectionDocker, "Connection")
	if !ok || c.Severity != sevFail {
		t.Fatalf("Docker/Connection = %#v, want a FAIL", c)
	}
	if !strings.Contains(c.Message, "unreachable") {
		t.Fatalf("Docker/Connection message = %q, want it to say unreachable", c.Message)
	}
	if _, ok := findCheck(report, sectionDocker, "Containers"); ok {
		t.Fatal("Docker/Containers check ran despite Ping failing — should have stopped early")
	}
	if report.ExitCode() != 2 {
		t.Fatalf("ExitCode() = %d, want 2 (a failure present)", report.ExitCode())
	}
}

// TestBuildDoctorReportMalformedConfig checks configSanityIssues surfaces
// problems NormalizeSystems would otherwise silently paper over: an
// unsupported auth mode, an unknown system kind, and a duplicate ID.
func TestBuildDoctorReportMalformedConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	deps := baseDoctorDeps(t)
	deps.loadRawSettings = func(string) (config.Settings, error) {
		return config.Settings{
			Systems: []config.System{
				{ID: "a", Name: "a", Kind: "ssh", SSHHost: "host", SSHAuth: "carrier-pigeon"},
				{ID: "a", Name: "a-again", Kind: "docker-machine"},
			},
		}, nil
	}

	report := buildDoctorReport(context.Background(), deps)

	var messages []string
	for _, c := range report.Checks {
		if c.Section == sectionApp && c.Name == "Config sanity" {
			messages = append(messages, c.Message)
		}
	}
	joined := strings.Join(messages, " | ")
	for _, want := range []string{"unsupported auth mode", "unknown kind", "duplicate system id"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Config sanity messages = %q, missing %q", joined, want)
		}
	}
	if report.ExitCode() != 2 {
		t.Fatalf("ExitCode() = %d, want 2 (malformed config is a failure)", report.ExitCode())
	}
}

// TestBuildDoctorReportStaleRemoteTunnelSocket checks a socket file that
// exists but isn't actually accepting connections is reported as a WARN,
// not silently treated as healthy or as a hard failure — and that doctor
// never removes it (still present after the check runs).
func TestBuildDoctorReportStaleRemoteTunnelSocket(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	socketPath := filepath.Join(t.TempDir(), "whatthedock-vger.sock")
	if err := os.WriteFile(socketPath, []byte("not a real socket"), 0o644); err != nil {
		t.Fatal(err)
	}
	deps := baseDoctorDeps(t)
	deps.loadRawSettings = func(string) (config.Settings, error) {
		return config.Settings{
			ActiveSystem: "local",
			Systems: []config.System{
				config.DefaultSystem(),
				{ID: "vger", Name: "vger", Kind: "ssh", SSHHost: "vger.local", SSHAuth: "config", LocalSocket: socketPath, RemoteSocket: "/var/run/docker.sock"},
			},
		}, nil
	}

	report := buildDoctorReport(context.Background(), deps)

	c, ok := findCheck(report, sectionRemote, "vger")
	if !ok || c.Severity != sevWarn || !strings.Contains(c.Message, "stale") {
		t.Fatalf("Remote systems/vger = %#v, want a stale-tunnel WARN", c)
	}
	if c.Remediation == "" {
		t.Fatal("stale tunnel WARN has no remediation text")
	}
	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("socket file was removed by the check (must never mutate): %v", err)
	}
	if report.ExitCode() != 1 {
		t.Fatalf("ExitCode() = %d, want 1 (a warning, no failures)", report.ExitCode())
	}
}

// TestBuildDoctorReportMissingRemoteFields checks a system with no SSH
// host configured fails clearly in both Remote systems and Compose (which
// depends on the same field), rather than a generic or missing error.
func TestBuildDoctorReportMissingRemoteFields(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	deps := baseDoctorDeps(t)
	deps.loadRawSettings = func(string) (config.Settings, error) {
		return config.Settings{
			ActiveSystem: "local",
			Systems: []config.System{
				config.DefaultSystem(),
				{ID: "backup", Name: "backup", Kind: "ssh"},
			},
		}, nil
	}

	report := buildDoctorReport(context.Background(), deps)

	remote, ok := findCheck(report, sectionRemote, "backup")
	if !ok || remote.Severity != sevFail || !strings.Contains(remote.Message, "missing SSH host") {
		t.Fatalf("Remote systems/backup = %#v, want FAIL missing SSH host", remote)
	}
	compose, ok := findCheck(report, sectionCompose, "backup")
	if !ok || compose.Severity != sevWarn || !strings.Contains(compose.Message, "unavailable") {
		t.Fatalf("Compose/backup = %#v, want WARN remote Compose unavailable", compose)
	}
	if report.ExitCode() != 2 {
		t.Fatalf("ExitCode() = %d, want 2 (missing SSH host is a failure)", report.ExitCode())
	}
}

// TestBuildDoctorReportSummaryCounts checks Passed/Warned/Failed are
// counted correctly across a mix of severities in one run.
func TestBuildDoctorReportSummaryCounts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	staleSocket := filepath.Join(t.TempDir(), "whatthedock-warn-me.sock")
	if err := os.WriteFile(staleSocket, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	deps := baseDoctorDeps(t)
	deps.loadRawSettings = func(string) (config.Settings, error) {
		return config.Settings{
			ActiveSystem: "local",
			Systems: []config.System{
				config.DefaultSystem(),
				{ID: "warn-me", Name: "warn-me", Kind: "ssh", SSHHost: "h", SSHAuth: "config", LocalSocket: staleSocket, RemoteSocket: "/var/run/docker.sock"},
				{ID: "fail-me", Name: "fail-me", Kind: "ssh"},
			},
		}, nil
	}

	report := buildDoctorReport(context.Background(), deps)

	if report.Passed+report.Warned+report.Failed != len(report.Checks) {
		t.Fatalf("counts (%d/%d/%d) don't add up to %d total checks", report.Passed, report.Warned, report.Failed, len(report.Checks))
	}
	if report.Warned == 0 {
		t.Fatal("Warned = 0, want at least the stale-tunnel warning")
	}
	if report.Failed == 0 {
		t.Fatal("Failed = 0, want at least the missing-SSH-host failure")
	}
}

// TestBuildDoctorReportRedactsKeychainSecret is the security-critical
// test: even when a keychain-mode system's password is genuinely
// available, its value must never appear anywhere in the report — the
// check only ever reports presence, never the secret itself.
func TestBuildDoctorReportRedactsKeychainSecret(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const secret = "hunter2-super-secret-password"
	deps := baseDoctorDeps(t)
	deps.passwordFor = func(string) (string, error) { return secret, nil }
	deps.loadRawSettings = func(string) (config.Settings, error) {
		return config.Settings{
			ActiveSystem: "local",
			Systems: []config.System{
				config.DefaultSystem(),
				{ID: "vger", Name: "vger", Kind: "ssh", SSHHost: "vger.local", SSHAuth: "keychain", LocalSocket: filepath.Join(t.TempDir(), "vger.sock"), RemoteSocket: "/var/run/docker.sock"},
			},
		}, nil
	}

	report := buildDoctorReport(context.Background(), deps)
	text := renderDoctorText(report)
	if strings.Contains(text, secret) {
		t.Fatalf("rendered report contains the raw keychain secret:\n%s", text)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("JSON report contains the raw keychain secret: %s", data)
	}
}

// TestRunDoctorCommandExitCodes checks the documented exit-code scheme —
// 0 clean, 1 warning-only, 2 any failure — against three settings that
// each deterministically land on exactly one tier. A missing (never
// connected) tunnel socket is deliberately included as a "clean" case:
// remoteSystemChecks only warns on a *stale* socket (one that exists but
// isn't live), never on one that simply hasn't been created yet.
func TestRunDoctorCommandExitCodes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	staleSocket := filepath.Join(t.TempDir(), "whatthedock-stale.sock")
	if err := os.WriteFile(staleSocket, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		settings config.Settings
		wantCode int
	}{
		{name: "clean, no remote systems", settings: config.Settings{}, wantCode: 0},
		{
			name: "clean, remote socket never created yet",
			settings: config.Settings{ActiveSystem: "local", Systems: []config.System{
				config.DefaultSystem(),
				{ID: "fresh", Name: "fresh", Kind: "ssh", SSHHost: "h", SSHAuth: "config", RemoteSocket: "/var/run/docker.sock", LocalSocket: filepath.Join(t.TempDir(), "never-created.sock")},
			}},
			wantCode: 0,
		},
		{
			name: "warning only, stale tunnel socket",
			settings: config.Settings{ActiveSystem: "local", Systems: []config.System{
				config.DefaultSystem(),
				{ID: "stale", Name: "stale", Kind: "ssh", SSHHost: "h", SSHAuth: "config", RemoteSocket: "/var/run/docker.sock", LocalSocket: staleSocket},
			}},
			wantCode: 1,
		},
		{
			name: "failure, missing SSH host",
			settings: config.Settings{ActiveSystem: "local", Systems: []config.System{
				config.DefaultSystem(),
				{ID: "broken", Name: "broken", Kind: "ssh"},
			}},
			wantCode: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := baseDoctorDeps(t)
			settings := tt.settings
			deps.loadRawSettings = func(string) (config.Settings, error) { return settings, nil }
			report := buildDoctorReport(context.Background(), deps)
			if report.ExitCode() != tt.wantCode {
				t.Fatalf("ExitCode() = %d, want %d:\n%s", report.ExitCode(), tt.wantCode, renderDoctorText(report))
			}
		})
	}
}

// TestRunDoctorCommandJSONOutputParsesAndMatchesReport checks --json
// produces valid, structurally correct JSON via the real entry point
// (runDoctorCommand), not just buildDoctorReport directly.
func TestRunDoctorCommandJSONOutputParsesAndMatchesReport(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Swap defaultDoctorDeps' effect by going through buildDoctorReport
	// directly for the expected shape, then compare against what the real
	// command entry point prints — runDoctorCommand itself always builds
	// deps via defaultDoctorDeps, so this exercises the real settings
	// path/user/Docker-from-env behavior rather than a fake, which is
	// exactly what's worth checking about the JSON *plumbing* (does
	// runDoctorCommand actually marshal and print what buildDoctorReport
	// produced, and does it exit with the right code).
	var buf strings.Builder
	code := runDoctorCommand(context.Background(), []string{"--json"}, &buf, "v9.9.9", "deadbeef", "2026-01-01")

	var decoded doctorReport
	if err := json.Unmarshal([]byte(buf.String()), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if decoded.Version != "v9.9.9" {
		t.Fatalf("decoded.Version = %q, want v9.9.9", decoded.Version)
	}
	if len(decoded.Checks) == 0 {
		t.Fatal("decoded.Checks is empty")
	}
	// checkResult.Severity is deliberately json:"-" (its custom
	// MarshalJSON emits a "severity" string field instead — see
	// checkResult.MarshalJSON) so it doesn't round-trip back into the Go
	// field on Unmarshal; verify the string form landed in the raw JSON
	// directly instead.
	var rawChecks []struct {
		Severity string `json:"severity"`
	}
	if err := json.Unmarshal([]byte(buf.String()), &struct {
		Checks *[]struct {
			Severity string `json:"severity"`
		} `json:"checks"`
	}{Checks: &rawChecks}); err != nil {
		t.Fatalf("could not decode raw severities: %v", err)
	}
	for _, c := range rawChecks {
		if c.Severity != "PASS" && c.Severity != "WARN" && c.Severity != "FAIL" {
			t.Fatalf("check has unrecognized JSON severity %q", c.Severity)
		}
	}
	if code != decoded.ExitCode() {
		t.Fatalf("runDoctorCommand() returned %d, want it to match the report's own ExitCode() %d", code, decoded.ExitCode())
	}
}

func TestPluralCount(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0 warnings"},
		{1, "1 warning"},
		{2, "2 warnings"},
	}
	for _, tt := range tests {
		if got := pluralCount(tt.n, "warning"); got != tt.want {
			t.Fatalf("pluralCount(%d, warning) = %q, want %q", tt.n, got, tt.want)
		}
	}
}
