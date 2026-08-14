// Package config loads and validates deployment configuration from the environment.
//
// This is the only package in the repository that reads environment variables. Everything else
// receives a validated *Config. Configuration is resolved once at startup and never re-read, so a
// running process cannot observe a half-changed configuration.
//
// Validation is fail-fast: Load returns every problem it finds at once rather than surfacing them
// one restart at a time.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Env is the deployment environment. It gates behaviour that must differ between development and
// production, such as log format and whether secure cookies are required.
type Env string

const (
	EnvDevelopment Env = "development"
	EnvProduction  Env = "production"
)

func (e Env) IsProduction() bool { return e == EnvProduction }

// Config is the fully validated configuration for one server process.
type Config struct {
	Env      Env
	HTTP     HTTP
	Database Database
	Log      Log
}

// HTTP configures the public API listener and the separate internal listener.
type HTTP struct {
	// Addr is the public listener carrying /api/v1 and, from Phase 5, /ws.
	Addr string

	// InternalAddr carries pprof and, from Phase 19, metrics. It must never be exposed publicly:
	// bind it to loopback in development, and to the container network only in Docker.
	InternalAddr string

	// AllowedOrigins is the strict allowlist used for CORS and, from Phase 5, for the WebSocket
	// upgrade Origin check. Matching is exact string equality — never a prefix or suffix test,
	// which is the classic way this check gets bypassed. See ADR 0009.
	AllowedOrigins []string

	// ShutdownTimeout bounds graceful shutdown. Exceeding it means in-flight requests are dropped,
	// which is preferable to a deploy that hangs forever.
	ShutdownTimeout time.Duration
}

// Database configures the PostgreSQL connection pool.
type Database struct {
	URL             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
	ConnectTimeout  time.Duration
}

// Log configures structured logging.
type Log struct {
	Level  string // debug | info | warn | error
	Format string // json | text
}

// Load reads configuration from the environment, applies defaults, and validates the result.
//
// It reports every validation failure together, so a misconfigured deployment surfaces all its
// problems on the first start rather than one per restart.
func Load() (*Config, error) {
	var errs []error
	get := func(key, fallback string) string {
		if v, ok := os.LookupEnv(key); ok && v != "" {
			return v
		}
		return fallback
	}
	getInt := func(key string, fallback int32) int32 {
		raw, ok := os.LookupEnv(key)
		if !ok || raw == "" {
			return fallback
		}
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %q is not an integer", key, raw))
			return fallback
		}
		return int32(n)
	}
	getDur := func(key string, fallback time.Duration) time.Duration {
		raw, ok := os.LookupEnv(key)
		if !ok || raw == "" {
			return fallback
		}
		d, err := time.ParseDuration(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %q is not a duration (want e.g. 15s, 5m)", key, raw))
			return fallback
		}
		return d
	}

	cfg := &Config{
		Env: Env(get("APP_ENV", string(EnvDevelopment))),
		HTTP: HTTP{
			Addr:            get("HTTP_ADDR", ":8080"),
			InternalAddr:    get("HTTP_INTERNAL_ADDR", "127.0.0.1:6060"),
			AllowedOrigins:  splitList(get("HTTP_ALLOWED_ORIGINS", "http://billiards.localhost")),
			ShutdownTimeout: getDur("HTTP_SHUTDOWN_TIMEOUT", 15*time.Second),
		},
		Database: Database{
			URL:             os.Getenv("DATABASE_URL"),
			MaxConns:        getInt("DATABASE_MAX_CONNS", 10),
			MinConns:        getInt("DATABASE_MIN_CONNS", 2),
			MaxConnLifetime: getDur("DATABASE_MAX_CONN_LIFETIME", time.Hour),
			MaxConnIdleTime: getDur("DATABASE_MAX_CONN_IDLE_TIME", 30*time.Minute),
			ConnectTimeout:  getDur("DATABASE_CONNECT_TIMEOUT", 10*time.Second),
		},
		Log: Log{
			Level:  strings.ToLower(get("LOG_LEVEL", "info")),
			Format: strings.ToLower(get("LOG_FORMAT", defaultLogFormat())),
		},
	}

	errs = append(errs, cfg.validate()...)
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("invalid configuration:\n%w", err)
	}
	return cfg, nil
}

