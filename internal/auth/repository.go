package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sassinzz13/billiards-online/platform/postgres"
)

const qInsertCredential = `
	INSERT INTO credentials (user_id, password_hash)
	VALUES ($1, $2)`

const qUpdateCredential = `
	UPDATE credentials
	SET password_hash = $2, updated_at = now()
	WHERE user_id = $1`

const qCredentialByEmail = `
	SELECT c.user_id, c.password_hash
	FROM credentials c
	JOIN users u ON u.id = c.user_id
	WHERE u.email = $1`

const qInsertSession = `
	INSERT INTO sessions (id, user_id, token_hash, expires_at)
	VALUES ($1, $2, $3, $4)`

// The hot path: one indexed lookup per authenticated request.
//
// Liveness is part of the WHERE clause rather than checked in Go. An expired or revoked session
// then returns no row at all, so there is no branch where a caller could forget to check and treat
// a dead session as valid.
const qLiveSessionByToken = `
	SELECT s.id, s.user_id, s.created_at, s.last_seen_at, s.expires_at, u.handle
	FROM sessions s
	JOIN users u ON u.id = s.user_id
	WHERE s.token_hash = $1
	  AND s.revoked_at IS NULL
	  AND s.expires_at > now()`

const qRevokeSession = `
	UPDATE sessions
	SET revoked_at = now()
	WHERE id = $1 AND revoked_at IS NULL`

const qRevokeAllForUser = `
	UPDATE sessions
	SET revoked_at = now()
	WHERE user_id = $1 AND revoked_at IS NULL`

const qTouchSession = `
	UPDATE sessions
	SET last_seen_at = now(), expires_at = $2
	WHERE id = $1`

// Housekeeping. Dead rows are deleted rather than kept: a revoked session has no audit value that
// the ledger-style tables elsewhere in this system would need.
const qDeleteExpiredSessions = `
	DELETE FROM sessions
	WHERE expires_at < now() - interval '30 days'`

func insertCredential(ctx context.Context, db postgres.DB, userID uuid.UUID, hash string) error {
	if _, err := db.Exec(ctx, qInsertCredential, userID, hash); err != nil {
		return fmt.Errorf("insert credential: %w", err)
	}
	return nil
}

func updateCredential(ctx context.Context, db postgres.DB, userID uuid.UUID, hash string) error {
	if _, err := db.Exec(ctx, qUpdateCredential, userID, hash); err != nil {
		return fmt.Errorf("update credential: %w", err)
	}
	return nil
}

type credential struct {
	UserID       uuid.UUID `db:"user_id"`
	PasswordHash string    `db:"password_hash"`
}

func selectCredentialByEmail(ctx context.Context, db postgres.DB, email string) (credential, error) {
	rows, err := db.Query(ctx, qCredentialByEmail, email)
	if err != nil {
		return credential{}, fmt.Errorf("query credential: %w", err)
	}
	c, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[credential])
	if errors.Is(err, pgx.ErrNoRows) {
		return credential{}, ErrInvalidCredentials
	}
	if err != nil {
		return credential{}, fmt.Errorf("scan credential: %w", err)
	}
	return c, nil
}

func insertSession(ctx context.Context, db postgres.DB, id, userID uuid.UUID, tokenHash []byte, expiresAt time.Time) error {
	if _, err := db.Exec(ctx, qInsertSession, id, userID, tokenHash, expiresAt); err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

// liveSession carries the handle too, so authenticating a request is a single query rather than a
// session lookup followed by a user lookup on every request.
type liveSession struct {
	ID         uuid.UUID `db:"id"`
	UserID     uuid.UUID `db:"user_id"`
	CreatedAt  time.Time `db:"created_at"`
	LastSeenAt time.Time `db:"last_seen_at"`
	ExpiresAt  time.Time `db:"expires_at"`
	Handle     string    `db:"handle"`
}

func selectLiveSession(ctx context.Context, db postgres.DB, tokenHash []byte) (liveSession, error) {
	rows, err := db.Query(ctx, qLiveSessionByToken, tokenHash)
	if err != nil {
		return liveSession{}, fmt.Errorf("query session: %w", err)
	}
	s, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[liveSession])
	if errors.Is(err, pgx.ErrNoRows) {
		return liveSession{}, ErrUnauthenticated
	}
	if err != nil {
		return liveSession{}, fmt.Errorf("scan session: %w", err)
	}
	return s, nil
}

// revokeSession reports whether a live session was actually revoked. False means it was already
// revoked or never existed — which makes logout idempotent.
func revokeSession(ctx context.Context, db postgres.DB, id uuid.UUID) (bool, error) {
	tag, err := db.Exec(ctx, qRevokeSession, id)
	if err != nil {
		return false, fmt.Errorf("revoke session: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func revokeAllForUser(ctx context.Context, db postgres.DB, userID uuid.UUID) (int64, error) {
	tag, err := db.Exec(ctx, qRevokeAllForUser, userID)
	if err != nil {
		return 0, fmt.Errorf("revoke sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

func touchSession(ctx context.Context, db postgres.DB, id uuid.UUID, expiresAt time.Time) error {
	if _, err := db.Exec(ctx, qTouchSession, id, expiresAt); err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

func deleteExpiredSessions(ctx context.Context, db postgres.DB) (int64, error) {
	tag, err := db.Exec(ctx, qDeleteExpiredSessions)
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}
