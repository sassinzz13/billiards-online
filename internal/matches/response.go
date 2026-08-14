package matches

import (
	"time"

	"github.com/google/uuid"
)

// sideResponse is the wire shape of one Side.
type sideResponse struct {
	Players []uuid.UUID `json:"players"`
}

// turnResponse mirrors TurnRef; omitted entirely (via the pointer in matchResponse) until the
// match has actually started.
type turnResponse struct {
	Side      SideID `json:"side"`
	PlayerIdx int    `json:"playerIdx"`
}

type matchResponse struct {
	ID               uuid.UUID       `json:"id"`
	RoomID           uuid.UUID       `json:"roomId"`
	State            State           `json:"state"`
	Mode             Mode            `json:"mode"`
	Ranked           bool            `json:"ranked"`
	Ruleset          string          `json:"ruleset"`
	ShotTimerSeconds int             `json:"shotTimerSeconds"`
	Sides            [2]sideResponse `json:"sides"`
	Turn             *turnResponse   `json:"turn,omitempty"`
	StartedAt        *time.Time      `json:"startedAt,omitempty"`
	CompletedAt      *time.Time      `json:"completedAt,omitempty"`
	CreatedAt        time.Time       `json:"createdAt"`
}

func toMatchResponse(m Match) matchResponse {
	resp := matchResponse{
		ID: m.ID, RoomID: m.RoomID, State: m.State, Mode: m.Mode, Ranked: m.Ranked,
		Ruleset: m.Ruleset, ShotTimerSeconds: m.ShotTimerSeconds,
		StartedAt: m.StartedAt, CompletedAt: m.CompletedAt, CreatedAt: m.CreatedAt,
	}
	for i, side := range m.Sides {
		players := side.Players
		if players == nil {
			players = []uuid.UUID{}
		}
		resp.Sides[i] = sideResponse{Players: players}
	}
	if m.Turn != nil {
		resp.Turn = &turnResponse{Side: m.Turn.Side, PlayerIdx: m.Turn.PlayerIdx}
	}
	return resp
}
