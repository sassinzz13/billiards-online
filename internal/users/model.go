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

var (
	ErrNotFound      = errors.New("user not found")
	ErrEmailTaken    = errors.New("email already registered")
	ErrHandleTaken   = errors.New("handle already taken")
	ErrInvalidEmail  = errors.New("invalid email address")
	ErrInvalidHandle = errors.New("handle must be 3-24 characters, letters, digits, or underscore")
)

const (
	MinHandleLen = 3
	MaxHandleLen = 24
	MaxEmailLen  = 254 // RFC 5321 maximum path length
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
