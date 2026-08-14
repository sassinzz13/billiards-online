// Package rooms owns the pre-match lobby: players preparing to play.
//
// A room is not a match. It holds configuration and ready flags; from Phase 6 it creates a match
// and then has no further authority over it (MEMORY.md §14).
//
// Layer L4 — this package may import users (L0) and platform/*, and nothing above it. See
// MEMORY.md §5.
package rooms

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Visibility controls discovery, not entry. A public room is listed and joined by id; a private
// room is never listed and is joined only by its code (ADR-equivalent decision recorded in the
// migration 000004 comment).
type Visibility string

const (
	VisibilityPublic  Visibility = "public"
	VisibilityPrivate Visibility = "private"
)

// Mode determines how many sides and how many players per side a room holds. It is the same
// concept game/state's Side model will use from Phase 6 (MEMORY.md §14) — 1v1 is one player per
// side, 2v2 is two.
type Mode string

const (
	Mode1v1 Mode = "1v1"
	Mode2v2 Mode = "2v2"
)

// PlayersPerSide reports how many slots exist per side. seatOrder relies on this to know how many
// (side, slot) pairs to generate.
func (m Mode) PlayersPerSide() int {
	if m == Mode2v2 {
		return 2
	}
	return 1
}

// Capacity is the total number of seats in the room: two sides times PlayersPerSide.
func (m Mode) Capacity() int {
	return 2 * m.PlayersPerSide()
}

// seatOrder is the sequence a room's seats fill in, chosen to balance sides as players arrive
// rather than filling one side completely before the other. For 2v2 that means a third joiner
// starts the second side rather than stacking three players onto the first — a room with three
// members is closer to playable that way even though nothing here starts a match yet.
func (m Mode) seatOrder() [][2]int {
	switch m {
	case Mode2v2:
		return [][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}}
	default:
		return [][2]int{{0, 0}, {1, 0}}
	}
}

// State is a room's lifecycle. There is no "full" state — fullness is capacity vs member count,
// computed at read time rather than stored, so it can never drift out of sync with membership.
type State string

const (
	StateOpen   State = "open"
	StateClosed State = "closed"
)

// Room is the configuration record. Ruleset is always "8ball" through Phase 14 (MEMORY.md §25) and
// is not part of CreateInput — there is nothing yet to choose between.
type Room struct {
	ID                uuid.UUID  `db:"id"`
	Visibility        Visibility `db:"visibility"`
	Mode              Mode       `db:"mode"`
	Ranked            bool       `db:"ranked"`
	Ruleset           string     `db:"ruleset"`
	ShotTimerSeconds  int        `db:"shot_timer_seconds"`
	WagerAmount       int64      `db:"wager_amount"`
	SpectatorsAllowed bool       `db:"spectators_allowed"`
	State             State      `db:"state"`
	HostUserID        uuid.UUID  `db:"host_user_id"`
	JoinCode          *string    `db:"join_code"`
	CreatedAt         time.Time  `db:"created_at"`
	UpdatedAt         time.Time  `db:"updated_at"`
}

// Member is one occupied seat.
type Member struct {
	RoomID   uuid.UUID `db:"room_id"`
	UserID   uuid.UUID `db:"user_id"`
	Side     int       `db:"side"`
	Slot     int       `db:"slot"`
	Ready    bool      `db:"ready"`
	JoinedAt time.Time `db:"joined_at"`
}

// MemberView adds the handle a client actually wants to render, joined in from users (L0) at read
// time rather than duplicated onto room_members.
type MemberView struct {
	UserID   uuid.UUID `json:"userId"`
	Handle   string    `json:"handle"`
	Side     int       `json:"side"`
	Slot     int       `json:"slot"`
	Ready    bool      `json:"ready"`
	JoinedAt time.Time `json:"joinedAt"`
}

