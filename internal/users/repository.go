package users

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sassinzz13/billiards-online/platform/postgres"
)

// SQL lives here as constants, next to the code that runs it. No ORM, no query builder, no
// generated code — the statement you read is the statement PostgreSQL receives, which is what makes
// EXPLAIN ANALYZE a copy-paste away (ADR 0002).
//
// Columns are always listed explicitly. SELECT * would silently start returning any column a future
// migration adds, including ones that should never leave this package.

const qInsertUser = `
	INSERT INTO users (id, email, handle)
	VALUES ($1, $2, $3)
	RETURNING id, email, handle, created_at, updated_at`

const qUserByID = `
	SELECT id, email, handle, created_at, updated_at
	FROM users
	WHERE id = $1`

const qUserByEmail = `
	SELECT id, email, handle, created_at, updated_at
	FROM users
	WHERE email = $1`

// Constraint names from migration 000001. Mapping them to domain errors is what turns a race
// between two concurrent signups into a clean "email already registered" rather than a 500.
const (
	constraintEmailUnique  = "users_email_key"
	constraintHandleUnique = "users_handle_lower_key"
)

func insertUser(ctx context.Context, db postgres.DB, id uuid.UUID, email, handle string) (User, error) {
	rows, err := db.Query(ctx, qInsertUser, id, email, handle)
	if err != nil {
		return User{}, mapUserError(err)
	}
	u, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[User])
	if err != nil {
		return User{}, mapUserError(err)
	}
	return u, nil
}

func selectUser(ctx context.Context, db postgres.DB, query string, arg any) (User, error) {
	rows, err := db.Query(ctx, query, arg)
	if err != nil {
		return User{}, fmt.Errorf("query user: %w", err)
	}
	u, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[User])
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("scan user: %w", err)
	}
	return u, nil
}

// mapUserError converts PostgreSQL constraint violations into domain errors.
//
// The database is the authority on uniqueness, not an application-level "does this exist?" check —
// that check is a race between two concurrent signups. Letting the insert fail and translating the
// error is the only correct approach (§10).
func mapUserError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		switch pgErr.ConstraintName {
		case constraintEmailUnique:
			return ErrEmailTaken
		case constraintHandleUnique:
			return ErrHandleTaken
		}
	}
	// Wrapped, not returned bare: the caller logs this, and the raw pgx error names tables and
	// constraints that must not reach an API response (§51).
	return fmt.Errorf("users repository: %w", err)
}
