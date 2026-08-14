package config

import (
	"strings"
	"testing"
	"time"
)

// minimalEnv is the smallest environment that produces a valid Config.
func minimalEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/billiards")
}

func TestLoadDefaults(t *testing.T) {
	minimalEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if cfg.HTTP.Addr != ":8080" {
		t.Errorf("HTTP.Addr = %q, want :8080", cfg.HTTP.Addr)
	}
	if cfg.Database.MaxConns != 10 {
		t.Errorf("Database.MaxConns = %d, want 10", cfg.Database.MaxConns)
	}
	if cfg.HTTP.ShutdownTimeout != 15*time.Second {
		t.Errorf("HTTP.ShutdownTimeout = %s, want 15s", cfg.HTTP.ShutdownTimeout)
	}
	if cfg.Log.Format != "text" {
		t.Errorf("Log.Format = %q, want text in development", cfg.Log.Format)
	}
}

// A missing DATABASE_URL is the single most likely misconfiguration, so it must fail loudly at
// startup rather than at the first query.
func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", "")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() = nil, want error for missing DATABASE_URL")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error %q does not name DATABASE_URL", err)
	}
}

// Load reports every problem at once so a misconfigured deployment does not need one restart per
// mistake.
func TestLoadReportsAllErrorsTogether(t *testing.T) {
	t.Setenv("APP_ENV", "staging")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("LOG_LEVEL", "verbose")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() = nil, want error")
	}
	for _, want := range []string{"APP_ENV", "DATABASE_URL", "LOG_LEVEL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s:\n%v", want, err)
		}
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"non-integer max conns", map[string]string{"DATABASE_MAX_CONNS": "lots"}, "DATABASE_MAX_CONNS"},
		{"zero max conns", map[string]string{"DATABASE_MAX_CONNS": "0"}, "DATABASE_MAX_CONNS"},
		{"min exceeds max", map[string]string{"DATABASE_MIN_CONNS": "20", "DATABASE_MAX_CONNS": "5"}, "DATABASE_MIN_CONNS"},
		{"bad duration", map[string]string{"HTTP_SHUTDOWN_TIMEOUT": "soon"}, "HTTP_SHUTDOWN_TIMEOUT"},
		{"origin without scheme", map[string]string{"HTTP_ALLOWED_ORIGINS": "billiards.localhost"}, "HTTP_ALLOWED_ORIGINS"},
		{"origin with path", map[string]string{"HTTP_ALLOWED_ORIGINS": "http://billiards.localhost/app"}, "HTTP_ALLOWED_ORIGINS"},
		{"empty origin list", map[string]string{"HTTP_ALLOWED_ORIGINS": ","}, "HTTP_ALLOWED_ORIGINS"},
		{"bad log format", map[string]string{"LOG_FORMAT": "xml"}, "LOG_FORMAT"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			minimalEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			_, err := Load()
			if err == nil {
				t.Fatalf("Load() = nil, want error mentioning %s", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not mention %s:\n%v", tc.want, err)
			}
		})
	}
}

// pprof exposes heap contents and goroutine stacks. Sharing a listener with the public API would
// publish it, so the two must differ.
func TestLoadRejectsInternalAddrSharingPublicListener(t *testing.T) {
	minimalEnv(t)
	t.Setenv("HTTP_ADDR", ":8080")
	t.Setenv("HTTP_INTERNAL_ADDR", ":8080")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() = nil, want error for internal listener sharing the public address")
	}
	if !strings.Contains(err.Error(), "HTTP_INTERNAL_ADDR") {
		t.Errorf("error does not mention HTTP_INTERNAL_ADDR:\n%v", err)
	}
}

func TestLoadRequiresHTTPSOriginsInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://user:pass@db:5432/billiards")
	t.Setenv("HTTP_ALLOWED_ORIGINS", "http://billiards-online.duckdns.org")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() = nil, want error for plaintext origin in production")
	}
	if !strings.Contains(err.Error(), "https") {
		t.Errorf("error does not explain the https requirement:\n%v", err)
	}
}

// Origin matching guards the WebSocket upgrade from Phase 5 onward. A prefix or suffix comparison
// here would admit billiards.localhost.evil.com, so the match must be exact.
func TestAllowsOriginMatchesExactly(t *testing.T) {
	minimalEnv(t)
	t.Setenv("HTTP_ALLOWED_ORIGINS", "http://billiards.localhost, https://billiards-online.duckdns.org")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}

	allowed := []string{"http://billiards.localhost", "https://billiards-online.duckdns.org"}
	for _, o := range allowed {
		if !cfg.AllowsOrigin(o) {
			t.Errorf("AllowsOrigin(%q) = false, want true", o)
		}
	}

	denied := []string{
		"http://billiards.localhost.evil.com",        // suffix attack
		"http://evil.com/http://billiards.localhost", // prefix attack
		"https://billiards.localhost",                // scheme mismatch
		"http://billiards.localhost:8080",            // port mismatch
		"null",
		"",
	}
	for _, o := range denied {
		if cfg.AllowsOrigin(o) {
			t.Errorf("AllowsOrigin(%q) = true, want false", o)
		}
	}
}