// Detail is the full view of one room: its configuration and everyone currently seated. JoinCode is
// populated only for the host and current members of a private room — see Service.Detail.
type Detail struct {
	Room    Room
	Members []MemberView
}

// Summary is the public discovery projection: enough to decide whether to join, nothing more. Only
// ever built from public, open rooms.
type Summary struct {
	ID                uuid.UUID `json:"id"`
	Mode              Mode      `json:"mode"`
	Ranked            bool      `json:"ranked"`
	ShotTimerSeconds  int       `json:"shotTimerSeconds"`
	WagerAmount       int64     `json:"wagerAmount"`
	SpectatorsAllowed bool      `json:"spectatorsAllowed"`
	MemberCount       int       `json:"memberCount"`
	Capacity          int       `json:"capacity"`
	CreatedAt         time.Time `json:"createdAt"`
}

// CreateInput is what a host chooses at creation. Everything else — id, host, ruleset, state — is
// decided by the service.
type CreateInput struct {
	Visibility        Visibility
	Mode              Mode
	Ranked            bool
	ShotTimerSeconds  *int   // nil = default
	WagerAmount       *int64 // nil = default (0)
	SpectatorsAllowed *bool  // nil = default (true)
}

const (
	DefaultShotTimerSeconds  = 30
	MinShotTimerSeconds      = 15
	MaxShotTimerSeconds      = 120
	DefaultSpectatorsAllowed = true

	JoinCodeLength = 8

	DefaultPageSize = 20
	MaxPageSize     = 50
)

var (
	ErrNotFound          = errors.New("room not found")
	ErrRoomClosed        = errors.New("room is closed")
	ErrRoomFull          = errors.New("room is full")
	ErrAlreadyMember     = errors.New("already a member of this room")
	ErrNotMember         = errors.New("not a member of this room")
	ErrInvalidVisibility = errors.New("visibility must be public or private")
	ErrInvalidMode       = errors.New("mode must be 1v1 or 2v2")
	ErrInvalidShotTimer  = errors.New("shot timer must be between 15 and 120 seconds")
	ErrInvalidWager      = errors.New("wager amount must not be negative")
	ErrInvalidJoinCode   = errors.New("invalid join code")
	// ErrWrongJoinPath is returned when /rooms/:id/join is used for a private room, or
	// join-by-code is used for a public one — each visibility has exactly one entry path, and
	// mixing them up is a client bug worth a specific message rather than a generic 404.
	ErrWrongJoinPath = errors.New("private rooms are joined by code; public rooms are joined by id")

	// Errors from Start — turning a room into a match (MEMORY.md §14).
	ErrNotHost     = errors.New("only the host can start the match")
	ErrRoomNotFull = errors.New("room is not full")
	ErrNotAllReady = errors.New("not every member is ready")
)

func (v Visibility) valid() bool { return v == VisibilityPublic || v == VisibilityPrivate }
func (m Mode) valid() bool       { return m == Mode1v1 || m == Mode2v2 }

// Validate checks and fills in defaults for a create request. It never touches the database —
// uniqueness of the eventual join code is the repository's job, since only it can check.
func (in *CreateInput) normalize() error {
	if !in.Visibility.valid() {
		return ErrInvalidVisibility
	}
	if !in.Mode.valid() {
		return ErrInvalidMode
	}
	if in.ShotTimerSeconds == nil {
		d := DefaultShotTimerSeconds
		in.ShotTimerSeconds = &d
	} else if *in.ShotTimerSeconds < MinShotTimerSeconds || *in.ShotTimerSeconds > MaxShotTimerSeconds {
		return ErrInvalidShotTimer
	}
	if in.WagerAmount == nil {
		var z int64
		in.WagerAmount = &z
	} else if *in.WagerAmount < 0 {
		return ErrInvalidWager
	}
	if in.SpectatorsAllowed == nil {
		d := DefaultSpectatorsAllowed
		in.SpectatorsAllowed = &d
	}
	return nil
}
