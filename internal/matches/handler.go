package matches

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/sassinzz13/billiards-online/internal/auth"
	"github.com/sassinzz13/billiards-online/platform/logging"
)

// Handler exposes matches over HTTP. matches sits at L3, above auth's L1, so — same as
// internal/rooms — it imports auth directly rather than needing the users' context-carrier
// workaround (MEMORY.md §5).
type Handler struct {
	svc     *Service
	authSvc *auth.Service
}

func NewHandler(svc *Service, authSvc *auth.Service) *Handler {
	return &Handler{svc: svc, authSvc: authSvc}
}

func (h *Handler) get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "matches.invalid_id", "Not a valid match id.")
		return
	}

	match, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"code": "matches.not_found", "message": "No such match."}})
			return
		}
		internalError(c, "get match", err)
		return
	}
	c.JSON(http.StatusOK, toMatchResponse(match))
}

func (h *Handler) listForUser(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "matches.invalid_user_id", "Not a valid user id.")
		return
	}

	limit := 0 // Service clamps <=0 to its default.
	if raw := c.Query("limit"); raw != "" {
		if n, ok := parsePositiveInt(raw); ok {
			limit = n
		}
	}

	items, next, err := h.svc.ListForUser(c.Request.Context(), userID, c.Query("cursor"), limit)
	if err != nil {
		if errors.Is(err, errInvalidCursor) {
			badRequest(c, "matches.invalid_cursor", "Pagination cursor is invalid.")
			return
		}
		internalError(c, "list matches for user", err)
		return
	}

	responses := make([]matchResponse, len(items))
	for i, m := range items {
		responses[i] = toMatchResponse(m)
	}
	c.JSON(http.StatusOK, gin.H{"matches": responses, "nextCursor": next})
}

func parsePositiveInt(s string) (int, bool) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

func badRequest(c *gin.Context, code, message string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": code, "message": message}})
}

func internalError(c *gin.Context, msg string, err error) {
	logging.Logger(c.Request.Context()).Error(msg, "error", err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
		"code":    "internal",
		"message": "An internal error occurred.",
	}})
}
