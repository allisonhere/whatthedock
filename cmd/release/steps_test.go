package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in                  string
		major, minor, patch int
		ok                  bool
	}{
		{"v1.2.3", 1, 2, 3, true},
		{"1.2.3", 1, 2, 3, true},
		{"v0.1.0", 0, 1, 0, true},
		{"", 0, 0, 0, false},
		{"v1.2", 0, 0, 0, false},
		{"v1.2.3.4", 0, 0, 0, false},
		{"not-a-version", 0, 0, 0, false},
	}
	for _, tt := range tests {
		major, minor, patch, ok := parseVersion(tt.in)
		if major != tt.major || minor != tt.minor || patch != tt.patch || ok != tt.ok {
			t.Errorf("parseVersion(%q) = (%d,%d,%d,%v), want (%d,%d,%d,%v)", tt.in, major, minor, patch, ok, tt.major, tt.minor, tt.patch, tt.ok)
		}
	}
}

func TestSuggestNextVersion(t *testing.T) {
	tests := []struct{ latest, want string }{
		{"v0.1.0", "v0.1.1"},
		{"v1.9.9", "v1.9.10"},
		{"", "v0.1.0"},
		{"not-a-tag", "v0.1.0"},
	}
	for _, tt := range tests {
		if got := suggestNextVersion(tt.latest); got != tt.want {
			t.Errorf("suggestNextVersion(%q) = %q, want %q", tt.latest, got, tt.want)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int // sign only
	}{
		{"v1.0.0", "v0.9.9", 1},
		{"v0.9.9", "v1.0.0", -1},
		{"v1.2.3", "v1.2.3", 0},
		{"v1.10.0", "v1.9.0", 1}, // numeric, not lexicographic
	}
	for _, tt := range tests {
		got := compareVersions(tt.a, tt.b)
		sign := 0
		if got > 0 {
			sign = 1
		} else if got < 0 {
			sign = -1
		}
		if sign != tt.want {
			t.Errorf("compareVersions(%q, %q) sign = %d, want %d", tt.a, tt.b, sign, tt.want)
		}
	}
}

func TestReleaseBinaryName(t *testing.T) {
	name := releaseBinaryName("v1.2.3")
	if name == "" {
		t.Fatal("releaseBinaryName returned empty string")
	}
	const prefix = "whatthedock-v1.2.3-"
	if len(name) <= len(prefix) || name[:len(prefix)] != prefix {
		t.Fatalf("releaseBinaryName(%q) = %q, want prefix %q", "v1.2.3", name, prefix)
	}
}

func TestSignBinaryRoundTripsThroughVerify(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("a whatthedock release binary")

	sig, err := signBinary(hex.EncodeToString(priv), data)
	if err != nil {
		t.Fatalf("signBinary() error = %v", err)
	}
	if !ed25519.Verify(pub, data, sig) {
		t.Fatal("signBinary() produced a signature that doesn't verify against the matching public key")
	}
}

func TestSignBinaryRejectsMalformedHexKey(t *testing.T) {
	if _, err := signBinary("not-hex", []byte("data")); err == nil {
		t.Fatal("signBinary() error = nil, want an error for non-hex input")
	}
}

func TestSignBinaryRejectsWrongLengthKey(t *testing.T) {
	if _, err := signBinary(hex.EncodeToString([]byte("too short")), []byte("data")); err == nil {
		t.Fatal("signBinary() error = nil, want an error for a wrong-length key")
	}
}

func TestGenKeyPrintsMatchingKeypair(t *testing.T) {
	var buf bytes.Buffer
	if err := genKey(&buf); err != nil {
		t.Fatalf("genKey() error = %v", err)
	}
	out := buf.String()

	var pubHex, privHex string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case len(line) == hex.EncodedLen(ed25519.PublicKeySize) && pubHex == "":
			pubHex = line
		case strings.HasPrefix(line, "export "+signingKeyEnvVar+"="):
			privHex = strings.TrimPrefix(line, "export "+signingKeyEnvVar+"=")
		}
	}
	if pubHex == "" || privHex == "" {
		t.Fatalf("genKey() output missing public/private key lines:\n%s", out)
	}

	pub, err := hex.DecodeString(pubHex)
	if err != nil {
		t.Fatalf("printed public key isn't valid hex: %v", err)
	}
	priv, err := hex.DecodeString(privHex)
	if err != nil {
		t.Fatalf("printed private key isn't valid hex: %v", err)
	}
	sig := ed25519.Sign(ed25519.PrivateKey(priv), []byte("check"))
	if !ed25519.Verify(ed25519.PublicKey(pub), []byte("check"), sig) {
		t.Fatal("genKey() printed a public/private key pair that don't match each other")
	}
}
