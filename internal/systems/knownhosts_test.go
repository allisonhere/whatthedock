package systems

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestLoadKnownHostsWithHybridKey(t *testing.T) {
	trusted, err := ssh.NewPublicKey(ed25519.PublicKey(make([]byte, ed25519.PublicKeySize)))
	if err != nil {
		t.Fatal(err)
	}
	other, err := ssh.NewPublicKey(ed25519.PublicKey(append([]byte{1}, make([]byte, ed25519.PublicKeySize-1)...)))
	if err != nil {
		t.Fatal(err)
	}

	hybridBlob := ssh.Marshal(struct {
		Type string
		Data []byte
	}{hybridHostKeyType, make([]byte, 1344)})
	hybridLine := "jarvis\t" + hybridHostKeyType + "\t" + base64.StdEncoding.EncodeToString(hybridBlob) + " saved by OpenSSH\n"
	trustedLine := knownhosts.Line([]string{knownhosts.HashHostname("jarvis")}, trusted) + "\n"
	revokedLine := "@revoked " + knownhosts.Line([]string{"jarvis"}, trusted) + "\n"
	remote := &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 22}

	for _, tc := range []struct {
		name    string
		content string
		want    error
		badFile bool
	}{
		{name: "mixed keys", content: hybridLine + trustedLine},
		{name: "hybrid key alone", content: hybridLine, want: &knownhosts.KeyError{}},
		{name: "revoked supported key", content: hybridLine + trustedLine + revokedLine, want: &knownhosts.RevokedError{}},
		{name: "marked hybrid key", content: hybridLine + "@revoked " + hybridLine + trustedLine, badFile: true},
		{name: "malformed entry", content: hybridLine + "jarvis ssh-ed25519 !\n" + trustedLine, badFile: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "known_hosts")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			callback, err := loadKnownHosts(path)
			if tc.badFile {
				if err == nil {
					t.Fatal("accepted an invalid known_hosts file")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			err = callback("jarvis:22", remote, trusted)
			switch tc.want.(type) {
			case *knownhosts.KeyError:
				if keyErr, ok := errors.AsType[*knownhosts.KeyError](err); !ok || len(keyErr.Want) != 0 {
					t.Fatalf("expected an unknown host, got %v", err)
				}
			case *knownhosts.RevokedError:
				if _, ok := errors.AsType[*knownhosts.RevokedError](err); !ok {
					t.Fatalf("expected a revoked key, got %v", err)
				}
			default:
				if err != nil {
					t.Fatalf("saved key rejected: %v", err)
				}
				err = callback("jarvis:22", remote, other)
				if keyErr, ok := errors.AsType[*knownhosts.KeyError](err); !ok || len(keyErr.Want) != 1 {
					t.Fatalf("expected a host key mismatch, got %v", err)
				}
			}
			if saved, err := os.ReadFile(path); err != nil || string(saved) != tc.content {
				t.Fatalf("original known_hosts changed or unreadable: %v", err)
			}
		})
	}
}
