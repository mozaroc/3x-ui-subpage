# Architecture

## Overview

The subscription service is a standalone Go binary that sits in front of a
3x-ui panel, talking to it exclusively through its official REST API. It
resolves subscription tokens (`subId`) to a set of proxy configurations and
renders them as an HTML page, Clash/Mihomo YAML, or an Xray share-link
subscription, depending on the requesting client and the subscriber's
assigned template profile. Every admin-editable surface — settings, app
catalog, themes, generator templates, per-user assignments — lives in one
SQLite database, edited either through a server-rendered admin web UI
(`/admin`) or directly via SQL; the binary's only on-disk dependency is a
tiny bootstrap file naming that database's path.

```
                      ┌──────────────────────────────┐
 client (browser,     │  subscription-service          │        ┌────────────┐
 Clash, v2rayNG, ...) │  /        → httpserver          │  REST   │            │
        ───────────►  │             resolver            │────────►│   3x-ui    │
        GET /sub/{id}  │             generator/*         │  API    │   panel    │
                      │             theme / apps / qr    │◄────────│            │
 admin (browser)      │  /admin  → admin (session+CSRF)  │         └────────────┘
        ───────────►  │             adminauth            │
                      │        │                         │
                      │   internal/store ──────────────────► SQLite (single file)
                      └──────────────────────────────┘
```

## Layers

- **`internal/xui`** — REST client for the 3x-ui panel. Owns auth (cookie
  session + automatic re-login on 401), retry/backoff, and decoding of the
  panel's inbound list (including the embedded `settings`/`streamSettings`
  JSON strings). Wrapped by `CachedLister` (`internal/xui/cache.go`) which
  adds a short TTL cache + singleflight de-duplication so many concurrent
  subscription requests collapse into one upstream call.
- **`internal/domain`** — protocol-agnostic core types (`MatchedClient`,
  `Subscription`, `TrafficStats`, `Status`). Every other layer depends on
  this, never on `internal/xui`'s raw API shapes.
- **`internal/resolver`** — turns a `subId` into a `domain.Subscription` by
  scanning the (cached) inbound list for every client whose `subId` matches,
  aggregating traffic/expiry/status.
- **`internal/store`** — opens the single SQLite database (pure-Go
  `modernc.org/sqlite` driver, WAL journal mode), applies the embedded
  schema (`CREATE TABLE IF NOT EXISTS`, no migration framework — the schema
  is small enough not to need one). Every other package that touches the
  database takes a `*sql.DB` and owns its own queries against its own
  table(s); `store` itself has no query logic.
- **`internal/assignment`** — resolves `subId -> profile` (table
  `assignments`), the mechanism behind per-user template assignment.
  Subscribers with no row get `"default"`. Also exposes `Set`/`Delete`/`List`
  for the admin UI.
- **`internal/generator/*`** — render a resolved subscription into a client
  config format, using the subscriber's assigned profile:
  - `linkgen` — Xray share links (`vless://`, `vmess://`, `trojan://`,
    `ss://`) and the base64 subscription body.
  - `xrayjson` — full xray-core client JSON config.
  - `clash` / `mihomo` — YAML configs (thin wrappers around the shared
    `yamlgen` rendering engine — same mechanics, different `format` string).
  - `happ` / `incy` — thin wrappers around the shared `rawgen` rendering
    engine, for the Happ and Incy clients. Unlike Clash/Mihomo/Xray, this
    project has no verified, authoritative knowledge of either client's wire
    format, so `rawgen` applies no output validation (no YAML/JSON parse
    check) — the admin-authored template fully owns the output bytes, same
    trust model as every other format's template but without an assumed
    schema to validate against. Also injects `.Rules` (from
    `internal/routing`) into the template context alongside `.Clients`.
  - Shared support packages: `tmplctx` (flattens a `MatchedClient` into the
    field set every template sees), `tmplcache` (loads+caches templates from
    the `templates` table keyed by `(format, profile, protocol)`, falling
    back to the `"default"` profile and re-parsing only when a row's
    `updated_at` changes), `tmplfuncs` (shared template funcs: `urlquery`,
    `b64`, `join`).
- **`internal/templatestore`** — the admin-write counterpart to
  `tmplcache`'s hot-reloaded reads: plain CRUD over the `templates` table,
  used only by the admin UI.
- **`internal/routing`** — administrator-editable routing rules (table
  `routing_rules`): GEOIP, geosite, domain/domain-suffix/domain-keyword,
  regex, CIDR, IP range, process, protocol, port, DNS, and custom rules,
  keyed by `(profile, sort_order)` with the same "falls back to `default`"
  convention as `assignment`/`tmplcache`. Consumed by generator templates
  (currently `happ`/`incy`, but any format's template can reference
  `.Rules`) so the routing-rule *data* lives in one structured table while
  each client format's *template* decides how to render it into that
  client's own syntax.
