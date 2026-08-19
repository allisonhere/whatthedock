package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func TestVerifyBinaryAcceptsValidSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("a whatthedock release binary")
	sig := ed25519.Sign(priv, data)

	if err := verifyBinary(pub, data, sig); err != nil {
		t.Fatalf("verifyBinary() error = %v, want nil", err)
	}
}

func TestVerifyBinaryRejectsTamperedData(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, []byte("original bytes"))

	if err := verifyBinary(pub, []byte("tampered bytes!"), sig); err == nil {
		t.Fatal("verifyBinary() error = nil, want a verification failure for tampered data")
	}
}

func TestVerifyBinaryRejectsWrongKey(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrongPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("a whatthedock release binary")
	sig := ed25519.Sign(priv, data)

	if err := verifyBinary(wrongPub, data, sig); err == nil {
		t.Fatal("verifyBinary() error = nil, want a verification failure for the wrong public key")
	}
}

func TestVerifyBinaryRejectsMalformedSignatureWithoutPanicking(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	// A garbage/truncated .sig asset is untrusted input just like the
	// binary — ed25519.Verify panics on a wrong-length signature, so this
	// must be caught before ever reaching it.
	if err := verifyBinary(pub, []byte("data"), []byte("too short")); err == nil {
		t.Fatal("verifyBinary() error = nil, want an error for a malformed signature")
	}
}

func TestVerifyBinaryRejectsMalformedKeyWithoutPanicking(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("data")
	sig := ed25519.Sign(priv, data)

	if err := verifyBinary(ed25519.PublicKey([]byte("too short")), data, sig); err == nil {
		t.Fatal("verifyBinary() error = nil, want an error for a malformed public key")
	}
}

func TestReleasePublicKeyHexParsesToValidKeySize(t *testing.T) {
	if len(verificationPublicKey) != ed25519.PublicKeySize {
		t.Fatalf("verificationPublicKey length = %d, want %d", len(verificationPublicKey), ed25519.PublicKeySize)
	}
}
