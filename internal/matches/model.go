// Package matches owns match lifecycle: matches exist as first-class entities with sides and an
// enforced state machine. No physics yet — see game/physics, built from Phase 7 (MEMORY.md §6).
//
// Layer L3 — this package may import users (L0), leaderboards/wagering... actually only L0-L2 and
// game/*, per MEMORY.md §5. It must never import rooms (L4): rooms creates matches, not the other
// way around (MEMORY.md §14).
package matches

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Mode determines how many players sit on each side. Mirrors rooms.Mode, deliberately duplicated
// rather than shared — matches cannot import rooms (L4 is above L3), and this is the second
// occurrence of the concept, not the third that would justify an abstraction (CLAUDE.md).
type Mode string

const (
	Mode1v1 Mode = "1v1"
	Mode2v2 Mode = "2v2"
)

// PlayersPerSide reports how many players belong on one side under this mode.
func (m Mode) PlayersPerSide() int {
	if m == Mode2v2 {
		return 2
	}
	return 1
}

func (m Mode) valid() bool { return m == Mode1v1 || m == Mode2v2 }

// State is a match's lifecycle position.
//
//	Waiting ──> Starting ──> InProgress ──> Completed
//	                │             ├──> Paused ──> InProgress
//	                └─────────────┴──> Cancelled | Abandoned
//
// See Transition — the one function that decides which edges are legal (MEMORY.md §13).
type State string

const (
	StateWaiting    State = "waiting"
	StateStarting   State = "starting"
	StateInProgress State = "in_progress"
	StatePaused     State = "paused"
	StateCompleted  State = "completed"
	StateCancelled  State = "cancelled"
	StateAbandoned  State = "abandoned"
)

// Terminal reports whether no further transition is ever legal from this state.
func (s State) Terminal() bool {
	return s == StateCompleted || s == StateCancelled || s == StateAbandoned
}

var ErrIllegalTransition = errors.New("illegal match state transition")

// legalTransitions is the adjacency list the state diagram above draws. Built once, checked by
// Transition — see that function's doc comment for why this is the only place that happens.
var legalTransitions = map[State]map[State]bool{
	StateWaiting: {
		StateStarting: true,
	},
	StateStarting: {
		StateInProgress: true,
		StateCancelled:  true,
		StateAbandoned:  true,
	},
	StateInProgress: {
		StateCompleted: true,
		StatePaused:    true,
		StateCancelled: true,
		StateAbandoned: true,
	},
	StatePaused: {
		StateInProgress: true,
		StateCancelled:  true,
		StateAbandoned:  true,
	},
}

// Transition is the only place a state change is validated (MEMORY.md §13, §20). Every caller —
// the actor, the repository, tests — goes through this rather than comparing states inline, so
// there is exactly one definition of "legal" to ever get out of sync.
func Transition(from, to State) error {
	if legalTransitions[from][to] {
		return nil
	}
	return ErrIllegalTransition
}

// SideID identifies one of the two sides in a match. There are always exactly two, win or lose
// together — a bye or a spectator side does not exist.
type SideID int

const (
	SideA SideID = 0
	SideB SideID = 1
)

// Side is one team. len(Players) == 1 for 1v1, == 2 for 2v2 — 1v1 and 2v2 differ only in this
// length and in the turn-advance function, never in a separate code path (MEMORY.md §14).
type Side struct {
	ID      SideID
	Players []uuid.UUID
}

// TurnRef names whose turn it is: a side, and which player on that side (meaningful only in 2v2).
type TurnRef struct {
	Side      SideID
	PlayerIdx int
}

// Match is a match's full in-memory shape — lifecycle plus the sides model. Ball state is never
// part of this: that lives only in game/state, only in the actor's memory, from Phase 7 onward
// (MEMORY.md §15).
type Match struct {
	ID               uuid.UUID
	RoomID           uuid.UUID
	State            State
	Mode             Mode
	Ranked           bool
	Ruleset          string
	ShotTimerSeconds int
	Sides            [2]Side
	Turn             *TurnRef // nil until the match starts
	TurnStartedAt    *time.Time
	StartedAt        *time.Time
	CompletedAt      *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// CreateInput is what the caller (rooms, creating a match from a full and ready room) supplies.
// Everything else — id, state, turn — is decided here.
type CreateInput struct {
	RoomID           uuid.UUID
	Mode             Mode
	Ranked           bool
	Ruleset          string
	ShotTimerSeconds int
	Sides            [2]Side
}

const (
	DefaultPageSize = 20
	MaxPageSize     = 50
)

var (
	ErrNotFound        = errors.New("match not found")
	ErrInvalidMode     = errors.New("mode must be 1v1 or 2v2")
	ErrInvalidSides    = errors.New("each side must have exactly PlayersPerSide players, no duplicates")
	ErrInvalidRuleset  = errors.New("ruleset must be 8ball")
	ErrInvalidShotTime = errors.New("shot timer must be positive")
)

// Validate reports whether in's shape is legal, without mutating it or touching the database.
// Exists mainly for tests that want to check CreateInput validation in isolation, without also
// wiring a Service — Service.CreateInTx calls normalize directly on its own copy.
func (in CreateInput) Validate() error {
	cp := in
	return cp.normalize()
}

// normalize checks in's shape and fills in defaults. It never touches the database —
// repository.go is the authority on anything that needs a query.
func (in *CreateInput) normalize() error {
	if !in.Mode.valid() {
		return ErrInvalidMode
	}
	if in.Ruleset == "" {
		in.Ruleset = "8ball"
	} else if in.Ruleset != "8ball" {
		return ErrInvalidRuleset
	}
	if in.ShotTimerSeconds <= 0 {
		return ErrInvalidShotTime
	}

	seen := make(map[uuid.UUID]bool)
	want := in.Mode.PlayersPerSide()
	for i, side := range in.Sides {
		if side.ID != SideID(i) {
			return ErrInvalidSides
		}
		if len(side.Players) != want {
			return ErrInvalidSides
		}
		for _, p := range side.Players {
			if p == uuid.Nil || seen[p] {
				return ErrInvalidSides
			}
			seen[p] = true
		}
	}
	return nil
}
