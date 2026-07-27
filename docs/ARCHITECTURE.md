# Architecture

## Overview

The subscription service is a standalone Go binary that sits in front of a
3x-ui panel, talking to it exclusively through its official REST API. It
resolves subscription tokens (`subId`) to a set of proxy configurations and
renders them as an HTML page, Clash/Mihomo YAML, or an Xray share-link
subscription, depending on the requesting client and the subscriber's
assigned template profile. The Xray share links themselves are never
reconstructed by this project — they're 3x-ui's own canonical strings,
fetched from the panel's REST API and used verbatim, parsed only to feed
the formats that need structured fields (Clash/Mihomo/xray-json/Happ/Incy).
Every admin-editable surface — settings, app
catalog, themes, generator templates, per-user template assignments,
per-user Happ/Incy routing profiles, and now user accounts themselves
(synced out to 3x-ui automatically) —
lives in one SQLite database, edited either through a server-rendered
admin web UI
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

- **`internal/xui`** — REST client for the 3x-ui panel. Authenticates with a
  static API token (from the panel's own **Settings → Security → API
  Token**), sent as `Authorization: Bearer <token>` on every request — no
  session cookie, no login/re-login flow. Retries with backoff on transient
  errors; a 401/403 is treated as a terminal auth failure (retrying with
  the same bad key can't help). Decodes the panel's inbound list, including
  `settings`/`streamSettings`, which are handled *flexibly*
  (`unmarshalFlexible` in `parse.go`) since panel versions disagree on
  whether these arrive as nested JSON objects or as JSON-encoded strings —
  confirmed empirically: a live 3x-ui 3.5.0 test instance serves objects,
  vanilla 3x-ui's documented convention is strings, and this project
  doesn't assume either. Wrapped by `CachedLister` (`internal/xui/cache.go`)
  which adds a short TTL cache + singleflight de-duplication so many
  concurrent subscription requests collapse into one upstream call;
  `Invalidate()` forces the next read to bypass the cache, called by the
  sync worker after every successful write so readers see the change
  immediately.

  The *write* side (`AddClient`/`UpdateClient`/`AttachClient`/
  `DetachClient`/`DeleteClient`/`ResetClientTraffic`/`GetClient`) targets
  the panel's own client-management API, `/panel/api/clients/*` — this was
  **verified against a live 3x-ui 3.5.0 instance's own OpenAPI spec**
  (served at `{base_url}/panel/api/openapi.json` once logged into the
  panel — session-gated, not reachable with just the API key) and a real
  create → get → update → attach/detach → delete round trip, not guessed.
  Every operation there is keyed by the client's **email** — there's no
  identifier ambiguity to resolve (an earlier draft of this integration,
  built against generic community knowledge of *vanilla* 3x-ui's older
  `/panel/api/inbounds/addClient`-style endpoints, assumed an id-vs-password
  ambiguity that turned out not to exist once tested against a real,
  current instance; those endpoints don't exist on 3.5.0 at all). Two
  behaviors confirmed empirically that this project's Users feature works
  around rather than fights: a client's `uuid` is immutable after creation
  (the panel silently ignores any `uuid` sent on create or update and keeps
  its own generated value — harmless for serving subscriptions, since
  `internal/resolver` always reads the panel's live value, never this
  service's local copy, but it does mean "regenerate UUID" has no visible
  effect for vless/vmess clients on this panel version); `subId` **is**
  honored on create, so `/sub/{subId}` links always match what this
  service assigned. If you're running a different 3x-ui version or fork,
  check its own `/panel/api-docs` and verify one create + one edit before
  relying on this for production traffic — the endpoint *shape* (client-
  centric, email-keyed) is a deliberate, well-designed API on 3.5.0, but
  nothing guarantees an older/forked panel exposes the same one.

  `GetSubLinks` (`/panel/api/clients/subLinks/{subId}`, same bearer token,
  same envelope decoding) fetches the panel's own canonical share links for
  a subscriber, verbatim — confirmed present in 3x-ui v3.5.0's own
  `internal/web/controller/client.go`. This is the one place this project
  deliberately does *not* try to be clever: every connection parameter
  (host overrides, Reality keys, xhttp settings, whatever the panel adds
  next) comes from the panel's own rendering, never reconstructed here.
  Unlike `ListInbounds`/`ListHosts`, `CachedLister` does **not** cache this
  call — it's inherently per-subscriber rather than a broadcast list, and
  skipping the cache means a panel-side config change shows up on the very
  next request.
- **`internal/domain`** — protocol-agnostic core types (`MatchedClient`,
  `Subscription`, `TrafficStats`, `Status`). Every other layer depends on
  this, never on `internal/xui`'s raw API shapes. `Subscription.Links`
  carries the panel's canonical share-link strings alongside `Clients`
  (used for traffic/status/expiry, unrelated to connection parameters).
- **`internal/resolver`** — turns a `subId` into a `domain.Subscription` by
  scanning the (cached) inbound list for every client whose `subId` matches,
  aggregating traffic/expiry/status, and fetching that subscriber's
  canonical share links via `GetSubLinks`. A `GetSubLinks` failure fails the
  whole resolve (the same severity as an `ListInbounds` failure) — unlike
  `ListHosts`, which is a genuine enhancement that degrades gracefully,
  there's no other source of truth for connection parameters to fall back
  to.
- **`internal/store`** — opens the single SQLite database (pure-Go
  `modernc.org/sqlite` driver, WAL journal mode), applies the embedded
  schema (`CREATE TABLE IF NOT EXISTS`, no general migration framework — the
  schema is small enough not to need one). Every other package that touches
  the database takes a `*sql.DB` and owns its own queries against its own
  table(s); `store` itself has no query logic. The one exception so far is
  `assignment.MigrateLegacy` (called once at startup, right after
  `store.Open`), a hand-written one-shot migration that carries forward the
  old single-column `assignments` table into `template_assignments` on
  existing installs, then drops it — not a general framework, just a
  targeted fix for that one table rename.
- **`internal/assignment`** — resolves `(subId, format) -> profile` (table
  `template_assignments`, keyed by `(sub_id, client_type)`), the mechanism
  behind per-user, per-client-type template assignment. `ClientTypes` maps
  each admin-facing client type (Xray, Clash, Mihomo, Happ, Incy) to the
  `templates` table format(s) it governs — "Xray" only governs `xray_json`
  now (share links have no template at all — see `tmplctx` below).
  Subscribers with no row for a client type get `"default"`. Also exposes
  `Set`/`ForSubID`/`DeleteAll` for the admin UI — set directly on each
  user's create/edit page, not a separate page.
- **`internal/generator/*`** — render a resolved subscription into a client
  config format, using the subscriber's assigned profile:
  - `tmplctx` — **not** a renderer but the seam every other generator
    depends on: `ParseShareLink`/`ParseEntries` parse 3x-ui's own canonical
    share-link strings (vless/vmess/trojan/ss) into `ClientContext`, the
    flat struct every template below sees. Every named field a template can
    reference (`UUID`, `Server`, `SNI`, `ALPN`, ...) is parsed straight out
    of the link; anything without a named field (xhttp's `mode`/`extra`/
    `x_padding_bytes`, ECH, pinned-cert hashes, whatever 3x-ui adds next)
    still survives in `RawParams map[string]string`, unfiltered, so a
    template can reach it (`{{.RawParams.extra}}`) without a code change
    here. An entry that fails to parse (a link shape this project doesn't
    understand yet) is dropped from generation with a logged warning, but
    the raw string itself is untouched everywhere else (subscription body,
    direct-link display, QR) — those never parse at all.
  - `xrayjson` — full xray-core client JSON config; splices `RawParams.extra`
    verbatim into `xhttpSettings` when present, for byte-accurate xhttp
    support without modeling every sub-field individually.
  - `clash` / `mihomo` — YAML configs (thin wrappers around the shared
    `yamlgen` rendering engine — same mechanics, different `format` string).
    `yamlgen/inject.go` builds each proxy entry from `ClientContext`
    directly; xhttp gets a minimal `xhttp-opts: {path, host}` (deliberately
    not chasing every xhttp sub-field into mihomo's YAML schema — that's
    what `xrayjson`'s raw splice is for).
  - `happ` / `incy` — thin wrappers around the shared `rawgen` rendering
    engine, for the Happ and Incy clients. Unlike Clash/Mihomo/Xray, this
    project has no verified, authoritative knowledge of either client's wire
    format, so `rawgen` applies no output validation (no YAML/JSON parse
    check) — the admin-authored template fully owns the output bytes, same
    trust model as every other format's template but without an assumed
    schema to validate against. (These clients' own native "Routing
    Profile" feature is unrelated to this template — see `internal/routing`
    below; it's delivered via response headers, never templated.)
  - Shared support packages: `tmplcache` (loads+caches templates from
    the `templates` table keyed by `(format, profile, protocol)`, falling
    back to the `"default"` profile and re-parsing only when a row's
    `updated_at` changes), `tmplfuncs` (shared template funcs: `urlquery`,
    `b64`, `join`, `jsonstr`, `splitJSON`).

  There is no `linkgen` package anymore — hand-building Xray share links
  (and the admin-editable per-protocol templates that used to drive it) was
  removed outright once the panel's own canonical strings became the
  source of truth; see `httpserver.writeXrayLinks`, which is now a
  two-line join+base64 of `Subscription.Links`.
- **`internal/templatestore`** — the admin-write counterpart to
  `tmplcache`'s hot-reloaded reads: plain CRUD over the `templates` table,
  used only by the admin UI.
- **`internal/routing`** — Happ/Incy "Routing Profile" (the client apps' own
  native traffic-splitting feature — `GlobalProxy`, `RouteOrder`,
  `DomainStrategy`, remote/domestic DNS, `DnsHosts`, GeoIP/GeoSite URLs,
  Direct/Proxy/Block site+IP lists, `FakeDNS`, `UseChunkFiles`, documented
  at routing.happ.su / docs.incy.cc/en/routing), split across two tables:
  - `routing_generator` (single row): the admin's Routing Generator page
    (`/admin/routing`) builds a `Profile` from the full field set and
    `Profile.Encode` renders the exact wire JSON (two real quirks:
    `GlobalProxy`/`FakeDNS` are the literal strings `"true"`/`"false"`, not
    JSON booleans, while `UseChunkFiles` is a genuine bool) into a
    `GeneratedRouting{PreviewJSON, Base64}`. The Base64 string is what the
    admin copies out.
  - `user_routing` (keyed by `sub_id`): just an `enabled` toggle and a
    `routing_b64` string pasted in from the generator's output — no JSON,
    no per-user rule editing. One configuration, assigned to any number of
    subscribers, each independently toggled.

  `httpserver.writeRoutingHeaders` embeds a subscriber's stored
  `routing_b64` verbatim as `{scheme}://routing/onadd/{routing_b64}` in the
  `Routing` response header (plus `Routing-Enable: true|false`) on every
  endpoint a Happ or Incy client actually hits — no JSON encoding happens
  at request time at all, mirroring upstream 3x-ui's own
  `ApplyCommonHeaders` but scoped per-subscriber. This is unrelated to an
  earlier, unrelated global feature of the same package name (Xray-core
  GEOIP/domain/CIDR rules on a standalone `/admin/routing` page) — the two
  happened to share the word "routing" but modeled completely different
  things; the old one was removed outright, not migrated. (A later
  revision also briefly stored the full `Profile` per-subscriber before
  settling on the generate-once/paste-many-times model above;
  `routing.MigrateLegacy` carries forward any such rows.)
- **`internal/users`** — the canonical source of truth for subscriber
  accounts (table `users`) and their inbound assignments (table
  `user_inbounds`). Owns local bookkeeping only — a `User`'s
  uuid/password/method/flow apply uniformly to every inbound it's assigned
  to, regardless of that inbound's protocol. Does not talk to 3x-ui; that's
  `internal/sync`'s job.
- **`internal/sync`** — the outbox/audit-log between `internal/users` and
  the panel. Every admin mutation that needs to reach 3x-ui (create/edit/
  delete/enable-disable/reset-traffic/regenerate-uuid/assign/unassign)
  enqueues a `sync_jobs` row *snapshotting* the client fields it needs
  (email/subId/credentials/limits) at enqueue time, rather than looking the
  user back up when it runs — so a job survives a later edit or even
  deletion of the user it was for, and its log entry stays meaningful.  A
  background `Worker` (started from `cmd/subscription-service`, stopped on
  the same shutdown context as the HTTP server) polls for due jobs, calls
  the matching `xui.Client` write method, and retries with exponential
  backoff (capped, terminal after 8 attempts) on failure. Before an assign,
  it calls `GetClient` to check whether a client with this email already
  exists on the panel (drift from a prior partial failure, or simply this
  user's second/third inbound assignment) and attaches to the new inbound
  instead of erroring out on a duplicate-create.
  `sync_jobs` rows are kept as an audit log (`ListForUser`/`ListRecent`,
  backing `/admin/sync` and each user's detail page) and pruned only once
  `success` and older than 7 days — failed/pending rows are never pruned by
  age, so nothing silently disappears before an admin sees it.
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
  users (create/edit/delete/suspend/reactivate/reset-traffic/change-limits/
  change-expiry/regenerate-uuid/search/filter/sort/bulk-ops, inbound
  assignment, per-client-type template assignment, per-subscriber Happ/Incy
  routing profile, direct connection links, and synchronization status,
  plus the `/admin/sync` history/retry view).
  Session + CSRF + secure-header middleware; mounted at `/admin` by
  `cmd/subscription-service`.
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
   clients by `subId`, fetches the subscriber's canonical share links via
   `GetSubLinks`, and builds a `domain.Subscription{Clients, Links}`.
3. Content negotiation on `User-Agent` picks a renderer:
   - browser → `theme.Engine.Render` (HTML page; app catalog, support info,
     and `connlink.Build` for the direct-connection-links card)
   - Clash/Mihomo/stash → `tmplctx.ParseEntries(sub.Links)` filtered to
     successfully-parsed contexts, fed to the `clash`/`mihomo` generator
     using the subscriber's assigned profile (`assignment.Store.Resolve`)
   - everything else → `sub.Links` joined and base64-encoded verbatim, no
     profile, no parsing, no generation at all
4. Response is gzip-compressed and logged.

## Write path (admin → 3x-ui)

`internal/resolver` and everything downstream of it (every generator, the
subscription page) is completely unchanged by user management — it still
does a live scan of whatever's actually on the panel, since xray-core is
what actually enforces limits and meters traffic. `internal/users`/
`internal/sync` are a separate *management* layer that pushes desired
state at the panel:

1. An admin action in `/admin/users` (create/edit/delete/toggle/reset-
   traffic/regenerate-uuid/assign-inbounds/bulk) writes the local `users`/
   `user_inbounds` row(s) via `users.Store`, then enqueues one `sync_jobs`
   row per affected inbound via `sync.Store.Enqueue` — in the same request,
   immediately after the local write succeeds. (This is deliberately not a
   single cross-package SQL transaction spanning both stores — the gap
   between the two calls is one local step in one process, and any missed
   enqueue is always visible and manually retriable from `/admin/sync`, a
   simplification judged proportionate to this system's actual risk
   profile rather than threading `*sql.Tx` through every `users.Store`
   method.)
2. The request returns immediately — the admin isn't waiting on a
   potentially slow/unreachable panel call.
3. `sync.Worker` (running in the background, started from
   `cmd/subscription-service`) picks up the job on its next tick, calls the
   matching `xui.Client` write method, and updates the job's status
   (`success`/retried-with-backoff/terminally `failed`).
4. On success, the shared `CachedLister` is invalidated, so the existing
   resolver/subscription path picks up the change on the very next
   request — no resolver code involved at all.

`update`/`reset_traffic` jobs are enqueued once per assigned inbound (the
same granularity as `assign`/`unassign`, so the `sync_jobs` schema didn't
need a separate "user-level, no specific inbound" job shape), even though
the panel's client API operates per-*client* (by email) rather than
per-inbound — a user with 3 assigned inbounds fires 3 idempotent, identical
`UpdateClient`/`ResetClientTraffic` calls instead of 1. Accepted as a minor
inefficiency, not a correctness issue, in exchange for not having two
different job shapes.

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

## What's deferred

Nothing is currently deferred from the original spec. Write-back to 3x-ui
(centralized user management, syncing to the panel) is implemented — see
`internal/users`/`internal/sync` above. Multiple admin accounts /
role-based access is out of scope by design — single admin account is
intentional, not a placeholder. 3x-ui's own multi-node/multi-panel
management is likewise out of scope by design — this service talks to one
connected panel.
