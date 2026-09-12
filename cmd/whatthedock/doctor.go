// whatthedock doctor is a read-only diagnostic report: environment,
// config, and Docker/remote-system health checks, with nothing that
// mutates state — no tunnels started, no stale sockets removed, no config
// written, no credentials touched beyond a yes/no "is one stored" check.
// See runDoctorCommand for the entry point and buildDoctorReport for the
// actual checks; everything here is deliberately dependency-injected
// (doctorDeps) so tests never need a real Docker daemon or SSH server.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/allisonhere/whatthedock/internal/config"
	"github.com/allisonhere/whatthedock/internal/docker"
	"github.com/allisonhere/whatthedock/internal/domain"
	"github.com/allisonhere/whatthedock/internal/systems"
)

// dockerCheckTimeout bounds every live Docker call doctor makes (Ping,
// ServerVersion, Snapshot) — an unreachable daemon must fail the check
// quickly, not hang the whole report.
const dockerCheckTimeout = 5 * time.Second

// composeCheckTimeout bounds the local `docker compose version` probe.
const composeCheckTimeout = 3 * time.Second

type severity int

const (
	sevPass severity = iota
	sevWarn
	sevFail
)

func (s severity) String() string {
	switch s {
	case sevWarn:
		return "WARN"
	case sevFail:
		return "FAIL"
	default:
		return "PASS"
	}
}

// checkResult is one report line. Remediation is only ever shown (and only
// ever needed) for WARN/FAIL — a PASS speaks for itself.
type checkResult struct {
	Section     string   `json:"section"`
	Name        string   `json:"name"`
	Severity    severity `json:"-"`
	Message     string   `json:"message"`
	Remediation string   `json:"remediation,omitempty"`
}

// MarshalJSON gives Severity its string form ("PASS"/"WARN"/"FAIL") in
// JSON output instead of a bare int, without needing a second exported
// field just for that.
func (c checkResult) MarshalJSON() ([]byte, error) {
	type alias checkResult
	return json.Marshal(struct {
		alias
		Severity string `json:"severity"`
	}{alias(c), c.Severity.String()})
}

type doctorReport struct {
	Version string        `json:"version"`
	Commit  string        `json:"commit,omitempty"`
	Date    string        `json:"date,omitempty"`
	Checks  []checkResult `json:"checks"`
	Passed  int           `json:"passed"`
	Warned  int           `json:"warned"`
	Failed  int           `json:"failed"`
}

func (r *doctorReport) add(section, name string, sev severity, message, remediation string) {
	r.Checks = append(r.Checks, checkResult{Section: section, Name: name, Severity: sev, Message: message, Remediation: remediation})
	switch sev {
	case sevWarn:
		r.Warned++
	case sevFail:
		r.Failed++
	default:
		r.Passed++
	}
}

// ExitCode follows the scheme the task asked for: 0 clean, 1 warnings
// only, 2 any failure — failures take priority over warnings since a
// failure is the more actionable signal.
func (r *doctorReport) ExitCode() int {
	switch {
	case r.Failed > 0:
		return 2
	case r.Warned > 0:
		return 1
	default:
		return 0
	}
}

// dockerChecker is the small slice of *docker.LocalProvider's surface
// doctor actually needs — a seam so tests can fake Docker connectivity
// (or its absence) without a real daemon. *docker.LocalProvider satisfies
// this implicitly.
type dockerChecker interface {
	Ping(context.Context) error
	ServerVersion(context.Context) (version, apiVersion string, err error)
	Snapshot(context.Context) (domain.Snapshot, error)
}

// doctorDeps bundles everything buildDoctorReport needs from the outside
// world, all replaceable in tests.
type doctorDeps struct {
	version, commit, date string
	settingsPath          string
	settingsPathErr       error
	loadRawSettings       func(path string) (config.Settings, error)
	newDockerChecker      func(dockerHost string) (dockerChecker, error)
	runComposeVersion     func(ctx context.Context) (string, error)
	currentUser           func() (string, error)
	// passwordFor checks only whether a keychain-mode system has a stored
	// password — never the value itself (see remoteSystemChecks) — kept
	// injectable so tests never touch the real OS keychain.
	passwordFor func(systemID string) (string, error)
}

func defaultDoctorDeps(version, commit, date string) doctorDeps {
	settingsPath, pathErr := config.SettingsPath()
	return doctorDeps{
		version:           version,
		commit:            commit,
		date:              date,
		settingsPath:      settingsPath,
		settingsPathErr:   pathErr,
		loadRawSettings:   config.LoadSettings,
		newDockerChecker:  newRealDockerChecker,
		runComposeVersion: runComposeVersionCommand,
		currentUser:       currentUsername,
		passwordFor:       systems.PasswordFor,
	}
}

