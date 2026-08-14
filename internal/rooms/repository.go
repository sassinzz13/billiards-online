package rooms

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sassinzz13/billiards-online/platform/postgres"
)

// SQL lives here as constants, next to the code that runs it — no ORM, no query builder (ADR 0002).

const qInsertRoom = `
	INSERT INTO rooms (id, visibility, mode, ranked, shot_timer_seconds, wager_amount,
	                    spectators_allowed, host_user_id, join_code)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	RETURNING id, visibility, mode, ranked, ruleset, shot_timer_seconds, wager_amount,
	          spectators_allowed, state, host_user_id, join_code, created_at, updated_at`

const qInsertMember = `
	INSERT INTO room_members (room_id, user_id, side, slot)
	VALUES ($1, $2, $3, $4)`

const roomColumns = `id, visibility, mode, ranked, ruleset, shot_timer_seconds, wager_amount,
	          spectators_allowed, state, host_user_id, join_code, created_at, updated_at`

// FOR UPDATE serializes join/leave transactions for the same room.
//
// Verified, not assumed: TestConcurrentJoinsOnTheLastSeatProduceExactlyOneWinner still passes with
// this lock removed, because room_members_seat_key's UNIQUE(room_id, side, slot) index is what
// actually guarantees "exactly one winner" — Postgres serializes the conflicting INSERTs on that
// index regardless of any lock taken on the parent row. The unique constraint is the real
// enforcement of the capacity invariant, exactly as §10 asks for: a database constraint, not a Go
// `if`. The lock earns its place for two other reasons: it turns what would otherwise be N-1 doomed
// INSERT attempts into a clean, immediate ErrRoomFull for the transaction that loses the race, and
// it is genuinely required — with no unique-constraint backstop — for Leave's host-handoff, which
// reads current members and then conditionally writes a new host across two statements.
const qRoomForUpdate = `SELECT ` + roomColumns + ` FROM rooms WHERE id = $1 FOR UPDATE`

const qRoomByJoinCodeForUpdate = `
	SELECT ` + roomColumns + `
	FROM rooms
	WHERE join_code = $1 AND state = 'open'
	FOR UPDATE`

const qRoom = `SELECT ` + roomColumns + ` FROM rooms WHERE id = $1`

const qMembersByRoom = `
	SELECT room_id, user_id, side, slot, ready, joined_at
	FROM room_members
	WHERE room_id = $1
	ORDER BY side, slot`

const qMemberViewsByRoom = `
	SELECT m.user_id, u.handle, m.side, m.slot, m.ready, m.joined_at
	FROM room_members m
	JOIN users u ON u.id = m.user_id
	WHERE m.room_id = $1
	ORDER BY m.side, m.slot`

const qPublicOpenRooms = `
	SELECT r.id, r.mode, r.ranked, r.shot_timer_seconds, r.wager_amount, r.spectators_allowed,
	       r.created_at,
	       (SELECT count(*) FROM room_members m WHERE m.room_id = r.id) AS member_count
	FROM rooms r
	WHERE r.visibility = 'public' AND r.state = 'open'
	  AND (r.created_at, r.id) < ($1, $2)
	ORDER BY r.created_at DESC, r.id DESC
	LIMIT $3`

// A cursor-free first page. $1/$2 in qPublicOpenRooms need a value even when there is no cursor;
// this sentinel is later than any real room ever will be, so "< sentinel" matches everything.
const qPublicOpenRoomsFirstPage = `
	SELECT r.id, r.mode, r.ranked, r.shot_timer_seconds, r.wager_amount, r.spectators_allowed,
	       r.created_at,
	       (SELECT count(*) FROM room_members m WHERE m.room_id = r.id) AS member_count
	FROM rooms r
	WHERE r.visibility = 'public' AND r.state = 'open'
	ORDER BY r.created_at DESC, r.id DESC
	LIMIT $1`

const qUpdateMemberReady = `
	UPDATE room_members
	SET ready = $3
	WHERE room_id = $1 AND user_id = $2
	RETURNING room_id, user_id, side, slot, ready, joined_at`

