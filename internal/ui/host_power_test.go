package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/allisonhere/tideui"
	"github.com/allisonhere/whatthedock/internal/config"
	"github.com/allisonhere/whatthedock/internal/domain"
)

// withFakeHostPowerRun stubs hostPowerRun (the seam startHostPower and
// retryHostPowerWithPasswordCmd call for the actual OS-level
// shutdown/reboot) so tests never shell out for real — the same seam-swap
// convention as withFakeSSHRun (create_test.go). By default it fails the
// non-interactive attempt (empty password) and succeeds once a non-empty
// password is supplied, matching the common "needs a password" case;
// tests that want different behavior set succeed/err directly.
type fakeHostPowerRun struct {
	calls   []hostPowerRunCall
	err     error // returned regardless of password, when set
	wantErr error // returned only when password == "" (needs-password case); ignored if err is set
}

type hostPowerRunCall struct {
	system   config.System
	kind     hostPowerKind
	password string
}

func (f *fakeHostPowerRun) run(_ context.Context, system config.System, kind hostPowerKind, password string) error {
	f.calls = append(f.calls, hostPowerRunCall{system: system, kind: kind, password: password})
	if f.err != nil {
		return f.err
	}
	if password == "" && f.wantErr != nil {
		return f.wantErr
	}
	return nil
}

func withFakeHostPowerRun(t *testing.T) *fakeHostPowerRun {
	t.Helper()
	fake := &fakeHostPowerRun{}
	original := hostPowerRun
	hostPowerRun = fake.run
	t.Cleanup(func() { hostPowerRun = original })
	return fake
}

func TestOpenHostPowerConfirmRefusesDemoProvider(t *testing.T) {
	provider := newFakeProvider()
	provider.host = domain.Host{ID: "demo", Name: "demo homelab"}
	model := testModel()
	model.provider = provider

	updated, cmd := model.openHostPowerConfirm(hostPowerShutdown)
	got := updated.(Model)

	if cmd != nil {
		t.Fatal("cmd != nil, want no command for a refused demo shutdown")
	}
	if got.overlay != overlayNone {
		t.Fatalf("overlay = %v, want overlayNone", got.overlay)
	}
	if !got.statusErr || !strings.Contains(got.status, "demo") {
		t.Fatalf("status/statusErr = %q/%v, want an error mentioning demo mode", got.status, got.statusErr)
	}
	if provider.stops != 0 {
		t.Fatalf("stops = %d, want 0", provider.stops)
	}
}

func TestHostPowerConfirmOverlayListsRunningAcrossProjectsAndStandalone(t *testing.T) {
	host := domain.Host{ID: "local", Name: "jarvis"}
	containers := []domain.Container{
		{ID: domain.ResourceID{Host: "local", ID: "1"}, Name: "radarr-1", State: domain.StateRunning, Compose: domain.ComposeRef{Project: "media", Service: "radarr"}},
		{ID: domain.ResourceID{Host: "local", ID: "2"}, Name: "jellyfin-1", State: domain.StateStopped, Compose: domain.ComposeRef{Project: "media", Service: "jellyfin"}},
		{ID: domain.ResourceID{Host: "local", ID: "3"}, Name: "watchtower", State: domain.StateRunning},
		{ID: domain.ResourceID{Host: "local", ID: "4"}, Name: "old-tool", State: domain.StateExited},
	}
	snapshot := domain.BuildSnapshot(host, containers, time.Unix(1, 0))
	provider := newFakeProvider()
	provider.host = host
	provider.snapshot = snapshot

	model := testModel()
	model.provider = provider
	model.snapshot = snapshot
	model.width, model.height = 100, 40
	model.hostPowerKind = hostPowerShutdown
	model.overlay = overlayHostPowerConfirm

	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	overlay := model.hostPowerConfirmOverlay(renderer)
	if overlay == nil {
		t.Fatal("hostPowerConfirmOverlay() = nil")
	}
	content := ansi.Strip(overlay.Content)
	if !strings.Contains(content, "radarr-1") {
		t.Fatalf("overlay missing running compose container radarr-1:\n%s", content)
	}
	if !strings.Contains(content, "watchtower") {
		t.Fatalf("overlay missing running standalone container watchtower:\n%s", content)
	}
	if strings.Contains(content, "jellyfin-1") {
		t.Fatal("overlay lists stopped container jellyfin-1 — only running containers should be itemized")
	}
	if strings.Contains(content, "old-tool") {
		t.Fatal("overlay lists exited container old-tool — only running containers should be itemized")
	}
	if !strings.Contains(content, "jarvis") {
		t.Fatalf("overlay missing host name jarvis:\n%s", content)
	}
}

