package ui

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/allisonhere/whatthedock/internal/config"
	"github.com/allisonhere/whatthedock/internal/domain"
	"github.com/allisonhere/whatthedock/internal/systems"
)

// hostPowerKind is which of the two Ctrl-K "shut down"/"reboot" host
// actions overlayHostPowerConfirm is confirming — see Model.hostPowerKind.
type hostPowerKind int

const (
	hostPowerShutdown hostPowerKind = iota
	hostPowerReboot
)

// verb is the present-tense wording used in the itemized confirm prompt
// ("Shut down %q?"/"Reboot %q?") and in refusal/progress status text.
func (k hostPowerKind) verb() string {
	if k == hostPowerReboot {
		return "reboot"
	}
	return "shut down"
}

// label is the terse actionDoneMsg label — matches the existing
// Restart/StartStop convention ("restart complete"/"start/stop complete").
func (k hostPowerKind) label() string {
	if k == hostPowerReboot {
		return "reboot"
	}
	return "shutdown"
}

// promptVerb is verb's capitalized form for the confirm prompt's leading
// word ("Shut down %q?"/"Reboot %q?").
func (k hostPowerKind) promptVerb() string {
	if k == hostPowerReboot {
		return "Reboot"
	}
	return "Shut down"
}

// outcome describes what happens to the host once the command runs, for
// the confirm prompt's closing clause ("...then the host powers off."/
// "...then the host reboots.").
func (k hostPowerKind) outcome() string {
	if k == hostPowerReboot {
		return "reboots"
	}
	return "powers off"
}

// progressVerb is the present-continuous form for the progress modal's
// header line ("Shutting down %q…"/"Rebooting %q…") — distinct from the
// question-form promptVerb and the terse status/log form verb.
func (k hostPowerKind) progressVerb() string {
	if k == hostPowerReboot {
		return "Rebooting"
	}
	return "Shutting down"
}

// nonInteractiveCommand is the first attempt: -n makes sudo fail
// immediately (never hang trying to reach a tty) if it needs a password —
// the deterministic signal startHostPower uses to fall back to the in-app
// password prompt instead. `shutdown` is used rather than `systemctl
// poweroff`/`reboot` since it's the one command present on essentially
// every Linux distro regardless of init system.
func (k hostPowerKind) nonInteractiveCommand() []string {
	if k == hostPowerReboot {
		return []string{"sudo", "-n", "shutdown", "-r", "now"}
	}
	return []string{"sudo", "-n", "shutdown", "-h", "now"}
}

// passwordCommand is the retry once the user has typed a password into
// the in-app prompt — -S reads it from stdin instead of a tty, which is
// what makes an in-app (not terminal-handoff) prompt possible at all.
func (k hostPowerKind) passwordCommand() []string {
	if k == hostPowerReboot {
		return []string{"sudo", "-S", "shutdown", "-r", "now"}
	}
	return []string{"sudo", "-S", "shutdown", "-h", "now"}
}

// runningContainers lists every running container across snapshot's
// Compose projects (each service can have multiple container instances —
// domain.Service.Containers, not domain.Service itself) and standalone
// containers — the whole host, not just whatever's currently selected in
// the tree, since a host shutdown/reboot takes all of them down regardless.
func runningContainers(snapshot domain.Snapshot) []domain.Container {
	var out []domain.Container
	for _, p := range snapshot.Projects {
		for _, svc := range p.Services {
			for _, c := range svc.Containers {
				if c.IsRunning() {
					out = append(out, c)
				}
			}
		}
	}
	for _, c := range snapshot.Standalone {
		if c.IsRunning() {
			out = append(out, c)
		}
	}
	return out
}

// openHostPowerConfirm opens the itemized shutdown/reboot confirm overlay.
// Refuses outright for the demo provider (m.provider.Host().ID == "demo"
// is the only runtime signal demo mode leaves — there's no dedicated
// Model field for it) rather than opening a confirm for an action that can
// never succeed against a fake, in-memory host.
func (m Model) openHostPowerConfirm(kind hostPowerKind) (tea.Model, tea.Cmd) {
	if m.provider.Host().ID == "demo" {
		m.status, m.statusErr = "demo mode has no real host to "+kind.verb(), true
		return m, nil
	}
	m.hostPowerKind = kind
	m.overlay = overlayHostPowerConfirm
	return m, nil
}

