package users

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sassinzz13/billiards-online/platform/postgres"
)

// Service is the entry point to the users feature. Other features reach identity through here and
// never touch the users table directly (§35).
//
// A concrete struct rather than an interface: there is no second implementation and no need to
// substitute one. Interfaces are for genuine boundaries, not for every type ending in "Service"
// (§64).
type Service struct {
	db postgres.DB
}

func NewService(db postgres.DB) *Service {
	return &Service{db: db}
}

// WithDB returns a Service bound to a different DB — typically a transaction, so an operation that
// spans features can be atomic.
//
// internal/auth uses this during signup: creating the account and its credential must either both
// happen or neither. Passing the transaction explicitly keeps that visible at the call site,
// instead of hiding it behind ambient state.
func (s *Service) WithDB(db postgres.DB) *Service {
	return &Service{db: db}
}

// Create registers an account and its profile.
//
// Uniqueness is enforced by the database, not by a prior existence check: two concurrent signups
// for the same address would both pass such a check. ErrEmailTaken and ErrHandleTaken come from
// translating the constraint violation.
//
// The user row and its profile row are inserted in one transaction, so "every user has a profile"
// holds unconditionally rather than as a rule a future call site has to remember. Wrapping in
// postgres.InTx is correct even when s.db is already a transaction — from auth.Signup via WithDB —
// because pgx turns the nested Begin into a SAVEPOINT.
func (s *Service) Create(ctx context.Context, email, handle string) (User, error) {
	email = NormalizeEmail(email)
	if err := ValidateEmail(email); err != nil {
		return User{}, err
	}
	if err := ValidateHandle(handle); err != nil {
		return User{}, err
	}

	// UUIDv7: time-ordered, so index inserts stay at the right edge of the B-tree, while remaining
	// non-enumerable in a URL (ADR 0011).
	id, err := uuid.NewV7()
	if err != nil {
		return User{}, fmt.Errorf("generate user id: %w", err)
	}

	var user User
	err = postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		user, err = insertUser(ctx, tx, id, email, handle)
		if err != nil {
			return err
		}
		return insertProfile(ctx, tx, user.ID)
	})
	if err != nil {
		return User{}, err
	}
	return user, nil
}

// ByID returns an account, or ErrNotFound.
func (s *Service) ByID(ctx context.Context, id uuid.UUID) (User, error) {
	return selectUser(ctx, s.db, qUserByID, id)
}

// ByEmail returns an account by address, or ErrNotFound. The address is normalized first, so
// lookup matches however the caller cased it.
func (s *Service) ByEmail(ctx context.Context, email string) (User, error) {
	return selectUser(ctx, s.db, qUserByEmail, NormalizeEmail(email))
}

// Account returns the full record — identity plus profile, including email — for the account's own
// owner. Never call this to answer "what does player X look like to player Y"; that is Public.
func (s *Service) Account(ctx context.Context, id uuid.UUID) (Account, error) {
	return selectAccount(ctx, s.db, id)
}

// Public returns the projection safe to show to other players: no email.
func (s *Service) Public(ctx context.Context, id uuid.UUID) (PublicProfile, error) {
	return selectPublicProfile(ctx, s.db, id)
}

// UpdateProfile applies a tri-state edit to the caller's own profile and returns the resulting
// Account.
//
// There is deliberately no way to name a different user here — id always comes from the
// authenticated session at the call site (internal/auth's RequireAuth), never from a request body
// or path parameter. That is what makes "user A cannot edit user B" true by construction rather
// than by a check that could be forgotten (§13, exit criterion of Phase 3).
func (s *Service) UpdateProfile(ctx context.Context, id uuid.UUID, in UpdateProfileInput) (Account, error) {
	displayName, err := normalizeTriState(in.DisplayName, ValidateDisplayName)
	if err != nil {
		return Account{}, err
	}
	avatarRef, err := normalizeTriState(in.AvatarRef, ValidateAvatarRef)
	if err != nil {
		return Account{}, err
	}

	if err := updateProfile(ctx, s.db, id, displayName, avatarRef); err != nil {
		return Account{}, err
	}
	return s.Account(ctx, id)
}

// normalizeTriState trims a touched field and validates it unless the trimmed result is empty, in
// which case empty is passed through as the "clear this field" signal the repository expects. An
// untouched (nil) field passes straight through.
func normalizeTriState(v *string, validate func(string) error) (*string, error) {
	if v == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*v)
	if trimmed == "" {
		return &trimmed, nil
	}
	if err := validate(trimmed); err != nil {
		return nil, err
	}
	return &trimmed, nil
}