func newRealDockerChecker(dockerHost string) (dockerChecker, error) {
	return docker.NewProvider("doctor", "doctor", dockerHost)
}

func runComposeVersionCommand(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", "compose", "version", "--short")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func currentUsername() (string, error) {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username, nil
	}
	if name := os.Getenv("USER"); name != "" {
		return name, nil
	}
	if name := os.Getenv("USERNAME"); name != "" {
		return name, nil
	}
	return "", fmt.Errorf("could not determine current user")
}

// runDoctorCommand is the "whatthedock doctor" entry point. args is
// everything after "doctor" on the command line (main strips that token
// off before calling in).
func runDoctorCommand(ctx context.Context, args []string, stdout io.Writer, version, commit, date string) int {
	jsonOutput := false
	for _, a := range args {
		switch a {
		case "--json", "-json":
			jsonOutput = true
		}
	}
	report := buildDoctorReport(ctx, defaultDoctorDeps(version, commit, date))
	if jsonOutput {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(stdout, "whatthedock doctor: %v\n", err)
			return 2
		}
		fmt.Fprintln(stdout, string(data))
	} else {
		fmt.Fprint(stdout, renderDoctorText(report))
	}
	return report.ExitCode()
}

// buildDoctorReport runs every check and returns the assembled report. It
// never mutates anything: no tunnel is established, no stale socket is
// removed, no config is written, no credential is ever printed — only a
// yes/no "is a keychain password stored" check touches secret storage at
// all, and only its presence (never the value) is reported.
func buildDoctorReport(ctx context.Context, deps doctorDeps) doctorReport {
	report := doctorReport{Version: deps.version, Commit: deps.commit, Date: deps.date}

	raw, rawErr := config.Settings{}, error(nil)
	if deps.settingsPathErr == nil {
		raw, rawErr = deps.loadRawSettings(deps.settingsPath)
	} else {
		rawErr = deps.settingsPathErr
	}
	// NormalizeSystems takes Settings by value but Systems is a slice —
	// its backing array is shared, so NormalizeSystems' in-place field
	// coercion (an invalid SSHAuth silently becomes "config", etc.) would
	// otherwise mutate raw.Systems too, defeating configSanityIssues'
	// whole point of inspecting the *pre*-normalization values.
	rawForSanity := raw
	rawForSanity.Systems = append([]config.System(nil), raw.Systems...)
	normalized := config.NormalizeSystems(raw)

	appChecks(&report, deps, rawForSanity, rawErr, normalized)
	dockerChecks(ctx, &report, deps, normalized)
	localSystemChecks(&report, deps, normalized)
	remoteSystemChecks(&report, deps, normalized)
	composeChecks(ctx, &report, deps, normalized)

	return report
}

const sectionApp = "App"

func appChecks(report *doctorReport, deps doctorDeps, raw config.Settings, rawErr error, normalized config.Settings) {
	versionMsg := deps.version
	var meta []string
	if deps.commit != "" {
		meta = append(meta, "commit "+deps.commit)
	}
	if deps.date != "" {
		meta = append(meta, "built "+deps.date)
	}
	if len(meta) > 0 {
		versionMsg += " (" + strings.Join(meta, ", ") + ")"
	}
	report.add(sectionApp, "Version", sevPass, versionMsg, "")
	report.add(sectionApp, "OS", sevPass, runtime.GOOS, "")
	report.add(sectionApp, "Architecture", sevPass, runtime.GOARCH, "")

	if name, err := deps.currentUser(); err == nil {
		report.add(sectionApp, "User", sevPass, name, "")
	} else {
		report.add(sectionApp, "User", sevWarn, "could not determine current user", err.Error())
	}

	if deps.settingsPathErr != nil {
		report.add(sectionApp, "Config", sevFail, "could not resolve config path", deps.settingsPathErr.Error())
	} else {
		report.add(sectionApp, "Config", sevPass, deps.settingsPath, "")
		if rawErr != nil {
			report.add(sectionApp, "Config loads", sevFail, "settings.json failed to parse: "+rawErr.Error(), "Fix or remove the file so WhatTheDock can write fresh defaults.")
		} else {
			report.add(sectionApp, "Config loads", sevPass, "ok", "")
		}
	}

	active := config.FindSystem(normalized.Systems, normalized.ActiveSystem)
	if active != nil {
		report.add(sectionApp, "Active system", sevPass, active.Name, "")
	} else {
		// NormalizeSystems always resolves ActiveSystem to a real system,
		// so this can only happen if normalization itself was given no
		// systems at all — defensive, not expected in practice.
		report.add(sectionApp, "Active system", sevFail, "none resolved", "Open Systems and configure at least one.")
	}
	report.add(sectionApp, "Configured systems", sevPass, fmt.Sprintf("%d", len(normalized.Systems)), "")

	issues := configSanityIssues(raw)
	for _, issue := range issues {
		report.add(sectionApp, "Config sanity", issue.severity, issue.message, issue.remediation)
	}
	if len(issues) == 0 {
		report.add(sectionApp, "Config sanity", sevPass, "no issues found", "")
	}
}

