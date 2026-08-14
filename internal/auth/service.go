package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sassinzz13/billiards-online/internal/users"
	"github.com/sassinzz13/billiards-online/platform/logging"
	"github.com/sassinzz13/billiards-online/platform/postgres"
	"github.com/sassinzz13/billiards-online/platform/security"
)

// Service implements signup, login, logout, and session verification.
//
// It depends on *users.Service concretely. users is L0 and auth is L1, so the dependency points
// downward and is legal; an interface here would add indirection without a second implementation
// to justify it (§64).
type Service struct {
	db    postgres.DB
	users *users.Service
}

func NewService(db postgres.DB, u *users.Service) *Service {
	return &Service{db: db, users: u}
}

// Result is a completed authentication. Token is plaintext and appears exactly once, here — the
// caller sets it as a cookie and it is never stored, logged, or returned again.
type Result struct {
	User      users.User
	Token     security.Token
	ExpiresAt time.Time
}

// Signup creates an account and its credential atomically.
//
// The two live in different tables owned by different features, so a partial failure would leave an
// account that can never be logged into. One transaction makes that impossible; the users service
// is rebound to it with WithDB so both writes share it.
func (s *Service) Signup(ctx context.Context, email, handle, password string) (Result, error) {
	// Hash before opening the transaction. Argon2id deliberately takes ~50ms and 64 MiB, and
	// holding a database connection for that long is exactly the kind of thing that turns a slow
	// path into connection-pool exhaustion under load (§37).
	hash, err := security.HashPassword(password)
	if err != nil {
		return Result{}, err
	}

	var user users.User
	err = postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		user, err = s.users.WithDB(tx).Create(ctx, email, handle)
		if err != nil {
			return err
		}
		return insertCredential(ctx, tx, user.ID, hash)
	})
	if err != nil {
		return Result{}, err
	}

	return s.issueSession(ctx, user)
}

// Login verifies a password and issues a session.
func (s *Service) Login(ctx context.Context, email, password string) (Result, error) {
	cred, err := selectCredentialByEmail(ctx, s.db, users.NormalizeEmail(email))
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			// Spend the same ~50ms a real verification would. Returning immediately here makes
			// response time a reliable oracle for which addresses are registered.
			security.DummyVerify(password)
			return Result{}, ErrInvalidCredentials
		}
		return Result{}, err
	}

	ok, err := security.VerifyPassword(password, cred.PasswordHash)
	if err != nil {
		// A malformed stored hash is a data problem, not a client problem: log it, tell the client
		// nothing beyond "invalid credentials".
		logging.Logger(ctx).Error("stored password hash is unreadable",
			logging.KeyUserID, cred.UserID.String(), "error", err)
		return Result{}, ErrInvalidCredentials
	}
	if !ok {
		return Result{}, ErrInvalidCredentials
	}

	user, err := s.users.ByID(ctx, cred.UserID)
	if err != nil {
		return Result{}, fmt.Errorf("load user after credential check: %w", err)
	}

	// Transparently upgrade the hash if policy has since been raised. This is the only moment the
	// plaintext is available, so it is the only chance to do it.
	if security.NeedsRehash(cred.PasswordHash) {
		if newHash, hErr := security.HashPassword(password); hErr == nil {
			if uErr := updateCredential(ctx, s.db, user.ID, newHash); uErr != nil {
				// Non-fatal: the login itself succeeded and the old hash is still valid.
				logging.Logger(ctx).Warn("password rehash failed",
					logging.KeyUserID, user.ID.String(), "error", uErr)
			}
		}
	}

	return s.issueSession(ctx, user)
}

func (s *Service) issueSession(ctx context.Context, user users.User) (Result, error) {
	token, err := security.NewToken()
	if err != nil {
		return Result{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Result{}, fmt.Errorf("generate session id: %w", err)
	}

	expiresAt := time.Now().Add(SessionTTL).UTC()
	if err := insertSession(ctx, s.db, id, user.ID, security.HashToken(token), expiresAt); err != nil {
		return Result{}, err
	}

	return Result{User: user, Token: token, ExpiresAt: expiresAt}, nil
}

// Authenticate resolves a token to an identity, or returns ErrUnauthenticated.
//
// This runs on every authenticated request, so it is a single indexed query. Expiry and revocation
// are part of the WHERE clause rather than checked afterwards, so there is no path where a dead
// session is mistakenly accepted.
func (s *Service) Authenticate(ctx context.Context, token security.Token) (Identity, error) {
	if token == "" {
		return Identity{}, ErrUnauthenticated
	}

	sess, err := selectLiveSession(ctx, s.db, security.HashToken(token))
	if err != nil {
		return Identity{}, err
	}

	// Sliding expiry: an active player is never logged out mid-session. Renewal only writes when
	// the session is actually close to expiring, so the common request does no write at all.
	expiresAt := sess.ExpiresAt
	if time.Until(expiresAt) < SessionRenewThreshold {
		renewed := time.Now().Add(SessionTTL).UTC()
		if err := touchSession(ctx, s.db, sess.ID, renewed); err != nil {
			// Non-fatal: the session is valid right now, and failing the request over a bookkeeping
			// write would be worse than letting it expire on schedule.
			logging.Logger(ctx).Warn("session renewal failed",
				logging.KeySessionID, sess.ID.String(), "error", err)
		} else {
			expiresAt = renewed
		}
	}

	return Identity{
		UserID:    sess.UserID,
		SessionID: sess.ID,
		Handle:    sess.Handle,
		ExpiresAt: expiresAt,
	}, nil
}

// Logout revokes a single session. It is idempotent: revoking an already-revoked session succeeds.
func (s *Service) Logout(ctx context.Context, sessionID uuid.UUID) error {
	_, err := revokeSession(ctx, s.db, sessionID)
	return err
}

// LogoutAll revokes every live session for a user, returning how many were revoked.
//
// This is the operation JWTs cannot offer without a denylist, and the reason opaque sessions were
// chosen (ADR 0009). It backs "log out everywhere" and is the containment action after a
// compromise.
func (s *Service) LogoutAll(ctx context.Context, userID uuid.UUID) (int64, error) {
	return revokeAllForUser(ctx, s.db, userID)
}

// PurgeExpiredSessions deletes sessions dead for over 30 days. Intended for a periodic job; not
// wired to a scheduler yet.
func (s *Service) PurgeExpiredSessions(ctx context.Context) (int64, error) {
	return deleteExpiredSessions(ctx, s.db)
}