const qDeleteMember = `DELETE FROM room_members WHERE room_id = $1 AND user_id = $2`

const qSetHost = `UPDATE rooms SET host_user_id = $2, updated_at = now() WHERE id = $1`

const qCloseRoom = `UPDATE rooms SET state = 'closed', updated_at = now() WHERE id = $1`

const (
	constraintJoinCodeUnique = "rooms_join_code_key"
	constraintSeatUnique     = "room_members_seat_key"
)

// joinCodeAlphabet excludes visually ambiguous characters (0/O, 1/I/L) — a code is read off one
// screen and typed into another, often by hand.
const joinCodeAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// generateJoinCode returns a random capability token, not merely a unique one. A join code is what
// actually guards entry to a private room (migration 000004's rooms_join_code_matches_visibility
// constraint), so it must be unpredictable — crypto/rand, not math/rand.
func generateJoinCode() (string, error) {
	b := make([]byte, JoinCodeLength)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate join code: %w", err)
	}
	code := make([]byte, JoinCodeLength)
	for i, v := range b {
		code[i] = joinCodeAlphabet[int(v)%len(joinCodeAlphabet)]
	}
	return string(code), nil
}

func insertRoom(ctx context.Context, db postgres.DB, id uuid.UUID, in CreateInput, hostID uuid.UUID, joinCode *string) (Room, error) {
	rows, err := db.Query(ctx, qInsertRoom,
		id, in.Visibility, in.Mode, in.Ranked, *in.ShotTimerSeconds, *in.WagerAmount,
		*in.SpectatorsAllowed, hostID, joinCode,
	)
	if err != nil {
		return Room{}, mapRoomError(err)
	}
	return pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Room])
}

func insertMember(ctx context.Context, db postgres.DB, roomID, userID uuid.UUID, side, slot int) error {
	if _, err := db.Exec(ctx, qInsertMember, roomID, userID, side, slot); err != nil {
		return mapRoomError(err)
	}
	return nil
}

func selectRoomForUpdate(ctx context.Context, db postgres.DB, roomID uuid.UUID) (Room, error) {
	return scanRoom(db.Query(ctx, qRoomForUpdate, roomID))
}

func selectRoomByJoinCodeForUpdate(ctx context.Context, db postgres.DB, code string) (Room, error) {
	return scanRoom(db.Query(ctx, qRoomByJoinCodeForUpdate, code))
}

func selectRoom(ctx context.Context, db postgres.DB, roomID uuid.UUID) (Room, error) {
	return scanRoom(db.Query(ctx, qRoom, roomID))
}

func scanRoom(rows pgx.Rows, err error) (Room, error) {
	if err != nil {
		return Room{}, fmt.Errorf("query room: %w", err)
	}
	r, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Room])
	if errors.Is(err, pgx.ErrNoRows) {
		return Room{}, ErrNotFound
	}
	if err != nil {
		return Room{}, fmt.Errorf("scan room: %w", err)
	}
	return r, nil
}

func selectMembers(ctx context.Context, db postgres.DB, roomID uuid.UUID) ([]Member, error) {
	rows, err := db.Query(ctx, qMembersByRoom, roomID)
	if err != nil {
		return nil, fmt.Errorf("query members: %w", err)
	}
	members, err := pgx.CollectRows(rows, pgx.RowToStructByName[Member])
	if err != nil {
		return nil, fmt.Errorf("scan members: %w", err)
	}
	return members, nil
}

func selectMemberViews(ctx context.Context, db postgres.DB, roomID uuid.UUID) ([]MemberView, error) {
	rows, err := db.Query(ctx, qMemberViewsByRoom, roomID)
	if err != nil {
		return nil, fmt.Errorf("query member views: %w", err)
	}
	views, err := pgx.CollectRows(rows, pgx.RowToStructByName[MemberView])
	if err != nil {
		return nil, fmt.Errorf("scan member views: %w", err)
	}
	// CollectRows returns an empty, non-nil slice for zero rows, which is what a JSON response
	// should serialize as `[]` rather than `null`.
	if views == nil {
		views = []MemberView{}
	}
	return views, nil
}

