package matches

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sassinzz13/billiards-online/platform/postgres"
)

// SQL lives here as constants next to the code that runs it — no ORM, no query builder (ADR 0002).

const matchColumns = `id, room_id, state, mode, ranked, ruleset, shot_timer_seconds,
	          turn_side, turn_player_idx, turn_started_at, started_at, completed_at,
	          created_at, updated_at`

const qInsertMatch = `
	INSERT INTO matches (id, room_id, state, mode, ranked, ruleset, shot_timer_seconds)
	VALUES ($1, $2, $3, $4, $5, $6, $7)
	RETURNING ` + matchColumns

const qInsertSide = `INSERT INTO match_sides (match_id, side) VALUES ($1, $2)`

const qInsertParticipant = `
	INSERT INTO match_participants (match_id, user_id, side, slot)
	VALUES ($1, $2, $3, $4)`

const qMatch = `SELECT ` + matchColumns + ` FROM matches WHERE id = $1`

const qMatchForUpdate = `SELECT ` + matchColumns + ` FROM matches WHERE id = $1 FOR UPDATE`

const qUpdateMatchState = `
	UPDATE matches
	SET state = $2, started_at = $3, completed_at = $4, updated_at = now()
	WHERE id = $1`

const qUpdateMatchTurn = `
	UPDATE matches
	SET turn_side = $2, turn_player_idx = $3, turn_started_at = $4, updated_at = now()
	WHERE id = $1`

const qParticipantsByMatch = `
	SELECT user_id, side, slot
	FROM match_participants
	WHERE match_id = $1
	ORDER BY side, slot`

const qMatchesByUser = `
	SELECT m.id, m.room_id, m.state, m.mode, m.ranked, m.ruleset, m.shot_timer_seconds,
	       m.turn_side, m.turn_player_idx, m.turn_started_at, m.started_at, m.completed_at,
	       m.created_at, m.updated_at
	FROM matches m
	JOIN match_participants p ON p.match_id = m.id
	WHERE p.user_id = $1 AND (m.created_at, m.id) < ($2, $3)
	ORDER BY m.created_at DESC, m.id DESC
	LIMIT $4`

const qMatchesByUserFirstPage = `
	SELECT m.id, m.room_id, m.state, m.mode, m.ranked, m.ruleset, m.shot_timer_seconds,
	       m.turn_side, m.turn_player_idx, m.turn_started_at, m.started_at, m.completed_at,
	       m.created_at, m.updated_at
	FROM matches m
	JOIN match_participants p ON p.match_id = m.id
	WHERE p.user_id = $1
	ORDER BY m.created_at DESC, m.id DESC
	LIMIT $2`

// matchRow mirrors the matches table exactly, scanned with pgx.RowToStructByName and then
// translated to the domain Match by toMatch — kept as a distinct type because the table's
// turn_side/turn_player_idx pair collapses into Match.Turn's single nullable *TurnRef, a shape SQL
// has no direct way to produce.
type matchRow struct {
	ID               uuid.UUID  `db:"id"`
	RoomID           uuid.UUID  `db:"room_id"`
	State            State      `db:"state"`
	Mode             Mode       `db:"mode"`
	Ranked           bool       `db:"ranked"`
	Ruleset          string     `db:"ruleset"`
	ShotTimerSeconds int        `db:"shot_timer_seconds"`
	TurnSide         *int16     `db:"turn_side"`
	TurnPlayerIdx    *int16     `db:"turn_player_idx"`
	TurnStartedAt    *time.Time `db:"turn_started_at"`
	StartedAt        *time.Time `db:"started_at"`
	CompletedAt      *time.Time `db:"completed_at"`
	CreatedAt        time.Time  `db:"created_at"`
	UpdatedAt        time.Time  `db:"updated_at"`
}

func (r matchRow) toMatch() Match {
	m := Match{
		ID: r.ID, RoomID: r.RoomID, State: r.State, Mode: r.Mode, Ranked: r.Ranked,
		Ruleset: r.Ruleset, ShotTimerSeconds: r.ShotTimerSeconds,
		TurnStartedAt: r.TurnStartedAt, StartedAt: r.StartedAt, CompletedAt: r.CompletedAt,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		Sides: [2]Side{{ID: SideA}, {ID: SideB}},
	}
	if r.TurnSide != nil && r.TurnPlayerIdx != nil {
		m.Turn = &TurnRef{Side: SideID(*r.TurnSide), PlayerIdx: int(*r.TurnPlayerIdx)}
	}
	return m
}

