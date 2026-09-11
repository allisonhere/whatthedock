package systems

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/allisonhere/whatthedock/internal/config"
)

// fakeSSHHandler answers one "exec" request on a session channel. It may
// read stdin from ch before replying — used to check that
// remoteKeychainCommand actually pipes stdin through, the same way
// RemoteCommand's shelled-out ssh does via cmd.Stdin.
type fakeSSHHandler func(cmd string, ch ssh.Channel) (output []byte, fail bool)

// startFakeSSHServer starts an in-process SSH server on 127.0.0.1 that
// accepts only password auth matching wantPassword and answers every "exec"
// request with handler. It's the test double for a real sshd across every
// keychain-mode (native Go SSH client) test in this file — no real network
// or subprocess involved, so context-cancellation and auth-failure behavior
// can be asserted deterministically. Returns the listener's host:port.
func startFakeSSHServer(t *testing.T, wantPassword string, handler fakeSSHHandler) string {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey() err = %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("ssh.NewSignerFromKey() err = %v", err)
	}

	cfg := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if string(password) != wantPassword {
				return nil, errors.New("wrong password")
			}
			return nil, nil
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() err = %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	addr := ln.Addr().String()
	line := knownhosts.Line([]string{addr}, signer.PublicKey())
	hostsPath := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(hostsPath, []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}
	original := knownHostsPath
	knownHostsPath = func() string { return hostsPath }
	t.Cleanup(func() { knownHostsPath = original })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveFakeSSHConn(conn, cfg, handler)
		}
	}()

	return addr
}

func serveFakeSSHConn(conn net.Conn, cfg *ssh.ServerConfig, handler fakeSSHHandler) {
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "unsupported channel type")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer channel.Close()
			for req := range requests {
				if req.Type != "exec" {
					if req.WantReply {
						_ = req.Reply(false, nil)
					}
					continue
				}
				var payload struct{ Command string }
				_ = ssh.Unmarshal(req.Payload, &payload)
				if req.WantReply {
					_ = req.Reply(true, nil)
				}

				output, fail := handler(payload.Command, channel)
				_, _ = channel.Write(output)
				status := uint32(0)
				if fail {
					status = 1
				}
				_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
				return
			}
		}()
	}
}

func keychainTestSystem(addr string) config.System {
	host, port, _ := net.SplitHostPort(addr)
	return config.System{
		ID:      "jarvis",
		Name:    "jarvis",
		SSHHost: host,
		SSHUser: "allie",
		SSHPort: port,
		SSHAuth: "keychain",
	}
}

func TestRemoteKeychainCommandAuthenticatesAndReturnsOutput(t *testing.T) {
	addr := startFakeSSHServer(t, "hunter2", func(cmd string, _ ssh.Channel) ([]byte, bool) {
		return []byte("ran: " + cmd), false
	})

	output, err := remoteKeychainCommand(context.Background(), keychainTestSystem(addr), "hunter2", "cat /etc/hostname", "")
	if err != nil {
		t.Fatalf("remoteKeychainCommand() err = %v", err)
	}
	if string(output) != "ran: cat /etc/hostname" {
		t.Fatalf("remoteKeychainCommand() output = %q, want %q", output, "ran: cat /etc/hostname")
	}
}

func TestRemoteKeychainCommandWrongPasswordFailsFastWithoutHanging(t *testing.T) {
	addr := startFakeSSHServer(t, "hunter2", func(cmd string, _ ssh.Channel) ([]byte, bool) {
		return []byte("should not run"), false
	})

	start := time.Now()
	_, err := remoteKeychainCommand(context.Background(), keychainTestSystem(addr), "wrong-password", "cat /etc/hostname", "")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("remoteKeychainCommand() err = nil, want an auth error for the wrong password")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("remoteKeychainCommand() took %s to fail on a bad password, want a fast failure", elapsed)
	}
}

func TestRemoteKeychainCommandPipesStdin(t *testing.T) {
	addr := startFakeSSHServer(t, "hunter2", func(cmd string, ch ssh.Channel) ([]byte, bool) {
		stdin, _ := io.ReadAll(ch)
		return append([]byte("stdin was: "), stdin...), false
	})

	output, err := remoteKeychainCommand(context.Background(), keychainTestSystem(addr), "hunter2", "cat > /tmp/x", "hello world")
	if err != nil {
		t.Fatalf("remoteKeychainCommand() err = %v", err)
	}
	if string(output) != "stdin was: hello world" {
		t.Fatalf("remoteKeychainCommand() output = %q, want stdin to have been piped through", output)
	}
}

// TestRemoteKeychainCommandContextCancellationAbortsPromptly is the
// regression test for "poor context cancellation": before
// dialKeychainClient existed, canceling ctx while a remote command was
// still running had no effect on the underlying connection — the caller's
// goroutine (and the SSH round trip) would only return once the remote side
// finished on its own. The remote command here deliberately runs far longer
// than the cancellation delay; a fix makes canceling ctx close the
// connection and return quickly instead of waiting it out.
func TestRemoteKeychainCommandContextCancellationAbortsPromptly(t *testing.T) {
	addr := startFakeSSHServer(t, "hunter2", func(cmd string, _ ssh.Channel) ([]byte, bool) {
		time.Sleep(5 * time.Second)
		return []byte("too slow"), false
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := remoteKeychainCommand(ctx, keychainTestSystem(addr), "hunter2", "sleep 5", "")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("remoteKeychainCommand() err = nil, want an error once the context is canceled")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("remoteKeychainCommand() took %s after cancellation, want it to abort promptly instead of waiting out the remote command", elapsed)
	}
}

func TestRemoteExecKeychainModeUsesNativeClientNotSubprocess(t *testing.T) {
	stubSecrets(t)
	addr := startFakeSSHServer(t, "hunter2", func(cmd string, _ ssh.Channel) ([]byte, bool) {
		return []byte("native: " + cmd), false
	})
	if err := StorePassword("jarvis", "hunter2"); err != nil {
		t.Fatalf("StorePassword() err = %v", err)
	}

	output, err := RemoteExec(context.Background(), keychainTestSystem(addr), "docker compose ps", "")
	if err != nil {
		t.Fatalf("RemoteExec() err = %v", err)
	}
	if string(output) != "native: docker compose ps" {
		t.Fatalf("RemoteExec() output = %q, want the fake server's native-client response", output)
	}
}

func TestRemoteExecNonKeychainModeDoesNotConsultKeychain(t *testing.T) {
	fake := stubSecrets(t)
	// A "config" (key/agent) auth system must never touch the keychain —
	// only SSHAuth=="keychain" does. Nothing stored, and RemoteExec must
	// still take the shelled-out RemoteCommand path (which fails here
	// because there's no real "unreachable-host" ssh binary behavior to
	// simulate, but the point is it must not call PasswordFor).
	sys := config.System{SSHHost: "127.0.0.1", SSHUser: "nobody", SSHPort: "1"}
	_, _ = RemoteExec(context.Background(), sys, "true", "")
	if len(fake.values) != 0 {
		t.Fatalf("RemoteExec() touched the keychain for a non-keychain system: %#v", fake.values)
	}
}
