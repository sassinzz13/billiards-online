-- Owner: internal/matches (L3)
--
-- A match is a first-class entity distinct from the room that created it (MEMORY.md §14): once
-- created, a room has no further authority over it. Live gameplay state (ball positions) never
-- touches Postgres (MEMORY.md §15) — this schema holds only lifecycle and identity, never physics.

CREATE TABLE matches (
    id                  uuid        PRIMARY KEY,
    -- Not a foreign key to rooms: matches is L3, rooms is L4, and an L3 table must not reference an
    -- L4 table (that would make matches' schema depend on rooms existing, upside down from the
    -- import direction in MEMORY.md §5). The association is still queryable — just not enforced by
    -- the database — which is fine because a room row cannot be deleted out from under its
    -- membership (room deletion is not implemented), and a stale room_id here does not corrupt
    -- anything a match itself needs to function.
    room_id             uuid        NOT NULL,

    state               text        NOT NULL DEFAULT 'waiting',
    mode                text        NOT NULL,
    ranked              boolean     NOT NULL DEFAULT false,
    ruleset             text        NOT NULL DEFAULT '8ball',
    shot_timer_seconds  integer     NOT NULL,

    -- Whose turn it is. NULL until the match actually starts. Advanced only by the match actor —
    -- see matches.Transition and matches/turn.go; nothing else may compute this (MEMORY.md §14).
    turn_side           smallint,
    turn_player_idx     smallint,
    turn_started_at     timestamptz,

    started_at          timestamptz,
    completed_at        timestamptz,

    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT matches_state_valid   CHECK (state IN ('waiting', 'starting', 'in_progress', 'paused', 'completed', 'cancelled', 'abandoned')),
    CONSTRAINT matches_mode_valid    CHECK (mode IN ('1v1', '2v2')),
    CONSTRAINT matches_ruleset_valid CHECK (ruleset = '8ball'),
    CONSTRAINT matches_turn_side_valid CHECK (turn_side IS NULL OR turn_side IN (0, 1)),
    CONSTRAINT matches_shot_timer_positive CHECK (shot_timer_seconds > 0)
);

-- Powers GET /users/:id/matches: every match a user has ever played, newest first.
CREATE INDEX matches_room_idx ON matches (room_id);

-- A side is a first-class row, not just a column value on match_participants, because a side will
-- carry data no individual participant owns — ball group (stripes/solids) and score from Phase 11
-- onward. Kept minimal now (§72): the column exists so that phase is a data change, not a migration.
CREATE TABLE match_sides (
    match_id  uuid     NOT NULL REFERENCES matches (id) ON DELETE CASCADE,
    side      smallint NOT NULL,
    score     smallint NOT NULL DEFAULT 0,

    PRIMARY KEY (match_id, side),
    CONSTRAINT match_sides_side_valid CHECK (side IN (0, 1))
);

CREATE TABLE match_participants (
    match_id  uuid     NOT NULL REFERENCES matches (id) ON DELETE CASCADE,
    user_id   uuid     NOT NULL REFERENCES users (id) ON DELETE CASCADE,

    -- len 1 per side = 1v1, len 2 per side = 2v2 — the same Side model rooms uses (MEMORY.md §14).
    side      smallint NOT NULL,
    slot      smallint NOT NULL,

    PRIMARY KEY (match_id, user_id),
    CONSTRAINT match_participants_side_valid CHECK (side IN (0, 1)),
    CONSTRAINT match_participants_slot_valid CHECK (slot IN (0, 1))
);

-- One participant per (side, slot): the database enforcement of "at most two players per side",
-- the same pattern room_members_seat_key uses for rooms.
CREATE UNIQUE INDEX match_participants_seat_key ON match_participants (match_id, side, slot);
CREATE INDEX match_participants_user_idx ON match_participants (user_id);

-- Populated from Phase 9 onward, once shots exist to record (MEMORY.md §9, §15): one row per shot,
-- written after the shot resolves, off the simulation path. The table is created now so its owning
-- phase does not need its own migration.
CREATE TABLE match_events (
    id         bigserial   PRIMARY KEY,
    match_id   uuid        NOT NULL REFERENCES matches (id) ON DELETE CASCADE,
    seq        bigint      NOT NULL,
    type       text        NOT NULL,
    payload    jsonb       NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),

    CONSTRAINT match_events_seq_positive CHECK (seq > 0)
);

CREATE UNIQUE INDEX match_events_match_seq_key ON match_events (match_id, seq);
