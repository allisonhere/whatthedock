package docker

import "testing"

// TestNewProviderKeepsEnvWhenHostIsExplicit guards against dropping
// client.FromEnv when a system has an explicit dockerHost configured. The
// explicit host must still win over DOCKER_HOST, but the other environment
// settings (API version, and the TLS/CA/cert vars read by the same
// FromEnv) must not be discarded along with it.
func TestNewProviderKeepsEnvWhenHostIsExplicit(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CERT_PATH", "")
	t.Setenv("DOCKER_TLS_VERIFY", "")
	t.Setenv("DOCKER_API_VERSION", "1.44")

	provider, err := NewProvider("srv", "srv", "tcp://explicit.example:2375")
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	if got := provider.cli.ClientVersion(); got != "1.44" {
		t.Fatalf("ClientVersion() = %q, want 1.44 — FromEnv was dropped when an explicit host was set", got)
	}
	if got := provider.cli.DaemonHost(); got != "tcp://explicit.example:2375" {
		t.Fatalf("DaemonHost() = %q, want the explicit host to win", got)
	}
}
