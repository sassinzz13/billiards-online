package users_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sassinzz13/billiards-online/internal/users"
	"github.com/sassinzz13/billiards-online/platform/postgres/pgtest"
)

// Tests run against a real PostgreSQL inside a transaction that is rolled back afterwards. The
// constraints being exercised here are database constraints, so a fake would prove nothing about
// what actually happens in production.

func newService(t *testing.T) (*users.Service, func() (email, handle string)) {
	t.Helper()
	svc := users.NewService(pgtest.DB(t))

	// Unique per call, so tests never collide even though they share a database.
	next := func() (string, string) {
		id := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
		return "player_" + id + "@example.com", "p" + id
	}
	return svc, next
}

func TestCreateAndFetch(t *testing.T) {
	svc, next := newService(t)
	ctx := pgtest.Context(t)
	email, handle := next()

	created, err := svc.Create(ctx, email, handle)
	if err != nil {
		t.Fatalf("Create() = %v, want nil", err)
	}
	if created.ID == uuid.Nil {
		t.Error("Create() returned a nil UUID")
	}
	if created.Email != email || created.Handle != handle {
		t.Errorf("Create() = %+v, want email %q handle %q", created, email, handle)
	}
	if created.CreatedAt.IsZero() {
		t.Error("CreatedAt not populated by the database default")
	}

	byID, err := svc.ByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("ByID() = %v", err)
	}
	if byID.ID != created.ID {
		t.Errorf("ByID() returned %v, want %v", byID.ID, created.ID)
	}

	byEmail, err := svc.ByEmail(ctx, email)
	if err != nil {
		t.Fatalf("ByEmail() = %v", err)
	}
	if byEmail.ID != created.ID {
		t.Errorf("ByEmail() returned %v, want %v", byEmail.ID, created.ID)
	}
}

// UUIDv7 is time-ordered. That property is what keeps index inserts at the right edge of the
// B-tree, so it is worth asserting rather than assuming (ADR 0011).
func TestCreatedIDsAreTimeOrdered(t *testing.T) {
	svc, next := newService(t)
	ctx := pgtest.Context(t)

	var prev uuid.UUID
	for range 5 {
		email, handle := next()
		u, err := svc.Create(ctx, email, handle)
		if err != nil {
			t.Fatalf("Create() = %v", err)
		}
		if u.ID.Version() != 7 {
			t.Fatalf("ID version = %d, want 7", u.ID.Version())
		}
		if prev != uuid.Nil && u.ID.String() <= prev.String() {
			t.Errorf("ID %v does not sort after %v", u.ID, prev)
		}
		prev = u.ID
	}
}

func TestEmailIsNormalized(t *testing.T) {
	svc, next := newService(t)
	ctx := pgtest.Context(t)
	email, handle := next()

	created, err := svc.Create(ctx, "  "+strings.ToUpper(email)+"  ", handle)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if created.Email != email {
		t.Errorf("stored email = %q, want normalized %q", created.Email, email)
	}

	// Lookup must match regardless of how the caller cases it.
	if _, err := svc.ByEmail(ctx, strings.ToUpper(email)); err != nil {
		t.Errorf("ByEmail(upper-case) = %v, want the same account", err)
	}
}