// sanityIssue is configSanityIssues' own lightweight result shape —
// appChecks folds these into the App section's checkResult rows itself.
type sanityIssue struct {
	severity    severity
	message     string
	remediation string
}

// configSanityIssues checks raw (pre-NormalizeSystems) settings for
// problems Normalize would otherwise silently paper over — an unsupported
// auth mode gets quietly coerced to "config", a missing active system ID
// gets quietly replaced with the first configured one, and so on. Doctor
// needs to see the mistake, not the patched-over result.
func configSanityIssues(raw config.Settings) []sanityIssue {
	var issues []sanityIssue
	seenID := map[string]bool{}
	for _, sys := range raw.Systems {
		if sys.ID != "" {
			if seenID[sys.ID] {
				issues = append(issues, sanityIssue{sevFail, fmt.Sprintf("duplicate system id %q", sys.ID), "Give each system a unique id in settings.json."})
			}
			seenID[sys.ID] = true
		}
		switch sys.Kind {
		case "", "local", "ssh":
		default:
			issues = append(issues, sanityIssue{sevFail, fmt.Sprintf("system %q has unknown kind %q", displaySystemName(sys), sys.Kind), "Kind must be \"local\" or \"ssh\"."})
		}
		if sys.Kind == "ssh" {
			switch sys.SSHAuth {
			case "", "config", "password", "keychain":
			default:
				issues = append(issues, sanityIssue{sevFail, fmt.Sprintf("system %q has unsupported auth mode %q", displaySystemName(sys), sys.SSHAuth), "Auth must be config/agent, password, or keychain."})
			}
			if strings.TrimSpace(sys.LocalSocket) != "" && !filepath.IsAbs(sys.LocalSocket) {
				issues = append(issues, sanityIssue{sevWarn, fmt.Sprintf("system %q has a non-absolute local socket path %q", displaySystemName(sys), sys.LocalSocket), "Use an absolute path, or leave it blank to use the default."})
			}
		}
		if sys.Kind != "ssh" && strings.TrimSpace(sys.DockerHost) != "" {
			if u, err := url.Parse(sys.DockerHost); err != nil || u.Scheme == "" {
				issues = append(issues, sanityIssue{sevWarn, fmt.Sprintf("system %q has a Docker host value that doesn't look like a valid URI: %q", displaySystemName(sys), sys.DockerHost), "Expected something like tcp://host:2375 or unix:///path/to.sock."})
			}
		}
	}
	if raw.ActiveSystem != "" && len(raw.Systems) > 0 && config.FindSystem(raw.Systems, raw.ActiveSystem) == nil {
		issues = append(issues, sanityIssue{sevFail, fmt.Sprintf("active system %q is not in the configured systems list", raw.ActiveSystem), "Switch to a configured system, or fix activeSystem in settings.json."})
	}
	return issues
}

func displaySystemName(sys config.System) string {
	if sys.Name != "" {
		return sys.Name
	}
	if sys.ID != "" {
		return sys.ID
	}
	return "(unnamed)"
}

const sectionDocker = "Docker"

