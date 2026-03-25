#!/usr/bin/env bash
# UWAS Uninstaller
# Usage: curl -fsSL https://raw.githubusercontent.com/uwaserver/uwas/main/scripts/uninstall.sh | bash
set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "\033[0;34m[INFO]\033[0m  $*"; }
ok()    { echo -e "${GREEN}[OK]${NC}    $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }

echo ""
echo -e "${RED}UWAS Uninstaller${NC}"
echo ""

[[ "$(id -u)" -eq 0 ]] || { echo "Run as root: sudo bash uninstall.sh"; exit 1; }

# ── Confirm ─────────────────────────────────────────────

echo "This will remove:"
echo "  - /usr/local/bin/uwas (binary)"
echo "  - /usr/bin/uwas (symlink)"
echo "  - /etc/systemd/system/uwas.service"
echo ""
echo -e "${YELLOW}This will NOT remove:${NC}"
echo "  - /etc/uwas/ (config files)"
echo "  - /var/www/ (website files)"
echo "  - MySQL databases"
echo "  - SSL certificates"
echo ""
echo -n "Continue? [y/N] "
read -r REPLY
[[ "$REPLY" =~ ^[Yy] ]] || { info "Cancelled."; exit 0; }

# ── Stop service ────────────────────────────────────────

if systemctl is-active uwas >/dev/null 2>&1; then
  info "Stopping UWAS..."
  systemctl stop uwas
  ok "Service stopped"
fi

if systemctl is-enabled uwas >/dev/null 2>&1; then
  systemctl disable uwas
  ok "Service disabled"
fi

# ── Remove files ────────────────────────────────────────

[[ -f /etc/systemd/system/uwas.service ]] && rm /etc/systemd/system/uwas.service && ok "Removed systemd service"
[[ -L /usr/bin/uwas ]] && rm /usr/bin/uwas && ok "Removed /usr/bin/uwas symlink"
[[ -f /usr/local/bin/uwas ]] && rm /usr/local/bin/uwas && ok "Removed /usr/local/bin/uwas binary"

systemctl daemon-reload 2>/dev/null || true

echo ""
ok "UWAS uninstalled"
echo ""
echo "Config and data preserved at:"
echo "  - /etc/uwas/ (config)"
echo "  - /var/www/ (websites)"
echo ""
echo "To remove everything:"
echo -e "  ${RED}rm -rf /etc/uwas /var/www${NC}"
echo ""
