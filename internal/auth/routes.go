package auth

import "github.com/gin-gonic/gin"

// RegisterRoutes mounts the auth endpoints under an /api/v1 group.
//
// Routes live with the feature that implements them rather than in a central router file, so
// everything auth owns is visible in one directory (§4).
func (h *Handler) RegisterRoutes(v1 *gin.RouterGroup) {
	g := v1.Group("/auth")

	// Public. Both are rate limited inside the handler — they are the endpoints an attacker
	// hammers, and each one costs 64 MiB of Argon2 (§59).
	g.POST("/signup", h.signup)
	g.POST("/login", h.login)

	// Logout uses OptionalAuth rather than RequireAuth: logging out with an already-dead session
	// should succeed quietly, not return 401. That keeps it idempotent and avoids telling a caller
	// with a stolen cookie whether it was still live.
	g.POST("/logout", h.svc.OptionalAuth(), h.logout)

	// Authenticated.
	g.GET("/session", h.svc.RequireAuth(), h.session)
	g.POST("/logout-all", h.svc.RequireAuth(), h.logoutAll)
}