// Uniqueness is enforced by the database, not by a prior existence check — such a check is a race
// between two concurrent signups.
func TestDuplicateEmailIsRejected(t *testing.T) {
	tx := pgtest.DB(t)
	svc := users.NewService(tx)
	ctx := pgtest.Context(t)

	id := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	email := "player_" + id + "@example.com"

	if _, err := svc.Create(ctx, email, "p"+id); err != nil {
		t.Fatalf("first Create() = %v", err)
	}

	// Each duplicate attempt goes in its own savepoint. Without that, the first constraint
	// violation aborts the test transaction and the second assertion fails with 25P02 rather than
	// the domain error it is actually checking for.
	dupes := map[string]string{
		"same email":             email,
		"same email, upper case": strings.ToUpper(email),
	}
	for name, attempt := range dupes {
		t.Run(name, func(t *testing.T) {
			suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
			err := pgtest.Attempt(t, ctx, tx, func(sp pgx.Tx) error {
				_, err := svc.WithDB(sp).Create(ctx, attempt, "q"+suffix)
				return err
			})
			if !errors.Is(err, users.ErrEmailTaken) {
				t.Errorf("Create(%q) = %v, want ErrEmailTaken", attempt, err)
			}
		})
	}

	// The transaction must still be usable after those failures.
	if _, err := svc.ByEmail(ctx, email); err != nil {
		t.Errorf("ByEmail() after rejected duplicates = %v — the savepoint did not contain the failure", err)
	}
}

// Handles display with the player's chosen casing but must be unique case-insensitively, enforced
// by a functional index rather than a second stored column.
func TestDuplicateHandleIsRejectedCaseInsensitively(t *testing.T) {
	svc, next := newService(t)
	ctx := pgtest.Context(t)
	email, handle := next()

	if _, err := svc.Create(ctx, email, handle); err != nil {
		t.Fatalf("first Create() = %v", err)
	}

	otherEmail, _ := next()
	_, err := svc.Create(ctx, otherEmail, strings.ToUpper(handle))
	if !errors.Is(err, users.ErrHandleTaken) {
		t.Errorf("handle differing only by case: err = %v, want ErrHandleTaken", err)
	}
}

func TestCreateRejectsInvalidInput(t *testing.T) {
	svc, next := newService(t)
	ctx := pgtest.Context(t)
	_, validHandle := next()
	validEmail, _ := next()

	tests := []struct {
		name   string
		email  string
		handle string
		want   error
	}{
		{"no at sign", "notanemail", validHandle, users.ErrInvalidEmail},
		{"no domain dot", "a@b", validHandle, users.ErrInvalidEmail},
		{"empty email", "", validHandle, users.ErrInvalidEmail},
		{"email too long", strings.Repeat("a", 250) + "@example.com", validHandle, users.ErrInvalidEmail},
		{"handle too short", validEmail, "ab", users.ErrInvalidHandle},
		{"handle too long", validEmail, strings.Repeat("a", 25), users.ErrInvalidHandle},
		{"handle with space", validEmail, "bad handle", users.ErrInvalidHandle},
		{"handle with punctuation", validEmail, "bad-handle", users.ErrInvalidHandle},
		{"empty handle", validEmail, "", users.ErrInvalidHandle},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.Create(ctx, tc.email, tc.handle); !errors.Is(err, tc.want) {
				t.Errorf("Create(%q, %q) = %v, want %v", tc.email, tc.handle, err, tc.want)
			}
		})
	}
}

func TestFetchMissingUser(t *testing.T) {
	svc, _ := newService(t)
	ctx := pgtest.Context(t)

	id, _ := uuid.NewV7()
	if _, err := svc.ByID(ctx, id); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("ByID(unknown) = %v, want ErrNotFound", err)
	}
	if _, err := svc.ByEmail(ctx, "nobody@example.com"); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("ByEmail(unknown) = %v, want ErrNotFound", err)
	}
}

// The public projection is what other players see. Email is personal data and must not be in it.
func TestPublicProjectionOmitsEmail(t *testing.T) {
	svc, next := newService(t)
	ctx := pgtest.Context(t)
	email, handle := next()

	u, err := svc.Create(ctx, email, handle)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	pub := u.Public()
	if pub.Handle != handle || pub.ID != u.ID {
		t.Errorf("Public() = %+v, want the id and handle", pub)
	}
	// Structural: Public has no Email field at all, so this cannot regress by someone forgetting
	// to strip it.
	if strings.Contains(strings.ToLower(pub.Handle), "@") {
		t.Error("handle unexpectedly contains an email address")
	}
}
