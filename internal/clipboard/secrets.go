package clipboard

import (
	"strings"
	"unicode"
)

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
	return hasSecretWord(nameWords(key))
}

// secretWords are whole words of a name that mark it sensitive on their own.
// They can't be substring markers: "key" would hit MONKEY and KEYBOARD_LAYOUT,
// and "conn" CONNECTED. Matching words catches STRIPE_KEY, SIGNING_KEY,
// encryptionKey, WEBHOOK_URL — none of which contain a secretKeyMarkers entry.
var secretWords = map[string]bool{
	"key": true, "keys": true,
	"webhook": true, "webhooks": true,
	"connection": true, "conn": true,
}

// urlOwners are words that, next to "url"/"uri", say it's a connection string
// or endpoint that commonly embeds credentials (postgres://user:pass@host).
// A plain APP_URL or PUBLIC_URL isn't masked.
var urlOwners = map[string]bool{
	"database": true, "db": true, "sql": true, "postgres": true, "postgresql": true, "pg": true,
	"mysql": true, "mariadb": true, "mongo": true, "mongodb": true, "redis": true,
	"amqp": true, "rabbitmq": true, "broker": true, "celery": true, "kafka": true,
	"cache": true, "queue": true, "sentry": true, "elasticsearch": true, "smtp": true,
	"proxy": true, "sqlalchemy": true,
}

func hasSecretWord(words []string) bool {
	hasURL, hasOwner := false, false
	for _, word := range words {
		switch {
		case secretWords[word]:
			return true
		case word == "url" || word == "uri":
			hasURL = true
		case urlOwners[word]:
			hasOwner = true
		}
	}
	return hasURL && hasOwner
}

// nameWords splits an env var name into lowercase words at anything that
// isn't a letter or digit and at camelCase boundaries: "STRIPE_KEY" and
// "stripeKey" both give [stripe key].
func nameWords(name string) []string {
	var words []string
	var current []rune
	flush := func() {
		if len(current) > 0 {
			words = append(words, strings.ToLower(string(current)))
			current = current[:0]
		}
	}
	var prev rune
	for _, r := range name {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r) && unicode.IsLower(prev):
			flush()
			current = append(current, r)
		default:
			current = append(current, r)
		}
		prev = r
	}
	flush()
	return words
}
