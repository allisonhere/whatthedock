package systems

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/allisonhere/whatthedock/internal/config"
)

// A server that accepts the TCP connection and then never speaks must not
// hang dialKeychainClient when the caller's ctx has no deadline (the startup
// path uses context.Background()).
func TestDialKeychainClientBoundsSilentHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()

	hosts := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(hosts, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	origHosts, origTimeout := knownHostsPath, sshHandshakeTimeout
	knownHostsPath = func() string { return hosts }
	sshHandshakeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { knownHostsPath, sshHandshakeTimeout = origHosts, origTimeout })

	host, port, _ := net.SplitHostPort(ln.Addr().String())
	done := make(chan error, 1)
	go func() {
		_, err := dialKeychainClient(context.Background(), config.System{SSHHost: host, SSHPort: port, SSHUser: "u"}, "pw")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("dialKeychainClient succeeded against a silent server")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dialKeychainClient hung on a silent server despite the handshake timeout")
	}
}
