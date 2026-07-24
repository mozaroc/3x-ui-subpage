CREATE TABLE IF NOT EXISTS settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS applications (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL,
    icon         TEXT NOT NULL DEFAULT '',
    description  TEXT NOT NULL DEFAULT '',
    platforms    TEXT NOT NULL DEFAULT '[]',
    download     TEXT NOT NULL DEFAULT '',
    deeplink     TEXT NOT NULL DEFAULT '',
    instructions TEXT NOT NULL DEFAULT '',
    visible      INTEGER NOT NULL DEFAULT 1,
    sort_order   INTEGER NOT NULL DEFAULT 0,
    updated_at   INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS themes (
    slug        TEXT PRIMARY KEY,
    name        TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    logo        TEXT NOT NULL DEFAULT '',
    favicon     TEXT NOT NULL DEFAULT '',
    colors      TEXT NOT NULL DEFAULT '{}',
    fonts       TEXT NOT NULL DEFAULT '{}',
    updated_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS theme_files (
    theme_slug TEXT NOT NULL,
    path       TEXT NOT NULL,
    content    BLOB NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (theme_slug, path)
);

CREATE TABLE IF NOT EXISTS templates (
    format     TEXT NOT NULL,
    profile    TEXT NOT NULL,
    protocol   TEXT NOT NULL DEFAULT '',
    content    TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (format, profile, protocol)
);

-- Tracks which (format, profile, protocol) template rows the importer itself
-- last wrote, and when — lets a re-import prune a row whose source file was
-- since deleted, but only when the row's own updated_at still matches what
-- the importer last set it to (i.e. no admin edit happened in between, so
-- nothing hand-authored is ever silently deleted). See internal/importer.
CREATE TABLE IF NOT EXISTS import_manifest (
    format     TEXT NOT NULL,
    profile    TEXT NOT NULL,
    protocol   TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (format, profile, protocol)
);

CREATE TABLE IF NOT EXISTS assignments (
    sub_id     TEXT PRIMARY KEY,
    profile    TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS admin_users (
    username      TEXT PRIMARY KEY,
    password_hash TEXT NOT NULL,
    updated_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    id         TEXT PRIMARY KEY,
    username   TEXT NOT NULL,
    csrf_token TEXT NOT NULL,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS routing_rules (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    profile    TEXT NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    type       TEXT NOT NULL,
    value      TEXT NOT NULL,
    outbound   TEXT NOT NULL DEFAULT '',
    enabled    INTEGER NOT NULL DEFAULT 1,
    updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_routing_rules_profile ON routing_rules(profile, sort_order);

CREATE TABLE IF NOT EXISTS users (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    username   TEXT NOT NULL UNIQUE,
    sub_id     TEXT NOT NULL UNIQUE,
    uuid       TEXT NOT NULL,
    password   TEXT NOT NULL,
    method     TEXT NOT NULL DEFAULT 'chacha20-ietf-poly1305',
    flow       TEXT NOT NULL DEFAULT '',
    enabled    INTEGER NOT NULL DEFAULT 1,
    total_gb   INTEGER NOT NULL DEFAULT 0,
    expiry_ms  INTEGER NOT NULL DEFAULT 0,
    notes      TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS user_inbounds (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    inbound_id  INTEGER NOT NULL,
    inbound_tag TEXT NOT NULL DEFAULT '',
    protocol    TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    UNIQUE(user_id, inbound_id)
);
CREATE INDEX IF NOT EXISTS idx_user_inbounds_user ON user_inbounds(user_id);

CREATE TABLE IF NOT EXISTS sync_jobs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id         INTEGER NOT NULL,
    inbound_id      INTEGER NOT NULL,
    op              TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',
    attempts        INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT NOT NULL DEFAULT '',
    next_attempt_at INTEGER NOT NULL DEFAULT 0,
    payload         TEXT NOT NULL DEFAULT '{}',
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sync_jobs_claim ON sync_jobs(status, next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_sync_jobs_user ON sync_jobs(user_id);
