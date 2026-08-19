package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
)

// signingKeyEnvVar is where the Sign step (see steps.go) reads the release
// private key from — never committed, never in CI, set once by the
// maintainer after running -genkey.
const signingKeyEnvVar = "WHATTHEDOCK_SIGNING_KEY"

// genKey generates a fresh ed25519 keypair for signing release binaries
// and prints both halves with instructions — a one-time setup step, run
// directly (`go run ./cmd/release -genkey`), never part of the regular
// release flow itself. It doesn't touch git, GitHub, or any file on disk;
// what to do with the printed private key is left to the maintainer.
func genKey(w io.Writer) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate signing key: %w", err)
	}
	fmt.Fprintln(w, "Generated a new release signing keypair.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Public key — paste this into internal/update/verify.go's releasePublicKeyHex:")
	fmt.Fprintln(w, "  "+hex.EncodeToString(pub))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Private key — store it somewhere safe OUTSIDE this repo (password manager, a")
	fmt.Fprintln(w, "local env file that's never committed, etc.), then export it before running a")
	fmt.Fprintln(w, "real release:")
	fmt.Fprintln(w, "  export "+signingKeyEnvVar+"="+hex.EncodeToString(priv))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "This key never needs to touch GitHub, CI, or version control — only this")
	fmt.Fprintln(w, "machine, at release time. Anyone who gets it can sign a binary whatthedock's")
	fmt.Fprintln(w, "self-updater will trust, so treat it like any other private key.")
	return nil
}

// signBinary parses privKeyHex (as printed by genKey / stored in
// signingKeyEnvVar) and returns an ed25519 signature over data. Kept as a
// small pure function, independent of the release step machinery around
// it, so it's directly unit-testable (see steps_test.go).
func signBinary(privKeyHex string, data []byte) ([]byte, error) {
	key, err := hex.DecodeString(privKeyHex)
	if err != nil {
		return nil, fmt.Errorf("%s is not valid hex: %w", signingKeyEnvVar, err)
	}
	if len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%s is %d bytes, want %d", signingKeyEnvVar, len(key), ed25519.PrivateKeySize)
	}
	return ed25519.Sign(ed25519.PrivateKey(key), data), nil
}
