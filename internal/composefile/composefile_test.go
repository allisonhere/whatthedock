package composefile

import (
	"strings"
	"testing"
)

const (
	full = `services:
  dash:
    build: .
    image: dash:latest
    container_name: dash
    restart: unless-stopped
    network_mode: host
    ports:
      - "3939:3939"
`
	reduced = `services:
  dash:
    image: dash:latest
    restart: always
`
)

func TestServiceKeys(t *testing.T) {
	keys, err := ServiceKeys([]byte(full), "dash")
	if err != nil {
		t.Fatalf("ServiceKeys() error = %v", err)
	}
	for _, want := range []string{"build", "container_name", "network_mode", "image", "restart", "ports"} {
		if !keys[want] {
			t.Fatalf("ServiceKeys() missing %q: %#v", want, keys)
		}
	}

	// A document that doesn't define the service yields no keys, no error.
	missing, err := ServiceKeys([]byte(full), "cache")
	if err != nil {
		t.Fatalf("ServiceKeys(missing) error = %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("ServiceKeys(missing) = %#v, want empty", missing)
	}
}

// TestLostUnmanagedKeysIgnoresManagedFields guards the check against false
// positives: a user clearing a ports/environment field in the form is an
// intentional, managed edit and must not be reported, while unmanaged keys
// (build/container_name/network_mode) going missing is exactly what should be.
func TestLostUnmanagedKeysIgnoresManagedFields(t *testing.T) {
	// reduced dropped build/container_name/network_mode AND intentionally
	// changed image/restart/ports.
	lost, err := LostUnmanagedKeys([]byte(reduced), []byte(full), "dash")
	if err != nil {
		t.Fatalf("LostUnmanagedKeys() error = %v", err)
	}
	if strings.Join(lost, ",") != "build,container_name,network_mode" {
		t.Fatalf("LostUnmanagedKeys() = %#v, want the unmanaged keys only", lost)
	}

	if got, _ := LostUnmanagedKeys([]byte(full), []byte(full), "dash"); len(got) != 0 {
		t.Fatalf("LostUnmanagedKeys(same, same) = %#v, want none", got)
	}
}

func TestLostUnmanagedKeysNoServiceIsNoLoss(t *testing.T) {
	lost, err := LostUnmanagedKeys([]byte("services: {}\n"), []byte(full), "dash")
	if err != nil {
		t.Fatalf("LostUnmanagedKeys() error = %v", err)
	}
	// The current file dropped the whole service — that's a delete, not a
	// silent field loss, so nothing is reported as a lost unmanaged key.
	if len(lost) != 0 {
		t.Fatalf("LostUnmanagedKeys() = %#v, want none when the service is simply absent", lost)
	}
}
