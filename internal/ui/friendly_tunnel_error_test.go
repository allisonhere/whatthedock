package ui

import (
	"errors"
	"strings"
	"testing"
)

// A tunnel whose remote end refuses the Docker socket (the remote user isn't
// in the docker group, say) surfaces from the Docker client as a bare
// "connection reset by peer" on the local tunnel socket. That tells the user
// nothing, so it gets an explanation.
func TestFriendlyDockerErrorExplainsRefusedTunnel(t *testing.T) {
	err := errors.New(`error during connect: Get "http://%2Ftmp%2Fwhatthedock-remote-3.sock/v1.51/containers/json?all=1": read unix @->/tmp/whatthedock-remote-3.sock: read: connection reset by peer`)
	got := friendlyDockerError(err)
	if !strings.Contains(got, "remote Docker socket refused") || !strings.Contains(got, "docker group") {
		t.Fatalf("friendlyDockerError = %q, want an explanation naming the remote socket and the docker group", got)
	}

	// An unrelated reset, with no tunnel socket in it, is left as it was.
	plain := errors.New("read tcp 10.0.0.1:1234->10.0.0.2:2375: connection reset by peer")
	if got := friendlyDockerError(plain); got != plain.Error() {
		t.Fatalf("friendlyDockerError(unrelated reset) = %q, want it untouched", got)
	}
}
