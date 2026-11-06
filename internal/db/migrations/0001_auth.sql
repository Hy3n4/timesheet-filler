CREATE TABLE users (
    id                BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email             TEXT        NOT NULL,
    display_name      TEXT        NOT NULL DEFAULT '',
    eos_name          TEXT        NOT NULL DEFAULT '',
    role              TEXT        NOT NULL CHECK (role IN ('admin', 'member')),
    google_sub        TEXT UNIQUE,
    password_hash     TEXT,
    totp_secret_enc   TEXT,
    totp_confirmed_at TIMESTAMPTZ,
    totp_last_step    BIGINT      NOT NULL DEFAULT 0,
    disabled_at       TIMESTAMPTZ,
    failed_logins     INTEGER     NOT NULL DEFAULT 0,
    locked_until      TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at     TIMESTAMPTZ,
    CONSTRAINT users_has_login CHECK (google_sub IS NOT NULL OR password_hash IS NOT NULL)
);
CREATE UNIQUE INDEX users_email_key ON users (lower(email));

CREATE TABLE sessions (
    token_hash   BYTEA PRIMARY KEY,
    user_id      BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    stage        TEXT        NOT NULL CHECK (stage IN ('mfa', 'enroll', 'full')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);

CREATE TABLE invites (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    token_hash       BYTEA       NOT NULL UNIQUE,
    email            TEXT        NOT NULL,
    role             TEXT        NOT NULL CHECK (role IN ('admin', 'member')),
    eos_name         TEXT        NOT NULL DEFAULT '',
    invited_by       BIGINT REFERENCES users (id) ON DELETE SET NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at       TIMESTAMPTZ NOT NULL,
    accepted_at      TIMESTAMPTZ,
    accepted_user_id BIGINT REFERENCES users (id) ON DELETE SET NULL,
    revoked_at       TIMESTAMPTZ
);

CREATE TABLE oauth_states (
    state_hash    BYTEA PRIMARY KEY,
    nonce         TEXT        NOT NULL,
    pkce_verifier TEXT        NOT NULL,
    invite_id     BIGINT REFERENCES invites (id) ON DELETE CASCADE,
    expires_at    TIMESTAMPTZ NOT NULL
);

CREATE TABLE recovery_codes (
    user_id   BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash BYTEA  NOT NULL,
    used_at   TIMESTAMPTZ,
    PRIMARY KEY (user_id, code_hash)
);

CREATE TABLE email_codes (
    user_id    BIGINT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    code_mac   BYTEA       NOT NULL,
    sent_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    attempts   INTEGER     NOT NULL DEFAULT 0
);

CREATE TABLE audit_log (
    id       BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_id BIGINT REFERENCES users (id) ON DELETE SET NULL,
    action   TEXT        NOT NULL,
    target   TEXT        NOT NULL DEFAULT '',
    ip       TEXT        NOT NULL DEFAULT ''
);
CREATE INDEX audit_log_at_idx ON audit_log (at DESC);
