package rooms

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sassinzz13/billiards-online/internal/matches"
	"github.com/sassinzz13/billiards-online/platform/postgres"
)

// Service is the entry point to the rooms feature.
//
// Member handles are read via a SQL join against users (qMemberViewsByRoom in repository.go)
// rather than a per-member call into users.Service. Both are legal — users is L0, rooms is L4, so
// either direction of dependency is downward — but the join is one round trip instead of N, which
// is what §10 asks for. This is a read-only join across two features' tables; it never writes
// through anything but each feature's own repository, so §35's "a feature never writes another
// feature's tables" still holds. Recorded here because it is a deliberate reading of that rule, not
// an oversight.
type Service struct {
	db      postgres.DB
	matches *matches.Service
}

func NewService(db postgres.DB, matchesSvc *matches.Service) *Service {
	return &Service{db: db, matches: matchesSvc}
}

// WithDB returns a Service bound to a different DB — see users.Service.WithDB for why this exists.
func (s *Service) WithDB(db postgres.DB) *Service {
	return &Service{db: db, matches: s.matches}
}

// Create opens a room and seats the host in the first slot.
//
// A private room's join code is generated before the insert and regenerated on the rare occasion
// two rooms would collide (astronomically unlikely at 8 characters from a 32-symbol alphabet, but
// the database is still the authority — see mapRoomError). Each attempt is its own call to
// postgres.InTx: a failed INSERT aborts whatever transaction it ran in, so retrying must open a
// fresh one rather than reuse a poisoned one.
func (s *Service) Create(ctx context.Context, hostID uuid.UUID, in CreateInput) (Detail, error) {
	if err := in.normalize(); err != nil {
		return Detail{}, err
	}

	id, err := uuid.NewV7()
	if err != nil {
		return Detail{}, fmt.Errorf("generate room id: %w", err)
	}

	const maxAttempts = 3
	var room Room
	for attempt := 0; attempt < maxAttempts; attempt++ {
		var joinCode *string
		if in.Visibility == VisibilityPrivate {
			code, genErr := generateJoinCode()
			if genErr != nil {
				return Detail{}, genErr
			}
			joinCode = &code
		}

		err = postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
			r, insertErr := insertRoom(ctx, tx, id, in, hostID, joinCode)
			if insertErr != nil {
				return insertErr
			}
			room = r
			return insertMember(ctx, tx, room.ID, hostID, 0, 0)
		})
		if !errors.Is(err, errJoinCodeCollision) {
			break
		}
	}
	if err != nil {
		return Detail{}, err
	}

	return s.Detail(ctx, room.ID, hostID)
}

// Join seats userID in a public room. Private rooms are joined exclusively through JoinByCode —
// see ErrWrongJoinPath and migration 000004's comment on why the code, not the id, is what guards
// entry to a private room.
func (s *Service) Join(ctx context.Context, roomID, userID uuid.UUID) (Detail, error) {
	err := postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		room, err := selectRoomForUpdate(ctx, tx, roomID)
		if err != nil {
			return err
		}
		if room.Visibility != VisibilityPublic {
			return ErrWrongJoinPath
		}
		return seatMember(ctx, tx, room, userID)
	})
	if err != nil {
		return Detail{}, err
	}
	return s.Detail(ctx, roomID, userID)
}

// JoinByCode seats userID in whichever private room the code belongs to — the only entry path for
// a private room, so the caller never needs to know its id.
func (s *Service) JoinByCode(ctx context.Context, code string, userID uuid.UUID) (Detail, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return Detail{}, ErrInvalidJoinCode
	}

	var roomID uuid.UUID
	err := postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		room, err := selectRoomByJoinCodeForUpdate(ctx, tx, code)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				// A code that matches nothing (wrong, expired-by-closure, or never existed) is a
				// distinct client mistake from an unknown room id — worth its own message.
				return ErrInvalidJoinCode
			}
			return err
		}
		roomID = room.ID
		return seatMember(ctx, tx, room, userID)
	})
	if err != nil {
		return Detail{}, err
	}
	return s.Detail(ctx, roomID, userID)
}

// seatMember assigns the first open seat under room's already-held FOR UPDATE lock. Called from
// both join paths so the seat-selection logic exists exactly once.
func seatMember(ctx context.Context, tx pgx.Tx, room Room, userID uuid.UUID) error {
	if room.State != StateOpen {
		return ErrRoomClosed
	}

	members, err := selectMembers(ctx, tx, room.ID)
	if err != nil {
		return err
	}

	occupied := make(map[[2]int]bool, len(members))
	for _, m := range members {
		if m.UserID == userID {
			return ErrAlreadyMember
		}
		occupied[[2]int{m.Side, m.Slot}] = true
	}

	for _, seat := range room.Mode.seatOrder() {
		if !occupied[seat] {
			return insertMember(ctx, tx, room.ID, userID, seat[0], seat[1])
		}
	}
	return ErrRoomFull
}

// Leave removes userID from the room. If they were the last member, the room closes; if they were
// the host and others remain, the earliest-joined remaining member becomes the new host.
func (s *Service) Leave(ctx context.Context, roomID, userID uuid.UUID) error {
	return postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		room, err := selectRoomForUpdate(ctx, tx, roomID)
		if err != nil {
			return err
		}

		deleted, err := deleteMember(ctx, tx, roomID, userID)
		if err != nil {
			return err
		}
		if !deleted {
			return ErrNotMember
		}

		remaining, err := selectMembers(ctx, tx, roomID)
		if err != nil {
			return err
		}

		if len(remaining) == 0 {
			return closeRoom(ctx, tx, roomID)
		}
		if room.HostUserID != userID {
			return nil
		}

		next := remaining[0]
		for _, m := range remaining[1:] {
			if m.JoinedAt.Before(next.JoinedAt) {
				next = m
			}
		}
		return setHost(ctx, tx, roomID, next.UserID)
	})
}

