package clipboard

import "testing"

func TestIsLikelySecretFlagsCommonPatterns(t *testing.T) {
	secretKeys := []string{
		"PASSWORD", "DB_PASSWORD", "POSTGRES_PASSWORD", "MYSQL_ROOT_PASSWORD",
		"API_KEY", "APIKEY", "ACCESS_KEY", "SECRET_KEY", "CLIENT_SECRET",
		"AUTH_TOKEN", "JWT_SECRET", "PRIVATE_KEY", "SESSION_SECRET",
		"DATABASE_DSN", "PWD_HASH", "PASSPHRASE",
	}
	for _, key := range secretKeys {
		if !IsLikelySecret(key) {
			t.Errorf("IsLikelySecret(%q) = false, want true", key)
		}
	}
}

func TestIsLikelySecretIgnoresOrdinaryVars(t *testing.T) {
	ordinaryKeys := []string{
		"PUID", "PGID", "TZ", "PORT", "LOG_LEVEL", "NODE_ENV", "DEBUG",
		"HOSTNAME", "LANG", "COLOR",
	}
	for _, key := range ordinaryKeys {
		if IsLikelySecret(key) {
			t.Errorf("IsLikelySecret(%q) = true, want false", key)
		}
	}
}

func TestIsLikelySecretEmptyKey(t *testing.T) {
	if IsLikelySecret("") {
		t.Fatal("IsLikelySecret(\"\") = true, want false")
	}
}

// Names whose sensitivity is a whole word, not a substring: keys, webhook and
// connection URLs. None contains an entry of secretKeyMarkers.
func TestIsLikelySecretFlagsKeyAndConnectionStringNames(t *testing.T) {
	for _, name := range []string{
		"STRIPE_KEY", "SIGNING_KEY", "ENCRYPTION_KEY", "encryptionKey", "stripeKey",
		"DATABASE_URL", "REDIS_URL", "MONGO_URI", "databaseUrl", "CELERY_BROKER_URL",
		"WEBHOOK_URL", "SLACK_WEBHOOK", "DB_CONNECTION", "SQL_CONN",
	} {
		if !IsLikelySecret(name) {
			t.Errorf("IsLikelySecret(%q) = false, want true", name)
		}
	}
}

func TestIsLikelySecretLeavesLookalikesAlone(t *testing.T) {
	for _, name := range []string{"MONKEY", "KEYBOARD_LAYOUT", "CONNECTED", "APP_URL", "PUBLIC_URL", "TZ", "DOMAIN"} {
		if IsLikelySecret(name) {
			t.Errorf("IsLikelySecret(%q) = true, want false", name)
		}
	}
}
