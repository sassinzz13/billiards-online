package auth

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/sassinzz13/billiards-online/platform/logging"
	"github.com/sassinzz13/billiards-online/platform/security"
)

// CookieName is the session cookie. The __Host- prefix would be stronger still, but it requires
// Secure, which cannot be set over plain HTTP in local development.
const CookieName = "billiards_session"

type identityKey struct{}

// RequireAuth rejects requests without a live session.
//
// It proves identity only. Whether that identity may act on a particular match, room, or wallet is
// decided by the feature that owns it — being authenticated authorizes nothing by itself (§36).
func (s *Service) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, err := s.identify(c)
		if err != nil {
			// Deliberately uniform: absent, malformed, expired, and revoked all look identical to
			// the client. The distinction is in the logs, not in the response.
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{
				"code":    "auth.required",
				"message": "Authentication required.",
			}})
			return
		}

		c.Request = c.Request.WithContext(withIdentity(c.Request.Context(), identity))
		c.Next()
	}
}

// OptionalAuth attaches an identity when one is present but never rejects.
//
// For endpoints whose response varies by viewer — a public profile that additionally shows an
// "edit" affordance to its owner, say.
func (s *Service) OptionalAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if identity, err := s.identify(c); err == nil {
			c.Request = c.Request.WithContext(withIdentity(c.Request.Context(), identity))
		}
		c.Next()
	}
}

func (s *Service) identify(c *gin.Context) (Identity, error) {
	raw, err := c.Cookie(CookieName)
	if err != nil || raw == "" {
		return Identity{}, ErrUnauthenticated
	}
	return s.Authenticate(c.Request.Context(), security.Token(raw))
}

func withIdentity(ctx context.Context, id Identity) context.Context {
	ctx = context.WithValue(ctx, identityKey{}, id)
	// Attaching to the logger here means every subsequent line for this request carries the user,
	// with no handler having to remember to add it (§47).
	return logging.With(ctx,
		logging.KeyUserID, id.UserID.String(),
		logging.KeySessionID, id.SessionID.String(),
	)
}

// IdentityFrom returns the authenticated identity, if any.
//
// Exported so other features can answer "who is this?" without importing auth's internals. From
// Phase 4 onward, rooms and matches use it as the input to their own authorization checks.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}

// MustUserID returns the authenticated user's ID, and false if there is none.
//
// Only meaningful behind RequireAuth; the boolean exists so a handler mounted without it fails
// visibly rather than acting on a zero UUID.
func MustUserID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := IdentityFrom(ctx)
	if !ok {
		return uuid.Nil, false
	}
	return id.UserID, true
}
