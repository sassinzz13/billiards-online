package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sassinzz13/billiards-online/internal/auth"
	"github.com/sassinzz13/billiards-online/internal/users"
	"github.com/sassinzz13/billiards-online/platform/postgres/pgtest"
	"github.com/sassinzz13/billiards-online/platform/security"
)

const testPassword = "correct-horse-battery-staple"

type fixture struct {
	svc  *auth.Service
	tx   pgx.Tx
	ctx  context.Context
	next func() (email, handle string)
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	tx := pgtest.DB(t)
	usersSvc := users.NewService(tx)

	return fixture{
		svc: auth.NewService(tx, usersSvc),
		tx:  tx,
		ctx: pgtest.Context(t),
		next: func() (string, string) {
			id := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
			return "player_" + id + "@example.com", "p" + id
		},
	}
}

func (f fixture) signup(t *testing.T) (auth.Result, string) {
	t.Helper()
	email, handle := f.next()
	res, err := f.svc.Signup(f.ctx, email, handle, testPassword)
	if err != nil {
		t.Fatalf("Signup() = %v, want nil", err)
	}
	return res, email
}

func TestSignupCreatesUsableAccount(t *testing.T) {
	f := newFixture(t)
	res, email := f.signup(t)

	if res.User.Email != email {
		t.Errorf("User.Email = %q, want %q", res.User.Email, email)
	}
	if res.Token == "" {
		t.Error("Signup() returned an empty token")
	}
	if !res.ExpiresAt.After(time.Now()) {
		t.Errorf("ExpiresAt = %v, want a future time", res.ExpiresAt)
	}

	// The session it issued must immediately work.
	id, err := f.svc.Authenticate(f.ctx, res.Token)
	if err != nil {
		t.Fatalf("Authenticate() after signup = %v", err)
	}
	if id.UserID != res.User.ID {
		t.Errorf("Authenticate() gave user %v, want %v", id.UserID, res.User.ID)
	}
}

// The account and its credential live in tables owned by different features. A partial failure
// would leave an account nobody can ever log into, so signup runs in one transaction.
func TestSignupIsAtomic(t *testing.T) {
	f := newFixture(t)
	email, handle := f.next()

	if _, err := f.svc.Signup(f.ctx, email, handle, testPassword); err != nil {
		t.Fatalf("first Signup() = %v", err)
	}

	// A second signup reusing the handle fails on the users insert. The credential insert must not
	// have left an orphan behind.
	otherEmail, _ := f.next()
	if _, err := f.svc.Signup(f.ctx, otherEmail, handle, testPassword); !errors.Is(err, users.ErrHandleTaken) {
		t.Fatalf("duplicate handle: err = %v, want ErrHandleTaken", err)
	}

	var count int
	if err := f.tx.QueryRow(f.ctx,
		`SELECT count(*) FROM credentials c
		 LEFT JOIN users u ON u.id = c.user_id
		 WHERE u.id IS NULL`).Scan(&count); err != nil {
		t.Fatalf("orphan check: %v", err)
	}
	if count != 0 {
		t.Errorf("found %d orphaned credential rows — signup is not atomic", count)
	}
}

func TestSignupRejectsWeakPassword(t *testing.T) {
	f := newFixture(t)
	email, handle := f.next()

	_, err := f.svc.Signup(f.ctx, email, handle, "short")
	if !errors.Is(err, security.ErrPasswordTooShort) {
		t.Errorf("Signup(short password) = %v, want ErrPasswordTooShort", err)
	}
}

func TestLoginSucceedsWithCorrectPassword(t *testing.T) {
	f := newFixture(t)
	signed, email := f.signup(t)

	res, err := f.svc.Login(f.ctx, email, testPassword)
	if err != nil {
		t.Fatalf("Login() = %v, want nil", err)
	}
	if res.User.ID != signed.User.ID {
		t.Errorf("Login() gave user %v, want %v", res.User.ID, signed.User.ID)
	}
	// Each login is a distinct session, so signing in on a second device does not invalidate the
	// first.
	if res.Token == signed.Token {
		t.Error("Login() reissued the signup token instead of a new session")
	}
	if _, err := f.svc.Authenticate(f.ctx, signed.Token); err != nil {
		t.Errorf("the earlier session stopped working after a new login: %v", err)
	}
}

