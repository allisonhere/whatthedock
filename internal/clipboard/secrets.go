package clipboard

import "strings"

// secretKeyMarkers are substrings of an env var's *name* (matched
// case-insensitively) that commonly indicate its value is sensitive.
// Deliberately broad and substring-based rather than an exact-match list —
// real-world env var names vary too much (DB_PASSWORD, POSTGRES_PASSWORD,
// MYSQL_ROOT_PASSWORD, ...) for an exact list to be worth maintaining, and a
// false positive here only means an unnecessary mask, never a missed
// secret's plaintext leaking into a preview.
var secretKeyMarkers = []string{
	"password", "passwd", "pwd",
	"secret",
	"token",
	"apikey", "api_key",
	"access_key", "accesskey",
	"private_key", "privatekey",
	"credential",
	"auth",
	"client_secret",
	"session",
	"cookie",
	"jwt",
	"dsn", // connection strings often embed credentials
	"passphrase",
}

// IsLikelySecret reports whether key (an environment variable name) looks
// like it holds a sensitive value, using a substring match against
// secretKeyMarkers. This drives PortableEnv.Secret at yank time — see
// FromContainer — and is intentionally a name-based heuristic: whatthedock
// has no way to know a value is a real secret, only that its name suggests
// one, so the check stays conservative in the direction of masking more,
// not less.
func IsLikelySecret(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	if lower == "" {
		return false
	}
	for _, marker := range secretKeyMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
