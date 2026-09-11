package systems

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/config"
	dockerprovider "github.com/allisonhere/whatthedock/internal/docker"
)

type Runner func(context.Context, string, ...string) error

type Factory struct {
	Runner Runner
}

func NewFactory() Factory {
	return Factory{Runner: runCommand}
}

func (f Factory) Provider(ctx context.Context, system config.System) (app.Provider, error) {
	system = config.NormalizeSystems(config.Settings{ActiveSystem: system.ID, Systems: []config.System{system}}).Systems[0]
	switch system.Kind {
	case "ssh":
		if system.SSHAuth == "keychain" {
			password, err := PasswordFor(system.ID)
			if err != nil {
				if errors.Is(err, errNoStoredPassword) {
					return nil, fmt.Errorf("no password stored in keychain for %q — open Systems and set one", system.Name)
				}
				return nil, err
			}
			if err := dialKeychainTunnel(ctx, system, password); err != nil {
				return nil, err
			}
		} else if err := f.ensureSSHTunnel(ctx, system); err != nil {
			return nil, err
		}
		return dockerprovider.NewProvider(system.ID, system.Name, "unix://"+system.LocalSocket)
	case "local", "":
		return dockerprovider.NewProvider(system.ID, system.Name, system.DockerHost)
	default:
		return nil, fmt.Errorf("unknown system kind %q", system.Kind)
	}
}

func (f Factory) ensureSSHTunnel(ctx context.Context, system config.System) error {
	args, err := SSHCommandArgs(system)
	if err != nil || args == nil {
		return err
	}
	runner := f.Runner
	if runner != nil {
		return runner(ctx, "ssh", args...)
	}
	cmd, err := SSHCommandContext(ctx, system)
	if err != nil || cmd == nil {
		return err
	}
	return cmd.Run()
}

// SSHCommand builds the tunnel command for an *interactive* connection
// attempt — the caller hands the real terminal over to it (tea.ExecProcess
// in switchSystemCmd/testSystemCmd), so a password-auth system can safely
// prompt on it. Never use this for a connection attempt that isn't backed
// by a real terminal handoff — see SSHCommandArgs's doc comment for why.
func SSHCommand(system config.System) (*exec.Cmd, error) {
	args, err := sshCommandArgs(system, true)
	if err != nil || args == nil {
		return nil, err
	}
	return exec.Command("ssh", args...), nil
}

// SSHCommandContext builds the tunnel command for an *automatic*
// connection attempt — see SSHCommandArgs.
func SSHCommandContext(ctx context.Context, system config.System) (*exec.Cmd, error) {
	args, err := SSHCommandArgs(system)
	if err != nil || args == nil {
		return nil, err
	}
	return exec.CommandContext(ctx, "ssh", args...), nil
}

// SSHCommandArgs builds the tunnel args for an *automatic* connection
// attempt — app launch (providerForMode) and any other non-interactive
// caller, none of which wire the child's stdio up to a real terminal (see
// ensureSSHTunnel/runCommand). Without BatchMode, a password-auth system
// ssh can't reach programmatically would still try to prompt by opening
// /dev/tty directly — bypassing the disconnected Cmd.Stdin entirely — and
// then hang indefinitely with nothing on screen to explain why, since
// there's no context timeout on this path either. This was reported live
// as "the app wants a password... it's not connecting": ssh was sitting at
// a bare tty prompt before the TUI had even painted a frame. BatchMode
// makes ssh fail fast and cleanly instead of ever attempting to prompt;
// ConnectTimeout bounds the separate case of an unreachable host. Neither
// applies to SSHCommand's interactive path, which needs the prompt to work.
func SSHCommandArgs(system config.System) ([]string, error) {
	return sshCommandArgs(system, false)
}

func sshCommandArgs(system config.System, interactive bool) ([]string, error) {
	system = config.NormalizeSystems(config.Settings{ActiveSystem: system.ID, Systems: []config.System{system}}).Systems[0]
	if system.SSHHost == "" {
		return nil, fmt.Errorf("ssh host is required")
	}
	if system.LocalSocket == "" {
		return nil, fmt.Errorf("local socket is required")
	}
	if system.RemoteSocket == "" {
		return nil, fmt.Errorf("remote socket is required")
	}
	live, err := prepareLocalSocket(system.LocalSocket)
	if err != nil {
		return nil, err
	}
	if live {
		return nil, nil
	}
	args := []string{"-fN"}
	if !interactive {
		args = append(args, "-o", "BatchMode=yes", "-o", "ConnectTimeout=10")
	}
	if system.SSHPort != "" {
		args = append(args, "-p", system.SSHPort)
	}
	args = append(args, "-L", system.LocalSocket+":"+system.RemoteSocket, SSHTarget(system))
	return args, nil
}

func SSHTarget(system config.System) string {
	if system.SSHUser == "" {
		return system.SSHHost
	}
	return system.SSHUser + "@" + system.SSHHost
}

