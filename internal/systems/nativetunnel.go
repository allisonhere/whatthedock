package systems

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
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

	hostKeyCallback, err := knownhosts.New(knownHostsPath())
	if err != nil {
		return fmt.Errorf("read known_hosts: %w — connect once with ssh first to add %s", err, system.SSHHost)
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
	client, err := ssh.Dial("tcp", net.JoinHostPort(system.SSHHost, port), cfg)
	if err != nil {
		return fmt.Errorf("ssh %s: %w", system.SSHHost, err)
	}

	listener, err := net.Listen("unix", system.LocalSocket)
	if err != nil {
		client.Close()
		return fmt.Errorf("listen on %s: %w", system.LocalSocket, err)
	}

	go serveKeychainTunnel(listener, client, system.RemoteSocket)
	return nil
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