func TestLoginIsCaseInsensitiveOnEmail(t *testing.T) {
	f := newFixture(t)
	_, email := f.signup(t)

	if _, err := f.svc.Login(f.ctx, strings.ToUpper(email), testPassword); err != nil {
		t.Errorf("Login(upper-case email) = %v, want success", err)
	}
}

// Wrong password and unknown account must be indistinguishable to the caller — otherwise the API
// becomes an oracle for which addresses are registered.
func TestLoginFailuresAreIndistinguishable(t *testing.T) {
	f := newFixture(t)
	_, email := f.signup(t)

	cases := []struct{ name, email, password string }{
		{"wrong password", email, "wrong-password-entirely"},
		{"unknown email", "nobody-" + email, testPassword},
		{"empty password", email, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.svc.Login(f.ctx, tc.email, tc.password)
			if !errors.Is(err, auth.ErrInvalidCredentials) {
				t.Errorf("Login() = %v, want ErrInvalidCredentials", err)
			}
		})
	}
}

// Immediate revocation is the reason opaque sessions were chosen over JWTs (ADR 0009). A logged-out
// session must stop working on the very next request, not at expiry.
func TestLogoutRevokesImmediately(t *testing.T) {
	f := newFixture(t)
	res, _ := f.signup(t)

	id, err := f.svc.Authenticate(f.ctx, res.Token)
	if err != nil {
		t.Fatalf("Authenticate() before logout = %v", err)
	}

	if err := f.svc.Logout(f.ctx, id.SessionID); err != nil {
		t.Fatalf("Logout() = %v", err)
	}

	if _, err := f.svc.Authenticate(f.ctx, res.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("Authenticate() after logout = %v, want ErrUnauthenticated", err)
	}
}

func TestLogoutIsIdempotent(t *testing.T) {
	f := newFixture(t)
	res, _ := f.signup(t)

	id, err := f.svc.Authenticate(f.ctx, res.Token)
	if err != nil {
		t.Fatalf("Authenticate() = %v", err)
	}

	for i := range 3 {
		if err := f.svc.Logout(f.ctx, id.SessionID); err != nil {
			t.Errorf("Logout() call %d = %v, want nil", i+1, err)
		}
	}
	// Logging out a session that never existed must also succeed.
	unknown, _ := uuid.NewV7()
	if err := f.svc.Logout(f.ctx, unknown); err != nil {
		t.Errorf("Logout(unknown session) = %v, want nil", err)
	}
}

// "Log out everywhere" — the containment action after a compromise, and the operation a stateless
// token cannot offer without a denylist.
func TestLogoutAllRevokesEverySession(t *testing.T) {
	f := newFixture(t)
	first, email := f.signup(t)

	second, err := f.svc.Login(f.ctx, email, testPassword)
	if err != nil {
		t.Fatalf("Login() = %v", err)
	}
	third, err := f.svc.Login(f.ctx, email, testPassword)
	if err != nil {
		t.Fatalf("Login() = %v", err)
	}

	n, err := f.svc.LogoutAll(f.ctx, first.User.ID)
	if err != nil {
		t.Fatalf("LogoutAll() = %v", err)
	}
	if n != 3 {
		t.Errorf("LogoutAll() revoked %d sessions, want 3", n)
	}

	for name, tok := range map[string]security.Token{
		"signup session": first.Token, "second login": second.Token, "third login": third.Token,
	} {
		if _, err := f.svc.Authenticate(f.ctx, tok); !errors.Is(err, auth.ErrUnauthenticated) {
			t.Errorf("%s still authenticates after LogoutAll: %v", name, err)
		}
	}
}

func TestAuthenticateRejectsBadTokens(t *testing.T) {
	f := newFixture(t)
	f.signup(t)

	forged, err := security.NewToken()
	if err != nil {
		t.Fatalf("NewToken() = %v", err)
	}

	for name, tok := range map[string]security.Token{
		"empty":     "",
		"garbage":   "not-a-real-token",
		"forged":    forged, // correctly shaped, never issued
		"truncated": "abc",
	} {
		if _, err := f.svc.Authenticate(f.ctx, tok); !errors.Is(err, auth.ErrUnauthenticated) {
			t.Errorf("Authenticate(%s) = %v, want ErrUnauthenticated", name, err)
		}
	}
}

