# Configuration Guide

Almost everything is stored in a single SQLite database and edited through
the admin web UI at `/admin`. The only thing read from disk at startup is a
tiny bootstrap file that says where that database lives — see
`configs/bootstrap.example.yaml`.

```bash
cp configs/bootstrap.example.yaml bootstrap.yaml
$EDITOR bootstrap.yaml   # set database.path

./subscription-service -config bootstrap.yaml -import web            # seed from the example web/ tree
./subscription-service -config bootstrap.yaml -create-admin admin -create-admin-password 'change-me'

./subscription-service -config bootstrap.yaml                         # run
```

Then open `http://<host>:8080/admin` and log in. `-db /path/to/data.db` is a
shortcut that skips the bootstrap file entirely (useful for quick local
testing).

`xui` and `subscription` settings have no safe default and must be filled in
via **Settings** before the service is useful — the panel's URL/credentials
and this service's own public subscription URL.

## Admin UI (`/admin`)

- **Settings** — one JSON editor per section: `server`, `xui`,
  `subscription`, `theme`, `qr`, `support`, `security`, `logging` (same
  fields as `internal/config`'s Go structs — see the table below). These
  load once at startup; changing them needs a restart.
- **Applications** — the catalog shown on the subscription page: name,
  icon, description, platforms, download URL, deeplink (supports
  `{subscription}`/`{profileTitle}` placeholders), instructions,
  visibility, sort order. Hot-reloaded, no restart needed.
- **Themes** — per-theme metadata (name, logo, favicon, colors, fonts) plus
  every file (`layout.html`, `partials/*.html`, `pages/subscription.html`,
  `static/**`). Editing a file's content or a theme's metadata takes effect
  on the next request — no restart.
- **Templates** — the Xray-link/Xray-JSON/Clash/Mihomo generator templates,
  keyed by `(format, profile, protocol)`. Create a new profile by saving a
  template under a new profile name; a profile missing a template for a
  given format/protocol falls back to `"default"`. Hot-reloaded.
- **Assignments** — which template profile each subscriber (`subId`) uses.
  Unassigned subscribers use `"default"`. Hot-reloaded — reassigning a
  subscriber changes what they're served on their very next request.

Login is a single admin account (bcrypt password, server-side session,
12h TTL, CSRF-protected forms, secure cookies). There's only one account —
`-create-admin` overwrites it if run again with the same username.

## Settings reference

| Section | Fields |
|---|---|
| `server` | `listen`, `read_timeout`, `write_timeout`, `idle_timeout` |
| `xui` (required) | `base_url`, `username`, `password`, `base_path`, `timeout`, `retry.max_attempts`, `retry.backoff`, `insecure_skip_verify` |
| `subscription` (required) | `public_url`, `server_host`, `update_interval`, `cache_ttl` |
| `theme` | `active` — which theme slug to render |
| `qr` | `size`, `margin`, `foreground`, `background` |
| `support` | `telegram`, `discord`, `email`, `website`, `custom` |
| `security` | `rate_limit.requests_per_minute`, `rate_limit.burst`, `csp`, `trusted_hosts` |
| `logging` | `level` (`debug`/`info`/`warn`/`error`), `format` (`json`/`console`) |

Durations are JSON integers in **nanoseconds** (e.g. `30000000000` = 30s) —
the settings editor's textarea is raw JSON matching Go's `time.Duration`
marshaling, not a YAML-style `"30s"` string.

## Advanced: editing the database directly

Everything above is also a plain SQLite table, editable with the `sqlite3`
CLI (or any SQLite client) if you'd rather script it than click through the
UI — useful for bulk imports/exports or one-off automation:

```sql
-- tables: settings, applications, themes, theme_files, templates, assignments
INSERT INTO settings (key, value, updated_at) VALUES
  ('support', '{"telegram":"https://t.me/example"}', unixepoch());

INSERT INTO templates (format, profile, protocol, content, updated_at)
  VALUES ('mihomo', 'gaming', '', '<your gaming mihomo yaml>', unixepoch());

INSERT INTO assignments (sub_id, profile, updated_at)
  VALUES ('the-subscribers-subid', 'gaming', unixepoch());
```

The admin UI and direct SQL are just two ways of writing the same rows —
freely mix both.

## Seeding from the example content (`-import`)

`./subscription-service -config bootstrap.yaml -import web` walks a
`web/`-shaped directory (this repo ships one with a default theme, a 6-app
catalog, and default templates for every format) and inserts it as the
`"default"` profile / `"default"` theme. Safe to re-run — it replaces what
it manages without touching anything added through the admin UI or by hand.
