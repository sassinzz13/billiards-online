-- Owner: internal/users (L0)
--
-- Identity only. There is deliberately NO password column here: credentials are owned by
-- internal/auth in the next migration, so the users feature never touches a password hash and
-- cannot leak one (§42).

CREATE TABLE users (
    id         uuid        PRIMARY KEY,
    email      text        NOT NULL,
    handle     text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    -- The application lowercases email before insert. This makes that a guarantee rather than a
    -- convention, so a future code path cannot quietly create a duplicate that differs only by case.
    CONSTRAINT users_email_is_lowercase CHECK (email = lower(email)),
    CONSTRAINT users_email_shape        CHECK (email LIKE '_%@_%._%' AND char_length(email) <= 254),
    CONSTRAINT users_handle_length      CHECK (char_length(handle) BETWEEN 3 AND 24),
    -- Letters, digits, underscore. Keeps handles usable in URLs and unambiguous in a scoreboard.
    CONSTRAINT users_handle_shape       CHECK (handle ~ '^[A-Za-z0-9_]+$')
);

-- Serves login (WHERE email = $1) and enforces one account per address.
CREATE UNIQUE INDEX users_email_key ON users (email);

-- Handles are displayed with the casing the player chose but must be unique case-insensitively,
-- so "Rocket" and "rocket" cannot both exist. A functional index enforces that without a second
-- stored column to keep in sync.
CREATE UNIQUE INDEX users_handle_lower_key ON users (lower(handle));
