-- Owner: internal/rooms (L4)
--
-- A room is players preparing to play — not a match. It holds configuration and ready flags,
-- and (from Phase 6) creates a match; it has no authority beyond that (MEMORY.md §14).
--
-- Only two lifecycle states exist: open and closed. There is deliberately no stored "full" state —
-- fullness is capacity vs COUNT(room_members), computed at read time. A stored flag would have to
-- be kept in sync with every join and leave, and any missed update would be a silent bug. Computing
-- it removes that whole class of bug (§10).

CREATE TABLE rooms (
    id                  uuid        PRIMARY KEY,
    visibility          text        NOT NULL,
    mode                text        NOT NULL,
    ranked              boolean     NOT NULL DEFAULT false,

    -- 8-ball is the only ruleset through Phase 14 (MEMORY.md §25). The column exists now so a
    -- future ruleset is a data change, not a migration; the API does not expose it as configurable
    -- input yet because there is nothing to choose between.
    ruleset             text        NOT NULL DEFAULT '8ball',

    shot_timer_seconds  integer     NOT NULL DEFAULT 30,
    -- Minor units, matching the wallet's convention (ADR 0010) even though no wallet exists yet.
    -- Structure only: nothing reserves or settles this amount before Phase 17.
    wager_amount        bigint      NOT NULL DEFAULT 0,
    spectators_allowed  boolean     NOT NULL DEFAULT true,

    state               text        NOT NULL DEFAULT 'open',
    host_user_id        uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,

    -- Present only for private rooms. Joining a private room happens exclusively through this code
    -- (POST /rooms/join-by-code) — the room's own id is never sufficient, so an id leaked through
    -- logs or a screenshot does not by itself grant entry.
    join_code           text,

    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT rooms_visibility_valid CHECK (visibility IN ('public', 'private')),
    CONSTRAINT rooms_mode_valid       CHECK (mode IN ('1v1', '2v2')),
    CONSTRAINT rooms_ruleset_valid    CHECK (ruleset = '8ball'),
    CONSTRAINT rooms_state_valid      CHECK (state IN ('open', 'closed')),
    CONSTRAINT rooms_shot_timer_range CHECK (shot_timer_seconds BETWEEN 15 AND 120),
    CONSTRAINT rooms_wager_nonnegative CHECK (wager_amount >= 0),
    -- A public room has no code to guard entry with; a private room must have one. Enforced here
    -- rather than trusted to application code, so a bug cannot create an unguarded "private" room.
    CONSTRAINT rooms_join_code_matches_visibility CHECK (
        (visibility = 'private' AND join_code IS NOT NULL) OR
        (visibility = 'public'  AND join_code IS NULL)
    )
);

-- Powers both public discovery (WHERE visibility='public' AND state='open') and the join-by-code
-- lookup (WHERE join_code=$1 AND state='open'). Partial on state='open': closed rooms are never
-- looked up by either path, so they should not cost index maintenance.
CREATE INDEX rooms_public_open_idx ON rooms (created_at DESC, id DESC)
    WHERE visibility = 'public' AND state = 'open';

CREATE UNIQUE INDEX rooms_join_code_key ON rooms (join_code) WHERE join_code IS NOT NULL;

CREATE TABLE room_members (
    room_id   uuid        NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    user_id   uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,

    -- 0 or 1. len 1 per side = 1v1, len 2 per side = 2v2 — the same Side model game/state uses
    -- (MEMORY.md §14), stored as an integer here because a room has no game/state package to import.
    side      smallint    NOT NULL,
    slot      smallint    NOT NULL,

    ready     boolean     NOT NULL DEFAULT false,
    -- clock_timestamp(), not now(). now() (= transaction_timestamp()) is fixed at the START of the
    -- enclosing transaction and stays constant for every statement inside it — correct for an
    -- audit-style created_at, wrong here, because Leave's host handoff needs joined_at to reflect
    -- genuine insertion order. Two joins that happen to land in the same transaction would
    -- otherwise get an identical timestamp and make "earliest member" ambiguous. clock_timestamp()
    -- advances on every call regardless of transaction boundaries.
    joined_at timestamptz NOT NULL DEFAULT clock_timestamp(),

    PRIMARY KEY (room_id, user_id),

    CONSTRAINT room_members_side_valid CHECK (side IN (0, 1)),
    CONSTRAINT room_members_slot_valid CHECK (slot IN (0, 1))
);

-- THE capacity constraint — this is what actually guarantees "exactly one winner" when two joins
-- race for the last seat, not merely a backstop for the application-level row lock taken on
-- `rooms` during a join. Verified directly: internal/rooms' concurrency test still passes with
-- that lock removed, because Postgres serializes conflicting INSERTs on this index regardless.
-- The lock still earns its place for other reasons — see qRoomForUpdate in repository.go — but the
-- capacity invariant itself lives here, in the database, not in a Go `if` (§10).
CREATE UNIQUE INDEX room_members_seat_key ON room_members (room_id, side, slot);

-- Every membership change touches this: capacity counting on join, host handoff and closure on
-- leave.
CREATE INDEX room_members_room_idx ON room_members (room_id);
