package users

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/sassinzz13/billiards-online/platform/logging"
)

// Handler exposes users over HTTP.
//
// authMiddleware is a plain []gin.HandlerFunc rather than an import of internal/auth's Service:
// users is L0 and auth is L1, so users may never import auth (MEMORY.md §5). The composition root
// passes authSvc.RequireAuth() plus its own identity-bridging middleware here, which satisfies this
// with zero coupling — the same consumer-side pattern as ADR 0001, just against a stdlib-shaped
// type instead of a custom interface.
//
// These are registered as ordinary members of Gin's own middleware chain (RegisterRoutes appends
// the route handler after them) rather than invoked as nested function calls. gin.Context.Next()
// advances a shared index across the *whole* chain, so calling one middleware's returned
// gin.HandlerFunc from inside another causes its internal c.Next() to run every following handler
// immediately — including the real route handler — before the outer call resumes. Two entries in
// one flat chain avoids that trap entirely.
type Handler struct {
	svc            *Service
	authMiddleware []gin.HandlerFunc
}

func NewHandler(svc *Service, authMiddleware ...gin.HandlerFunc) *Handler {
	return &Handler{svc: svc, authMiddleware: authMiddleware}
}

type accountResponse struct {
	ID            uuid.UUID `json:"id"`
	Email         string    `json:"email"`
	Handle        string    `json:"handle"`
	DisplayName   string    `json:"displayName"`
	AvatarRef     *string   `json:"avatarRef,omitempty"`
	MatchesPlayed int       `json:"matchesPlayed"`
	Wins          int       `json:"wins"`
	Losses        int       `json:"losses"`
	CreatedAt     string    `json:"createdAt"`
}

func toAccountResponse(a Account) accountResponse {
	return accountResponse{
		ID:            a.User.ID,
		Email:         a.User.Email,
		Handle:        a.User.Handle,
		DisplayName:   a.DisplayName(),
		AvatarRef:     a.Profile.AvatarRef,
		MatchesPlayed: a.Profile.MatchesPlayed,
		Wins:          a.Profile.Wins,
		Losses:        a.Profile.Losses,
		CreatedAt:     a.User.CreatedAt.Format(rfc3339),
	}
}

const rfc3339 = "2006-01-02T15:04:05.999999999Z07:00"

// me returns the caller's own record, including email — safe here because RequireAuth guarantees
// the caller is looking at their own account, never another player's.
func (h *Handler) me(c *gin.Context) {
	// This handler is mounted behind requireAuth, so a missing identity means the middleware chain
	// was wired wrong, not a client mistake — hence 500, not 401.
	userID, ok := UserIDFromContext(c.Request.Context())
	if !ok {
		internalError(c, "users.me reached without identity", errors.New("missing identity"))
		return
	}

	account, err := h.svc.Account(c.Request.Context(), userID)
	if err != nil {
		internalError(c, "load account", err)
		return
	}
	c.JSON(http.StatusOK, toAccountResponse(account))
}

type updateMeRequest struct {
	DisplayName *string `json:"displayName"`
	AvatarRef   *string `json:"avatarRef"`
}

// updateMe edits the caller's own profile.
//
// There is no user ID anywhere in this request — not in the body, not in the path. It always acts
// on the identity RequireAuth attached to the context. That is what makes "user A cannot edit user
// B" true by construction: there is no field to put B's ID into (§13, exit criterion of Phase 3).
func (h *Handler) updateMe(c *gin.Context) {
	userID, ok := UserIDFromContext(c.Request.Context())
	if !ok {
		internalError(c, "users.updateMe reached without identity", errors.New("missing identity"))
		return
	}

	var req updateMeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "users.invalid_request", "Request body could not be parsed.")
		return
	}

	account, err := h.svc.UpdateProfile(c.Request.Context(), userID, UpdateProfileInput{
		DisplayName: req.DisplayName,
		AvatarRef:   req.AvatarRef,
	})
	if err != nil {
		h.writeUpdateError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAccountResponse(account))
}

// byID returns the public projection of any player — no email, whether or not the caller is
// authenticated.
func (h *Handler) byID(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "users.invalid_id", "Not a valid user id.")
		return
	}

	profile, err := h.svc.Public(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{
				"code":    "users.not_found",
				"message": "No such user.",
			}})
			return
		}
		internalError(c, "load public profile", err)
		return
	}
	c.JSON(http.StatusOK, profile)
}

func (h *Handler) writeUpdateError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidDisplay):
		badRequest(c, "users.invalid_display_name", err.Error())
	case errors.Is(err, ErrInvalidAvatar):
		badRequest(c, "users.invalid_avatar_ref", err.Error())
	case errors.Is(err, ErrNotFound):
		// Only reachable if the account was deleted between authentication and this call — an
		// account-deletion feature does not exist yet, so this is effectively unreachable today,
		// but the response is still the correct one if it ever happens.
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{
			"code":    "users.not_found",
			"message": "Account no longer exists.",
		}})
	default:
		internalError(c, "update profile", err)
	}
}

func badRequest(c *gin.Context, code, message string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": code, "message": message}})
}

// internalError logs the cause and returns an opaque response. The error is never echoed to the
// client: pgx errors name tables, columns, and constraints that must stay server-side (§42, §51).
func internalError(c *gin.Context, msg string, err error) {
	logging.Logger(c.Request.Context()).Error(msg, "error", err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
		"code":    "internal",
		"message": "An internal error occurred.",
	}})
}
