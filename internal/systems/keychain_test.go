package systems

import (
	"context"
	"errors"
	"testing"

	"github.com/allisonhere/whatthedock/internal/config"
)

// fakeSecretStore is an in-memory secretStore — tests never touch a real
// OS keychain, matching the seam pattern executableOverride/
// verificationPublicKey already use in internal/update.
type fakeSecretStore struct {
	values map[string]string // keyed by service+"\x00"+user
}

func newFakeSecretStore() *fakeSecretStore {
	return &fakeSecretStore{values: map[string]string{}}
}

func (f *fakeSecretStore) key(service, user string) string { return service + "\x00" + user }

func (f *fakeSecretStore) Set(service, user, password string) error {
	f.values[f.key(service, user)] = password
	return nil
}

func (f *fakeSecretStore) Get(service, user string) (string, error) {
	v, ok := f.values[f.key(service, user)]
	if !ok {
		return "", errSecretNotFound
	}
	return v, nil
}

func (f *fakeSecretStore) Delete(service, user string) error {
	if _, ok := f.values[f.key(service, user)]; !ok {
		return errSecretNotFound
	}
	delete(f.values, f.key(service, user))
	return nil
}

// stubSecrets swaps the package's secretStore seam for a fake, restoring
// the original on cleanup.
func stubSecrets(t *testing.T) *fakeSecretStore {
	t.Helper()
	fake := newFakeSecretStore()
	original := secrets
	secrets = fake
	t.Cleanup(func() { secrets = original })
	return fake
}

func TestStorePasswordThenPasswordForRoundTrips(t *testing.T) {
	stubSecrets(t)

	if err := StorePassword("jarvis", "hunter2"); err != nil {
		t.Fatalf("StorePassword() err = %v", err)
	}
	got, err := PasswordFor("jarvis")
	if err != nil {
		t.Fatalf("PasswordFor() err = %v", err)
	}
	if got != "hunter2" {
		t.Fatalf("PasswordFor() = %q, want hunter2", got)
	}
}

func TestPasswordForReturnsErrNoStoredPasswordWhenUnset(t *testing.T) {
	stubSecrets(t)

	if _, err := PasswordFor("jarvis"); !errors.Is(err, errNoStoredPassword) {
		t.Fatalf("PasswordFor() err = %v, want errNoStoredPassword", err)
	}
}

func TestForgetPasswordRemovesStoredEntry(t *testing.T) {
	stubSecrets(t)
	if err := StorePassword("jarvis", "hunter2"); err != nil {
		t.Fatalf("StorePassword() err = %v", err)
	}

	if err := ForgetPassword("jarvis"); err != nil {
		t.Fatalf("ForgetPassword() err = %v", err)
	}
	if _, err := PasswordFor("jarvis"); !errors.Is(err, errNoStoredPassword) {
		t.Fatalf("PasswordFor() after ForgetPassword() err = %v, want errNoStoredPassword", err)
	}
}

func TestForgetPasswordOnUnsetEntryIsNotAnError(t *testing.T) {
	stubSecrets(t)

	if err := ForgetPassword("never-stored"); err != nil {
		t.Fatalf("ForgetPassword() err = %v, want nil for an entry that was never set", err)
	}
}

// TestFactoryProviderKeychainModeErrorsClearlyWithNoStoredPassword checks
// Factory.Provider's keychain branch never attempts to dial anything when
// there's no password to try — a clear, actionable error instead.
func TestFactoryProviderKeychainModeErrorsClearlyWithNoStoredPassword(t *testing.T) {
	stubSecrets(t)
	factory := Factory{}

	_, err := factory.Provider(context.Background(), config.System{
		ID:           "jarvis",
		Name:         "jarvis",
		Kind:         "ssh",
		SSHHost:      "192.168.86.74",
		SSHUser:      "allie",
		SSHAuth:      "keychain",
		RemoteSocket: "/var/run/docker.sock",
		LocalSocket:  t.TempDir() + "/jarvis.sock",
	})
	if err == nil {
		t.Fatal("Provider() err = nil, want an error when no password is stored")
	}
	const want = `no password stored in keychain for "jarvis" — open Systems and set one`
	if err.Error() != want {
		t.Fatalf("Provider() err = %q, want %q", err.Error(), want)
	}
}