// RemoteCommand returns an *exec.Cmd that runs script on system's SSH host,
// using the same ssh binary and host/port/user convention as the Docker
// socket tunnel above. It's for one-shot remote operations outside the
// tunnel — listing, reading, and writing Compose files, and running
// `docker compose` — not for the persistent -fN port forward.
//
// Like SSHCommandArgs's automatic path, this always sets BatchMode=yes and
// ConnectTimeout=10: every caller (remote Compose file operations,
// `docker compose` invocations) runs headless inside a tea.Cmd goroutine
// with no terminal handed over, so without BatchMode a password-auth system
// ssh can't reach programmatically would try to prompt on /dev/tty anyway —
// fighting the running TUI for the real terminal — and hang until the
// caller's own context timeout, rather than failing fast and cleanly.
// ConnectTimeout bounds the separate case of an unreachable host. Keychain
// systems don't reach this path at all — see RemoteExec.
func RemoteCommand(ctx context.Context, system config.System, script string) (*exec.Cmd, error) {
	if system.SSHHost == "" {
		return nil, fmt.Errorf("ssh host is required")
	}
	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10"}
	if system.SSHPort != "" {
		args = append(args, "-p", system.SSHPort)
	}
	args = append(args, SSHTarget(system), script)
	return exec.CommandContext(ctx, "ssh", args...), nil
}

// RemoteExec runs script on system's SSH host and returns its combined
// output, piping stdin to the remote command when non-empty. It's the
// single entry point every remote Compose operation goes through (see
// internal/ui/create.go's sshRun seam), and it picks the same
// authentication path Factory.Provider already picks for the Docker socket
// tunnel: a keychain-mode system's stored password goes through the native
// Go SSH client (never touching a subprocess's argv or environment — see
// dialKeychainTunnel), everything else shells out via RemoteCommand.
//
// A password-prompt system (SSHAuth=="password") has no stored credential
// to use here — by design, that mode only ever prompts on a real terminal
// handoff (SSHCommand/tea.ExecProcess), which this headless path doesn't
// have. It still goes through RemoteCommand and, thanks to BatchMode, fails
// fast with a clear error instead of hanging.
func RemoteExec(ctx context.Context, system config.System, script string, stdin string) ([]byte, error) {
	if system.SSHAuth == "keychain" {
		password, err := PasswordFor(system.ID)
		if err != nil {
			if errors.Is(err, errNoStoredPassword) {
				return nil, fmt.Errorf("no password stored in keychain for %q — open Systems and set one", system.Name)
			}
			return nil, err
		}
		return remoteKeychainCommand(ctx, system, password, script, stdin)
	}
	cmd, err := RemoteCommand(ctx, system, script)
	if err != nil {
		return nil, err
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		if text == "" {
			text = err.Error()
		}
		return nil, errors.New(text)
	}
	return output, nil
}

// ShellQuote wraps s in single quotes for safe inclusion in a remote shell
// command, escaping any embedded single quotes POSIX-style.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func isSocket(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeSocket != 0
}

// isLiveSocket reports whether path is a unix socket with something actually
// listening on it. A dead SSH tunnel (killed process, network drop, machine
// sleep/wake) leaves the socket file behind with its mode bit intact, so a
// mode-only check like isSocket treats a stale file as an active tunnel and
// SSHCommandArgs never re-establishes it. Dialing catches that case.
func isLiveSocket(path string) bool {
	if !isSocket(path) {
		return false
	}
	conn, err := net.DialTimeout("unix", path, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// prepareLocalSocket reports whether path is already a live tunnel socket
// (nothing to do — caller should reuse it) or, if not, removes any stale
// socket file and ensures its parent directory exists so a fresh listener
// can bind there. Shared by both the shelled-out ssh tunnel (SSHCommandArgs)
// and the native keychain tunnel (dialKeychainTunnel) — the exact same
// stale-socket handling either way.
func prepareLocalSocket(path string) (live bool, err error) {
	if isLiveSocket(path) {
		return true, nil
	}
	_ = os.Remove(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return false, nil
}

func runCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.Run()
}

// DockerHostFor returns the DOCKER_HOST value for an already-connected
// system — the same value Factory.Provider resolves internally — without
// re-establishing an SSH tunnel, since callers (the exec-shell subprocess)
// only need this after a provider is already active for the system. An
// empty return means "use Docker's own default resolution" rather than a
// value to set explicitly.
func DockerHostFor(system config.System) string {
	switch system.Kind {
	case "ssh":
		return "unix://" + system.LocalSocket
	default:
		return system.DockerHost
	}
}

func DockerHostLabel(system config.System) string {
	switch system.Kind {
	case "ssh":
		return "unix://" + system.LocalSocket
	case "local":
		if system.DockerHost != "" {
			return system.DockerHost
		}
		return "docker default"
	default:
		if system.DockerHost != "" {
			if u, err := url.Parse(system.DockerHost); err == nil && u.Scheme != "" {
				return system.DockerHost
			}
		}
		return system.Kind
	}
}
