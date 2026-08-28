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