func TestHostPowerConfirmYPressStopsRunningContainersThenRunsCommand(t *testing.T) {
	fake := withFakeHostPowerRun(t) // non-interactive attempt succeeds (passwordless sudo case)
	model := testModel()            // container "1" running, "2" stopped, both project "media"
	model.overlay = overlayHostPowerConfirm
	model.hostPowerKind = hostPowerShutdown

	updated, cmd := model.handleHostPowerConfirmKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	if model.overlay != overlayHostPowerProgress {
		t.Fatalf("overlay = %v, want overlayHostPowerProgress immediately after y", model.overlay)
	}
	if !model.busy {
		t.Fatal("busy = false, want true while the progress modal is open")
	}
	msg := runCmd(t, cmd)
	done, ok := msg.(actionDoneMsg)
	if !ok {
		t.Fatalf("msg = %#v, want actionDoneMsg", msg)
	}
	if done.err != nil {
		t.Fatalf("actionDoneMsg.err = %v, want nil", done.err)
	}
	if !done.skipRefresh {
		t.Fatal("actionDoneMsg.skipRefresh = false, want true — a successful shutdown must not trigger the usual post-action refresh")
	}

	updated, refreshCmd := model.Update(msg)
	model = updated.(Model)

	if refreshCmd != nil {
		t.Fatal("Update(actionDoneMsg) returned a non-nil cmd, want nil — skipRefresh must suppress the usual refreshCmd()")
	}
	if model.overlay != overlayNone {
		t.Fatalf("overlay = %v, want overlayNone once the progress modal's result lands", model.overlay)
	}
	provider := model.provider.(*fakeProvider)
	if provider.stops != 1 {
		t.Fatalf("stops = %d, want 1 (only the running container)", provider.stops)
	}
	if len(fake.calls) != 1 || fake.calls[0].kind != hostPowerShutdown || fake.calls[0].password != "" {
		t.Fatalf("hostPowerRun calls = %#v, want one non-interactive (empty password) call with hostPowerShutdown", fake.calls)
	}
	if model.statusErr || model.status != "shutdown complete" {
		t.Fatalf("status/statusErr = %q/%v, want shutdown complete/false", model.status, model.statusErr)
	}
}

// TestRestartActionDoneMsgStillRefreshesAsBefore guards actionDoneMsg's new
// skipRefresh field against regressing every *other* actionCmd caller —
// Restart/StartStop never set it, so their post-action refresh must be
// completely unaffected by host-power's opt-out.
func TestRestartActionDoneMsgStillRefreshesAsBefore(t *testing.T) {
	model := testModel()
	updated, cmd := model.Update(actionDoneMsg{label: "restart", err: nil})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("Update(actionDoneMsg) returned a nil cmd, want the usual refreshCmd()")
	}
	if model.status != "restart complete" {
		t.Fatalf("status = %q, want restart complete", model.status)
	}
}

// TestHostPowerNonInteractiveFailureOpensPasswordPrompt is a regression
// test for the actual reported bug: a host without passwordless sudo used
// to hard-fail here instead of letting the user finish authenticating
// in-app.
func TestHostPowerNonInteractiveFailureOpensPasswordPrompt(t *testing.T) {
	fake := withFakeHostPowerRun(t)
	fake.wantErr = errors.New("sudo: a password is required")
	model := testModel()
	model.overlay = overlayHostPowerConfirm
	model.hostPowerKind = hostPowerReboot

	updated, cmd := model.handleHostPowerConfirmKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	msg := runCmd(t, cmd)
	if _, ok := msg.(hostPowerNeedsPasswordMsg); !ok {
		t.Fatalf("msg = %#v, want hostPowerNeedsPasswordMsg", msg)
	}
	updated, _ = model.Update(msg)
	model = updated.(Model)

	provider := model.provider.(*fakeProvider)
	if provider.stops != 1 {
		t.Fatalf("stops = %d, want 1 — containers must already be stopped by the time the password prompt opens", provider.stops)
	}
	if model.overlay != overlayHostPowerPassword {
		t.Fatalf("overlay = %v, want overlayHostPowerPassword", model.overlay)
	}
	if model.busy {
		t.Fatal("busy = true, want false while waiting on the password prompt")
	}
	if !strings.Contains(model.hostPowerPasswordError, "password is required") {
		t.Fatalf("hostPowerPasswordError = %q, want it to contain the sudo failure", model.hostPowerPasswordError)
	}
	if model.hostPowerKind != hostPowerReboot {
		t.Fatalf("hostPowerKind = %v, want hostPowerReboot preserved into the password prompt", model.hostPowerKind)
	}
}

