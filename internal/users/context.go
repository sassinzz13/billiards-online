package users

import (
	"context"

	"github.com/google/uuid"
)

// This package never imports internal/auth (users is L0, auth is L1 — MEMORY.md §5), so it cannot
// read auth's Identity type directly. Instead it defines its own minimal carrier for "which user is
// making this request," and the composition root bridges auth's identity into it with a small
// adapter middleware. See apps/server/cmd/server/main.go.

type userIDKey struct{}

// WithUserID attaches the authenticated caller's ID to a context.
func WithUserID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, userIDKey{}, id)
}

// UserIDFromContext returns the authenticated caller's ID, if the request passed through an
// authentication bridge. False means either the route is public or the bridge was not wired —
// callers behind RequireAuth should treat false as a wiring bug (§13, §36).
func UserIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(userIDKey{}).(uuid.UUID)
	return id, ok
}
