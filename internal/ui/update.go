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

// updateRestartDelay is how long a successful "updated to vX — restarting…"
// status stays on screen before quitting actually happens. main.go's
// restartInto replaces the process image with syscall.Exec — instant, no
// visible transition — so without a deliberate pause here the success
// message never has time to actually be read: reported live as the whole
// install "happening in a flash with no messaging."
const updateRestartDelay = 1200 * time.Millisecond

// updateRestartMsg fires once updateRestartDelay has elapsed after a
// successful install, finally triggering the tea.Quit that lets main()
// re-exec into the new binary. It's only ever produced by
// tickUpdateRestart, itself only ever returned from the updateInstalledMsg
// success case, so there's no scenario where a stale one could arrive and
// needs guarding against.
type updateRestartMsg struct{}

func tickUpdateRestart() tea.Cmd {
	return tea.Tick(updateRestartDelay, func(time.Time) tea.Msg { return updateRestartMsg{} })
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
func (m Model) handleUpdateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "n", "q":
		m.overlay = overlayNone
		m.updateIgnoredVersion = m.updateAvailableVersion
		m.saveSettings()
		m.updateAvailableVersion = ""
	case "y":
		m.overlay = overlayNone
		m.updateInstalling = true
		m.status, m.statusErr = "updating to "+m.updateAvailableVersion+"…", false
		return m, m.installUpdateCmd(m.updateAvailableVersion)
	}
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
