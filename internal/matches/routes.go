package matches

import "github.com/gin-gonic/gin"

// RegisterRoutes mounts the matches read endpoints under an /api/v1 group. There is no create
// route here — a match is created only as a side effect of a room starting (rooms.Handler), never
// directly (MEMORY.md §14: rooms create matches, not clients).
func (h *Handler) RegisterRoutes(v1 *gin.RouterGroup) {
	g := v1.Group("")
	g.Use(h.authSvc.RequireAuth())

	g.GET("/matches/:id", h.get)
	g.GET("/users/:id/matches", h.listForUser)
}
