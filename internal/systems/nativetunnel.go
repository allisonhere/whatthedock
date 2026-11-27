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
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

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

	tunnel := &keychainTunnel{
		client:       client,
		remoteSocket: system.RemoteSocket,
		// Re-read the password from the keychain on reconnect rather than
		// holding it in memory for the life of the process.
		redial: func(ctx context.Context) (*ssh.Client, error) {
			password, err := PasswordFor(system.ID)
			if err != nil {
				return nil, err
			}
			return dialKeychainClient(ctx, system, password)
		},
	}
	go serveKeychainTunnel(listener, tunnel)
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
	hostKeyCallback, err := loadKnownHosts(knownHostsPath())
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
		Timeout:         sshHandshakeTimeout,
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
		// cfg.Timeout only bounds ssh.Dial; NewClientConn on an already
		// dialed conn has no timeout of its own, so a server that accepts
		// the TCP connection and then goes silent would hang here forever
		// whenever ctx carries no deadline. Bound the handshake explicitly,
		// then lift the deadline so it doesn't cut the live session short.
		_ = conn.SetDeadline(time.Now().Add(sshHandshakeTimeout))
		sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
		if err != nil {
			done <- handshakeResult{err: err}
			return
		}
		_ = conn.SetDeadline(time.Time{})
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

// sshHandshakeTimeout bounds one SSH handshake (a var so tests can shorten
// it). tunnelProbeTimeout bounds opening a channel (or a liveness probe) on
// an established connection, which otherwise blocks indefinitely on a
// half-open TCP connection after sleep/wake or network loss;
// tunnelRedialTimeout bounds a whole reconnect.
var sshHandshakeTimeout = 10 * time.Second

const (
	tunnelProbeTimeout  = 10 * time.Second
	tunnelRedialTimeout = 30 * time.Second
)

// keychainTunnel owns the SSH connection behind a keychain-mode Docker
// socket tunnel and re-establishes it when it dies. Without that, a dropped
// connection left the local listener up — so isLiveSocket kept reporting a
// healthy tunnel and nothing ever reconnected until the app restarted.
type keychainTunnel struct {
	mu           sync.Mutex
	client       *ssh.Client
	remoteSocket string
	redial       func(context.Context) (*ssh.Client, error)
}

// open dials the remote Docker socket, reconnecting SSH first if the
// existing connection is dead. A failure on a still-healthy connection (the
// remote daemon is down, say) is returned as-is: reconnecting wouldn't help
// and would just burn password-auth attempts.
func (t *keychainTunnel) open() (net.Conn, error) {
	t.mu.Lock()
	client := t.client
	t.mu.Unlock()
	if client != nil {
		conn, err := dialRemote(client, t.remoteSocket)
		if err == nil {
			return conn, nil
		}
		if connAlive(client) {
			return nil, err
		}
		t.discard(client)
	}
	client, err := t.reconnect()
	if err != nil {
		return nil, err
	}
	return dialRemote(client, t.remoteSocket)
}

func (t *keychainTunnel) discard(dead *ssh.Client) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.client == dead {
		t.client = nil
	}
	_ = dead.Close()
}

// reconnect returns the current client, dialing a new one only if none
// exists — concurrent callers serialize here, so one reconnect serves all.
func (t *keychainTunnel) reconnect() (*ssh.Client, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.client != nil {
		return t.client, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), tunnelRedialTimeout)
	defer cancel()
	client, err := t.redial(ctx)
	if err != nil {
		return nil, err
	}
	t.client = client
	return client, nil
}

func (t *keychainTunnel) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.client != nil {
		_ = t.client.Close()
		t.client = nil
	}
}

// dialRemote opens a channel to socket over client, giving up (and closing
// client, which fails the pending open) if the transport is stalled.
func dialRemote(client *ssh.Client, socket string) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		conn, err := client.Dial("unix", socket)
		ch <- result{conn, err}
	}()
	timer := time.NewTimer(tunnelProbeTimeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.conn, r.err
	case <-timer.C:
		_ = client.Close()
		if r := <-ch; r.conn != nil {
			_ = r.conn.Close()
		}
		return nil, errors.New("ssh tunnel stalled")
	}
}

// connAlive reports whether client's transport still answers. The request's
// own reply (OpenSSH accepts keepalive@openssh.com; others may refuse it)
// doesn't matter — only whether the round trip completes without an error.
func connAlive(client *ssh.Client) bool {
	ch := make(chan error, 1)
	go func() {
		_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
		ch <- err
	}()
	timer := time.NewTimer(tunnelProbeTimeout)
	defer timer.Stop()
	select {
	case err := <-ch:
		return err == nil
	case <-timer.C:
		_ = client.Close()
		return false
	}
}

// serveKeychainTunnel accepts local connections on listener and pipes each
// one to a freshly opened channel to the remote socket, until listener is
// closed (which callers never do explicitly today — it lives for the
// process's lifetime, same as the socket a shelled-out `-fN` tunnel leaves
// behind).
func serveKeychainTunnel(listener net.Listener, tunnel *keychainTunnel) {
	defer tunnel.close()
	defer listener.Close()
	for {
		local, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer local.Close()
			remote, err := tunnel.open()
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
