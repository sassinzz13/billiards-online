package rooms

import "github.com/gin-gonic/gin"

// RegisterRoutes mounts the rooms endpoints under an /api/v1 group.
//
// Every route requires a session — there is no anonymous room browsing in this phase, matching the
// rest of the application (the Angular shell itself requires sign-in before rendering anything).
// RequireAuth is applied once via g.Use, a single ordinary entry in Gin's chain, not nested inside
// another handler — see internal/users/handler.go for why that distinction matters.
func (h *Handler) RegisterRoutes(v1 *gin.RouterGroup) {
	g := v1.Group("/rooms")
	g.Use(h.authSvc.RequireAuth())

	g.POST("", h.create)
	g.GET("", h.list)
	g.POST("/join-by-code", h.joinByCode)
	g.GET("/:id", h.get)
	g.POST("/:id/join", h.join)
	g.POST("/:id/leave", h.leave)
	g.POST("/:id/ready", h.ready)
	g.POST("/:id/start", h.start)
}
