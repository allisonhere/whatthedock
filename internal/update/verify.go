package update

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
)

// releasePublicKeyHex is the hex-encoded ed25519 public key every release
// binary is signed against (see cmd/release's Sign step). The matching
// private key lives only on the maintainer's own machine, generated once
// via `go run ./cmd/release -genkey` and supplied to cmd/release through
// the WHATTHEDOCK_SIGNING_KEY environment variable — it never touches
// GitHub, CI, or this repo. That split is the whole point: this constant
// ships baked into every already-installed binary, so it's the *old*,
// already-trusted binary that verifies the new one, not anything fetched
// alongside the download it's checking.
const releasePublicKeyHex = "1df7ed51dea3d77c9c9cdda6b69806eac7438ca2b78f4c7e0ed6e79fcdb87447"

// verificationPublicKey is releasePublicKeyHex parsed once at init. A var,
// not a plain call site, so tests can swap it for a throwaway test key —
// the same seam pattern executableOverride uses in update.go.
var verificationPublicKey = mustParsePublicKey(releasePublicKeyHex)

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
