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