func dockerChecks(ctx context.Context, report *doctorReport, deps doctorDeps, normalized config.Settings) {
	active := config.FindSystem(normalized.Systems, normalized.ActiveSystem)
	if active == nil {
		local := config.DefaultSystem()
		active = &local
	}

	dockerHost := systems.DockerHostFor(*active)
	report.add(sectionDocker, "Target", sevPass, systems.DockerHostLabel(*active), "")

	if active.Kind == "ssh" && !systems.IsLiveSocket(active.LocalSocket) {
		report.add(sectionDocker, "Connection", sevWarn, "tunnel not active", "Switch to this system in WhatTheDock first to establish the tunnel — doctor never starts one itself.")
		return
	}

	checker, err := deps.newDockerChecker(dockerHost)
	if err != nil {
		report.add(sectionDocker, "Client", sevFail, "could not create Docker client", err.Error())
		return
	}

	pingCtx, cancel := context.WithTimeout(ctx, dockerCheckTimeout)
	pingErr := checker.Ping(pingCtx)
	cancel()
	if pingErr != nil {
		report.add(sectionDocker, "Connection", sevFail, "unreachable", pingErr.Error())
		return
	}
	report.add(sectionDocker, "Connection", sevPass, "reachable", "")

	verCtx, cancel := context.WithTimeout(ctx, dockerCheckTimeout)
	version, apiVersion, verErr := checker.ServerVersion(verCtx)
	cancel()
	if verErr != nil {
		report.add(sectionDocker, "Server", sevWarn, "version unavailable", verErr.Error())
	} else {
		msg := version
		if apiVersion != "" {
			msg += " (API " + apiVersion + ")"
		}
		report.add(sectionDocker, "Server", sevPass, msg, "")
	}

	snapCtx, cancel := context.WithTimeout(ctx, dockerCheckTimeout)
	snapshot, snapErr := checker.Snapshot(snapCtx)
	cancel()
	if snapErr != nil {
		report.add(sectionDocker, "Containers", sevFail, "listing failed", snapErr.Error())
		return
	}
	report.add(sectionDocker, "Containers", sevPass, fmt.Sprintf("%d visible", countContainers(snapshot)), "")
}

func countContainers(snapshot domain.Snapshot) int {
	n := len(snapshot.Standalone)
	for _, project := range snapshot.Projects {
		for _, service := range project.Services {
			n += len(service.Containers)
		}
	}
	return n
}

const sectionLocal = "Local system"

func localSystemChecks(report *doctorReport, deps doctorDeps, normalized config.Settings) {
	if deps.settingsPathErr == nil {
		configDir := filepath.Dir(deps.settingsPath)
		checkDirWritable(report, sectionLocal, "Config dir", configDir, true)
	}

	if home, err := os.UserHomeDir(); err == nil {
		// Same path convention paste.go's redirectMissingBindMountsCmd
		// already uses for placeholder bind-mount directories — checked
		// here, not created: a missing dir just means no paste has ever
		// needed a placeholder yet, not a problem.
		clipboardDir := filepath.Join(home, ".local", "share", "whatthedock", "paste-placeholders")
		checkDirWritable(report, sectionLocal, "Clipboard storage", clipboardDir, false)
	} else {
		report.add(sectionLocal, "Clipboard storage", sevWarn, "could not resolve home directory", err.Error())
	}

	staleTunnelSocketCheck(report, normalized)
}

// checkDirWritable reports whether dir exists and, if so, is writable —
// verified with a real temp-file create+remove (permission bits alone
// aren't reliable enough across filesystems to trust), never left behind.
// requireExists controls whether a missing directory is a WARN (config
// dir — should always exist once the app has run once) or just a PASS
// "not created yet" (clipboard dir — only created on first use).
func checkDirWritable(report *doctorReport, section, name, dir string, requireExists bool) {
	info, err := os.Stat(dir)
	if err != nil {
		if requireExists {
			report.add(section, name, sevWarn, dir+" does not exist yet", "It will be created the next time settings are saved.")
		} else {
			report.add(section, name, sevPass, dir+" (not created yet)", "")
		}
		return
	}
	if !info.IsDir() {
		report.add(section, name, sevFail, dir+" exists but is not a directory", "Remove or rename it.")
		return
	}
	probe, err := os.CreateTemp(dir, ".whatthedock-doctor-*")
	if err != nil {
		report.add(section, name, sevFail, dir+" is not writable", err.Error())
		return
	}
	probePath := probe.Name()
	_ = probe.Close()
	_ = os.Remove(probePath)
	report.add(section, name, sevPass, dir, "")
}

// staleTunnelSocketCheck scans the temp directory for whatthedock-*.sock
// files — the naming convention NormalizeSystems' default LocalSocket
// uses — independent of whether each one still belongs to a currently
// configured system (a system removed from config can leave its tunnel
// socket behind). Read-only: a stale entry is reported, never removed.
func staleTunnelSocketCheck(report *doctorReport, normalized config.Settings) {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		report.add(sectionLocal, "Tunnel sockets", sevWarn, "could not scan temp directory", err.Error())
		return
	}
	var live, stale []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "whatthedock-") || !strings.HasSuffix(name, ".sock") {
			continue
		}
		path := filepath.Join(os.TempDir(), name)
		if systems.IsLiveSocket(path) {
			live = append(live, name)
		} else {
			stale = append(stale, name)
		}
	}
	switch {
	case len(stale) > 0:
		report.add(sectionLocal, "Tunnel sockets", sevWarn,
			fmt.Sprintf("%d stale, %d live", len(stale), len(live)),
			"Stale sockets are recreated automatically the next time that system connects — safe to leave alone.")
	case len(live) > 0:
		report.add(sectionLocal, "Tunnel sockets", sevPass, fmt.Sprintf("%d live", len(live)), "")
	default:
		report.add(sectionLocal, "Tunnel sockets", sevPass, "none found", "")
	}
}

