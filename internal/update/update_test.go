package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIsNewer(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{"v0.1.3", "v0.1.4", true},
		{"v0.1.4", "v0.1.4", false},
		{"v0.1.4", "v0.1.3", false},
		{"v0.9.9", "v1.0.0", true},
		{"v1.2.0", "v1.10.0", true},
		{"dev", "v0.1.4", false},
		{"v0.1.3", "not-a-version", false},
		{"", "v0.1.4", false},
	}
	for _, tt := range tests {
		if got := IsNewer(tt.current, tt.latest); got != tt.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestAssetName(t *testing.T) {
	want := "whatthedock-v0.1.4-" + runtime.GOOS + "-" + runtime.GOARCH
	if got := AssetName("v0.1.4"); got != want {
		t.Errorf("AssetName(v0.1.4) = %q, want %q", got, want)
	}
}

func TestDownloadURL(t *testing.T) {
	got := DownloadURL("allisonhere/whatthedock", "v0.1.4")
	want := "https://github.com/allisonhere/whatthedock/releases/download/v0.1.4/" + AssetName("v0.1.4")
	if got != want {
		t.Errorf("DownloadURL() = %q, want %q", got, want)
	}
}

func TestLatestReleaseParsesTagName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/allisonhere/whatthedock/releases/latest" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v0.1.4","name":"v0.1.4"}`))
	}))
	defer server.Close()

	// The real GitHub API host is hardcoded in LatestRelease; point requests
	// at the test server instead by overriding the client's transport to
	// rewrite the host, since LatestRelease doesn't take a base URL.
	original := httpClient
	httpClient = &http.Client{Transport: rewriteHostTransport{target: server.URL, base: http.DefaultTransport}}
	defer func() { httpClient = original }()

	tag, err := LatestRelease(context.Background(), "allisonhere/whatthedock")
	if err != nil {
		t.Fatalf("LatestRelease() error = %v", err)
	}
	if tag != "v0.1.4" {
		t.Fatalf("LatestRelease() = %q, want v0.1.4", tag)
	}
}

func TestLatestReleaseErrorsOnNon200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	original := httpClient
	httpClient = &http.Client{Transport: rewriteHostTransport{target: server.URL, base: http.DefaultTransport}}
	defer func() { httpClient = original }()

	if _, err := LatestRelease(context.Background(), "allisonhere/whatthedock"); err == nil {
		t.Fatal("LatestRelease() error = nil, want an error for a 404 response")
	}
}

func TestReplaceRunningExecutableInstallsDownloadedAsset(t *testing.T) {
	newContent := []byte("#!/bin/sh\necho new-version\n")
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, newContent)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sig") {
			_, _ = w.Write(sig)
			return
		}
		_, _ = w.Write(newContent)
	}))
	defer server.Close()

	dir := t.TempDir()
	exePath := filepath.Join(dir, "whatthedock")
	if err := os.WriteFile(exePath, []byte("old-version"), 0o755); err != nil {
		t.Fatal(err)
	}

	original := httpClient
	httpClient = &http.Client{Transport: rewriteHostTransport{target: server.URL, base: http.DefaultTransport}}
	defer func() { httpClient = original }()

	defer stubVerificationKey(t, pub)()
	restoreExecutable := stubExecutable(t, exePath)
	defer restoreExecutable()

	got, err := ReplaceRunningExecutable(context.Background(), "allisonhere/whatthedock", "v0.1.4")
	if err != nil {
		t.Fatalf("ReplaceRunningExecutable() error = %v", err)
	}
	if got != exePath {
		t.Fatalf("ReplaceRunningExecutable() path = %q, want %q", got, exePath)
	}
	data, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(newContent) {
		t.Fatalf("executable content = %q, want %q", data, newContent)
	}
	info, err := os.Stat(exePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("replaced executable is not executable: mode=%v", info.Mode())
	}
}

// TestReplaceRunningExecutableRejectsInvalidSignature is the safety-
// property test for this whole feature: a binary whose signature doesn't
// verify must never make it into place over the running executable, no
// matter how far the download got. It also checks the original file is
// left byte-for-byte untouched — verification has to happen strictly
// before the rename, not as an afterthought.
func TestReplaceRunningExecutableRejectsInvalidSignature(t *testing.T) {
	newContent := []byte("#!/bin/sh\necho new-version\n")
	pub, _, err := ed25519.GenerateKey(rand.Reader) // unrelated key: sig below won't verify against it
	if err != nil {
		t.Fatal(err)
	}
	_, wrongPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	badSig := ed25519.Sign(wrongPriv, newContent)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sig") {
			_, _ = w.Write(badSig)
			return
		}
		_, _ = w.Write(newContent)
	}))
	defer server.Close()

	dir := t.TempDir()
	exePath := filepath.Join(dir, "whatthedock")
	original := []byte("old-version")
	if err := os.WriteFile(exePath, original, 0o755); err != nil {
		t.Fatal(err)
	}

	originalClient := httpClient
	httpClient = &http.Client{Transport: rewriteHostTransport{target: server.URL, base: http.DefaultTransport}}
	defer func() { httpClient = originalClient }()

	defer stubVerificationKey(t, pub)()
	restoreExecutable := stubExecutable(t, exePath)
	defer restoreExecutable()

	if _, err := ReplaceRunningExecutable(context.Background(), "allisonhere/whatthedock", "v0.1.4"); err == nil {
		t.Fatal("ReplaceRunningExecutable() error = nil, want a signature verification error")
	}

	data, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(original) {
		t.Fatalf("executable content = %q, want untouched %q", data, original)
	}
}

// rewriteHostTransport redirects every request to target's host, keeping
// the original path — lets tests point the package's hardcoded GitHub URLs
// at an httptest server without threading a base-URL parameter through the
// public API.
type rewriteHostTransport struct {
	target string
	base   http.RoundTripper
}

func (rt rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	targetURL, err := req.URL.Parse(rt.target)
	if err != nil {
		return nil, err
	}
	req = req.Clone(req.Context())
	req.URL.Scheme = targetURL.Scheme
	req.URL.Host = targetURL.Host
	req.Host = targetURL.Host
	return rt.base.RoundTrip(req)
}

// stubExecutable swaps executableOverride (update.go's seam over
// os.Executable) so ReplaceRunningExecutable operates on a throwaway file
// instead of the real go test binary's own path.
func stubExecutable(t *testing.T, path string) func() {
	t.Helper()
	original := executableOverride
	executableOverride = func() (string, error) { return path, nil }
	return func() { executableOverride = original }
}

// stubVerificationKey swaps verificationPublicKey (verify.go's seam over
// the real embedded releasePublicKeyHex) so tests can sign fixtures with
// their own throwaway keypair instead of needing the real private key,
// which by design exists nowhere in this repo.
func stubVerificationKey(t *testing.T, pub ed25519.PublicKey) func() {
	t.Helper()
	original := verificationPublicKey
	verificationPublicKey = pub
	return func() { verificationPublicKey = original }
}
