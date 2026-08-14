// Package realtime is the WebSocket gateway: it owns the /ws upgrade, authenticates the
// connection, and dispatches decoded envelopes.
//
// Layer L6, the top of the stack (MEMORY.md §5) — it may import any feature below it. It imports
// internal/auth directly, same as internal/rooms does, because auth (L1) sits below it; only L0
// features are ever forced into the context-carrier pattern internal/users needs.
package realtime

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/sassinzz13/billiards-online/game/protocol"
	"github.com/sassinzz13/billiards-online/internal/auth"
	"github.com/sassinzz13/billiards-online/platform/config"
	"github.com/sassinzz13/billiards-online/platform/logging"
	"github.com/sassinzz13/billiards-online/platform/security"
	"github.com/sassinzz13/billiards-online/platform/websocket"
)

// Gateway upgrades and authenticates WebSocket connections.
//
// It holds no per-connection registry yet — nothing in Phase 5 needs to look a connection up from
// outside its own handler goroutine (no rooms broadcasting, no match actors). Phase 6 adds one when
// something actually needs it (§72: don't build ahead of the feature that needs it).
type Gateway struct {
	authSvc     *auth.Service
	cfg         *config.Config
	shutdownCtx context.Context
}

// NewGateway takes the process's own shutdown context — the one signal.NotifyContext produces in
// main.go — rather than deriving each connection's lifetime from the per-request context.
// http.Server.Shutdown does not proactively cancel a hijacked request's Context(); it waits for the
// handler goroutine to return on its own, up to the shutdown timeout, and a connection blocked in
// Read has no other reason to return. Deriving from the shared shutdown context instead means every
// open connection is cancelled the instant a SIGTERM arrives, the same moment the rest of the
// server starts winding down, so a deploy is not held up waiting out the full timeout per open
// socket. Full connection bookkeeping (stopping match actors cleanly) is Phase 20's job; this is
// the small, already-available piece of it that belongs here now.
func NewGateway(authSvc *auth.Service, cfg *config.Config, shutdownCtx context.Context) *Gateway {
	return &Gateway{authSvc: authSvc, cfg: cfg, shutdownCtx: shutdownCtx}
}

// ServeWS is the /ws HTTP handler. Rejections happen entirely before the upgrade, over plain HTTP:
// a bad Origin or a missing/invalid session never gets as far as an open socket, let alone a close
// frame — the failure is an ordinary 403 or 401 response.
func (g *Gateway) ServeWS(c *gin.Context) {
	origin := c.Request.Header.Get("Origin")
	// Exact-match, not coder/websocket's own glob-based OriginPatterns — see platform/websocket's
	// Accept doc comment for why the two are not allowed to disagree.
	if origin == "" || !g.cfg.AllowsOrigin(origin) {
		logging.Logger(c.Request.Context()).Warn("websocket upgrade rejected: bad origin", "origin", origin)
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{
			"code":    "realtime.origin_rejected",
			"message": "Origin not allowed.",
		}})
		return
	}

	raw, err := c.Cookie(auth.CookieName)
	if err != nil || raw == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{
			"code":    "auth.required",
			"message": "Authentication required.",
		}})
		return
	}
	identity, err := g.authSvc.Authenticate(c.Request.Context(), security.Token(raw))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{
			"code":    "auth.required",
			"message": "Authentication required.",
		}})
		return
	}

	connID, err := uuid.NewV7()
	if err != nil {
		internalErrorHTTP(c, "generate connection id", err)
		return
	}

	// See NewGateway's doc comment: derived from the shared shutdown context, not c.Request.Context(),
	// so this connection is cancelled the moment the process starts shutting down (§62).
	ctx, cancel := context.WithCancel(g.shutdownCtx)
	defer cancel()

	conn, err := websocket.Accept(ctx, c.Writer, c.Request)
	if err != nil {
		// Accept has already written its own HTTP error response by this point (it failed before
		// upgrading), so there is nothing more to send — just log and return.
		logging.Logger(ctx).Warn("websocket accept failed", "error", err, "userId", identity.UserID.String())
		return
	}

	logCtx := logging.With(ctx,
		logging.KeyUserID, identity.UserID.String(),
		logging.KeyConnectionID, connID.String(),
	)
	logger := logging.Logger(logCtx)
	logger.Info("connection opened")
	defer logger.Info("connection closed")

	session := &connection{
		conn:   conn,
		userID: identity.UserID,
		connID: connID,
		logger: logger,
	}
	session.serve(ctx)
}

func internalErrorHTTP(c *gin.Context, msg string, err error) {
	logging.Logger(c.Request.Context()).Error(msg, "error", err)
	c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": gin.H{
		"code":    protocol.ErrCodeInternal,
		"message": "An internal error occurred.",
	}})
}
