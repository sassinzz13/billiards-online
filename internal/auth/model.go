// Package auth owns credentials and sessions.
//
// Authentication and authorization are separate concerns (§36). This package answers "who is this
// request from?" and nothing else. Whether that identity may act on a given match, room, or wallet
// is decided by the feature that owns it.
//
// Layer L1 — imports internal/users (L0) and platform/*. See MEMORY.md §5.
package auth

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// SessionTTL is how long a session stays valid without renewal.
//
// Two weeks is a deliberate trade. Sessions here are revocable server-side on logout, so a long TTL
// does not mean a long window of unstoppable access — the property that makes opaque tokens
// preferable to JWTs (ADR 0009).
const SessionTTL = 14 * 24 * time.Hour

// SessionRenewThreshold is how much remaining life triggers a sliding renewal on use. An active
// player is never logged out mid-session; an inactive one still expires.
const SessionRenewThreshold = 7 * 24 * time.Hour

// Session is a live login. The token itself is NOT a field: only its hash is stored, and the
// plaintext exists solely in the response that creates it (ADR 0009).
type Session struct {
	ID         uuid.UUID `db:"id"`
	UserID     uuid.UUID `db:"user_id"`
	CreatedAt  time.Time `db:"created_at"`
	LastSeenAt time.Time `db:"last_seen_at"`
	ExpiresAt  time.Time `db:"expires_at"`
}

var (
	// ErrInvalidCredentials covers both "no such email" and "wrong password", deliberately. A
	// distinct "no such account" error would let anyone enumerate which addresses are registered.
	ErrInvalidCredentials = errors.New("invalid email or password")

	// ErrUnauthenticated means no valid session: absent, malformed, expired, or revoked. The
	// distinction is logged, never returned, for the same reason.
	ErrUnauthenticated = errors.New("not authenticated")

	ErrRateLimited = errors.New("too many attempts")
)

// Identity is what a successful authentication yields, and what handlers downstream act on.
type Identity struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	Handle    string
	// ExpiresAt lets the client know when it will need to sign in again. It is already selected by
	// the session lookup, so carrying it costs nothing.
	ExpiresAt time.Time
}
