-- 0019_user_sessions.up.sql
-- F-13 (login): who is using the app.
--
-- 1. A login creates a row here and the browser keeps a random token in a
--    cookie. Only the token's SHA-256 is stored (32 bytes): someone who reads
--    this table (a dump, a backup) cannot use it to log in. Logging out, or
--    deactivating the user, deletes the rows, and that cuts access at once
--    (a signed token like a JWT would stay valid until it expires).
-- 2. A session ends at expires_at (the app sets 12 hours: one working day).
-- 3. Usernames are unique ignoring case: "Admin" and "admin" are the same
--    person at the login. The app also compares in lower case.

CREATE TABLE user_sessions (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT chk_user_sessions_token_hash CHECK (octet_length(token_hash) = 32),
    CONSTRAINT chk_user_sessions_expiry CHECK (expires_at > created_at)
);

CREATE UNIQUE INDEX uq_user_sessions_token_hash ON user_sessions (token_hash);
CREATE INDEX idx_user_sessions_user_id ON user_sessions (user_id);

CREATE UNIQUE INDEX uq_users_username_lower ON users (lower(username));
