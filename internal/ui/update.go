package ui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/allisonhere/whatthedock/internal/update"
)

// updateCheckMsg carries an update check's result back into Update.
// manual distinguishes a Settings-triggered check (always worth a status
// line, even "up to date" or a network error) from the automatic
// background one (silent unless it actually finds something to say).
type updateCheckMsg struct {
	manual bool
	latest string
	err    error
}

// updateInstalledMsg carries the result of downloading and installing a
// release back into Update. exePath, when err is nil, is where the
// now-updated binary lives — Model.RestartExecPath surfaces it to main()
// once the Bubble Tea program exits, so main() can re-exec into it after
// Bubble Tea has already restored the terminal.
type updateInstalledMsg struct {
	exePath string
	err     error
}

// updateProgressStep/Interval drive the update overlay's fake install
// progress bar: a step every 120ms fills the bar to 100% in ~2.4s,
// landing it in the "2 or 3 sec" range asked for. It's on its own fixed
// clock regardless of how long installUpdateCmd actually takes — see
// Model.updateInstallProgress's doc comment for how that's reconciled
// with the real result.
const (
	updateProgressStep     = 5
	updateProgressInterval = 120 * time.Millisecond
)

// updateProgressTickMsg advances the update overlay's fake progress bar by
// one step. Only ever produced by tickUpdateProgress, itself only ever
// returned while an install is in flight (see handleUpdateKey's "y" case
// and the updateProgressTickMsg handler in model.go), so a tick arriving
// after the install has already finalized is simply stale and ignored.
type updateProgressTickMsg struct{}

func tickUpdateProgress() tea.Cmd {
	return tea.Tick(updateProgressInterval, func(time.Time) tea.Msg { return updateProgressTickMsg{} })
}

// autoCheckForUpdateCmd returns the automatic on-launch update check —
// unconditional, every launch, no once-a-day throttle. updateLastCheck is
// still recorded after each check (see the updateCheckMsg handler in
// model.go) purely for Settings' "Check for update" row to show "up to
// date" once at least one check has happened, not to gate this.
func (m Model) autoCheckForUpdateCmd() tea.Cmd {
	return m.checkForUpdateCmd(false)
}

// checkForUpdateCmd asks GitHub for the latest release tag. manual marks
// the request as user-initiated (see updateCheckMsg).
func (m Model) checkForUpdateCmd(manual bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		latest, err := update.LatestRelease(ctx, update.Repo)
		return updateCheckMsg{manual: manual, latest: latest, err: err}
	}
}

// installUpdateCmd downloads version's release asset and replaces the
// running binary with it.
func (m Model) installUpdateCmd(version string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		exePath, err := update.ReplaceRunningExecutable(ctx, update.Repo, version)
		return updateInstalledMsg{exePath: exePath, err: err}
	}
}

// handleUpdateKey gates the "update available" confirmation the same way
// Delete/Replicate do: esc/n/q declines (and persists this version as
// ignored, so the automatic check won't prompt again for it), y installs.
// Once installing, the overlay stays up showing progress (see updateOverlay)
// and every key is ignored — there's no cancel, and re-pressing y shouldn't
// restart the install out from under itself. Once installed, it shows a
// success message and waits for enter — see updateInstallSucceeded's doc
// comment for why that's a deliberate user action rather than a timer.
func (m Model) handleUpdateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.updateInstallSucceeded {
		if msg.String() == "enter" {
			return m, tea.Quit
		}
		return m, nil
	}
	if m.updateInstalling {
		return m, nil
	}
	switch msg.String() {
	case "esc", "n", "q":
		m.overlay = overlayNone
		m.updateIgnoredVersion = m.updateAvailableVersion
		m.saveSettings()
		m.updateAvailableVersion = ""
	case "y":
		m.updateInstalling = true
		m.updateInstallProgress = 0
		m.updateInstallReady = false
		m.updateInstallErr = nil
		m.updateInstallExePath = ""
		return m, tea.Batch(m.installUpdateCmd(m.updateAvailableVersion), tickUpdateProgress())
	}
	return m, nil
}

// finalizeUpdateInstall applies the real installUpdateCmd result once it's
// safe to act on — either immediately (a failure) or once the fake
// progress bar has also finished animating (a success) — see
// updateInstalledMsg/updateProgressTickMsg in model.go. A failure returns
// straight to overlayNone with a status-bar error; a success keeps the
// overlay up in its "succeeded" state (see updateInstallSucceeded) so the
// user reads the confirmation and dismisses it with enter themselves,
// rather than the app guessing how long that takes and quitting on a timer.
func (m Model) finalizeUpdateInstall() (Model, tea.Cmd) {
	m.updateInstalling = false
	if m.updateInstallErr != nil {
		m.overlay = overlayNone
		m.status, m.statusErr = "update failed: "+friendlyDockerError(m.updateInstallErr), true
		return m, nil
	}
	m.updateInstallSucceeded = true
	m.restartExecPath = m.updateInstallExePath
	return m, nil
}

// RestartExecPath is set once an update finishes installing successfully —
// main(), after Bubble Tea's program.Run() returns (and has already
// restored the terminal), checks this and re-execs into it if non-empty.
// Replacing the process image is deliberately left to main(): doing it
// mid-program, before Bubble Tea has released the terminal (alt screen,
// raw mode), would replace the process out from under that cleanup.
func (m Model) RestartExecPath() string {
	return m.restartExecPath
}