// handleHostPowerConfirmKey answers the itemized shutdown/reboot confirm —
// same plain esc/n/q-cancels, y-proceeds shape as handleDeleteStackConfirmKey.
func (m Model) handleHostPowerConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "n", "q":
		m.overlay = overlayNone
	case "y":
		kind := m.hostPowerKind
		m.overlay = overlayNone
		return m.startHostPower(kind)
	}
	return m, nil
}

// hostPowerNeedsPasswordMsg is returned in place of actionDoneMsg whenever
// the non-interactive sudo attempt (or a subsequent password-supplied
// retry) fails, for any reason — see startHostPower's and
// retryHostPowerWithPasswordCmd's doc comments for why this app
// deliberately doesn't try to classify *why* it failed via sudo's error
// text before deciding whether to prompt.
type hostPowerNeedsPasswordMsg struct {
	kind hostPowerKind
	err  error
}

// startHostPower gracefully stops every running container on the host,
// then attempts the actual OS-level shutdown/reboot non-interactively —
// one background tea.Cmd, following startReplicate's standalone-path
// shape: a progress channel drained by the existing
// sendActionProgress/drainActionProgress machinery. On success this ends
// in the ordinary actionDoneMsg every other action already uses, so
// success status, the busy/progress reset, and the post-action
// refreshCmd() all come for free. On failure it returns
// hostPowerNeedsPasswordMsg instead of a hard error — sudo needing a
// password is expected here, not exceptional, since this app never hands
// over the terminal for sudo to prompt on directly (that would suspend the
// TUI at the exact moment the host is about to disappear); the in-app
// password overlay (handleHostPowerPasswordKey) is how the user finishes
// authenticating instead.
func (m Model) startHostPower(kind hostPowerKind) (tea.Model, tea.Cmd) {
	system := m.activeSystemConfig()
	host := m.provider.Host()
	running := runningContainers(m.snapshot)
	provider := m.provider
	label := kind.label()
	progress := make(chan string, 16)
	m.busy = true
	m.actionProgress = progress
	m.actionProgressText = "stopping containers…"
	m.actionProgressPercent = 0
	m.status, m.statusErr = label+"…", false
	m.overlay = overlayHostPowerProgress
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		for _, c := range running {
			sendActionProgress(progress, "stopping "+c.DisplayName()+"…")
			if err := provider.StopContainer(ctx, c.ID); err != nil {
				// Best-effort: the host is going down regardless, so one
				// container declining a graceful stop shouldn't block the
				// whole operation — refusing to reboot over it would leave
				// the user with no way to actually reboot the host.
				sendActionProgress(progress, c.DisplayName()+" did not stop cleanly: "+err.Error())
			}
		}
		sendActionProgress(progress, kind.verb()+" "+host.Name+"…")
		if err := hostPowerRun(ctx, system, kind, ""); err != nil {
			return hostPowerNeedsPasswordMsg{kind: kind, err: err}
		}
		return actionDoneMsg{label: label, err: nil, skipRefresh: true}
	}
}

// retryHostPowerWithPasswordCmd is the password overlay's "enter" —
// containers are already stopped by this point (startHostPower's job),
// so this only re-attempts the OS command itself, now with a password to
// supply on stdin. Failure (wrong password, or the same non-auth problem
// from the first attempt showing up again) loops back to
// hostPowerNeedsPasswordMsg so the overlay reopens with the error shown —
// retryable indefinitely, Esc always available, the same way a real
// terminal sudo prompt keeps asking until you succeed or give up.
func (m Model) retryHostPowerWithPasswordCmd(kind hostPowerKind, password string) tea.Cmd {
	system := m.activeSystemConfig()
	label := kind.label()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := hostPowerRun(ctx, system, kind, password)
		if err != nil {
			return hostPowerNeedsPasswordMsg{kind: kind, err: err}
		}
		return actionDoneMsg{label: label, err: nil, skipRefresh: true}
	}
}

