package systems

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/allisonhere/whatthedock/internal/config"
)

// startMultiKeySSHServer serves every given host key (the client's own
// preference order decides which one is negotiated) and points knownHostsPath
// at a file holding only knownSigner's key — a host that was added to
// known_hosts by a client that happened to negotiate that key type.
func startMultiKeySSHServer(t *testing.T, password string, knownSigner ssh.Signer, hostKeys ...ssh.Signer) (host, port string) {
	t.Helper()
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, got []byte) (*ssh.Permissions, error) {
			if string(got) != password {
				return nil, errors.New("wrong password")
			}
			return nil, nil
		},
	}
	for _, key := range hostKeys {
		cfg.AddHostKey(key)
	}

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
			go serveFakeSSHConn(conn, cfg, func(string, ssh.Channel) ([]byte, bool) { return []byte("ok"), false })
		}
	}()

	addr := ln.Addr().String()
	hostsPath := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(hostsPath, []byte(knownhosts.Line([]string{addr}, knownSigner.PublicKey())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	original := knownHostsPath
	knownHostsPath = func() string { return hostsPath }
	t.Cleanup(func() { knownHostsPath = original })

	host, port, _ = net.SplitHostPort(addr)
	return host, port
}

func newSigners(t *testing.T) (ed, ec ssh.Signer) {
	t.Helper()
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ed, err = ssh.NewSignerFromKey(edKey)
	if err != nil {
		t.Fatal(err)
	}
	ec, err = ssh.NewSignerFromKey(ecKey)
	if err != nil {
		t.Fatal(err)
	}
	return ed, ec
}

// A host recorded under its ed25519 key must still connect when the server
// also has an ECDSA key that Go would otherwise negotiate first. This was a
// "knownhosts: key mismatch" even though the host was trusted.
func TestDialKeychainClientUsesTheKeyTypeKnownHostsHas(t *testing.T) {
	ed, ec := newSigners(t)
	host, port := startMultiKeySSHServer(t, "pw", ed, ec, ed)

	// Confirm the scenario: a plain attempt really does mismatch.
	_, err := dialKeychainClientWith(context.Background(), config.System{SSHHost: host, SSHPort: port, SSHUser: "u"}, "pw", nil)
	var keyErr *knownhosts.KeyError
	if !errors.As(err, &keyErr) || len(keyErr.Want) == 0 {
		t.Skipf("this x/crypto already negotiates the known key type (err = %v); nothing to retry", err)
	}

	client, err := dialKeychainClient(context.Background(), config.System{SSHHost: host, SSHPort: port, SSHUser: "u"}, "pw")
	if err != nil {
		t.Fatalf("dialKeychainClient() err = %v, want the retry restricted to ed25519 to connect", err)
	}
	_ = client.Close()
}

// A genuinely changed key — same type, different key — must still be refused
// after the retry; the algorithm restriction is not a way around the check.
func TestDialKeychainClientStillRejectsAChangedKey(t *testing.T) {
	ed, ec := newSigners(t)
	otherEd, _ := newSigners(t)
	// known_hosts holds a different ed25519 key than the server presents.
	host, port := startMultiKeySSHServer(t, "pw", otherEd, ec, ed)

	_, err := dialKeychainClient(context.Background(), config.System{SSHHost: host, SSHPort: port, SSHUser: "u"}, "pw")
	var keyErr *knownhosts.KeyError
	if !errors.As(err, &keyErr) {
		t.Fatalf("dialKeychainClient() err = %v, want a host key mismatch", err)
	}
}

func TestHostKeyAlgorithmsForMapsKeyTypes(t *testing.T) {
	ed, ec := newSigners(t)
	got := hostKeyAlgorithmsFor([]knownhosts.KnownKey{{Key: ed.PublicKey()}, {Key: ec.PublicKey()}, {Key: ed.PublicKey()}})
	want := []string{ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("hostKeyAlgorithmsFor = %v, want %v (deduplicated, in order)", got, want)
	}
}
