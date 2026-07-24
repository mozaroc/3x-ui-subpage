#!/usr/bin/env bash
# install.sh — installs the 3x-ui subscription-service from a GitHub
# release, either:
#
#   - "dedicated" mode: on its own server, behind a fresh nginx vhost with
#     a Let's Encrypt certificate obtained via certbot, or
#   - "panel" mode: alongside an existing 3x-ui-pro installation
#     (https://github.com/mozaroc/3x-ui-pro), reusing its domain/cert by
#     dropping an nginx snippet into the existing panel vhost.
#
# Either way the service is reachable only under a randomly generated URL
# path (e.g. https://domain.tld/aB3xQz.../), never at the bare domain root,
# and runs as a systemd service (subscription-service.service) matching
# this repo's own deploy/subscription-service.service + docs/README.md
# systemd walkthrough: user/dir/service all named "subscription-service",
# under /opt, /etc, /var/lib.
#
# Usage:
#   sudo bash install.sh [options]
#   sudo bash install.sh --uninstall [-y]
#
# Options:
#   --mode dedicated|panel     Force install mode (auto-detected otherwise)
#   --version <tag>            Release tag to install (default: latest)
#   --domain <domain>          Domain this service is reachable under
#   --xui-base-url <url>       3x-ui panel API base URL
#   --xui-api-key <key>        3x-ui panel API token (Settings -> Security -> API Token)
#   --server-host <host>       Public address VPN clients should connect to
#   -y, --yes                  Assume yes on destructive confirmations (uninstall)
#   --uninstall                Remove the service (add -y to skip prompts)
#   -h, --help                 Show this help
#
# Recommended usage is download-then-run (not `curl | bash`), since this
# script prompts interactively for anything not passed as a flag:
#   wget -qO install.sh https://raw.githubusercontent.com/mozaroc/3x-ui-subpage/main/install.sh
#   sudo bash install.sh

set -euo pipefail

REPO="mozaroc/3x-ui-subpage"
GITHUB_API="https://api.github.com"

SERVICE_NAME="subscription-service"
SERVICE_USER="subscription-service"
INSTALL_DIR="/opt/subscription-service"
CONFIG_DIR="/etc/subscription-service"
DATA_DIR="/var/lib/subscription-service"
BOOTSTRAP_FILE="${CONFIG_DIR}/bootstrap.yaml"
DB_FILE="${DATA_DIR}/data.db"
BINARY_PATH="${INSTALL_DIR}/subscription-service"
SYSTEMD_UNIT="/etc/systemd/system/${SERVICE_NAME}.service"
NGINX_SNIPPET="/etc/nginx/snippets/${SERVICE_NAME}.conf"
NGINX_VHOST_DEDICATED="/etc/nginx/sites-available/${SERVICE_NAME}"
LISTEN_ADDR="127.0.0.1:8080"

MODE=""
VERSION="latest"
DOMAIN=""
XUI_BASE_URL=""
XUI_API_KEY=""
SERVER_HOST=""
UNINSTALL=""
ASSUME_YES="n"

WORKDIR=""
RELEASE_JSON=""
RELEASE_TAG=""
DOWNLOAD_BIN_NAME=""
RANDOM_PATH=""
PUBLIC_URL=""
ADMIN_PASSWORD=""
DETECTED_DOMAIN=""
DETECTED_XUI_PORT=""
DETECTED_VHOST=""

# ---------------------------------------------------------------------------
# logging / arg parsing
# ---------------------------------------------------------------------------

log()  { echo -e "\033[1;32m==>\033[0m $*"; }
warn() { echo -e "\033[1;33m==> WARNING:\033[0m $*" >&2; }
die()  { echo -e "\033[1;31m==> ERROR:\033[0m $*" >&2; exit 1; }