// Expiry is part of the SQL WHERE clause rather than a check in Go, so there is no code path where
// a caller forgets to test it and accepts a dead session.
func TestAuthenticateRejectsExpiredSession(t *testing.T) {
	f := newFixture(t)
	res, _ := f.signup(t)

	id, err := f.svc.Authenticate(f.ctx, res.Token)
	if err != nil {
		t.Fatalf("Authenticate() = %v", err)
	}

	// created_at moves back too. The sessions_expires_after_created CHECK rejects a row whose
	// expiry precedes its creation, so simulating an expired session means aging the whole row —
	// which is also what a genuinely old session looks like.
	if _, err := f.tx.Exec(f.ctx,
		`UPDATE sessions
		 SET created_at = now() - interval '30 days',
		     expires_at = now() - interval '1 second'
		 WHERE id = $1`, id.SessionID); err != nil {
		t.Fatalf("expire session: %v", err)
	}

	if _, err := f.svc.Authenticate(f.ctx, res.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("Authenticate(expired) = %v, want ErrUnauthenticated", err)
	}
}

// The database must hold only the hash. A dump should yield nothing that can be replayed as a
// cookie (ADR 0009).
func TestSessionTokenIsNeverStoredInPlaintext(t *testing.T) {
	f := newFixture(t)
	res, _ := f.signup(t)

	var stored []byte
	if err := f.tx.QueryRow(f.ctx,
		`SELECT token_hash FROM sessions WHERE token_hash = $1`,
		security.HashToken(res.Token)).Scan(&stored); err != nil {
		t.Fatalf("session row not found by token hash: %v", err)
	}

	if string(stored) == string(res.Token) {
		t.Fatal("sessions.token_hash contains the plaintext token")
	}
	if len(stored) != 32 {
		t.Errorf("token_hash is %d bytes, want 32", len(stored))
	}

	// And no column anywhere holds the token as text.
	var leaked int
	if err := f.tx.QueryRow(f.ctx,
		`SELECT count(*) FROM sessions WHERE encode(token_hash, 'escape') = $1`,
		string(res.Token)).Scan(&leaked); err != nil {
		t.Fatalf("leak check: %v", err)
	}
	if leaked != 0 {
		t.Error("found the plaintext token stored in the sessions table")
	}
}

// The password hash must never be reachable through the users feature — the reason credentials
// live in their own auth-owned table.
func TestPasswordIsStoredOnlyAsArgon2idHash(t *testing.T) {
	f := newFixture(t)
	res, _ := f.signup(t)

	var hash string
	if err := f.tx.QueryRow(f.ctx,
		`SELECT password_hash FROM credentials WHERE user_id = $1`, res.User.ID).Scan(&hash); err != nil {
		t.Fatalf("credential row not found: %v", err)
	}

	if strings.Contains(hash, testPassword) {
		t.Fatal("stored hash contains the plaintext password")
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("stored hash = %q, want an argon2id PHC string", hash)
	}

	// users owns no password column at all, so this must find nothing.
	var cols int
	if err := f.tx.QueryRow(f.ctx,
		`SELECT count(*) FROM information_schema.columns
		 WHERE table_name = 'users' AND column_name LIKE '%password%'`).Scan(&cols); err != nil {
		t.Fatalf("column check: %v", err)
	}
	if cols != 0 {
		t.Errorf("users table has %d password-like columns, want 0", cols)
	}
}

func TestPurgeExpiredSessionsLeavesLiveOnesAlone(t *testing.T) {
	f := newFixture(t)
	live, _ := f.signup(t)

	id, err := f.svc.Authenticate(f.ctx, live.Token)
	if err != nil {
		t.Fatalf("Authenticate() = %v", err)
	}
	// Long dead: past the 30-day retention window. created_at moves back further still, to satisfy
	// the sessions_expires_after_created CHECK.
	if _, err := f.tx.Exec(f.ctx,
		`UPDATE sessions
		 SET created_at = now() - interval '60 days',
		     expires_at = now() - interval '40 days'
		 WHERE id = $1`, id.SessionID); err != nil {
		t.Fatalf("age session: %v", err)
	}

	stillLive, _ := f.signup(t)

	if _, err := f.svc.PurgeExpiredSessions(f.ctx); err != nil {
		t.Fatalf("PurgeExpiredSessions() = %v", err)
	}
	if _, err := f.svc.Authenticate(f.ctx, stillLive.Token); err != nil {
		t.Errorf("purge removed a live session: %v", err)
	}
}
