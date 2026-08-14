package rooms

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/sassinzz13/billiards-online/internal/auth"
	"github.com/sassinzz13/billiards-online/platform/logging"
	"github.com/sassinzz13/billiards-online/platform/security"
)

// Handler exposes rooms over HTTP.
//
// Unlike internal/users, this package imports internal/auth directly. rooms is L4 and auth is L1;
// L1 is one of the layers L4 may legally import (MEMORY.md §5), so RequireAuth is registered once
// as an ordinary member of Gin's own middleware chain in RegisterRoutes, and handlers read the
// caller's identity with auth.MustUserID. There is no context-bridge indirection here because none
// is needed — that pattern in internal/users exists specifically because users sits at L0 and
// cannot import auth (L1) at all.
type Handler struct {
	svc           *Service
	authSvc       *auth.Service
	createLimiter *security.RateLimiter
}

func NewHandler(svc *Service, authSvc *auth.Service, createLimiter *security.RateLimiter) *Handler {
	return &Handler{svc: svc, authSvc: authSvc, createLimiter: createLimiter}
}

// requireUserID reads the identity RequireAuth attached. Every handler below is mounted behind it,
// so its absence means the middleware was not actually applied — a wiring bug, hence 500 not 401.
func requireUserID(c *gin.Context) (uuid.UUID, bool) {
	id, ok := auth.MustUserID(c.Request.Context())
	if !ok {
		internalError(c, "rooms handler reached without identity", errors.New("missing identity"))
	}
	return id, ok
}

type createRequest struct {
	Visibility        string `json:"visibility" binding:"required"`
	Mode              string `json:"mode"        binding:"required"`
	Ranked            bool   `json:"ranked"`
	ShotTimerSeconds  *int   `json:"shotTimerSeconds"`
	WagerAmount       *int64 `json:"wagerAmount"`
	SpectatorsAllowed *bool  `json:"spectatorsAllowed"`
}

func (h *Handler) create(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	if !h.allowCreate(c, userID) {
		return
	}

	var req createRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "rooms.invalid_request", "visibility and mode are required.")
		return
	}

	detail, err := h.svc.Create(c.Request.Context(), userID, CreateInput{
		Visibility:        Visibility(req.Visibility),
		Mode:              Mode(req.Mode),
		Ranked:            req.Ranked,
		ShotTimerSeconds:  req.ShotTimerSeconds,
		WagerAmount:       req.WagerAmount,
		SpectatorsAllowed: req.SpectatorsAllowed,
	})
	if err != nil {
		h.writeCreateError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toDetailResponse(detail, userID))
}

func (h *Handler) get(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	roomID, ok := parseRoomID(c)
	if !ok {
		return
	}

	detail, err := h.svc.Detail(c.Request.Context(), roomID, userID)
	if err != nil {
		h.writeRoomError(c, err)
		return
	}
	c.JSON(http.StatusOK, toDetailResponse(detail, userID))
}

func (h *Handler) list(c *gin.Context) {
	limit := 0 // Service clamps <=0 to its default.
	if raw := c.Query("limit"); raw != "" {
		if n, err := parsePositiveInt(raw); err == nil {
			limit = n
		}
	}

	items, next, err := h.svc.ListPublicOpen(c.Request.Context(), c.Query("cursor"), limit)
	if err != nil {
		if errors.Is(err, errInvalidCursor) {
			badRequest(c, "rooms.invalid_cursor", "Pagination cursor is invalid.")
			return
		}
		internalError(c, "list rooms", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"rooms": items, "nextCursor": next})
}

func (h *Handler) join(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	roomID, ok := parseRoomID(c)
	if !ok {
		return
	}

	detail, err := h.svc.Join(c.Request.Context(), roomID, userID)
	if err != nil {
		h.writeRoomError(c, err)
		return
	}
	c.JSON(http.StatusOK, toDetailResponse(detail, userID))
}

