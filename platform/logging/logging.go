// Package logging provides structured logging built on log/slog.
//
// The standard library is sufficient here, so no third-party logger is introduced.
//
// Identifiers travel through context.Context rather than being threaded manually through every
// signature. Handlers attach a request ID; later phases attach user, connection, room, and match
// IDs. Logger(ctx) then produces a logger carrying all of them, so a log line from deep inside a
// match is traceable back to the connection that caused it.
//
// Secrets must never reach this package. Password hashes, session tokens, and connection strings
// are not logged anywhere, at any level. See MEMORY.md §17.
package logging

import (
	"context"
	"io"
	"log/slog"
	"strings"
)

// Attribute keys. Kept as constants so queries over structured logs can rely on stable names.
const (
	KeyRequestID    = "requestId"
	KeyUserID       = "userId"
	KeyConnectionID = "connectionId"
	KeyRoomID       = "roomId"
	KeyMatchID      = "matchId"
)

type ctxKey struct{}

// New builds a logger from validated configuration. Format is "json" or "text"; level is one of
// debug, info, warn, error. Both are validated by platform/config, so an unrecognised value here
// falls back to a sane default rather than failing.
func New(w io.Writer, level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(level)}

	var h slog.Handler
	if format == "text" {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(h)
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// WithLogger returns a context carrying logger, for retrieval by Logger.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, logger)
}

// Logger returns the logger stored in ctx, or slog.Default if there is none.
//
// It never returns nil, so callers can log unconditionally without a nil check at every site.
func Logger(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

// With returns a context whose logger carries the additional attributes.
//
// Use it to attach an identifier once, at the point it becomes known, so every subsequent log line
// derived from that context includes it:
//
//	ctx = logging.With(ctx, logging.KeyMatchID, matchID.String())
func With(ctx context.Context, args ...any) context.Context {
	return WithLogger(ctx, Logger(ctx).With(args...))
}