func insertMatch(ctx context.Context, db postgres.DB, id uuid.UUID, in CreateInput) (Match, error) {
	rows, err := db.Query(ctx, qInsertMatch, id, in.RoomID, StateWaiting, in.Mode, in.Ranked, in.Ruleset, in.ShotTimerSeconds)
	if err != nil {
		return Match{}, fmt.Errorf("insert match: %w", err)
	}
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[matchRow])
	if err != nil {
		return Match{}, fmt.Errorf("scan inserted match: %w", err)
	}
	match := row.toMatch()

	for _, side := range in.Sides {
		if _, err := db.Exec(ctx, qInsertSide, id, side.ID); err != nil {
			return Match{}, fmt.Errorf("insert side: %w", err)
		}
		for slot, userID := range side.Players {
			if _, err := db.Exec(ctx, qInsertParticipant, id, userID, side.ID, slot); err != nil {
				return Match{}, fmt.Errorf("insert participant: %w", err)
			}
		}
	}
	match.Sides = in.Sides
	return match, nil
}

func selectMatch(ctx context.Context, db postgres.DB, id uuid.UUID) (Match, error) {
	return scanMatch(ctx, db, qMatch, id)
}

func selectMatchForUpdate(ctx context.Context, db postgres.DB, id uuid.UUID) (Match, error) {
	return scanMatch(ctx, db, qMatchForUpdate, id)
}

func scanMatch(ctx context.Context, db postgres.DB, query string, id uuid.UUID) (Match, error) {
	rows, err := db.Query(ctx, query, id)
	if err != nil {
		return Match{}, fmt.Errorf("query match: %w", err)
	}
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[matchRow])
	if errors.Is(err, pgx.ErrNoRows) {
		return Match{}, ErrNotFound
	}
	if err != nil {
		return Match{}, fmt.Errorf("scan match: %w", err)
	}
	match := row.toMatch()

	participants, err := selectParticipants(ctx, db, id)
	if err != nil {
		return Match{}, err
	}
	for _, p := range participants {
		match.Sides[p.Side].Players = append(match.Sides[p.Side].Players, p.UserID)
	}
	return match, nil
}

type participantRow struct {
	UserID uuid.UUID
	Side   SideID
	Slot   int
}

func selectParticipants(ctx context.Context, db postgres.DB, matchID uuid.UUID) ([]participantRow, error) {
	rows, err := db.Query(ctx, qParticipantsByMatch, matchID)
	if err != nil {
		return nil, fmt.Errorf("query participants: %w", err)
	}
	defer rows.Close()

	var out []participantRow
	for rows.Next() {
		var p participantRow
		if err := rows.Scan(&p.UserID, &p.Side, &p.Slot); err != nil {
			return nil, fmt.Errorf("scan participant: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func updateMatchState(ctx context.Context, db postgres.DB, id uuid.UUID, state State, startedAt, completedAt *time.Time) error {
	if _, err := db.Exec(ctx, qUpdateMatchState, id, state, startedAt, completedAt); err != nil {
		return fmt.Errorf("update match state: %w", err)
	}
	return nil
}

func updateMatchTurn(ctx context.Context, db postgres.DB, id uuid.UUID, turn TurnRef, startedAt time.Time) error {
	if _, err := db.Exec(ctx, qUpdateMatchTurn, id, turn.Side, turn.PlayerIdx, startedAt); err != nil {
		return fmt.Errorf("update match turn: %w", err)
	}
	return nil
}

func selectMatchesByUser(ctx context.Context, db postgres.DB, userID uuid.UUID, cursor *pageCursor, limit int) ([]Match, error) {
	var rows pgx.Rows
	var err error
	if cursor == nil {
		rows, err = db.Query(ctx, qMatchesByUserFirstPage, userID, limit)
	} else {
		rows, err = db.Query(ctx, qMatchesByUser, userID, cursor.CreatedAt, cursor.ID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("query matches by user: %w", err)
	}
	found, err := pgx.CollectRows(rows, pgx.RowToStructByName[matchRow])
	if err != nil {
		return nil, fmt.Errorf("scan matches by user: %w", err)
	}

	out := make([]Match, len(found))
	for i, r := range found {
		m := r.toMatch()
		participants, err := selectParticipants(ctx, db, m.ID)
		if err != nil {
			return nil, err
		}
		for _, p := range participants {
			m.Sides[p.Side].Players = append(m.Sides[p.Side].Players, p.UserID)
		}
		out[i] = m
	}
	return out, nil
}
