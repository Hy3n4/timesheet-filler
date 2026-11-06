CREATE TABLE eos_links (
    user_id            BIGINT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    member_id          BIGINT      NOT NULL,
    refresh_enc        TEXT        NOT NULL,
    refresh_session_id BIGINT,
    access_enc         TEXT,
    access_expires_at  TIMESTAMPTZ,
    status             TEXT        NOT NULL CHECK (status IN ('active', 'reauth')),
    linked_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    refreshed_at       TIMESTAMPTZ
);

CREATE TABLE eos_pending_logins (
    user_id    BIGINT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    code_enc   TEXT        NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
