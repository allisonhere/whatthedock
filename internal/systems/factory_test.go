package systems

import (
	"context"
	"net"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/allisonhere/whatthedock/internal/config"
)

func TestFactoryBuildsSSHTunnelCommand(t *testing.T) {
	var gotName string
	var gotArgs []string
	factory := Factory{Runner: func(_ context.Context, name string, args ...string) error {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return nil
	}}

	provider, err := factory.Provider(context.Background(), config.System{
		ID:           "jarvis",
		Name:         "Jarvis",
		Kind:         "ssh",
		SSHHost:      "allie@jarvis",
		RemoteSocket: "/var/run/docker.sock",
		LocalSocket:  "/tmp/whatthedock-test-jarvis.sock",
	})
	if err != nil {
		t.Fatalf("Provider() err = %v", err)
	}
	if provider == nil {
		t.Fatal("Provider() returned nil provider")
	}
	_ = provider.Close()

	wantArgs := []string{"-fN", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-L", "/tmp/whatthedock-test-jarvis.sock:/var/run/docker.sock", "allie@jarvis"}
	if gotName != "ssh" || !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("runner = %q %#v, want ssh %#v", gotName, gotArgs, wantArgs)
	}
}

func TestFactoryBuildsSSHTunnelCommandWithSeparateUserAndPort(t *testing.T) {
	var gotName string
	var gotArgs []string
	factory := Factory{Runner: func(_ context.Context, name string, args ...string) error {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return nil
	}}

	provider, err := factory.Provider(context.Background(), config.System{
		ID:           "jarvis",
		Name:         "Jarvis",
		Kind:         "ssh",
		SSHHost:      "jarvis.lan",
		SSHUser:      "allie",
		SSHPort:      "2222",
		RemoteSocket: "/var/run/docker.sock",
		LocalSocket:  "/tmp/whatthedock-test-jarvis.sock",
	})
	if err != nil {
		t.Fatalf("Provider() err = %v", err)
	}
	if provider == nil {
		t.Fatal("Provider() returned nil provider")
	}
	_ = provider.Close()

	wantArgs := []string{"-fN", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-p", "2222", "-L", "/tmp/whatthedock-test-jarvis.sock:/var/run/docker.sock", "allie@jarvis.lan"}
	if gotName != "ssh" || !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("runner = %q %#v, want ssh %#v", gotName, gotArgs, wantArgs)
	}
}

// TestSSHCommandOmitsBatchModeForInteractiveUse is the regression test for
// a live report: an automatic, non-interactive connection attempt to a
// password-auth system hung indefinitely, because ssh still tried to
// prompt for a password on /dev/tty despite the caller having no way to
// answer it. SSHCommandArgs (used by every automatic caller) now adds
// BatchMode=yes to fail fast instead — but SSHCommand specifically must
// keep allowing the prompt, since its only caller (switchSystemCmd/
// testSystemCmd) hands the real terminal over via tea.ExecProcess so a
// password-auth system can actually be used at all.
func TestSSHCommandOmitsBatchModeForInteractiveUse(t *testing.T) {
	cmd, err := SSHCommand(config.System{
		ID:           "jarvis",
		Kind:         "ssh",
		SSHHost:      "192.168.86.74",
		SSHUser:      "allie",
		RemoteSocket: "/var/run/docker.sock",
		LocalSocket:  filepath.Join(t.TempDir(), "jarvis.sock"),
	})
	if err != nil {
		t.Fatalf("SSHCommand() err = %v", err)
	}
	if cmd == nil {
		t.Fatal("SSHCommand() = nil command")
	}
	for _, arg := range cmd.Args {
		if arg == "BatchMode=yes" {
			t.Fatalf("SSHCommand() args = %v, want no BatchMode — it must still be able to prompt interactively", cmd.Args)
		}
	}
}

// TestSSHCommandArgsIncludesBatchModeForAutomaticUse is
// TestSSHCommandOmitsBatchModeForInteractiveUse's complement: every
// non-interactive caller (app launch, automatic reconnects) must get
// BatchMode so a password-auth system fails fast instead of hanging.
func TestSSHCommandArgsIncludesBatchModeForAutomaticUse(t *testing.T) {
	args, err := SSHCommandArgs(config.System{
		ID:           "jarvis",
		Kind:         "ssh",
		SSHHost:      "192.168.86.74",
		SSHUser:      "allie",
		RemoteSocket: "/var/run/docker.sock",
		LocalSocket:  filepath.Join(t.TempDir(), "jarvis.sock"),
	})
	if err != nil {
		t.Fatalf("SSHCommandArgs() err = %v", err)
	}
	found := false
	for _, arg := range args {
		if arg == "BatchMode=yes" {
			found = true
		}
	}
	if !found {
		t.Fatalf("SSHCommandArgs() args = %v, want BatchMode=yes so this never hangs on a password prompt it can't answer", args)
	}
}

