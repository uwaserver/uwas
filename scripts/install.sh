#!/usr/bin/env bash
# UWAS Installer — Unified Web Application Server
# Usage: curl -fsSL https://raw.githubusercontent.com/uwaserver/uwas/main/scripts/install.sh | bash
set -euo pipefail

REPO="uwaserver/uwas"
BIN="/usr/local/bin/uwas"
CONFIG_DIR="/etc/uwas"
SERVICE="/etc/systemd/system/uwas.service"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

info()  { echo -e "${BLUE}[INFO]${NC}  $*"; }
ok()    { echo -e "${GREEN}[OK]${NC}    $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
fail()  { echo -e "${RED}[FAIL]${NC}  $*"; exit 1; }

verify_release_checksum() {
  local asset_file=$1
  local asset_name=$2
  local checksum_file=$3
  local checksum_base asset_dir expected actual
  checksum_base=$(basename "$checksum_file")
  asset_dir=$(dirname "$asset_file")

  if ! awk -v name="$asset_name" '$2 == name { found=1 } END { exit found ? 0 : 1 }' "$checksum_file"; then
    fail "Checksum for $asset_name not found in SHA256SUMS"
  fi

  if command -v sha256sum >/dev/null 2>&1; then
    if ! (cd "$asset_dir" && awk -v name="$asset_name" '$2 == name { print; found=1 } END { if (!found) exit 1 }' "$checksum_base" | sha256sum -c - >/dev/null); then
      fail "Checksum verification failed for $asset_name"
    fi
  elif command -v shasum >/dev/null 2>&1; then
    expected=$(awk -v name="$asset_name" '$2 == name { print $1; exit }' "$checksum_file")
    actual=$(shasum -a 256 "$asset_file" | awk '{ print $1 }')
    if [[ -z "$expected" || "$expected" != "$actual" ]]; then
      fail "Checksum verification failed for $asset_name"
    fi
  else
    fail "Checksum verification requires sha256sum or shasum"
  fi
}

echo ""
echo -e "${GREEN}╔══════════════════════════════════════════╗${NC}"
echo -e "${GREEN}║     UWAS — Unified Web Application Server     ║${NC}"
echo -e "${GREEN}╚══════════════════════════════════════════╝${NC}"
echo ""

# ── Checks ──────────────────────────────────────────────

[[ "$(uname -s)" == "Linux" ]] || fail "UWAS only supports Linux"
[[ "$(id -u)" -eq 0 ]] || fail "Run as root: curl -fsSL ... | sudo bash"

ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ASSET="uwas-linux-amd64" ;;
  aarch64|arm64) ASSET="uwas-linux-arm64" ;;
  *) fail "Unsupported architecture: $ARCH" ;;
esac

command -v curl >/dev/null || fail "curl is required — apt install curl"

# ── Detect existing install ─────────────────────────────

if [[ -f "$BIN" ]]; then
  CURRENT=$("$BIN" version 2>/dev/null || echo "unknown")
  warn "UWAS already installed: $CURRENT"
  echo -n "  Upgrade to latest? [Y/n] "
  read -r REPLY
  [[ "$REPLY" =~ ^[Nn] ]] && { info "Cancelled."; exit 0; }
  UPGRADE=1
else
  UPGRADE=0
fi

# ── Download latest release ─────────────────────────────

info "Fetching latest release from GitHub..."
LATEST=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"//;s/".*//')
[[ -n "$LATEST" ]] || fail "Could not determine latest version"
ok "Latest version: $LATEST"

DOWNLOAD_URL="https://github.com/$REPO/releases/download/$LATEST/$ASSET"
CHECKSUM_URL="https://github.com/$REPO/releases/download/$LATEST/SHA256SUMS"
info "Downloading $ASSET..."
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT HUP INT TERM
TMP="$TMPDIR/$ASSET"
curl -fsSL -o "$TMP" "$DOWNLOAD_URL" || { rm -rf "$TMPDIR"; fail "Download failed: $DOWNLOAD_URL"; }
ok "Downloaded $(du -h "$TMP" | cut -f1)"

info "Downloading SHA256SUMS..."
curl -fsSL -o "$TMPDIR/SHA256SUMS" "$CHECKSUM_URL" || { rm -rf "$TMPDIR"; fail "Checksum download failed: $CHECKSUM_URL"; }
info "Verifying checksum..."
verify_release_checksum "$TMP" "$ASSET" "$TMPDIR/SHA256SUMS"
ok "Checksum verified"

# ── Stop existing service ───────────────────────────────

if [[ "$UPGRADE" -eq 1 ]]; then
  if systemctl is-active uwas >/dev/null 2>&1; then
    info "Stopping UWAS service..."
    systemctl stop uwas
    ok "Service stopped"
  fi
fi

# ── Install binary ──────────────────────────────────────