// clearPasswordBuffer overwrites buf's contents before dropping it — best
// effort only (see host_power.go's package doc / the plan this shipped
// under): Go strings/slices the buffer gets copied into along the way
// (strings.NewReader, sshRun's internals) can't be scrubbed the same way,
// so this isn't a hardened guarantee, just a reasonable precaution beyond
// doing nothing.
func clearPasswordBuffer(buf []rune) []rune {
	for i := range buf {
		buf[i] = 0
	}
	return nil
}

// handleHostPowerPasswordKey answers the in-app sudo password prompt
// (overlayHostPowerPassword). Deliberately minimal — append/backspace
// only, no cursor movement or selection, matching how every terminal
// password prompt already behaves — and never reuses the create-form's
// ripple.Model field editor, since that wires up OSC52 clipboard copy and
// undo history, both wrong for a password field.
func (m Model) handleHostPowerPasswordKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		kind := m.hostPowerKind
		m.hostPowerPassword = clearPasswordBuffer(m.hostPowerPassword)
		m.hostPowerPasswordError = ""
		m.overlay = overlayNone
		m.status, m.statusErr = kind.label()+" cancelled — containers were already stopped, sudo authentication is needed to finish", true
	case "enter":
		password := string(m.hostPowerPassword)
		m.hostPowerPassword = clearPasswordBuffer(m.hostPowerPassword)
		kind := m.hostPowerKind
		m.hostPowerPasswordError = ""
		m.busy = true
		m.actionProgressText = "authenticating…"
		m.actionProgressPercent = 0
		m.status, m.statusErr = "authenticating…", false
		m.overlay = overlayHostPowerProgress
		return m, m.retryHostPowerWithPasswordCmd(kind, password)
	case "backspace":
		if n := len(m.hostPowerPassword); n > 0 {
			m.hostPowerPassword[n-1] = 0
			m.hostPowerPassword = m.hostPowerPassword[:n-1]
		}
	case "ctrl+u":
		m.hostPowerPassword = clearPasswordBuffer(m.hostPowerPassword)
	default:
		if len(msg.Runes) > 0 {
			m.hostPowerPassword = append(m.hostPowerPassword, msg.Runes...)
		}
	}
	return m, nil
}

// hostPowerRun is a seam so tests can substitute a fake instead of
// shelling out for real — the same role sshRun already plays for remote
// Compose operations (internal/ui/create.go). An empty password means the
// non-interactive attempt; non-empty means the password-supplied retry.
var hostPowerRun = defaultHostPowerRun

// defaultHostPowerRun branches local-vs-SSH exactly like every other
// operation in create.go (e.g. defaultApplyComposeDeleteStack, runDockerCompose):
// SSH systems go through the existing sshRun seam with each word quoted for
// safe interpolation; local systems run the command directly, no shell
// involved since the command has no dynamic parts to interpolate. When
// password is non-empty it's piped as stdin (with -S in the command words)
// exactly the way sshRun already pipes stdin for e.g. writing Compose file
// content — no terminal handoff needed either way.
func defaultHostPowerRun(ctx context.Context, system config.System, kind hostPowerKind, password string) error {
	words := kind.nonInteractiveCommand()
	stdin := ""
	if password != "" {
		words = kind.passwordCommand()
		stdin = password + "\n"
	}
	if system.Kind == "ssh" {
		quoted := make([]string, len(words))
		for i, w := range words {
			quoted[i] = systems.ShellQuote(w)
		}
		_, err := sshRun(ctx, system, strings.Join(quoted, " "), stdin)
		return err
	}
	cmd := exec.CommandContext(ctx, words[0], words[1:]...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		if text == "" {
			text = err.Error()
		}
		return errors.New(text)
	}
	return nil
}
