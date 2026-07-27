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
via **Settings** before the service is useful — the panel's URL/API key and
this service's own public subscription URL. The API key comes from the
panel's own UI: **Settings → Security → API Token**; the service sends it as
`Authorization: Bearer <api_key>` on every request. There is no
username/password/session-login option — the panel's REST API is
bearer-token-only.

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
- **Templates** — the Xray-JSON/Clash/Mihomo/Happ/Incy generator templates,
  keyed by `(format, profile, protocol)`. Create a new profile by saving a
  template under a new profile name; a profile missing a template for a
  given format/protocol falls back to `"default"`. Hot-reloaded. **There is
  no Xray share-link (vless/vmess/trojan/ss) template** — this project
  fetches 3x-ui's own canonical share-link strings directly from the panel
  (`GET /panel/api/clients/subLinks/{subId}`) and uses them verbatim,
  parsing them only to feed the Xray-JSON/Clash/Mihomo/Happ/Incy
  generators; it deliberately never reconstructs a share link itself, so
  every connection parameter the panel produces (including future ones this
  project doesn't know about yet) survives intact.
  **Happ and Incy templates are a special case**: those two clients have no
  publicly-documented wire format this project asserts as authoritative, so
  unlike Clash/Mihomo/Xray-JSON (whose dialect is well-known), the
  admin-authored `happ`/`incy` templates fully control the output bytes
  with no schema validation applied. Verify the shipped example template
  against your installed client version before relying on it. Note that
  the real Happ app is deliberately **not** auto-routed to the `happ`
  format on plain `GET /sub/{subId}` — confirmed against Happ's own
  subscription tooling, a stock Happ install imports the same base64
  share-link subscription every other unrecognized client gets, and treats
  the `happ` JSON as an opaque file download instead. That JSON is only
  served from the explicit `GET /sub/{subId}/happ` endpoint, for admins who
  want to experiment with it deliberately (e.g. via a custom app-catalog
  deeplink).
- **Users** — the primary way to manage subscribers now: create, edit,
  suspend/reactivate, enable/disable, reset traffic, change expiration,
  change traffic limits, regenerate UUID, search/filter/sort, and bulk
  operations, all from one list. Every change is pushed out to the
  connected 3x-ui panel automatically (see "Synchronization" below) — you
  should not need to open the 3x-ui web UI to manage users day to day.
  Each user can be assigned to any number of the panel's vless/vmess/
  trojan/shadowsocks inbounds from the detail page's inbound picker; the
  same uuid/password/flow/method applies to every inbound a user is
  assigned to, regardless of that inbound's protocol — unused fields are
  simply ignored by protocols that don't need them. "Regenerate UUID"
  rotates both the uuid *and* the trojan/shadowsocks password together,
  since they're the equivalent credential for those protocols — **but see
  the verified caveat below: on the 3x-ui version this was tested against,
  the panel only actually honors the *password* half of that rotation.**
  The same create/edit form also has a **template assignment** section —
  one dropdown per client type (Xray, Clash, Mihomo, Happ, Incy) listing
  every profile that has a template for that type; picking one is what
  used to require the separate Assignments page. Unpicked client types use
  `"default"`. Hot-reloaded — reassigning a subscriber changes what they're
  served on their very next request. **Xray's own connection links are not
  templated at all** — every direct connection link, and the classic
  base64 subscription body, is 3x-ui's own canonical share-link string,
  fetched from the panel and shown verbatim (copy button, QR code, and a
  per-link config download), never reconstructed by this service. Only the
  full xray-core JSON config, Clash, Mihomo, Happ, and Incy formats are
  admin-templated. The same page also has a **Routing (Happ / Incy)**
  section — just a toggle and a Base64 paste-target, not a rule editor.
  The rules themselves are authored once on the standalone
  **Routing Generator** page (`/admin/routing`), a structured editor for
  every field the Happ/Incy apps' own native "Routing Profile"
  traffic-splitting feature exposes (per
  [Happ's Routing Generator](https://routing.happ.su)) — `GlobalProxy`,
  `RouteOrder`, `DomainStrategy`, remote/domestic DNS, `DnsHosts`,
  GeoIP/GeoSite URLs, Direct/Proxy/Block site+IP lists, `FakeDNS`,
  `UseChunkFiles` — plus a live JSON preview. Clicking Generate produces a
  single Base64 string (with a copy button) that the admin then pastes
  into any number of users' Routing sections, toggling each on
  independently. When enabled and a string is present, it's embedded
  automatically into that subscriber's Happ and Incy responses via the
  `Routing`/`Routing-Enable` HTTP headers (`happ://routing/onadd/<base64>`
  or `incy://...` depending on the client) — mirrors upstream 3x-ui's own
  per-panel implementation, scoped per-subscriber here. This is a
  completely different feature from the old global Xray-core routing rules
  (GEOIP/domain/CIDR baked into a generated config body) that used to live
  on a separate `/admin/routing` page — that page (and its rules) were
  removed outright, and this feature's own page now occupies the same
  URL; Clash/Mihomo/Xray-JSON templates can still express their own
  routing directly in their own template content if needed.
- **Sync** (`/admin/sync`, and the sync history on each user's detail page)
  — every push to 3x-ui is queued, retried with backoff on failure
  (terminally failed after 8 attempts, manually retriable from either
  view), and logged with its outcome. A user's rolled-up sync status is
  "Synced" (every assignment's latest push succeeded), "Syncing…" (a push
  is queued/in flight), "Sync error" (the latest push for at least one
  assignment failed — click through to see why and retry), or "Not synced"
  (no inbounds assigned yet, nothing to push).

  This integration was verified against a live 3x-ui **3.5.0** instance's
  own `/panel/api/clients/*` management API (documented at
  `{base_url}/panel/api-docs` once logged into the panel), keyed uniformly
  by client **email** — see `ARCHITECTURE.md` for the endpoint mapping.
  Two things confirmed empirically against that instance, worth knowing
  before you rely on this in production:
  - **The client's `uuid` is immutable once created** — the panel silently
    ignores any `uuid` sent on create *or* update and always keeps the
    value it generated itself. "Regenerate UUID" therefore has no visible
    effect for vless/vmess users on this panel version (their real,
    currently-active uuid is still whatever the panel assigned — the
    subscription page and generated configs always reflect that live
    value correctly, since they're read straight from the panel, not from
    this service's local copy — only the *local* record is stale). The
    `password` field (trojan/shadowsocks) **is** honored on update, so
    regeneration works fully for those protocols.
  - **`subId` is honored on create** — the subscription token you assign
    in this service's Users UI is what actually ends up on the panel, so
    `/sub/{subId}` links keep working as expected.

  If you're running a different 3x-ui version or fork, its API may differ
  from the above — check `{base_url}/panel/api-docs` (log into the panel
  first; it's session-gated) and verify one create and one edit before
  relying on this for production traffic.

Login is a single admin account (bcrypt password, server-side session,
12h TTL, CSRF-protected forms, secure cookies). There's only one account —
`-create-admin` overwrites it if run again with the same username.

## Settings reference

| Section | Fields |
|---|---|
| `server` | `listen`, `read_timeout`, `write_timeout`, `idle_timeout` |
| `xui` (required) | `base_url`, `api_key`, `timeout`, `retry.max_attempts`, `retry.backoff`, `insecure_skip_verify` |
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
-- tables: settings, applications, themes, theme_files, templates,
-- template_assignments, routing_generator, user_routing, users,
-- user_inbounds, sync_jobs
INSERT INTO settings (key, value, updated_at) VALUES
  ('support', '{"telegram":"https://t.me/example"}', unixepoch());

INSERT INTO templates (format, profile, protocol, content, updated_at)
  VALUES ('mihomo', 'gaming', '', '<your gaming mihomo yaml>', unixepoch());

-- client_type is one of: xray, clash, mihomo, happ, incy ("xray" only
-- governs the xray_json full-config template -- share links are 3x-ui's
-- own canonical strings, fetched directly, never templated by this project)
INSERT INTO template_assignments (sub_id, client_type, profile, updated_at)
  VALUES ('the-subscribers-subid', 'mihomo', 'gaming', unixepoch());

-- routing_b64 is the exact Base64 string produced by the Routing Generator
-- page (/admin/routing) -- an opaque blob, not something to hand-author here
INSERT INTO user_routing (sub_id, enabled, routing_b64, updated_at)
  VALUES ('the-subscribers-subid', 1, '<base64 from the Routing Generator>', unixepoch());
```

The admin UI and direct SQL are just two ways of writing the same rows —
freely mix both, **except for `users`/`user_inbounds`**. Those two are the
one place this isn't true: a row inserted or edited directly via SQL never
pushes anything to 3x-ui, because it's the admin UI's handlers — not the
tables themselves — that enqueue the `sync_jobs` rows a background worker
actually acts on. Manage users through `/admin/users`; use direct SQL on
these two tables only for read-only inspection (or a one-off fix paired
with manually restoring the matching state on the panel yourself).

## Seeding from the example content (`-import`)

`./subscription-service -config bootstrap.yaml -import web` walks a
`web/`-shaped directory (this repo ships one with a default theme, a 6-app
catalog, and default templates for every format) and inserts it as the
`"default"` profile / `"default"` theme. Safe to re-run — it replaces what
it manages without touching anything added through the admin UI or by hand.
