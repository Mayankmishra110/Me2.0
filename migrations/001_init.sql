-- 001_init.sql — full schema from docs/ARCHITECTURE.md §4
-- All timestamps are UTC ISO-8601 text. IDs are ULIDs. JSON columns are TEXT with json_valid.
-- schema_migrations is created by internal/db.Migrate (not here).

CREATE TABLE jobs (
    id           TEXT PRIMARY KEY,
    type         TEXT NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'dead', 'cancelled')),
    resource     TEXT NOT NULL CHECK (resource IN ('heavy', 'light', 'net')),
    priority     INTEGER NOT NULL DEFAULT 0,
    payload      TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(payload)),
    result       TEXT CHECK (result IS NULL OR json_valid(result)),
    run_at       TEXT NOT NULL,
    attempts     INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    lease_until  TEXT,
    worker       TEXT,
    last_error   TEXT,
    parent_id    TEXT,
    content_id   TEXT,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);

CREATE INDEX jobs_status_resource_run_at ON jobs (status, resource, run_at);

CREATE TABLE channels (
    id                TEXT PRIMARY KEY,
    platform          TEXT NOT NULL,
    handle            TEXT NOT NULL DEFAULT '',
    language          TEXT NOT NULL,
    niche             TEXT NOT NULL,
    account_ref       TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL CHECK (status IN ('active', 'paused', 'warming')),
    warmup_started_at TEXT
);

CREATE TABLE topics (
    id         TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL REFERENCES channels (id),
    title      TEXT NOT NULL,
    source     TEXT NOT NULL CHECK (source IN ('scout', 'manual')),
    source_url TEXT,
    score      REAL NOT NULL DEFAULT 0,
    signals    TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(signals)),
    status     TEXT NOT NULL CHECK (status IN ('new', 'picked', 'used', 'rejected')),
    created_at TEXT NOT NULL
);

CREATE TABLE content_items (
    id          TEXT PRIMARY KEY,
    topic_id    TEXT REFERENCES topics (id),
    channel_id  TEXT NOT NULL REFERENCES channels (id),
    kind        TEXT NOT NULL CHECK (kind IN ('long', 'short', 'blog', 'post')),
    format      TEXT NOT NULL DEFAULT '',
    language    TEXT NOT NULL,
    stage       TEXT NOT NULL DEFAULT '',
    script      TEXT CHECK (script IS NULL OR json_valid(script)),
    compliance  TEXT CHECK (compliance IS NULL OR json_valid(compliance)),
    created_at  TEXT NOT NULL
);

CREATE TABLE assets (
    id          TEXT PRIMARY KEY,
    content_id  TEXT NOT NULL REFERENCES content_items (id),
    kind        TEXT NOT NULL CHECK (kind IN ('voice', 'clip', 'render', 'thumb', 'subs', 'mdx')),
    path        TEXT NOT NULL,
    r2_key      TEXT,
    license_url TEXT,
    sha256      TEXT,
    bytes       INTEGER,
    delete_after TEXT
);

CREATE TABLE approvals (
    id                  TEXT PRIMARY KEY,
    content_id          TEXT NOT NULL REFERENCES content_items (id),
    kind                TEXT NOT NULL,
    summary             TEXT NOT NULL DEFAULT '',
    preview_path        TEXT,
    status              TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'rejected', 'redo', 'expired')),
    nonce               TEXT NOT NULL,
    note                TEXT,
    telegram_message_id INTEGER,
    decided_at          TEXT
);

CREATE TABLE publications (
    id              TEXT PRIMARY KEY,
    content_id      TEXT NOT NULL REFERENCES content_items (id),
    platform        TEXT NOT NULL,
    account         TEXT NOT NULL,
    scheduled_at    TEXT,
    status          TEXT NOT NULL,
    external_id     TEXT,
    url             TEXT,
    idempotency_key TEXT NOT NULL UNIQUE,
    error           TEXT,
    published_at    TEXT
);

CREATE TABLE metrics (
    publication_id TEXT NOT NULL REFERENCES publications (id),
    captured_at    TEXT NOT NULL,
    views          INTEGER NOT NULL DEFAULT 0,
    watch_seconds  REAL NOT NULL DEFAULT 0,
    avg_view_pct   REAL NOT NULL DEFAULT 0,
    likes          INTEGER NOT NULL DEFAULT 0,
    comments       INTEGER NOT NULL DEFAULT 0,
    shares         INTEGER NOT NULL DEFAULT 0,
    subs_gained    INTEGER NOT NULL DEFAULT 0,
    ctr            REAL NOT NULL DEFAULT 0,
    PRIMARY KEY (publication_id, captured_at)
);

CREATE TABLE scores (
    scope      TEXT NOT NULL CHECK (scope IN ('topic', 'format', 'hook', 'time_slot')),
    channel_id TEXT NOT NULL,
    key        TEXT NOT NULL,
    score      REAL NOT NULL DEFAULT 0,
    samples    INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (scope, channel_id, key)
);

CREATE TABLE script_fingerprints (
    content_id    TEXT PRIMARY KEY REFERENCES content_items (id),
    shingles_hash BLOB,
    embedding     BLOB
);

CREATE TABLE oauth_tokens (
    platform       TEXT NOT NULL,
    account        TEXT NOT NULL,
    encrypted_blob BLOB NOT NULL,
    expires_at     TEXT,
    PRIMARY KEY (platform, account)
);

CREATE TABLE builds (
    id            TEXT PRIMARY KEY,
    repo          TEXT NOT NULL,
    plan_path     TEXT NOT NULL DEFAULT '',
    phase         TEXT NOT NULL DEFAULT '',
    subphase      TEXT NOT NULL DEFAULT '',
    thread        TEXT NOT NULL CHECK (thread IN ('implementer', 'auditor')),
    status        TEXT NOT NULL,
    branch        TEXT NOT NULL DEFAULT '',
    worktree      TEXT NOT NULL DEFAULT '',
    session_id    TEXT,
    attempts      INTEGER NOT NULL DEFAULT 0,
    audit_verdict TEXT,
    pr_url        TEXT,
    last_error    TEXT
);

CREATE TABLE quotas (
    provider     TEXT NOT NULL,
    window_start TEXT NOT NULL,
    used         INTEGER NOT NULL DEFAULT 0,
    "limit"      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (provider, window_start)
);

CREATE TABLE revenue (
    id       TEXT PRIMARY KEY,
    line     TEXT NOT NULL CHECK (line IN ('ads', 'affiliate', 'agency', 'saas', 'sponsor')),
    source   TEXT NOT NULL DEFAULT '',
    amount   REAL NOT NULL,
    currency TEXT NOT NULL DEFAULT 'USD',
    date     TEXT NOT NULL,
    note     TEXT
);

CREATE TABLE events (
    id      TEXT PRIMARY KEY,
    at      TEXT NOT NULL,
    actor   TEXT NOT NULL CHECK (actor IN ('agent', 'user', 'system')),
    kind    TEXT NOT NULL,
    ref     TEXT,
    message TEXT NOT NULL DEFAULT '',
    data    TEXT CHECK (data IS NULL OR json_valid(data))
);

CREATE INDEX events_at ON events (at);

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);