func (c *Config) validate() []error {
	var errs []error
	bad := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	switch c.Env {
	case EnvDevelopment, EnvProduction:
	default:
		bad("APP_ENV: %q is not one of development, production", c.Env)
	}

	if c.HTTP.Addr == "" {
		bad("HTTP_ADDR: must not be empty")
	}
	if c.HTTP.InternalAddr == "" {
		bad("HTTP_INTERNAL_ADDR: must not be empty")
	}
	if c.HTTP.InternalAddr == c.HTTP.Addr {
		bad("HTTP_INTERNAL_ADDR: must differ from HTTP_ADDR — pprof must not be publicly reachable")
	}
	if c.HTTP.ShutdownTimeout <= 0 {
		bad("HTTP_SHUTDOWN_TIMEOUT: must be positive, got %s", c.HTTP.ShutdownTimeout)
	}
	if len(c.HTTP.AllowedOrigins) == 0 {
		bad("HTTP_ALLOWED_ORIGINS: must list at least one origin")
	}
	for _, o := range c.HTTP.AllowedOrigins {
		u, err := url.Parse(o)
		switch {
		case err != nil || u.Scheme == "" || u.Host == "":
			bad("HTTP_ALLOWED_ORIGINS: %q is not an absolute origin (want scheme://host[:port])", o)
		case u.Path != "" || u.RawQuery != "":
			bad("HTTP_ALLOWED_ORIGINS: %q must be scheme://host only, with no path or query", o)
		case c.Env.IsProduction() && u.Scheme != "https":
			bad("HTTP_ALLOWED_ORIGINS: %q must use https in production", o)
		}
	}

	if c.Database.URL == "" {
		bad("DATABASE_URL: required")
	} else if _, err := url.Parse(c.Database.URL); err != nil {
		bad("DATABASE_URL: not a valid URL: %v", err)
	}
	if c.Database.MaxConns < 1 {
		bad("DATABASE_MAX_CONNS: must be at least 1, got %d", c.Database.MaxConns)
	}
	if c.Database.MinConns < 0 {
		bad("DATABASE_MIN_CONNS: must not be negative, got %d", c.Database.MinConns)
	}
	if c.Database.MinConns > c.Database.MaxConns {
		bad("DATABASE_MIN_CONNS (%d): must not exceed DATABASE_MAX_CONNS (%d)",
			c.Database.MinConns, c.Database.MaxConns)
	}
	if c.Database.ConnectTimeout <= 0 {
		bad("DATABASE_CONNECT_TIMEOUT: must be positive, got %s", c.Database.ConnectTimeout)
	}

	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		bad("LOG_LEVEL: %q is not one of debug, info, warn, error", c.Log.Level)
	}
	switch c.Log.Format {
	case "json", "text":
	default:
		bad("LOG_FORMAT: %q is not one of json, text", c.Log.Format)
	}

	return errs
}

// AllowsOrigin reports whether origin is in the allowlist, by exact match.
//
// Exactness matters: a prefix or suffix comparison here would let https://billiards.localhost.evil
// through, which is precisely the cross-site WebSocket hijacking that the Phase 5 upgrade check
// exists to prevent.
func (c *Config) AllowsOrigin(origin string) bool {
	for _, allowed := range c.HTTP.AllowedOrigins {
		if origin == allowed {
			return true
		}
	}
	return false
}

func splitList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func defaultLogFormat() string {
	if Env(os.Getenv("APP_ENV")) == EnvProduction {
		return "json"
	}
	return "text"
}
