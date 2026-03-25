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
info "Downloading $ASSET..."
TMP=$(mktemp)
curl -fsSL -o "$TMP" "$DOWNLOAD_URL" || fail "Download failed: $DOWNLOAD_URL"
ok "Downloaded $(du -h "$TMP" | cut -f1)"

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
  systemctl daemon-reload
fi

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
