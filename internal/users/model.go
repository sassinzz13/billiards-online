// Package users owns player identity: the account record, and nothing secret.
//
// There is deliberately no password here. Credentials belong to internal/auth, in a separate table,
// so no query in this package can return a password hash even by mistake (§42).
//
// Layer L0 — this package imports platform/* only. See MEMORY.md §5.
package users

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// User is an account record. Every field here is safe to return to the account's owner.
type User struct {
	ID        uuid.UUID `db:"id"`
	Email     string    `db:"email"`
	Handle    string    `db:"handle"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// Public is the projection safe to show to *other* players. Email is personal data and never
// appears in it.
type Public struct {
	ID        uuid.UUID `json:"id"`
	Handle    string    `json:"handle"`
	CreatedAt time.Time `json:"createdAt"`
}

func (u User) Public() Public {
	return Public{ID: u.ID, Handle: u.Handle, CreatedAt: u.CreatedAt}
}

// Profile holds what a player customizes plus statistics that Phase 15 will maintain. One row per
// user, created alongside it — see migration 000003.
//
// DisplayName and AvatarRef are nil when unset. Nil display name means "show the handle instead";
// there is no separate "use default" sentinel because nil already means exactly that.
type Profile struct {
	UserID        uuid.UUID `db:"user_id"`
	DisplayName   *string   `db:"display_name"`
	AvatarRef     *string   `db:"avatar_ref"`
	MatchesPlayed int       `db:"matches_played"`
	Wins          int       `db:"wins"`
	Losses        int       `db:"losses"`
	UpdatedAt     time.Time `db:"updated_at"`
}

// Account is the full view of a user's own record — identity plus profile, including the email
// that Public/PublicProfile deliberately omit. Returned only to the account's owner.
type Account struct {
	User    User
	Profile Profile
}

// DisplayName resolves the fallback: the player's chosen name, or their handle if they never set
// one. Centralized here so "me" and "public" projections can never disagree about it.
func (a Account) DisplayName() string {
	if a.Profile.DisplayName != nil {
		return *a.Profile.DisplayName
	}
	return a.User.Handle
}

// PublicProfile is what other players see: identity, chosen presentation, and statistics — never
// an email address (§42).
type PublicProfile struct {
	ID            uuid.UUID `json:"id"`
	Handle        string    `json:"handle"`
	DisplayName   string    `json:"displayName"`
	AvatarRef     *string   `json:"avatarRef,omitempty"`
	MatchesPlayed int       `json:"matchesPlayed"`
	Wins          int       `json:"wins"`
	Losses        int       `json:"losses"`
	CreatedAt     time.Time `json:"createdAt"`
}

func (a Account) Public() PublicProfile {
	return PublicProfile{
		ID:            a.User.ID,
		Handle:        a.User.Handle,
		DisplayName:   a.DisplayName(),
		AvatarRef:     a.Profile.AvatarRef,
		MatchesPlayed: a.Profile.MatchesPlayed,
		Wins:          a.Profile.Wins,
		Losses:        a.Profile.Losses,
		CreatedAt:     a.User.CreatedAt,
	}
}

// UpdateProfileInput carries a tri-state edit: nil means "leave this field unchanged", a pointer to
// an empty string means "clear it" (falls back to the handle for DisplayName, to nothing for
// AvatarRef), and a pointer to a non-empty string sets it. This is exactly what JSON unmarshalling
// into *string already gives an omitted-vs-present request body, so the handler needs no extra
// bookkeeping to express it.
type UpdateProfileInput struct {
	DisplayName *string
	AvatarRef   *string
}

var (
	ErrNotFound       = errors.New("user not found")
	ErrEmailTaken     = errors.New("email already registered")
	ErrHandleTaken    = errors.New("handle already taken")
	ErrInvalidEmail   = errors.New("invalid email address")
	ErrInvalidHandle  = errors.New("handle must be 3-24 characters, letters, digits, or underscore")
	ErrInvalidDisplay = errors.New("display name must be 1-40 characters")
	ErrInvalidAvatar  = errors.New("avatar reference must be at most 512 characters")
)

const (
	MinHandleLen      = 3
	MaxHandleLen      = 24
	MaxEmailLen       = 254 // RFC 5321 maximum path length
	MinDisplayNameLen = 1
	MaxDisplayNameLen = 40
	MaxAvatarRefLen   = 512
)

// Deliberately permissive. Validating email syntax exactly is famously unproductive; the only
// authoritative check is sending mail to it. This rejects obvious nonsense and nothing more. The
// same shape is enforced as a database CHECK, so the rule cannot drift between the two.
var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s.]+(\.[^@\s.]+)+$`)

var handleRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// NormalizeEmail lowercases and trims. Addresses are compared case-insensitively, and storing the
// normalized form is what lets a plain UNIQUE index enforce one account per address.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// ValidateEmail checks an already-normalized address.
func ValidateEmail(email string) error {
	if len(email) == 0 || len(email) > MaxEmailLen || !emailRe.MatchString(email) {
		return ErrInvalidEmail
	}
	return nil
}

// ValidateHandle checks a display handle. Casing is preserved for display but must be unique
// case-insensitively, which the database enforces with a functional index.
func ValidateHandle(handle string) error {
	if len(handle) < MinHandleLen || len(handle) > MaxHandleLen || !handleRe.MatchString(handle) {
		return ErrInvalidHandle
	}
	return nil
}

// ValidateDisplayName checks an already-trimmed, non-empty display name. The database CHECK
// enforces the same bound, so the rule cannot drift between the two (§10).
func ValidateDisplayName(name string) error {
	if len(name) < MinDisplayNameLen || len(name) > MaxDisplayNameLen {
		return ErrInvalidDisplay
	}
	return nil
}

// ValidateAvatarRef checks an already-trimmed, non-empty avatar reference.
//
// This is deliberately shallow — no URL parsing, no scheme allowlist — because it is an opaque
// reference to a future asset (Phase 8+), not yet resolved to anything. Validating it as a URL now
// would encode assumptions about a pipeline that does not exist yet.
func ValidateAvatarRef(ref string) error {
	if len(ref) == 0 || len(ref) > MaxAvatarRefLen {
		return ErrInvalidAvatar
	}
	return nil
}