type joinByCodeRequest struct {
	Code string `json:"code" binding:"required"`
}

func (h *Handler) joinByCode(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	var req joinByCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "rooms.invalid_request", "A join code is required.")
		return
	}

	detail, err := h.svc.JoinByCode(c.Request.Context(), req.Code, userID)
	if err != nil {
		h.writeRoomError(c, err)
		return
	}
	c.JSON(http.StatusOK, toDetailResponse(detail, userID))
}

func (h *Handler) leave(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	roomID, ok := parseRoomID(c)
	if !ok {
		return
	}

	if err := h.svc.Leave(c.Request.Context(), roomID, userID); err != nil {
		h.writeRoomError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type readyRequest struct {
	Ready bool `json:"ready"`
}

func (h *Handler) ready(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	roomID, ok := parseRoomID(c)
	if !ok {
		return
	}

	var req readyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "rooms.invalid_request", "ready must be true or false.")
		return
	}

	detail, err := h.svc.SetReady(c.Request.Context(), roomID, userID, req.Ready)
	if err != nil {
		h.writeRoomError(c, err)
		return
	}
	c.JSON(http.StatusOK, toDetailResponse(detail, userID))
}

// allowCreate rate-limits by user id rather than IP: every caller here is authenticated, and a
// per-user key does not punish several players behind one NAT the way an IP key would (§59).
func (h *Handler) allowCreate(c *gin.Context, userID uuid.UUID) bool {
	if h.createLimiter.Allow(userID.String()) {
		return true
	}
	logging.Logger(c.Request.Context()).Warn("rate limited", "action", "room.create", "userId", userID.String())
	c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": gin.H{
		"code":    "rooms.rate_limited",
		"message": "Too many rooms created recently. Please wait and try again.",
	}})
	return false
}

func parseRoomID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "rooms.invalid_id", "Not a valid room id.")
		return uuid.Nil, false
	}
	return id, true
}

func parsePositiveInt(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errInvalidCursor // reuse: any error works, caller only checks err == nil
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

func (h *Handler) writeCreateError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidVisibility):
		badRequest(c, "rooms.invalid_visibility", err.Error())
	case errors.Is(err, ErrInvalidMode):
		badRequest(c, "rooms.invalid_mode", err.Error())
	case errors.Is(err, ErrInvalidShotTimer):
		badRequest(c, "rooms.invalid_shot_timer", err.Error())
	case errors.Is(err, ErrInvalidWager):
		badRequest(c, "rooms.invalid_wager", err.Error())
	default:
		internalError(c, "create room", err)
	}
}

func (h *Handler) writeRoomError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		notFound(c)
	case errors.Is(err, ErrRoomClosed):
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"code": "rooms.closed", "message": "This room is closed."}})
	case errors.Is(err, ErrRoomFull):
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"code": "rooms.full", "message": "This room is full."}})
	case errors.Is(err, ErrAlreadyMember):
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"code": "rooms.already_member", "message": "You are already in this room."}})
	case errors.Is(err, ErrNotMember):
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"code": "rooms.not_member", "message": "You are not in this room."}})
	case errors.Is(err, ErrWrongJoinPath):
		badRequest(c, "rooms.wrong_join_path", err.Error())
	case errors.Is(err, ErrInvalidJoinCode):
		badRequest(c, "rooms.invalid_join_code", "That code does not match an open room.")
	default:
		internalError(c, "room operation failed", err)
	}
}

func notFound(c *gin.Context) {
	c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"code": "rooms.not_found", "message": "No such room."}})
}

func badRequest(c *gin.Context, code, message string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": code, "message": message}})
}

// internalError logs the cause and returns an opaque response — pgx errors name tables, columns,
// and constraints that must stay server-side (§42, §51).
func internalError(c *gin.Context, msg string, err error) {
	logging.Logger(c.Request.Context()).Error(msg, "error", err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
		"code":    "internal",
		"message": "An internal error occurred.",
	}})
}