func TestSSHCommandArgsRebuildsTunnelForStaleSocketFile(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "stale.sock")

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("net.Listen() err = %v", err)
	}
	unixLn, ok := ln.(*net.UnixListener)
	if !ok {
		t.Fatalf("listener type = %T, want *net.UnixListener", ln)
	}
	// Leave the socket file behind on Close, the way a killed/crashed SSH
	// tunnel process would, instead of the default auto-unlink behavior.
	unixLn.SetUnlinkOnClose(false)
	if err := ln.Close(); err != nil {
		t.Fatalf("Close() err = %v", err)
	}

	args, err := SSHCommandArgs(config.System{
		SSHHost:      "jarvis",
		RemoteSocket: "/var/run/docker.sock",
		LocalSocket:  sockPath,
	})
	if err != nil {
		t.Fatalf("SSHCommandArgs() err = %v", err)
	}
	if args == nil {
		t.Fatal("SSHCommandArgs() = nil, want fresh tunnel args for a stale socket file")
	}
}

// TestRemoteCommandIncludesBatchModeAndConnectTimeout is the regression
// test for RemoteCommand having no equivalent protection to
// SSHCommandArgs's automatic path: every caller (remote Compose file
// operations, `docker compose` invocations) runs headless with no terminal
// handed over, so without BatchMode a password-auth system's ssh would try
// to prompt on /dev/tty anyway — fighting the running TUI for the real
// terminal — and hang until the caller's own context timeout. BatchMode
// makes it fail fast instead; ConnectTimeout bounds an unreachable host.
func TestRemoteCommandIncludesBatchModeAndConnectTimeout(t *testing.T) {
	cmd, err := RemoteCommand(context.Background(), config.System{
		SSHHost:      "jarvis",
		SSHUser:      "allie",
		RemoteSocket: "/var/run/docker.sock",
	}, "cat /etc/hostname")
	if err != nil {
		t.Fatalf("RemoteCommand() err = %v", err)
	}
	wantArgs := []string{"ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "allie@jarvis", "cat /etc/hostname"}
	if !reflect.DeepEqual(cmd.Args, wantArgs) {
		t.Fatalf("RemoteCommand() args = %#v, want %#v", cmd.Args, wantArgs)
	}
}

func TestRemoteCommandIncludesPortWithBatchMode(t *testing.T) {
	cmd, err := RemoteCommand(context.Background(), config.System{
		SSHHost:      "jarvis.lan",
		SSHUser:      "allie",
		SSHPort:      "2222",
		RemoteSocket: "/var/run/docker.sock",
	}, "ls -1Ap")
	if err != nil {
		t.Fatalf("RemoteCommand() err = %v", err)
	}
	wantArgs := []string{"ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-p", "2222", "allie@jarvis.lan", "ls -1Ap"}
	if !reflect.DeepEqual(cmd.Args, wantArgs) {
		t.Fatalf("RemoteCommand() args = %#v, want %#v", cmd.Args, wantArgs)
	}
}

// TestRemoteExecKeychainModeUsesStoredPasswordNotSubprocess checks
// RemoteExec's keychain branch: it must fetch the stored password and use
// it, rather than falling through to RemoteCommand's shelled-out ssh (which
// has no way to supply that password and would only ever fail or hang).
// Wiring is exercised end-to-end against a fake SSH server in
// nativetunnel_test.go; this only checks the routing decision + error
// message when nothing is stored, matching keychain_test.go's coverage of
// the same branch in Factory.Provider.
func TestRemoteExecKeychainModeErrorsClearlyWithNoStoredPassword(t *testing.T) {
	stubSecrets(t)

	_, err := RemoteExec(context.Background(), config.System{
		ID:           "jarvis",
		Name:         "jarvis",
		SSHHost:      "192.168.86.74",
		SSHUser:      "allie",
		SSHAuth:      "keychain",
		RemoteSocket: "/var/run/docker.sock",
	}, "cat /etc/hostname", "")
	if err == nil {
		t.Fatal("RemoteExec() err = nil, want an error when no password is stored")
	}
	const want = `no password stored in keychain for "jarvis" — open Systems and set one`
	if err.Error() != want {
		t.Fatalf("RemoteExec() err = %q, want %q", err.Error(), want)
	}
}

func TestSSHTargetOmitsEmptyUser(t *testing.T) {
	if got := SSHTarget(config.System{SSHHost: "jarvis"}); got != "jarvis" {
		t.Fatalf("SSHTarget() = %q, want jarvis", got)
	}
}

func TestDockerHostFor(t *testing.T) {
	tests := []struct {
		name   string
		system config.System
		want   string
	}{
		{name: "local default", system: config.System{Kind: "local"}, want: ""},
		{name: "local host", system: config.System{Kind: "local", DockerHost: "tcp://host:2375"}, want: "tcp://host:2375"},
		{name: "ssh", system: config.System{Kind: "ssh", LocalSocket: "/tmp/x.sock"}, want: "unix:///tmp/x.sock"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DockerHostFor(tt.system); got != tt.want {
				t.Fatalf("DockerHostFor() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDockerHostLabel(t *testing.T) {
	tests := []struct {
		name   string
		system config.System
		want   string
	}{
		{name: "local default", system: config.System{Kind: "local"}, want: "docker default"},
		{name: "local host", system: config.System{Kind: "local", DockerHost: "tcp://host:2375"}, want: "tcp://host:2375"},
		{name: "ssh", system: config.System{Kind: "ssh", LocalSocket: "/tmp/x.sock"}, want: "unix:///tmp/x.sock"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DockerHostLabel(tt.system); got != tt.want {
				t.Fatalf("DockerHostLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}
