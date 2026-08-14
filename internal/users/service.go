package users

import (
	"context"
	"fmt"

	"github.com/google/uuid"

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

// Create registers an account.
//
// Uniqueness is enforced by the database, not by a prior existence check: two concurrent signups
// for the same address would both pass such a check. ErrEmailTaken and ErrHandleTaken come from
// translating the constraint violation.
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

	return insertUser(ctx, s.db, id, email, handle)
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
