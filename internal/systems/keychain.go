package systems

import (
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// keychainService is the OS-keychain "service" name every whatthedock
// secret is stored under. The "user" half of the entry is the system's
// own ID — already the stable unique identifier NormalizeSystems assigns
// every system, so no extra field is needed just to key the keychain
// entry.
const keychainService = "whatthedock"

// errSecretNotFound is secretStore's own not-found sentinel — implementors
// translate their backend's specific not-found error into this one, so
// callers (and tests using a fake secretStore) only ever need to know
// about this package's error, never go-keyring's.
var errSecretNotFound = errors.New("secret not found")

// secretStore is the seam over the OS keychain — matches the pattern
// executableOverride/verificationPublicKey already use in internal/update:
// a package var pointing at the real implementation by default, swappable
// in tests so they never touch an actual OS keychain.
type secretStore interface {
	Set(service, user, password string) error
	Get(service, user string) (string, error)
	Delete(service, user string) error
}

type keyringStore struct{}

func (keyringStore) Set(service, user, password string) error {
	return keyring.Set(service, user, password)
}

func (keyringStore) Get(service, user string) (string, error) {
	password, err := keyring.Get(service, user)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", errSecretNotFound
	}
	return password, err
}

func (keyringStore) Delete(service, user string) error {
	if err := keyring.Delete(service, user); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return errSecretNotFound
		}
		return err
	}
	return nil
}

var secrets secretStore = keyringStore{}

// errNoStoredPassword is returned by PasswordFor when a keychain-mode
// system has no password stored yet — distinct from a keychain-backend
// error (no Secret Service running, etc.) so callers can give a more
// actionable message for the common "forgot to set one" case.
var errNoStoredPassword = errors.New("no password stored")

// StorePassword saves password in the OS keychain for systemID, called
// when saving a system whose SSHAuth is "keychain" (see
// internal/ui/model.go's saveSystemDraft).
func StorePassword(systemID, password string) error {
	if err := secrets.Set(keychainService, systemID, password); err != nil {
		return fmt.Errorf("store password in keychain: %w", err)
	}
	return nil
}

// ForgetPassword removes systemID's stored password, if any — called when
// deleting a keychain-mode system so no orphaned secret is left behind.
// A missing entry isn't an error: there's nothing to forget.
func ForgetPassword(systemID string) error {
	if err := secrets.Delete(keychainService, systemID); err != nil && !errors.Is(err, errSecretNotFound) {
		return fmt.Errorf("remove password from keychain: %w", err)
	}
	return nil
}

// PasswordFor fetches systemID's stored password. errNoStoredPassword
// specifically means "nothing saved yet" (open Systems and set one);
// anything else is a keychain-backend problem (no Secret Service running,
// etc).
func PasswordFor(systemID string) (string, error) {
	password, err := secrets.Get(keychainService, systemID)
	if errors.Is(err, errSecretNotFound) {
		return "", errNoStoredPassword
	}
	if err != nil {
		return "", fmt.Errorf("read password from keychain: %w", err)
	}
	return password, nil
}
