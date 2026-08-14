package rooms

import (
	"time"

	"github.com/google/uuid"
)

// detailResponse is the wire shape for a room's full detail. JoinCode is a plain *string here
// rather than filtered again: Service.Detail already refuses non-members of a private room before
// this is ever built (ErrNotFound), and a public room's JoinCode is always nil by the database's
// own CHECK constraint — there is nothing left to hide by the time a Room reaches this function.
type detailResponse struct {
	ID                uuid.UUID    `json:"id"`
	Visibility        Visibility   `json:"visibility"`
	Mode              Mode         `json:"mode"`
	Ranked            bool         `json:"ranked"`
	ShotTimerSeconds  int          `json:"shotTimerSeconds"`
	WagerAmount       int64        `json:"wagerAmount"`
	SpectatorsAllowed bool         `json:"spectatorsAllowed"`
	State             State        `json:"state"`
	HostUserID        uuid.UUID    `json:"hostUserId"`
	JoinCode          *string      `json:"joinCode,omitempty"`
	Capacity          int          `json:"capacity"`
	CreatedAt         time.Time    `json:"createdAt"`
	Members           []MemberView `json:"members"`
	// YouAre echoes the viewer's own seat back, so the client can highlight "you" without having
	// to search the members list by comparing ids itself.
	YouAre *MemberView `json:"youAre,omitempty"`
}

func toDetailResponse(d Detail, viewerID uuid.UUID) detailResponse {
	resp := detailResponse{
		ID: d.Room.ID, Visibility: d.Room.Visibility, Mode: d.Room.Mode, Ranked: d.Room.Ranked,
		ShotTimerSeconds: d.Room.ShotTimerSeconds, WagerAmount: d.Room.WagerAmount,
		SpectatorsAllowed: d.Room.SpectatorsAllowed, State: d.Room.State, HostUserID: d.Room.HostUserID,
		JoinCode: d.Room.JoinCode, Capacity: d.Room.Mode.Capacity(), CreatedAt: d.Room.CreatedAt,
		Members: d.Members,
	}
	for i := range d.Members {
		if d.Members[i].UserID == viewerID {
			resp.YouAre = &d.Members[i]
			break
		}
	}
	return resp
}
