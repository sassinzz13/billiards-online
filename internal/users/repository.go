package users

import (
	"context"
	"errors"
	"fmt"
	"time"

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

const qInsertProfile = `
	INSERT INTO player_profiles (user_id)
	VALUES ($1)`

// The two account-shaped queries join users and player_profiles in one round trip rather than
// issuing a query per table, which is what N+1 would look like here (§10). accountRow's aliases
// exist because both tables have an updated_at; RowToStructByName matches by column name, so a
// collision would silently scan the wrong one into the wrong field.
const qAccountByID = `
	SELECT u.id, u.email, u.handle, u.created_at, u.updated_at,
	       p.display_name, p.avatar_ref, p.matches_played, p.wins, p.losses,
	       p.updated_at AS profile_updated_at
	FROM users u
	JOIN player_profiles p ON p.user_id = u.id
	WHERE u.id = $1`

const qPublicProfileByID = `
	SELECT u.id, u.handle, u.created_at,
	       p.display_name, p.avatar_ref, p.matches_played, p.wins, p.losses
	FROM users u
	JOIN player_profiles p ON p.user_id = u.id
	WHERE u.id = $1`

// The tri-state update in one round trip: $2/$4 say whether the caller touched that field at all,
// so a field the caller did not mention keeps its current value; among touched fields, an empty
// string means "clear" and anything else is the new value. This avoids a read-modify-write and the
// races it would invite, at the cost of a slightly denser WHERE-free CASE expression.
const qUpdateProfile = `
	UPDATE player_profiles
	SET display_name = CASE WHEN $2 THEN NULLIF($3, '') ELSE display_name END,
	    avatar_ref   = CASE WHEN $4 THEN NULLIF($5, '') ELSE avatar_ref   END,
	    updated_at   = now()
	WHERE user_id = $1
	RETURNING user_id, display_name, avatar_ref, matches_played, wins, losses, updated_at`

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

func insertProfile(ctx context.Context, db postgres.DB, userID uuid.UUID) error {
	if _, err := db.Exec(ctx, qInsertProfile, userID); err != nil {
		return fmt.Errorf("insert profile: %w", err)
	}
	return nil
}

// accountRow mirrors qAccountByID exactly, including the profile_updated_at alias that avoids a
// column-name collision with u.updated_at (§10).
type accountRow struct {
	ID               uuid.UUID `db:"id"`
	Email            string    `db:"email"`
	Handle           string    `db:"handle"`
	CreatedAt        time.Time `db:"created_at"`
	UpdatedAt        time.Time `db:"updated_at"`
	DisplayName      *string   `db:"display_name"`
	AvatarRef        *string   `db:"avatar_ref"`
	MatchesPlayed    int       `db:"matches_played"`
	Wins             int       `db:"wins"`
	Losses           int       `db:"losses"`
	ProfileUpdatedAt time.Time `db:"profile_updated_at"`
}

func (r accountRow) toAccount() Account {
	return Account{
		User: User{ID: r.ID, Email: r.Email, Handle: r.Handle, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt},
		Profile: Profile{
			UserID:        r.ID,
			DisplayName:   r.DisplayName,
			AvatarRef:     r.AvatarRef,
			MatchesPlayed: r.MatchesPlayed,
			Wins:          r.Wins,
			Losses:        r.Losses,
			UpdatedAt:     r.ProfileUpdatedAt,
		},
	}
}

func selectAccount(ctx context.Context, db postgres.DB, userID uuid.UUID) (Account, error) {
	rows, err := db.Query(ctx, qAccountByID, userID)
	if err != nil {
		return Account{}, fmt.Errorf("query account: %w", err)
	}
	r, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[accountRow])
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("scan account: %w", err)
	}
	return r.toAccount(), nil
}

// publicProfileRow mirrors qPublicProfileByID. A separate type from PublicProfile is required, not
// optional: display_name and avatar_ref are nullable columns, but PublicProfile's DisplayName is a
// plain non-nullable string (it has already had the handle fallback applied) — scanning a NULL
// straight into that field fails outright rather than silently, which is what caught this.
type publicProfileRow struct {
	ID            uuid.UUID `db:"id"`
	Handle        string    `db:"handle"`
	CreatedAt     time.Time `db:"created_at"`
	DisplayName   *string   `db:"display_name"`
	AvatarRef     *string   `db:"avatar_ref"`
	MatchesPlayed int       `db:"matches_played"`
	Wins          int       `db:"wins"`
	Losses        int       `db:"losses"`
}

func (r publicProfileRow) toPublicProfile() PublicProfile {
	displayName := r.Handle
	if r.DisplayName != nil {
		displayName = *r.DisplayName
	}
	return PublicProfile{
		ID:            r.ID,
		Handle:        r.Handle,
		DisplayName:   displayName,
		AvatarRef:     r.AvatarRef,
		MatchesPlayed: r.MatchesPlayed,
		Wins:          r.Wins,
		Losses:        r.Losses,
		CreatedAt:     r.CreatedAt,
	}
}

func selectPublicProfile(ctx context.Context, db postgres.DB, userID uuid.UUID) (PublicProfile, error) {
	rows, err := db.Query(ctx, qPublicProfileByID, userID)
	if err != nil {
		return PublicProfile{}, fmt.Errorf("query public profile: %w", err)
	}
	r, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[publicProfileRow])
	if errors.Is(err, pgx.ErrNoRows) {
		return PublicProfile{}, ErrNotFound
	}
	if err != nil {
		return PublicProfile{}, fmt.Errorf("scan public profile: %w", err)
	}
	return r.toPublicProfile(), nil
}

// updateProfile applies a tri-state edit and reports whether a row existed to update. Absence
// means an unknown user ID rather than a database error.
func updateProfile(ctx context.Context, db postgres.DB, userID uuid.UUID, displayName, avatarRef *string) error {
	dnProvided, dnValue := providedValue(displayName)
	arProvided, arValue := providedValue(avatarRef)

	tag, err := db.Exec(ctx, qUpdateProfile, userID, dnProvided, dnValue, arProvided, arValue)
	if err != nil {
		return fmt.Errorf("update profile: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// providedValue turns a *string (nil = untouched, non-nil = touched) into the two-parameter form
// qUpdateProfile's CASE expressions expect.
func providedValue(v *string) (provided bool, value string) {
	if v == nil {
		return false, ""
	}
	return true, *v
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