- **`internal/apps`** — application catalog (table `applications`),
  hot-reloaded via `MAX(updated_at)`, deep-link placeholder rendering, plus
  `Create`/`Update`/`Delete`/`Get`/`ListAll` for the admin UI.
- **`internal/theme`** — HTML theme engine (`html/template`, tables
  `themes` + `theme_files`), hot-reloaded the same way; also serves the
  active theme's static assets (`ServeStatic`) straight from the database,
  with an in-process byte cache so static requests don't round-trip to
  SQLite under load. `AdminStore` (same package, separate type) provides the
  write side used by the admin UI — kept apart from `Engine` so the
  read/render/cache path and the write path don't blur together.
- **`internal/qrcode`** — PNG/SVG QR rendering from a raw module bitmap, so
  size/margin/colors are fully configurable.
- **`internal/importer`** — one-shot seeding from a `web/`-shaped directory
  into the database (`-import` flag); the migration path from the example
  file content checked into this repo to a running database.
- **`internal/ratelimit`** — per-key token-bucket limiter shared by the
  public subscription API and the admin login route (each gets its own
  instance/rate).
- **`internal/adminauth`** — the admin panel's authentication: a single
  bcrypt-hashed account (table `admin_users`) and server-side sessions
  (table `sessions`, each carrying a CSRF token, 12h TTL).
- **`internal/admin`** — the admin HTTP layer: server-rendered HTML forms
  (its own templates are `//go:embed`ded application code, **not**
  admin-editable database content — letting database content control the
  panel that administers the database would be a privilege-escalation
  footgun) covering settings, applications, themes, templates, and
  assignments. Session + CSRF + secure-header middleware; mounted at
  `/admin` by `cmd/subscription-service`.
- **`internal/httpserver`** — chi router, middleware (secure headers, rate
  limiting, gzip, request logging), and handlers that resolve a
  subscriber's profile and wire the above together per request. This is the
  public-facing HTTP layer, mounted at `/` alongside `internal/admin`'s `/admin`.
- **`internal/config`** — assembles typed `Config` from the `settings`
  table on top of built-in defaults (`LoadFromDB`), plus the tiny bootstrap
  YAML reader (`LoadBootstrap`) that's the one remaining file-based piece,
  plus `SaveSetting`/`GetSetting`/`ListSettings` for the admin UI.

Business logic (resolver, generators, catalog, theming, assignment) has no
dependency on `net/http` — handlers in `internal/httpserver` and
`internal/admin` are the only HTTP-aware layers, so everything else is
independently testable against an in-memory SQLite database, without a real
3x-ui panel or web server.

## Request flow (`GET /sub/{subId}`)

1. Middleware validates `subId` (bounded length, alnum/hyphen only).
2. `resolver.Resolve` fetches inbounds via the cached lister, matches
   clients by `subId`, builds a `domain.Subscription`.
3. `assignment.Store.Resolve` looks up the subscriber's template profile
   (`"default"` if unassigned).
4. Content negotiation on `User-Agent` picks a renderer:
   - browser → `theme.Engine.Render` (HTML page; app catalog and support
     info also loaded here, no profile involved)
   - Clash/Mihomo/stash → `clash`/`mihomo` generator, using the profile
   - everything else → `linkgen.BuildSubscription`, using the profile
5. Response is gzip-compressed and logged.

## Admin request flow (`POST /admin/...`)

1. `secureHeaders` applies CSP/X-Frame-Options/etc.
2. `requireSession` validates the session cookie against the `sessions`
   table (302→`/admin/login` on failure/expiry), injecting the session into
   the request context.
3. `verifyCSRF` (mutating routes only) compares the submitted form's
   `csrf_token` against the session's stored token (403 on mismatch).
4. The handler calls the relevant package's write method (e.g.
   `apps.Catalog.Update`, `theme.AdminStore.PutFile`,
   `assignment.Store.Set`) and redirects (302, POST-redirect-GET) back to
   the relevant list page.

## Hot reload strategy

Every editable-without-recompile asset (settings excluded — see below) uses
the same pattern regardless of table: query the relevant row(s)'
`MAX(updated_at)`, compare against what's cached, and only re-parse/re-query
in full when it changed. This is the direct SQL analogue of phase 1's
file-mtime checks and keeps the same cost profile — one small indexed query
per request, negligible next to the network calls to the 3x-ui panel it's
already coupled with. Since the admin UI writes through the exact same
tables, saving a template/theme/app/assignment through `/admin` is
indistinguishable from editing it via `sqlite3` — both just bump
`updated_at`.

Core service settings (`server`, `security`, `logging` — anything that would
require rebuilding the rate limiter, log handler, or `http.Server` to change
live) load once at startup from `LoadFromDB` and need a restart to pick up
changes. The app catalog, themes, generator templates, and per-user
assignments hot-reload per request with no restart.

## What's deferred (phase 5+)

- Write-back to 3x-ui (updating user info from this service).
- Multiple admin accounts / role-based access (currently a single account).
