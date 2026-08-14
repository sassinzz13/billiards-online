-- Owner: internal/auth (L1)
--
-- Credentials are separate from `users` on purpose. The users feature owns identity; auth owns the
-- secret. Splitting them means no query in internal/users can return a password hash even by
-- accident.

CREATE TABLE credentials (
    user_id       uuid        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    -- PHC string: $argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>. Parameters live inside the hash so
    -- they can be raised later without invalidating existing passwords.
    password_hash text        NOT NULL,
    updated_at    timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT credentials_hash_is_argon2id CHECK (password_hash LIKE '$argon2id$%')
);

CREATE TABLE sessions (
    id           uuid        PRIMARY KEY,
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- SHA-256 of the token, never the token itself. A database dump therefore yields nothing
    -- usable: an attacker would have to invert the hash to forge a cookie. See ADR 0009.
    token_hash   bytea       NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    -- NULL means live. Set on logout, which is what makes revocation immediate rather than
    -- waiting for expiry — the property JWTs cannot offer without a denylist.
    revoked_at   timestamptz,

    CONSTRAINT sessions_token_hash_is_sha256 CHECK (octet_length(token_hash) = 32),
    CONSTRAINT sessions_expires_after_created CHECK (expires_at > created_at)
);

-- Every authenticated request looks a session up by token hash, so this index is on the hottest
-- read path in the system. UNIQUE also makes a token collision a database error rather than a
-- silent account mix-up.
CREATE UNIQUE INDEX sessions_token_hash_key ON sessions (token_hash);

-- Partial, because expired and revoked rows are never the target of a "list this user's sessions"
-- or "revoke everything" query. Keeps the index small as dead sessions accumulate.
CREATE INDEX sessions_user_live_idx ON sessions (user_id) WHERE revoked_at IS NULL;

-- Supports periodic deletion of dead sessions.
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);
