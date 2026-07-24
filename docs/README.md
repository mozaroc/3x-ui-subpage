# 3x-ui Subscription Service

A standalone subscription page + config-generation service for the 3x-ui
proxy panel. Talks to 3x-ui only through its official REST API — no source
changes, no direct database access on the panel side. Single native binary,
no Docker, no runtime dependencies beyond the OS.

All admin-editable content (settings, app catalog, themes, generator
templates, per-user template assignments, per-user Happ/Incy routing
profiles, and user accounts themselves) lives in one SQLite database. The
binary reads only a tiny
bootstrap file to find that database. This service is the primary place to
manage subscribers day to day — creating/editing/suspending users, resetting
traffic, assigning inbounds — with every change synced out to the connected
3x-ui panel automatically; see "Centralized user management" below.

## Quickstart

```bash
# build
./scripts/build.sh
# or: go build -o subscription-service ./cmd/subscription-service

# bootstrap: tell it where the database goes
cp configs/bootstrap.example.yaml bootstrap.yaml
$EDITOR bootstrap.yaml   # set database.path

# seed the database from this repo's example theme/catalog/templates
./dist/subscription-service-linux-amd64 -config bootstrap.yaml -import web

# create the admin account
./dist/subscription-service-linux-amd64 -config bootstrap.yaml \
  -create-admin admin -create-admin-password 'change-me'

# run
./dist/subscription-service-linux-amd64 -config bootstrap.yaml
```