const sectionRemote = "Remote systems"

// remoteSystemChecks reports one summarized row per non-local configured
// system — the worst issue found for it, or a clean "kind / auth" summary
// if there's nothing to flag. Never dials out: only config fields and
// local socket file state are inspected (see IsLiveSocket's own doc
// comment on why doctor never establishes a tunnel itself).
func remoteSystemChecks(report *doctorReport, deps doctorDeps, normalized config.Settings) {
	for _, sys := range normalized.Systems {
		if sys.Kind != "ssh" {
			continue
		}
		if strings.TrimSpace(sys.SSHHost) == "" {
			report.add(sectionRemote, sys.Name, sevFail, "missing SSH host", "Open Systems and set the SSH host.")
			continue
		}
		if strings.TrimSpace(sys.LocalSocket) == "" || !filepath.IsAbs(sys.LocalSocket) {
			report.add(sectionRemote, sys.Name, sevFail, "invalid local socket path", "Open Systems and re-save this system so a valid default is filled in.")
			continue
		}
		if sys.SSHAuth == "keychain" {
			if _, err := deps.passwordFor(sys.ID); err != nil {
				report.add(sectionRemote, sys.Name, sevWarn, "keychain password not available", "Open Systems and set a password for this system.")
				continue
			}
		}
		if _, err := os.Stat(sys.LocalSocket); err == nil && !systems.IsLiveSocket(sys.LocalSocket) {
			report.add(sectionRemote, sys.Name, sevWarn, "tunnel socket stale", "It will be recreated on the next connection.")
			continue
		}
		report.add(sectionRemote, sys.Name, sevPass, "ssh / "+authModeLabel(sys.SSHAuth), "")
	}
}

func authModeLabel(mode string) string {
	switch mode {
	case "password":
		return "password prompt"
	case "keychain":
		return "keychain"
	default:
		return "config/agent"
	}
}

const sectionCompose = "Compose"

func composeChecks(ctx context.Context, report *doctorReport, deps doctorDeps, normalized config.Settings) {
	composeCtx, cancel := context.WithTimeout(ctx, composeCheckTimeout)
	version, err := deps.runComposeVersion(composeCtx)
	cancel()
	if err != nil {
		report.add(sectionCompose, "Local compose", sevWarn, "not available", "Install the Docker Compose plugin to use Compose features.")
	} else {
		report.add(sectionCompose, "Local compose", sevPass, version, "")
	}

	for _, sys := range normalized.Systems {
		if sys.Kind != "ssh" {
			continue
		}
		if strings.TrimSpace(sys.SSHHost) == "" || strings.TrimSpace(sys.RemoteSocket) == "" {
			report.add(sectionCompose, sys.Name, sevWarn, "remote Compose unavailable", "Fix this system's SSH configuration in Systems.")
			continue
		}
		report.add(sectionCompose, sys.Name, sevPass, "remote Compose ready", "")
	}
}

// renderDoctorText is the default human-readable report — grouped by
// section in the fixed order the checks were designed in, each WARN/FAIL
// row followed by its own indented remediation line (a PASS never has
// one, and needs none).
func renderDoctorText(report doctorReport) string {
	var b strings.Builder
	b.WriteString("WhatTheDock Doctor\n")
	for _, section := range []string{sectionApp, sectionDocker, sectionLocal, sectionRemote, sectionCompose} {
		var rows []checkResult
		for _, c := range report.Checks {
			if c.Section == section {
				rows = append(rows, c)
			}
		}
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s\n", section)
		for _, c := range rows {
			fmt.Fprintf(&b, "%-4s  %-20s %s\n", c.Severity.String(), c.Name, c.Message)
			if c.Remediation != "" {
				fmt.Fprintf(&b, "      %s\n", c.Remediation)
			}
		}
	}
	fmt.Fprint(&b, "\nSummary\n")
	fmt.Fprintf(&b, "%d passed\n", report.Passed)
	fmt.Fprintf(&b, "%s\n", pluralCount(report.Warned, "warning"))
	fmt.Fprintf(&b, "%s\n", pluralCount(report.Failed, "failure"))
	return b.String()
}

func pluralCount(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
