package users

import "github.com/gin-gonic/gin"

// RegisterRoutes mounts the users endpoints under an /api/v1 group.
//
// authMiddleware is expected to end with the composition root's identity-bridging middleware,
// which populates the context with WithUserID — /me and PATCH /me rely on UserIDFromContext to
// identify the caller. See apps/server/cmd/server/main.go.
func (h *Handler) RegisterRoutes(v1 *gin.RouterGroup) {
	g := v1.Group("/users")

	// Static routes are registered before the :id wildcard. Gin's router resolves this correctly
	// regardless of order, but keeping /me first mirrors how a reader encounters it.
	g.GET("/me", h.protected(h.me)...)
	g.PATCH("/me", h.protected(h.updateMe)...)

	// Public: no auth required, and the projection this returns never includes email.
	g.GET("/:id", h.byID)
}

// protected builds a fresh handler chain per call. Reusing h.authMiddleware directly across two
// route registrations via append risks the second call's appended final handler silently
// overwriting the first's, if the slice's capacity happens to allow it — copying avoids relying on
// append never doing that.
func (h *Handler) protected(final gin.HandlerFunc) []gin.HandlerFunc {
	chain := make([]gin.HandlerFunc, len(h.authMiddleware)+1)
	copy(chain, h.authMiddleware)
	chain[len(h.authMiddleware)] = final
	return chain
}