// SetReady toggles the caller's own ready flag. A single UPDATE keyed on the (room_id, user_id)
// primary key is already atomic; no explicit transaction or row lock is needed for a lone,
// idempotent flag flip.
func (s *Service) SetReady(ctx context.Context, roomID, userID uuid.UUID, ready bool) (Detail, error) {
	if _, err := updateMemberReady(ctx, s.db, roomID, userID, ready); err != nil {
		return Detail{}, err
	}
	return s.Detail(ctx, roomID, userID)
}

// Detail returns a room's configuration and current members, from viewerID's perspective.
//
// A private room whose viewer is not a member returns ErrNotFound rather than a 403 — confirming
// that a given id belongs to a private room at all is itself information an outsider should not
// get, the same reasoning as ADR 0009's exact-match Origin check: don't confirm what you don't have
// to.
func (s *Service) Detail(ctx context.Context, roomID, viewerID uuid.UUID) (Detail, error) {
	room, err := selectRoom(ctx, s.db, roomID)
	if err != nil {
		return Detail{}, err
	}

	views, err := selectMemberViews(ctx, s.db, roomID)
	if err != nil {
		return Detail{}, err
	}

	if room.Visibility == VisibilityPrivate {
		isMember := false
		for _, v := range views {
			if v.UserID == viewerID {
				isMember = true
				break
			}
		}
		if !isMember {
			return Detail{}, ErrNotFound
		}
	}

	return Detail{Room: room, Members: views}, nil
}

// Start turns a full, fully-ready room into a match — the "Room → match creation, in one
// transaction" step MEMORY.md §14 and PLAN.md's Phase 6 checklist call for. Closing the room and
// inserting the match's rows happen in the same transaction so a crash between them can never leave
// a room open with a match that does not exist, or a match whose room never actually closed.
//
// The room's authority ends here (§14): once this returns, s.matches owns the result completely,
// and this function's caller only sees the match id.
func (s *Service) Start(ctx context.Context, roomID, userID uuid.UUID) (matches.Match, error) {
	var match matches.Match
	err := postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		room, err := selectRoomForUpdate(ctx, tx, roomID)
		if err != nil {
			return err
		}
		if room.HostUserID != userID {
			return ErrNotHost
		}
		if room.State != StateOpen {
			return ErrRoomClosed
		}

		members, err := selectMembers(ctx, tx, roomID)
		if err != nil {
			return err
		}
		if len(members) != room.Mode.Capacity() {
			return ErrRoomNotFull
		}
		for _, m := range members {
			if !m.Ready {
				return ErrNotAllReady
			}
		}

		sides, err := buildMatchSides(room.Mode, members)
		if err != nil {
			return err
		}

		m, err := s.matches.CreateInTx(ctx, tx, matches.CreateInput{
			RoomID:           room.ID,
			Mode:             toMatchMode(room.Mode),
			Ranked:           room.Ranked,
			Ruleset:          room.Ruleset,
			ShotTimerSeconds: room.ShotTimerSeconds,
			Sides:            sides,
		})
		if err != nil {
			return err
		}
		match = m

		return closeRoom(ctx, tx, roomID)
	})
	if err != nil {
		return matches.Match{}, err
	}

	// Spawned only after the transaction above has actually committed — see
	// matches.Service.CreateInTx's doc comment for why an actor must never run against a match that
	// might still be rolled back.
	s.matches.StartActor(match)
	return match, nil
}

// buildMatchSides re-groups a room's members (side, slot, user id) into matches.Side's shape.
// Start has already checked the room is exactly at capacity, so every (side, slot) pair implied by
// mode is guaranteed to be occupied — the same invariant room_members_seat_key enforces in the
// database.
func buildMatchSides(mode Mode, members []Member) ([2]matches.Side, error) {
	var sides [2]matches.Side
	sides[0].ID, sides[1].ID = matches.SideA, matches.SideB
	perSide := mode.PlayersPerSide()
	sides[0].Players = make([]uuid.UUID, perSide)
	sides[1].Players = make([]uuid.UUID, perSide)

	for _, m := range members {
		if m.Side < 0 || m.Side > 1 || m.Slot < 0 || m.Slot >= perSide {
			return sides, fmt.Errorf("room member seat (%d,%d) out of range for mode %s", m.Side, m.Slot, mode)
		}
		sides[m.Side].Players[m.Slot] = m.UserID
	}
	return sides, nil
}

func toMatchMode(m Mode) matches.Mode {
	if m == Mode2v2 {
		return matches.Mode2v2
	}
	return matches.Mode1v1
}

// ListPublicOpen returns one page of public, joinable rooms plus the cursor for the next page
// (empty when this was the last page). cursorStr is opaque and produced by a previous call's
// nextCursor — see pagination.go.
func (s *Service) ListPublicOpen(ctx context.Context, cursorStr string, limit int) (items []Summary, nextCursor string, err error) {
	if limit <= 0 || limit > MaxPageSize {
		limit = DefaultPageSize
	}

	cursor, err := decodeCursor(cursorStr)
	if err != nil {
		return nil, "", err
	}

	// One extra row than requested reveals whether a next page exists without a second round trip
	// or a COUNT(*) query.
	fetched, err := selectPublicOpenRooms(ctx, s.db, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}

	if len(fetched) > limit {
		last := fetched[limit-1]
		nextCursor = EncodeCursor(last.CreatedAt, last.ID)
		fetched = fetched[:limit]
	}
	return fetched, nextCursor, nil
}
