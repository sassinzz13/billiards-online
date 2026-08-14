-- Owner: internal/users (L0)
--
-- Split from `users` rather than added as columns there, because a profile is what a player
-- customizes (display name, avatar) plus statistics that will be machine-written later (Phase 15),
-- while `users` stays pure identity. One row per user, created alongside it.
--
-- Statistics columns are structure only in this phase — nothing writes to them yet. They exist now
-- so the shape is settled and reviewable before Phase 15 starts maintaining them, per §71.

CREATE TABLE player_profiles (
    user_id        uuid        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,

    -- NULL means "use the handle". Distinct from handle because handle is the unique, permanent
    -- login identifier; display_name is what a player is called at the table and may change freely.
    display_name   text,

    -- Opaque reference to a future asset (Phase 8+ asset pipeline), not a URL. What it resolves to
    -- is a rendering concern, not this table's.
    avatar_ref     text,

    matches_played integer     NOT NULL DEFAULT 0,
    wins           integer     NOT NULL DEFAULT 0,
    losses         integer     NOT NULL DEFAULT 0,

    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT player_profiles_display_name_length
        CHECK (display_name IS NULL OR char_length(display_name) BETWEEN 1 AND 40),
    CONSTRAINT player_profiles_avatar_ref_length
        CHECK (avatar_ref IS NULL OR char_length(avatar_ref) <= 512),
    CONSTRAINT player_profiles_stats_nonnegative
        CHECK (matches_played >= 0 AND wins >= 0 AND losses >= 0),
    -- A domain invariant worth enforcing even though nothing writes these columns yet (§10):
    -- wins and losses can never outrun matches played, so a future bug in the Phase 15 writer
    -- cannot silently corrupt the count.
    CONSTRAINT player_profiles_stats_consistent
        CHECK (wins + losses <= matches_played)
);

-- Backfill for any account created before this migration, so "every user has a profile" holds
-- unconditionally rather than as a rule new code must remember.
INSERT INTO player_profiles (user_id)
SELECT id FROM users
ON CONFLICT (user_id) DO NOTHING;