usage() {
  sed -n '2,/^set -euo/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'
  exit "${1:-0}"
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --mode) MODE="${2:?--mode requires an argument}"; shift 2 ;;
      --version) VERSION="${2:?--version requires an argument}"; shift 2 ;;
      --domain) DOMAIN="${2:?--domain requires an argument}"; shift 2 ;;
      --xui-base-url) XUI_BASE_URL="${2:?--xui-base-url requires an argument}"; shift 2 ;;
      --xui-api-key) XUI_API_KEY="${2:?--xui-api-key requires an argument}"; shift 2 ;;
      --server-host) SERVER_HOST="${2:?--server-host requires an argument}"; shift 2 ;;
      --uninstall) UNINSTALL="1"; shift ;;
      -y|--yes) ASSUME_YES="y"; shift ;;
      -h|--help) usage 0 ;;
      *) die "unknown argument: $1 (see --help)" ;;
    esac
  done

  case "$MODE" in
    ""|dedicated|panel) ;;
    *) die "--mode must be 'dedicated' or 'panel', got: $MODE" ;;
  esac
}

require_root() {
  [[ "${EUID}" -eq 0 ]] || die "must run as root (sudo bash install.sh ...)"
}

# ---------------------------------------------------------------------------
# OS / arch
# ---------------------------------------------------------------------------

check_os() {
  [[ -r /etc/os-release ]] || die "cannot detect OS: /etc/os-release missing"
  local os_id os_version
  os_id="$(grep -oP '(?<=^ID=).+' /etc/os-release | tr -d '"')"
  os_version="$(grep -oP '(?<=^VERSION_ID=").+(?=")' /etc/os-release || true)"
  case "$os_id" in
    ubuntu)
      [[ "$os_version" == "24.04" || "$os_version" == "26.04" ]] && return 0
      ;;
    debian)
      [[ "$os_version" == "12" || "$os_version" == "13" ]] && return 0
      ;;
  esac
  die "unsupported OS: ${os_id} ${os_version} (supported: Ubuntu 24.04/26.04, Debian 12/13)"
}

arch_suffix() {
  case "$(uname -m)" in
    x86_64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    *) die "unsupported architecture: $(uname -m)" ;;
  esac
}

rand_str() {
  tr -dc 'a-zA-Z0-9' </dev/urandom | head -c "$1" || true
  echo
}

# ---------------------------------------------------------------------------
# dependencies
# ---------------------------------------------------------------------------

install_packages() {
  log "Installing dependencies (apt)"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq curl jq tar ca-certificates sqlite3 \
    nginx-full certbot python3-certbot-nginx
  systemctl enable --now nginx >/dev/null 2>&1 || true
}

# ---------------------------------------------------------------------------
# mode detection
# ---------------------------------------------------------------------------

detect_mode() {
  if [[ -n "$MODE" ]]; then
    log "Install mode (forced): ${MODE}"
    return
  fi
  if [[ -d /etc/x-ui ]] || systemctl list-unit-files 2>/dev/null | grep -q '^x-ui\.service'; then
    MODE="panel"
  else
    MODE="dedicated"
  fi
  log "Detected install mode: ${MODE}"
}

current_server_name() {
  awk '/server_name/{print $2; exit}' "$1" 2>/dev/null | tr -d ';'
}