// summaryRow mirrors qPublicOpenRooms / qPublicOpenRoomsFirstPage. Capacity is not selected — it is
// a pure function of Mode, computed in Go rather than duplicated into SQL.
type summaryRow struct {
	ID                uuid.UUID `db:"id"`
	Mode              Mode      `db:"mode"`
	Ranked            bool      `db:"ranked"`
	ShotTimerSeconds  int       `db:"shot_timer_seconds"`
	WagerAmount       int64     `db:"wager_amount"`
	SpectatorsAllowed bool      `db:"spectators_allowed"`
	CreatedAt         time.Time `db:"created_at"`
	MemberCount       int       `db:"member_count"`
}

func (r summaryRow) toSummary() Summary {
	return Summary{
		ID: r.ID, Mode: r.Mode, Ranked: r.Ranked, ShotTimerSeconds: r.ShotTimerSeconds,
		WagerAmount: r.WagerAmount, SpectatorsAllowed: r.SpectatorsAllowed,
		MemberCount: r.MemberCount, Capacity: r.Mode.Capacity(), CreatedAt: r.CreatedAt,
	}
}

func selectPublicOpenRooms(ctx context.Context, db postgres.DB, cursor *pageCursor, limit int) ([]Summary, error) {
	var rows pgx.Rows
	var err error
	if cursor == nil {
		rows, err = db.Query(ctx, qPublicOpenRoomsFirstPage, limit)
	} else {
		rows, err = db.Query(ctx, qPublicOpenRooms, cursor.CreatedAt, cursor.ID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("query rooms: %w", err)
	}
	found, err := pgx.CollectRows(rows, pgx.RowToStructByName[summaryRow])
	if err != nil {
		return nil, fmt.Errorf("scan rooms: %w", err)
	}
	out := make([]Summary, len(found))
	for i, r := range found {
		out[i] = r.toSummary()
	}
	return out, nil
}

func updateMemberReady(ctx context.Context, db postgres.DB, roomID, userID uuid.UUID, ready bool) (Member, error) {
	rows, err := db.Query(ctx, qUpdateMemberReady, roomID, userID, ready)
	if err != nil {
		return Member{}, fmt.Errorf("update ready: %w", err)
	}
	m, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Member])
	if errors.Is(err, pgx.ErrNoRows) {
		return Member{}, ErrNotMember
	}
	if err != nil {
		return Member{}, fmt.Errorf("scan ready update: %w", err)
	}
	return m, nil
}

func deleteMember(ctx context.Context, db postgres.DB, roomID, userID uuid.UUID) (bool, error) {
	tag, err := db.Exec(ctx, qDeleteMember, roomID, userID)
	if err != nil {
		return false, fmt.Errorf("delete member: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func setHost(ctx context.Context, db postgres.DB, roomID, newHostID uuid.UUID) error {
	if _, err := db.Exec(ctx, qSetHost, roomID, newHostID); err != nil {
		return fmt.Errorf("set host: %w", err)
	}
	return nil
}

func closeRoom(ctx context.Context, db postgres.DB, roomID uuid.UUID) error {
	if _, err := db.Exec(ctx, qCloseRoom, roomID); err != nil {
		return fmt.Errorf("close room: %w", err)
	}
	return nil
}

// mapRoomError converts constraint violations into domain errors, the same pattern used by
// internal/users and internal/auth (§10): let the database be the authority on uniqueness, then
// translate.
func mapRoomError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		switch pgErr.ConstraintName {
		case constraintJoinCodeUnique:
			// Not exported as a distinct sentinel: the service retries with a fresh code and the
			// caller never sees this. See Service.Create.
			return errJoinCodeCollision
		case constraintSeatUnique:
			// Should be unreachable given the row lock in every mutating path — reaching it means
			// two transactions both believed the same seat was free, which the lock exists
			// specifically to prevent. Surfacing it as ErrRoomFull is still the correct user-facing
			// outcome if it ever somehow happens.
			return ErrRoomFull
		}
	}
	return fmt.Errorf("rooms repository: %w", err)
}

var errJoinCodeCollision = errors.New("join code collision")