mv "$TMP" "$BIN"
rm -rf "$TMPDIR"
chmod 755 "$BIN"
ok "Binary installed: $BIN"

# Symlink for convenience
[[ -L /usr/bin/uwas ]] || ln -sf "$BIN" /usr/bin/uwas
ok "Symlink: /usr/bin/uwas -> $BIN"

# ── Create config directory ─────────────────────────────

if [[ ! -d "$CONFIG_DIR" ]]; then
  mkdir -p "$CONFIG_DIR/domains.d"
  ok "Config directory: $CONFIG_DIR"
else
  ok "Config directory exists: $CONFIG_DIR"
fi

# ── Create systemd service ──────────────────────────────

if [[ ! -f "$SERVICE" ]]; then
  cat > "$SERVICE" << 'SERVICEEOF'
[Unit]
Description=UWAS — Unified Web Application Server
After=network.target mariadb.service
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/uwas serve -c /etc/uwas/uwas.yaml
ExecStop=/usr/local/bin/uwas stop
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RestartSec=5
User=root
WorkingDirectory=/etc/uwas
LimitNOFILE=65536
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
SERVICEEOF
  systemctl daemon-reload
  systemctl enable uwas
  ok "Systemd service created and enabled"
else
  ok "Systemd service exists"
fi

# ── Resilience drop-in ──────────────────────────────────
# Written as a drop-in rather than by rewriting the unit: an existing install
# may carry operator edits, and an upgrade must not silently discard them.
# Drop-ins layer over whatever the base unit says, so this reaches old installs
# too — which is the point, since they are the ones without a watchdog.

DROPIN_DIR="/etc/systemd/system/uwas.service.d"
mkdir -p "$DROPIN_DIR"
cat > "$DROPIN_DIR/10-resilience.conf" << 'DROPINEOF'
# Managed by the UWAS installer. Edit the unit, not this file.
[Unit]
# Without a start limit systemd stops trying after 5 restarts in 10s, turning a
# recoverable overload into a permanent outage.
StartLimitIntervalSec=300
StartLimitBurst=10

[Service]
# NotifyAccess=main enables the sd_notify watchdog ping regardless of Type=.
NotifyAccess=main

# Restart on any exit: a process killed by the OOM killer or by the watchdog's
# SIGABRT must come back the same way a crash does.
Restart=always
RestartSec=5

# systemd's Restart= only sees a process that exits. WatchdogSec also catches
# the case it cannot: still running, still "active", answering nothing.
# UWAS pings only while its own liveness probe passes, so this requires
# global.watchdog.enabled in uwas.yaml — with the probe off no ping is ever
# sent and systemd would restart a healthy server every 60 seconds.
WatchdogSec=60

# Connection floods exhaust file descriptors before anything else, which shows
# up as "accept: too many open files" and a listener that stops accepting.
LimitNOFILE=1048576

# Persistent state (the autoblock list) so blocks survive a restart.
StateDirectory=uwas
StateDirectoryMode=0750
DROPINEOF

# The watchdog drop-in is only safe once the config asks for the probe.
# Enabling WatchdogSec against a binary or config that never pings would
# restart a perfectly healthy server on a timer.
if ! grep -qE '^[[:space:]]*watchdog:' /etc/uwas/uwas.yaml 2>/dev/null; then
  sed -i 's/^WatchdogSec=/#WatchdogSec=/' "$DROPIN_DIR/10-resilience.conf"
  warn "watchdog not configured in uwas.yaml — WatchdogSec left commented out."
  warn "Add global.watchdog.enabled: true, then uncomment it in $DROPIN_DIR/10-resilience.conf"
fi

systemctl daemon-reload
ok "Systemd resilience drop-in installed"

# ── Start service ───────────────────────────────────────

if [[ "$UPGRADE" -eq 1 ]]; then
  info "Starting UWAS service..."
  systemctl start uwas
  sleep 1
  if systemctl is-active uwas >/dev/null 2>&1; then
    ok "UWAS upgraded to $LATEST and running"
  else
    warn "Service started but may not be active yet — check: journalctl -u uwas"
  fi
else
  echo ""
  info "UWAS installed. Next steps:"
  echo ""
  echo "  1. Start the first-run wizard:"
  echo -e "     ${GREEN}uwas serve${NC}"
  echo ""
  echo "  2. Or start as a service:"
  echo -e "     ${GREEN}systemctl start uwas${NC}"
  echo ""
  echo "  3. Dashboard will be at:"
  echo -e "     ${BLUE}http://YOUR_IP:9443/_uwas/dashboard/${NC}"
  echo ""
fi

# ── Version info ────────────────────────────────────────

echo ""
"$BIN" version 2>/dev/null || true
echo ""
echo -e "${GREEN}Done!${NC} Docs: https://github.com/$REPO"
echo ""
