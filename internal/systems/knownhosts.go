package systems

import (
	"bytes"
	"encoding/base64"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const hybridHostKeyType = "ssh-mldsa44-ed25519"

func loadKnownHosts(path string) (ssh.HostKeyCallback, error) {
	callback, err := knownhosts.New(path)
	// Use the unmodified file as soon as the SSH library supports this key.
	if err == nil || !strings.HasSuffix(err.Error(), ": ssh: unknown key algorithm: "+hybridHostKeyType) {
		return callback, err
	}

	saved, readErr := os.ReadFile(path)
	if readErr != nil {
		return nil, readErr
	}
	var supported bytes.Buffer
	skipped := false
	for line := range bytes.Lines(saved) {
		if isHybridHostKeyLine(line) {
			// Keep line numbers for diagnostics from the remaining entries.
			supported.WriteByte('\n')
			skipped = true
			continue
		}
		supported.Write(line)
	}
	if !skipped {
		return nil, err
	}

	// knownhosts.New takes paths and reads them eagerly. Keep the temporary
	// copy private and let the library retain its usual verification rules.
	file, err := os.CreateTemp("", "whatthedock-known-hosts-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(file.Name())
	_, writeErr := supported.WriteTo(file)
	closeErr := file.Close()
	if writeErr != nil {
		return nil, writeErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return knownhosts.New(file.Name())
}

func isHybridHostKeyLine(line []byte) bool {
	fields := bytes.Fields(line)
	if len(fields) < 3 || fields[0][0] == '#' || fields[0][0] == '@' ||
		string(fields[1]) != hybridHostKeyType {
		return false
	}
	// Both the declared type and the wire format must match. In particular,
	// leave marked entries, malformed key blobs and other key types intact.
	blob, err := base64.StdEncoding.DecodeString(string(fields[2]))
	if err != nil {
		return false
	}
	var key struct {
		Type string
		Data []byte
	}
	const publicKeySize = 1312 + 32 // ML-DSA-44 and Ed25519 public keys
	return ssh.Unmarshal(blob, &key) == nil && key.Type == hybridHostKeyType && len(key.Data) == publicKeySize
}