detect_panel_domain_port() {
  local cert_file="" domain="" port="" vhost="" candidate
  if [[ -f /etc/x-ui/x-ui.db ]]; then
    cert_file="$(sqlite3 /etc/x-ui/x-ui.db "SELECT value FROM settings WHERE key='webCertFile'" 2>/dev/null || true)"
    if [[ -n "$cert_file" ]]; then
      domain="$(grep -oP '(?:/etc/letsencrypt/live|/root/cert)/\K[^/]+' <<<"$cert_file" || true)"
    fi
    port="$(sqlite3 /etc/x-ui/x-ui.db "SELECT value FROM settings WHERE key='webPort'" 2>/dev/null || true)"
  fi

  # Find the actual panel vhost file (not just its domain) so later steps
  # can insert into the file we actually found, instead of re-guessing a
  # path from the domain string (which may not match the real filename).
  for candidate in /etc/nginx/sites-available/*; do
    [[ -f "$candidate" ]] || continue
    grep -q 'listen 7443' "$candidate" || continue
    if [[ -z "$domain" ]]; then
      vhost="$candidate"
      domain="$(current_server_name "$candidate")"
      break
    elif [[ "$(current_server_name "$candidate")" == "$domain" ]]; then
      vhost="$candidate"
      break
    fi
  done

  DETECTED_DOMAIN="$domain"
  DETECTED_XUI_PORT="${port:-2053}"
  DETECTED_VHOST="$vhost"
}

# Locate the panel vhost file for $DOMAIN: reuse the file detect_panel_domain_port
# already found if the domain wasn't changed since, else search sites-available
# by server_name, else fall back to the filename==domain convention.
resolve_panel_vhost() {
  if [[ -n "$DETECTED_VHOST" && "$DOMAIN" == "$DETECTED_DOMAIN" ]]; then
    echo "$DETECTED_VHOST"
    return
  fi
  local f
  for f in /etc/nginx/sites-available/*; do
    [[ -f "$f" ]] || continue
    [[ "$(current_server_name "$f")" == "$DOMAIN" ]] && { echo "$f"; return; }
  done
  if [[ -f "/etc/nginx/sites-available/${DOMAIN}" ]]; then
    echo "/etc/nginx/sites-available/${DOMAIN}"
    return
  fi
  return 1
}

# ---------------------------------------------------------------------------
# interactive prompts (read from the controlling tty so this still works
# when the script itself was piped in, e.g. `curl ... | bash`)
# ---------------------------------------------------------------------------

prompt_if_empty() {
  local varname="$1" prompt="$2" default="${3:-}" current input
  current="${!varname}"
  [[ -n "$current" ]] && return
  if [[ -n "$default" ]]; then
    read -rp "${prompt} [${default}]: " input </dev/tty
    input="${input:-$default}"
  else
    read -rp "${prompt}: " input </dev/tty
  fi
  printf -v "$varname" '%s' "$input"
}

gather_config() {
  if [[ "$MODE" == "panel" ]]; then
    detect_panel_domain_port
    prompt_if_empty DOMAIN "Panel domain" "$DETECTED_DOMAIN"
    prompt_if_empty XUI_BASE_URL "3x-ui panel API base URL" "http://127.0.0.1:${DETECTED_XUI_PORT}"
  else
    prompt_if_empty DOMAIN "Domain for this service (must already point at this server)"
    prompt_if_empty XUI_BASE_URL "3x-ui panel API base URL"
  fi

  if [[ -z "$XUI_API_KEY" ]]; then
    echo "3x-ui API key: generate one in the panel's own UI under Settings -> Security -> API Token."
    read -rp "3x-ui API key: " XUI_API_KEY </dev/tty
  fi

  if [[ -z "$SERVER_HOST" ]]; then
    local detected
    detected="$(curl -fs -4 --max-time 5 https://api.ipify.org || true)"
    [[ -n "$detected" ]] || detected="$(hostname -I 2>/dev/null | awk '{print $1}')"
    prompt_if_empty SERVER_HOST "Public address VPN clients should connect to" "$detected"
  fi
}

# ---------------------------------------------------------------------------
# GitHub release download (public repo: plain unauthenticated API + the
# asset's public browser_download_url)
# ---------------------------------------------------------------------------

gh_api() {
  curl -fsSL -H "Accept: application/vnd.github+json" "$@"
}

asset_url_for() {
  local name="$1"
  jq -r --arg n "$name" '.assets[] | select(.name == $n) | .browser_download_url' <<<"$RELEASE_JSON"
}

resolve_release() {
  log "Resolving release: ${VERSION}"
  local url
  if [[ "$VERSION" == "latest" ]]; then
    url="${GITHUB_API}/repos/${REPO}/releases/latest"
  else
    url="${GITHUB_API}/repos/${REPO}/releases/tags/${VERSION}"
  fi
  RELEASE_JSON="$(gh_api "$url")" || die "failed to fetch release metadata from ${url} (network, or tag not found)"
  RELEASE_TAG="$(jq -r '.tag_name // empty' <<<"$RELEASE_JSON")"
  [[ -n "$RELEASE_TAG" ]] || die "could not resolve a release tag from ${url}"
  log "Using release ${RELEASE_TAG}"
}

download_release() {
  resolve_release
  local arch bin_name bin_url web_url sum_url
  arch="$(arch_suffix)"
  bin_name="subscription-service-linux-${arch}"

  bin_url="$(asset_url_for "$bin_name")"
  web_url="$(asset_url_for web.tar.gz)"
  sum_url="$(asset_url_for checksums.txt)"
  [[ -n "$bin_url" ]] || die "release ${RELEASE_TAG} has no asset named ${bin_name}"
  [[ -n "$web_url" ]] || die "release ${RELEASE_TAG} has no web.tar.gz asset (built from an older tag? try --version with a newer one)"
  [[ -n "$sum_url" ]] || die "release ${RELEASE_TAG} has no checksums.txt asset"

  log "Downloading ${bin_name}"
  curl -fsSL "$bin_url" -o "${WORKDIR}/${bin_name}"
  log "Downloading web.tar.gz"
  curl -fsSL "$web_url" -o "${WORKDIR}/web.tar.gz"
  log "Downloading checksums.txt"
  curl -fsSL "$sum_url" -o "${WORKDIR}/checksums.txt"

  log "Verifying checksum"
  (cd "$WORKDIR" && grep " ${bin_name}\$" checksums.txt | sha256sum -c -) \
    || die "checksum verification failed for ${bin_name}"

  DOWNLOAD_BIN_NAME="$bin_name"
}

# ---------------------------------------------------------------------------
# install: user, files, config, database
# ---------------------------------------------------------------------------

create_service_user() {
  if ! id "$SERVICE_USER" &>/dev/null; then
    log "Creating system user ${SERVICE_USER}"
    useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"
  fi
}

install_files() {
  log "Installing binary + web/ assets to ${INSTALL_DIR}"
  mkdir -p "$INSTALL_DIR" "$CONFIG_DIR" "$DATA_DIR"
  install -m 0755 -o root -g root "${WORKDIR}/${DOWNLOAD_BIN_NAME}" "$BINARY_PATH"
  rm -rf "${INSTALL_DIR}/web"
  tar -xzf "${WORKDIR}/web.tar.gz" -C "$INSTALL_DIR"
}

write_bootstrap_config() {
  log "Writing ${BOOTSTRAP_FILE}"
  cat >"$BOOTSTRAP_FILE" <<EOF
database:
  path: "${DB_FILE}"
EOF
  chmod 0644 "$BOOTSTRAP_FILE"
}

run_import() {
  log "Seeding database from bundled web/ content"
  "$BINARY_PATH" -config "$BOOTSTRAP_FILE" -import "${INSTALL_DIR}/web"
}

sql_escape() { sed "s/'/''/g" <<<"$1"; }

upsert_setting() {
  local key="$1" json="$2" escaped
  escaped="$(sql_escape "$json")"
  sqlite3 "$DB_FILE" \
    "INSERT INTO settings (key, value, updated_at) VALUES ('${key}', '${escaped}', strftime('%s','now')) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at;"
}

seed_settings() {
  log "Seeding server/xui/subscription settings"
  upsert_setting server "{\"listen\":\"${LISTEN_ADDR}\"}"
  upsert_setting xui "$(jq -n --arg u "$XUI_BASE_URL" --arg k "$XUI_API_KEY" '{base_url:$u, api_key:$k}')"
  upsert_setting subscription "$(jq -n --arg u "$PUBLIC_URL" --arg h "$SERVER_HOST" '{public_url:$u, server_host:$h}')"
}

create_admin() {
  log "Creating admin account"
  ADMIN_PASSWORD="$(rand_str 20)"
  "$BINARY_PATH" -config "$BOOTSTRAP_FILE" -create-admin admin -create-admin-password "$ADMIN_PASSWORD"
}

fix_permissions() {
  chown -R "${SERVICE_USER}:${SERVICE_USER}" "$INSTALL_DIR" "$DATA_DIR"
  chmod 0750 "$DATA_DIR"
}

install_systemd_unit() {
  log "Installing systemd unit"
  # Matches deploy/subscription-service.service in this repo verbatim.
  cat >"$SYSTEMD_UNIT" <<'EOF'
[Unit]
Description=3x-ui Subscription Service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=subscription-service
Group=subscription-service
WorkingDirectory=/opt/subscription-service
ExecStart=/opt/subscription-service/subscription-service -config /etc/subscription-service/bootstrap.yaml
Restart=on-failure
RestartSec=2s

# Hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/opt/subscription-service /var/lib/subscription-service
AmbientCapabilities=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable --now "${SERVICE_NAME}"
}

# ---------------------------------------------------------------------------
# nginx: shared location block, mode-specific vhost/snippet wiring
# ---------------------------------------------------------------------------

resolve_random_path() {
  local existing="" target=""
  if [[ "$MODE" == "panel" && -f "$NGINX_SNIPPET" ]]; then
    target="$NGINX_SNIPPET"
  elif [[ "$MODE" == "dedicated" && -f "$NGINX_VHOST_DEDICATED" ]]; then
    target="$NGINX_VHOST_DEDICATED"
  fi
  if [[ -n "$target" ]]; then
    existing="$(grep -oP 'location /\K[a-zA-Z0-9]+(?=/ \{)' "$target" | head -n1 || true)"
  fi
  if [[ -n "$existing" ]]; then
    RANDOM_PATH="$existing"
    log "Reusing existing random path from a previous install: /${RANDOM_PATH}/"
  else
    RANDOM_PATH="$(rand_str 12)"
  fi
}

# Appends the reverse-proxy location block to $1. Uses a quoted heredoc
# (no shell expansion) with a placeholder token, then sed-substitutes the
# random path afterward — keeps nginx's own $host/$scheme/... variables
# from being clobbered by bash's heredoc expansion.
#
# proxy_cookie_path/proxy_redirect/sub_filter exist specifically because
# this app's admin UI uses root-absolute paths everywhere (href="/admin/
# ...", Set-Cookie: Path=/admin, Location: /admin/...) and has no concept
# of being served under a prefix — without these three, session cookies
# would never round-trip under /RANDOM/ (Path=/admin doesn't prefix-match
# a /RANDOM/admin/... request) and every link/redirect in the admin UI
# would resolve back to the (unproxied) domain root.
append_location_block() {
  local out="$1"
  cat >>"$out" <<'NGINX'

    location = /__RANDOM_PATH__ {
        return 301 /__RANDOM_PATH__/;
    }

    location /__RANDOM_PATH__/ {
        proxy_pass http://127.0.0.1:8080/;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # The app gzips its own responses whenever it sees an incoming
        # Accept-Encoding: gzip (which every browser sends) — and
        # sub_filter can't rewrite a gzip-compressed body, it only ever
        # sees plaintext. Blank the header on this leg so the app always
        # answers uncompressed; nginx's own gzip module (already on
        # globally) still compresses what actually reaches the browser.
        proxy_set_header Accept-Encoding "";

        proxy_cookie_path /admin /__RANDOM_PATH__/admin;
        proxy_redirect / /__RANDOM_PATH__/;

        sub_filter_once off;
        sub_filter_types text/html;
        sub_filter 'href="/' 'href="/__RANDOM_PATH__/';
        sub_filter 'src="/' 'src="/__RANDOM_PATH__/';
        sub_filter 'action="/' 'action="/__RANDOM_PATH__/';
    }
NGINX
  sed -i "s/__RANDOM_PATH__/${RANDOM_PATH}/g" "$out"
}

nginx_test_and_reload() {
  if ! nginx -t; then
    die "nginx config test failed (see above) — not reloading"
  fi
  systemctl reload nginx
}

configure_nginx_dedicated() {
  resolve_random_path

  local existing_domain=""
  [[ -f "$NGINX_VHOST_DEDICATED" ]] && existing_domain="$(current_server_name "$NGINX_VHOST_DEDICATED")"

  # Idempotent re-run: if a previous run already wrote this vhost for the
  # same domain (it already has our location block), leave it and certbot's
  # own edits alone rather than clobbering them and re-requesting a
  # certificate — Let's Encrypt rate-limits repeated issuance for the same
  # domain.
  if [[ -n "$existing_domain" && "$existing_domain" == "$DOMAIN" ]] \
    && grep -q "location /${RANDOM_PATH}/ {" "$NGINX_VHOST_DEDICATED"; then
    log "Vhost ${NGINX_VHOST_DEDICATED} already configured for ${DOMAIN}, leaving it and the existing certificate as-is"
    nginx_test_and_reload
    return
  fi

  if [[ -n "$existing_domain" && "$existing_domain" != "$DOMAIN" ]]; then
    warn "existing vhost is for ${existing_domain}, requested domain is ${DOMAIN} — rewriting vhost and requesting a new certificate"
  fi

  log "Writing nginx vhost ${NGINX_VHOST_DEDICATED}"
  cat >"$NGINX_VHOST_DEDICATED" <<EOF
server {
    listen 80;
    listen [::]:80;
    server_name ${DOMAIN};
EOF
  append_location_block "$NGINX_VHOST_DEDICATED"
  echo "}" >>"$NGINX_VHOST_DEDICATED"

  ln -sf "$NGINX_VHOST_DEDICATED" "/etc/nginx/sites-enabled/${SERVICE_NAME}"
  nginx_test_and_reload

  log "Requesting a certificate via certbot (nginx plugin)"
  certbot --nginx -d "$DOMAIN" --non-interactive --agree-tos --redirect \
    --register-unsafely-without-email
  systemctl enable --now certbot.timer >/dev/null 2>&1 || true
}

configure_nginx_panel() {
  resolve_random_path
  local vhost
  vhost="$(resolve_panel_vhost)" || die "could not find an nginx vhost with server_name ${DOMAIN} under /etc/nginx/sites-available — pass --domain to match an existing vhost"

  log "Writing nginx snippet ${NGINX_SNIPPET}"
  : >"$NGINX_SNIPPET"
  append_location_block "$NGINX_SNIPPET"

  if grep -q "snippets/${SERVICE_NAME}.conf" "$vhost"; then
    log "Include already present in ${vhost}, leaving it as-is"
  else
    log "Inserting include into ${vhost} (backup kept alongside it)"
    cp "$vhost" "${vhost}.bak.$(date +%s)"
    if grep -q 'include /etc/nginx/snippets/includes.conf;' "$vhost"; then
      sed -i "s|^\([[:space:]]*\)include /etc/nginx/snippets/includes.conf;|\1include /etc/nginx/snippets/${SERVICE_NAME}.conf;\n\1include /etc/nginx/snippets/includes.conf;|" "$vhost"
    else
      sed -i "\$ s|^}\$|    include /etc/nginx/snippets/${SERVICE_NAME}.conf;\n}|" "$vhost"
    fi
  fi

  nginx_test_and_reload
}

# ---------------------------------------------------------------------------
# verification + summary
# ---------------------------------------------------------------------------

verify_install() {
  log "Waiting for the service to come up"
  local ok=""
  for _ in $(seq 1 20); do
    if curl -fso /dev/null "http://127.0.0.1:8080/healthz"; then
      ok=1
      break
    fi
    sleep 0.5
  done
  if [[ -z "$ok" ]]; then
    journalctl -u "${SERVICE_NAME}" -n 50 --no-pager >&2 || true
    die "service did not come up in time; see log above (journalctl -u ${SERVICE_NAME})"
  fi
  log "Service is healthy locally"

  if curl -fsko /dev/null --max-time 10 "https://${DOMAIN}/${RANDOM_PATH}/healthz"; then
    log "Public endpoint reachable: https://${DOMAIN}/${RANDOM_PATH}/"
  else
    warn "could not reach https://${DOMAIN}/${RANDOM_PATH}/healthz from this host yet (DNS propagation / firewall?) — the service itself is healthy locally"
  fi
}

print_summary() {
  cat <<EOF

=====================================================================
 subscription-service installed (mode: ${MODE})
=====================================================================
 Admin UI:        https://${DOMAIN}/${RANDOM_PATH}/admin
 Admin username:  admin
 Admin password:  ${ADMIN_PASSWORD}

 Subscription base URL (give subscribers /sub/<their-subId>):
   https://${DOMAIN}/${RANDOM_PATH}/sub/<subId>

 3x-ui panel API base URL: ${XUI_BASE_URL}
EOF
  if [[ -z "$XUI_API_KEY" ]]; then
    cat <<EOF

 WARNING: no 3x-ui API key was set. The service will not be able to talk
 to the panel until you set one at:
   https://${DOMAIN}/${RANDOM_PATH}/admin/settings
EOF
  fi
  cat <<EOF

 Config file: ${BOOTSTRAP_FILE}
 Database:    ${DB_FILE}
 Service:     systemctl status ${SERVICE_NAME}
 Logs:        journalctl -u ${SERVICE_NAME} -f
=====================================================================
EOF
}

# ---------------------------------------------------------------------------
# uninstall
# ---------------------------------------------------------------------------

do_uninstall() {
  log "Uninstalling ${SERVICE_NAME}"
  systemctl disable --now "${SERVICE_NAME}" 2>/dev/null || true
  rm -f "$SYSTEMD_UNIT"
  systemctl daemon-reload

  rm -rf "$INSTALL_DIR" "$CONFIG_DIR"

  if [[ "$ASSUME_YES" == "y" ]]; then
    rm -rf "$DATA_DIR"
  else
    local confirm=""
    read -rp "Remove database at ${DATA_DIR} too? [y/N]: " confirm </dev/tty || true
    [[ "$confirm" == "y" || "$confirm" == "Y" ]] && rm -rf "$DATA_DIR"
  fi

  if id "$SERVICE_USER" &>/dev/null; then
    userdel "$SERVICE_USER" 2>/dev/null || true
  fi

  if [[ -f "$NGINX_VHOST_DEDICATED" ]]; then
    rm -f "$NGINX_VHOST_DEDICATED" "/etc/nginx/sites-enabled/${SERVICE_NAME}"
  fi
  if [[ -f "$NGINX_SNIPPET" ]]; then
    rm -f "$NGINX_SNIPPET"
    local f
    for f in /etc/nginx/sites-available/*; do
      [[ -f "$f" ]] || continue
      sed -i "\|snippets/${SERVICE_NAME}.conf|d" "$f"
    done
  fi

  if command -v nginx &>/dev/null; then
    if nginx -t &>/dev/null; then
      systemctl reload nginx || true
    else
      warn "nginx config test failed after removing our snippet — please check /etc/nginx manually"
    fi
  fi

  log "Uninstall complete."
}

# ---------------------------------------------------------------------------
# main
# ---------------------------------------------------------------------------

main() {
  parse_args "$@"
  require_root

  if [[ -n "$UNINSTALL" ]]; then
    do_uninstall
    exit 0
  fi

  check_os
  detect_mode

  WORKDIR="$(mktemp -d)"
  # shellcheck disable=SC2064
  trap "rm -rf '${WORKDIR}'" EXIT

  install_packages
  download_release

  create_service_user
  install_files

  gather_config

  # Resolves (or reuses, on a re-run) RANDOM_PATH as a side effect — the
  # single source of truth for it, so PUBLIC_URL below and the actual
  # nginx config can never disagree on which random path is live.
  if [[ "$MODE" == "dedicated" ]]; then
    configure_nginx_dedicated
  else
    configure_nginx_panel
  fi
  PUBLIC_URL="https://${DOMAIN}/${RANDOM_PATH}"

  write_bootstrap_config
  run_import
  seed_settings
  create_admin
  fix_permissions

  install_systemd_unit
  verify_install
  print_summary
}

main "$@"
