package systems

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/allisonhere/whatthedock/internal/config"
)

// knownHostsPath is a seam over the real ~/.ssh/known_hosts location so
// tests can point it at a throwaway file instead.
var knownHostsPath = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ssh", "known_hosts")
}

// dialKeychainTunnel establishes a keychain-mode system's Docker socket
// tunnel using a native Go SSH client rather than shelling out to ssh (see
// ensureSSHTunnel) — password auth with a password that only ever came
// from the OS keychain (see keychain.go) shouldn't need it to touch a
// subprocess's argv or environment even momentarily.
//
// Unlike the shelled-out tunnel's `-fN` background process, this one runs
// for the life of the whatthedock process itself: it does not outlive the
// app, so a keychain-mode system re-dials on every launch rather than
// reusing a tunnel left running from a previous session. That trade-off
// is deliberate — no orphaned background ssh processes to leak — not an
// oversight.
//
// Host keys are checked against the user's real ~/.ssh/known_hosts, the
// same trust model the shelled-out ssh already uses: an unrecognized host
// fails clearly rather than ever being trusted blindly.
func dialKeychainTunnel(ctx context.Context, system config.System, password string) error {
	if system.SSHHost == "" {
		return fmt.Errorf("ssh host is required")
	}
	if system.LocalSocket == "" {
		return fmt.Errorf("local socket is required")
	}
	if system.RemoteSocket == "" {
		return fmt.Errorf("remote socket is required")
	}
	live, err := prepareLocalSocket(system.LocalSocket)
	if err != nil {
		return err
	}
	if live {
		return nil
	}

	client, err := dialKeychainClient(ctx, system, password)
	if err != nil {
		return err
	}

	listener, err := net.Listen("unix", system.LocalSocket)
	if err != nil {
		client.Close()
		return fmt.Errorf("listen on %s: %w", system.LocalSocket, err)
	}

	go serveKeychainTunnel(listener, client, system.RemoteSocket)
	return nil
}

// dialKeychainClient opens an authenticated SSH client connection to
// system's host using password auth, honoring ctx: canceling ctx while the
// TCP dial or the SSH handshake is still in flight aborts the attempt
// immediately instead of waiting out the full 10s handshake timeout.
// Shared by dialKeychainTunnel (the Docker socket tunnel) and
// remoteKeychainCommand (one-shot remote Compose operations) — the same
// native-Go-SSH connection either way, so the keychain password never
// touches a subprocess's argv or environment.
func dialKeychainClient(ctx context.Context, system config.System, password string) (*ssh.Client, error) {
	hostKeyCallback, err := knownhosts.New(knownHostsPath())
	if err != nil {
		return nil, fmt.Errorf("read known_hosts: %w — connect once with ssh first to add %s", err, system.SSHHost)
	}

	port := system.SSHPort
	if port == "" {
		port = "22"
	}
	cfg := &ssh.ClientConfig{
		User:            system.SSHUser,
		Auth:            []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: hostKeyCallback,
		Timeout:         10 * time.Second,
	}
	addr := net.JoinHostPort(system.SSHHost, port)

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("ssh %s: %w", system.SSHHost, err)
	}

	type handshakeResult struct {
		client *ssh.Client
		err    error
	}
	done := make(chan handshakeResult, 1)
	go func() {
		sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
		if err != nil {
			done <- handshakeResult{err: err}
			return
		}
		done <- handshakeResult{client: ssh.NewClient(sshConn, chans, reqs)}
	}()

	select {
	case <-ctx.Done():
		_ = conn.Close()
		<-done // let the handshake goroutine unblock and exit before returning
		return nil, ctx.Err()
	case res := <-done:
		if res.err != nil {
			return nil, fmt.Errorf("ssh %s: %w", system.SSHHost, res.err)
		}
		return res.client, nil
	}
}

// remoteKeychainCommand runs script on system's SSH host over a one-shot
// session on a freshly authenticated connection — the RemoteExec
// counterpart to RemoteCommand's shelled-out ssh, for keychain-mode systems
// specifically. Canceling ctx closes the connection, which aborts whatever
// is running remotely instead of leaving the goroutine to wait it out.
func remoteKeychainCommand(ctx context.Context, system config.System, password, script, stdin string) ([]byte, error) {
	client, err := dialKeychainClient(ctx, system, password)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = client.Close()
		case <-stop:
		}
	}()

	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("ssh %s: open session: %w", system.SSHHost, err)
	}
	defer session.Close()
	if stdin != "" {
		session.Stdin = strings.NewReader(stdin)
	}

	output, err := session.CombinedOutput(script)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		text := strings.TrimSpace(string(output))
		if text == "" {
			text = err.Error()
		}
		return nil, errors.New(text)
	}
	return output, nil
}

// serveKeychainTunnel accepts local connections on listener and pipes each
// one to a freshly opened channel to remoteSocket over client, until
// listener is closed (which callers never do explicitly today — it lives
// for the process's lifetime, same as the socket a shelled-out `-fN`
// tunnel leaves behind).
func serveKeychainTunnel(listener net.Listener, client *ssh.Client, remoteSocket string) {
	defer client.Close()
	defer listener.Close()
	for {
		local, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer local.Close()
			remote, err := client.Dial("unix", remoteSocket)
			if err != nil {
				return
			}
			defer remote.Close()
			done := make(chan struct{}, 2)
			go func() { io.Copy(remote, local); done <- struct{}{} }() //nolint:errcheck
			go func() { io.Copy(local, remote); done <- struct{}{} }() //nolint:errcheck
			<-done
		}()
	}
}
