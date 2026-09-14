package update

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// releasePublicKeyHex is the hex-encoded ed25519 public key every release
// binary is signed against (see cmd/release's Sign step). The matching
// private key lives only on the maintainer's own machine — supplied to
// cmd/release through WHATTHEDOCK_SIGNING_KEY or the gitignored
// .whatthedock-signing-key — and never touches GitHub, CI, or this repo.
// That split is the whole point: this constant ships baked into every
// already-installed binary, so it's the *old*, already-trusted binary that
// verifies the new one, not anything fetched alongside the download it's
// checking.
//
// This must stay in lockstep with the private key actually used to sign. The
// release tool calls SigningKeyMatches as a preflight (see cmd/release), so a
// drifted pair fails the build instead of publishing a release every
// installed app would refuse to install. If the key is rotated, update this
// constant and the local private key together.
const releasePublicKeyHex = "1df7ed51dea3d77c9c9cdda6b69806eac7438ca2b78f4c7e0ed6e79fcdb87447"

// verificationPublicKey is releasePublicKeyHex parsed once at init. A var,
// not a plain call site, so tests can swap it for a throwaway test key —
// the same seam pattern executableOverride uses in update.go.
var verificationPublicKey = mustParsePublicKey(releasePublicKeyHex)

// SigningKeyMatches reports whether privKeyHex (the hex ed25519 private key
// cmd/release signs with) corresponds to the public key this package bakes
// in. The release tool calls it as a preflight so a drifted or rotated key is
// caught before a build+sign, instead of producing a binary every installed
// app would reject. A malformed key is an error, not merely false.
func SigningKeyMatches(privKeyHex string) (bool, error) {
	key, err := hex.DecodeString(strings.TrimSpace(privKeyHex))
	if err != nil {
		return false, fmt.Errorf("signing key is not valid hex: %w", err)
	}
	if len(key) != ed25519.PrivateKeySize {
		return false, fmt.Errorf("signing key is %d bytes, want %d", len(key), ed25519.PrivateKeySize)
	}
	pub := ed25519.PrivateKey(key).Public().(ed25519.PublicKey)
	return bytes.Equal(pub, verificationPublicKey), nil
}

func mustParsePublicKey(hexKey string) ed25519.PublicKey {
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		panic("update: invalid releasePublicKeyHex: " + err.Error())
	}
	if len(key) != ed25519.PublicKeySize {
		panic(fmt.Sprintf("update: releasePublicKeyHex is %d bytes, want %d", len(key), ed25519.PublicKeySize))
	}
	return ed25519.PublicKey(key)
}

// verifyBinary reports whether sig is a valid ed25519 signature of data
// under pub. ed25519.Verify panics on a malformed public key, so the
// length is checked first rather than trusting every caller to only ever
// pass a well-formed key — a signature response that's the wrong size
// (truncated download, garbage asset) is just as much an untrusted input
// as the binary itself.
func verifyBinary(pub ed25519.PublicKey, data, sig []byte) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("update: invalid public key length %d", len(pub))
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("update: invalid signature length %d, want %d", len(sig), ed25519.SignatureSize)
	}
	if !ed25519.Verify(pub, data, sig) {
		return errors.New("update: signature verification failed")
	}
	return nil
}