func TestHostPowerConfirmEscCancelsWithoutStoppingAnything(t *testing.T) {
	fake := withFakeHostPowerRun(t)
	model := testModel()
	model.overlay = overlayHostPowerConfirm
	model.hostPowerKind = hostPowerShutdown

	updated, cmd := model.handleHostPowerConfirmKey(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)

	if cmd != nil {
		t.Fatal("cmd != nil, want no command after esc")
	}
	if model.overlay != overlayNone {
		t.Fatalf("overlay = %v, want overlayNone", model.overlay)
	}
	provider := model.provider.(*fakeProvider)
	if provider.stops != 0 {
		t.Fatalf("stops = %d, want 0", provider.stops)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("hostPowerRun calls = %#v, want none", fake.calls)
	}
}

// typePassword drives handleHostPowerPasswordKey for each rune of s, as
// individual keystrokes, the way a real terminal delivers typed input.
func typePassword(t *testing.T, model Model, s string) Model {
	t.Helper()
	for _, r := range s {
		updated, _ := model.handleHostPowerPasswordKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(Model)
	}
	return model
}

func TestHostPowerPasswordPromptCorrectPasswordCompletesShutdown(t *testing.T) {
	fake := withFakeHostPowerRun(t)
	fake.wantErr = errors.New("sudo: a password is required") // only the empty-password attempt fails
	model := testModel()
	model.overlay = overlayHostPowerPassword
	model.hostPowerKind = hostPowerShutdown
	model = typePassword(t, model, "hunter2")

	updated, cmd := model.handleHostPowerPasswordKey(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.overlay != overlayHostPowerProgress {
		t.Fatalf("overlay = %v, want overlayHostPowerProgress immediately after submitting", model.overlay)
	}
	if len(model.hostPowerPassword) != 0 {
		t.Fatal("hostPowerPassword not cleared immediately after submitting")
	}

	msg := runCmd(t, cmd)
	done, ok := msg.(actionDoneMsg)
	if !ok {
		t.Fatalf("msg = %#v, want actionDoneMsg", msg)
	}
	if done.err != nil {
		t.Fatalf("actionDoneMsg.err = %v, want nil", done.err)
	}
	if len(fake.calls) != 1 || fake.calls[0].password != "hunter2" {
		t.Fatalf("hostPowerRun calls = %#v, want one call with password hunter2", fake.calls)
	}

	updated, _ = model.Update(msg)
	model = updated.(Model)
	if model.statusErr || model.status != "shutdown complete" {
		t.Fatalf("status/statusErr = %q/%v, want shutdown complete/false", model.status, model.statusErr)
	}
}

func TestTickHostPowerActionProgressRampsOnlyWhileItsOwnOverlayIsOpenAndBusy(t *testing.T) {
	model := testModel()
	model.overlay = overlayHostPowerProgress
	model.busy = false
	model.tickHostPowerActionProgress()
	if model.actionProgressPercent != 0 {
		t.Fatalf("actionProgressPercent = %d, want 0 while not busy", model.actionProgressPercent)
	}

	model.busy = true
	model.overlay = overlayNone
	model.tickHostPowerActionProgress()
	if model.actionProgressPercent != 0 {
		t.Fatalf("actionProgressPercent = %d, want 0 while a different overlay is open", model.actionProgressPercent)
	}

	model.overlay = overlayHostPowerProgress
	model.tickHostPowerActionProgress()
	if model.actionProgressPercent != actionProgressStep {
		t.Fatalf("actionProgressPercent = %d, want %d after one tick", model.actionProgressPercent, actionProgressStep)
	}
}

func TestHostPowerPasswordPromptWrongPasswordReopensWithError(t *testing.T) {
	fake := withFakeHostPowerRun(t)
	fake.err = errors.New("sudo: 1 incorrect password attempt") // fails regardless of password
	model := testModel()
	model.overlay = overlayHostPowerPassword
	model.hostPowerKind = hostPowerShutdown
	model = typePassword(t, model, "wrong")

	updated, cmd := model.handleHostPowerPasswordKey(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	msg := runCmd(t, cmd)
	updated, _ = model.Update(msg)
	model = updated.(Model)

	if model.overlay != overlayHostPowerPassword {
		t.Fatalf("overlay = %v, want overlayHostPowerPassword again after a wrong password", model.overlay)
	}
	if !strings.Contains(model.hostPowerPasswordError, "incorrect password") {
		t.Fatalf("hostPowerPasswordError = %q, want the sudo failure shown", model.hostPowerPasswordError)
	}
	provider := model.provider.(*fakeProvider)
	if provider.stops != 0 {
		t.Fatalf("stops = %d, want 0 — this test never went through startHostPower, so nothing should have been stopped again", provider.stops)
	}
}

func TestHostPowerPasswordPromptEscCancelsAndExplainsContainersAlreadyStopped(t *testing.T) {
	model := testModel()
	model.overlay = overlayHostPowerPassword
	model.hostPowerKind = hostPowerShutdown
	model = typePassword(t, model, "partial")

	updated, cmd := model.handleHostPowerPasswordKey(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)

	if cmd != nil {
		t.Fatal("cmd != nil, want no command after esc")
	}
	if model.overlay != overlayNone {
		t.Fatalf("overlay = %v, want overlayNone", model.overlay)
	}
	if len(model.hostPowerPassword) != 0 {
		t.Fatal("hostPowerPassword not cleared after esc")
	}
	if !model.statusErr || !strings.Contains(model.status, "already stopped") {
		t.Fatalf("status/statusErr = %q/%v, want a message explaining containers were already stopped", model.status, model.statusErr)
	}
}

func TestHostPowerPasswordOverlayNeverRendersTypedCharacters(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 40
	model.overlay = overlayHostPowerPassword
	model.hostPowerKind = hostPowerShutdown
	model = typePassword(t, model, "hunter2")

	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	overlay := model.hostPowerPasswordOverlay(renderer)
	if overlay == nil {
		t.Fatal("hostPowerPasswordOverlay() = nil")
	}
	content := ansi.Strip(overlay.Content)
	if strings.Contains(content, "hunter2") {
		t.Fatalf("overlay leaked the typed password:\n%s", content)
	}
	if !strings.Contains(content, strings.Repeat("•", len("hunter2"))) {
		t.Fatalf("overlay missing the masked password of the right length:\n%s", content)
	}
}

func TestDefaultHostPowerRunOverSSHQuotesShutdownAndRebootCommands(t *testing.T) {
	fake := withFakeSSHRun(t)
	system := config.System{Kind: "ssh", SSHHost: "jarvis", Name: "jarvis"}

	if err := defaultHostPowerRun(context.Background(), system, hostPowerShutdown, ""); err != nil {
		t.Fatalf("defaultHostPowerRun(shutdown, no password) error = %v", err)
	}
	if err := defaultHostPowerRun(context.Background(), system, hostPowerReboot, ""); err != nil {
		t.Fatalf("defaultHostPowerRun(reboot, no password) error = %v", err)
	}
	if err := defaultHostPowerRun(context.Background(), system, hostPowerShutdown, "hunter2"); err != nil {
		t.Fatalf("defaultHostPowerRun(shutdown, password) error = %v", err)
	}

	if len(fake.calls) != 3 {
		t.Fatalf("calls = %#v, want 3", fake.calls)
	}
	if fake.calls[0] != "'sudo' '-n' 'shutdown' '-h' 'now'" {
		t.Fatalf("shutdown call = %q, want quoted non-interactive 'sudo' '-n' 'shutdown' '-h' 'now'", fake.calls[0])
	}
	if fake.calls[1] != "'sudo' '-n' 'shutdown' '-r' 'now'" {
		t.Fatalf("reboot call = %q, want quoted non-interactive 'sudo' '-n' 'shutdown' '-r' 'now'", fake.calls[1])
	}
	if fake.calls[2] != "'sudo' '-S' 'shutdown' '-h' 'now'\x00stdin=hunter2\n" {
		t.Fatalf("password call = %q, want quoted 'sudo' '-S' 'shutdown' '-h' 'now' with the password piped as stdin", fake.calls[2])
	}
}