Then open `http://localhost:8080/admin`, log in, and fill in **Settings →
xui** and **Settings → subscription** — those two have no safe default
(panel URL + API key — from the panel's own Settings → Security → API
Token — and this service's own public subscription URL).
Everything else (applications, themes, templates, per-subscriber template
assignments — set directly on each user's edit page) is editable from the
same admin UI.

See [docs/CONFIGURATION.md](CONFIGURATION.md) for every settings key, what
the admin UI covers, and the underlying table schemas if you'd rather script
changes via `sqlite3` directly.

## Endpoints

| Route | Behavior |
|---|---|
| `GET /sub/{subId}` | Content-negotiated: browsers get the HTML subscription page; Clash/Mihomo user agents get YAML; everything else gets the base64 Xray link subscription |
| `GET /sub/{subId}/xray` | Explicit base64 Xray share-link subscription |
| `GET /sub/{subId}/xray.json` | Full xray-core client JSON config |
| `GET /sub/{subId}/clash` | Clash YAML |
| `GET /sub/{subId}/mihomo` | Mihomo YAML |
| `GET /sub/{subId}/happ` | Happ config (admin-defined template, no built-in schema) |
| `GET /sub/{subId}/incy` | Incy config (admin-defined template, no built-in schema) |
| `GET /sub/{subId}/qr.png` \| `/qr.svg` | QR code for the subscription URL |
| `GET /sub/{subId}/link/{index}` | One canonical share link (3x-ui's own string, verbatim) by its position in the subscriber's link list |
| `GET /sub/{subId}/link/{index}/qr.png` \| `/qr.svg` | QR code for that single link |
| `GET /sub/{subId}/link/{index}/config.json` | Single-link full xray-core config download |
| `GET /api/v1/subscription/{subId}` | JSON view of the resolved subscription |
| `GET /api/v1/applications` | JSON application catalog |
| `GET /healthz` | Liveness check |

The Xray share links themselves (the base64 subscription body, and every
`/link/{index}` response) are **3x-ui's own canonical strings**, fetched
directly from the panel (`GET /panel/api/clients/subLinks/{subId}`) and
returned verbatim — this service never reconstructs a share link itself.
Every other format-specific endpoint (Clash, Mihomo, Happ, Incy,
`xray.json`) parses those same links and renders using the subscriber's
assigned template profile for that client type (table
`template_assignments`, keyed by `(sub_id, client_type)`; `"default"` if
unassigned).

`{subId}` is the same `subId` field 3x-ui already stores per client — this
service doesn't mint its own tokens, so existing 3x-ui subscription links
keep working. Users created through `/admin/users` get their `subId`
assigned locally and pushed to the panel by the sync worker, so the two
are always the same value on both sides.

## Centralized user management

`/admin/users` is now the primary way to manage subscribers — create,
edit, delete, suspend/reactivate, enable/disable, reset traffic, change
expiration, change traffic limits, regenerate UUID, search/filter/sort,
bulk operations, and assign one or more of the connected panel's inbounds
to each user. Every change is synced out to 3x-ui automatically by a
background worker, with retries, a self-healing conflict check, and a
`/admin/sync` history/retry view — day-to-day user management shouldn't
require logging into the 3x-ui web UI at all. See "Users" and "Sync" in
[docs/CONFIGURATION.md](CONFIGURATION.md), and the "Write path" section of
[docs/ARCHITECTURE.md](ARCHITECTURE.md) for how it's wired together.

This service talks to a single connected 3x-ui panel — multi-panel/
multi-node management is 3x-ui's own job, out of scope here by design.

## systemd

```bash
sudo useradd --system --no-create-home subscription-service
sudo mkdir -p /opt/subscription-service /etc/subscription-service /var/lib/subscription-service
sudo cp dist/subscription-service-linux-amd64 /opt/subscription-service/subscription-service
sudo cp configs/bootstrap.example.yaml /etc/subscription-service/bootstrap.yaml
# edit bootstrap.yaml's database.path to /var/lib/subscription-service/data.db, then seed:
sudo -u subscription-service /opt/subscription-service/subscription-service \
  -config /etc/subscription-service/bootstrap.yaml -import /opt/subscription-service/web
sudo -u subscription-service /opt/subscription-service/subscription-service \
  -config /etc/subscription-service/bootstrap.yaml -create-admin admin -create-admin-password 'change-me'
sudo cp deploy/subscription-service.service /etc/systemd/system/
sudo chown -R subscription-service:subscription-service /opt/subscription-service /var/lib/subscription-service
sudo systemctl daemon-reload
sudo systemctl enable --now subscription-service
```

(Copy this repo's `web/` directory to `/opt/subscription-service/web` first
if you want the `-import` step above to have something to seed from.)

## CI / Releases

- `.github/workflows/build.yml` — on every push/PR to `main`: gofmt check,
  `go vet`, `go build`, `go test`, then cross-compiles both binaries and
  uploads them as a build artifact.
- `.github/workflows/release.yml` — on pushing a tag matching `v*.*.*`
  (or manually via workflow_dispatch): runs tests, cross-compiles, generates
  checksums, and publishes a GitHub Release with both binaries attached.
  Cut a release with `git tag v1.0.0 && git push origin v1.0.0`.

## Docs

- [Configuration Guide](CONFIGURATION.md)
- [Architecture](ARCHITECTURE.md)

## Admin UI

`/admin` — single admin account (bcrypt password, server-side sessions,
CSRF-protected forms). Covers settings, applications, themes (metadata +
every file), generator templates (all formats/profiles/protocols), and
centralized user management (per-subscriber template assignments,
per-subscriber Happ/Incy routing profiles, synchronization status
monitoring). See [docs/CONFIGURATION.md](CONFIGURATION.md).

## Phase status

Phases 1-5 are complete: 3x-ui API client, subscriber resolution, Xray
share-link retrieval (3x-ui's own canonical links, fetched verbatim) + full
config generation, Clash/Mihomo YAML generation,
Happ/Incy config generation, per-subscriber Happ/Incy Routing Profile
configuration (delivered via response headers, per Happ's own Routing
Generator), HTML theme engine, JSON-derived application catalog, QR
generation, per-subscriber
template profile assignment, and centralized user management with
automatic 3x-ui synchronization (retries, conflict self-healing, status
monitoring) — all backed by SQLite with hot reload — plus a full admin web
UI. Multiple admin accounts / role-based access, and 3x-ui's own
multi-panel/multi-node management, are intentionally out of scope, not
deferred — see `ARCHITECTURE.md`'s "What's deferred" section.
