#!/bin/sh
set -e

# ╔═══════════════════════════════════════════════════════╗
# ║  UWAS — Unified Web Application Server               ║
# ║  One-line installer                                   ║
# ║                                                       ║
# ║  curl -fsSL https://uwaserver.com/install.sh | sh     ║
# ╚═══════════════════════════════════════════════════════╝

REPO="uwaserver/uwas"
INSTALL_DIR="/usr/local/bin"
BINARY="uwas"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

info()  { printf "${CYAN}${BOLD}▸${NC} %s\n" "$1"; }
ok()    { printf "${GREEN}${BOLD}✓${NC} %s\n" "$1"; }
fail()  { printf "${RED}${BOLD}✗${NC} %s\n" "$1"; exit 1; }

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64|amd64)  ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) fail "Unsupported architecture: $ARCH" ;;
esac

case "$OS" in
    linux)  SUFFIX="${OS}-${ARCH}" ;;
    darwin) SUFFIX="${OS}-${ARCH}" ;;
    *)      fail "Unsupported OS: $OS (Linux and macOS supported)" ;;
esac

# Get latest version from GitHub
info "Fetching latest release..."
VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | grep '"tag_name"' | head -1 | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\(.*\)".*/\1/')

if [ -z "$VERSION" ]; then
    # Fallback: try releases endpoint
    VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases" 2>/dev/null | grep '"tag_name"' | head -1 | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\(.*\)".*/\1/')
fi

if [ -z "$VERSION" ]; then
    fail "Could not determine latest version. Check https://github.com/$REPO/releases"
fi

FILENAME="uwas-${SUFFIX}"
URL="https://github.com/$REPO/releases/download/${VERSION}/${FILENAME}"

info "Downloading UWAS ${VERSION} for ${OS}/${ARCH}..."

# Download binary
TMPDIR=$(mktemp -d)
HTTP_CODE=$(curl -fsSL -w "%{http_code}" -o "$TMPDIR/$BINARY" "$URL" 2>/dev/null || echo "000")

if [ "$HTTP_CODE" != "200" ] && [ ! -s "$TMPDIR/$BINARY" ]; then
    rm -rf "$TMPDIR"
    fail "Download failed (HTTP $HTTP_CODE). Binary may not exist for this platform."
fi

chmod +x "$TMPDIR/$BINARY"

# Verify it's a real binary
if ! "$TMPDIR/$BINARY" version >/dev/null 2>&1; then
    rm -rf "$TMPDIR"
    fail "Downloaded file is not a valid UWAS binary"
fi

# Install
info "Installing to ${INSTALL_DIR}/${BINARY}..."
if [ -w "$INSTALL_DIR" ]; then
    mv "$TMPDIR/$BINARY" "$INSTALL_DIR/$BINARY"
else
    sudo mv "$TMPDIR/$BINARY" "$INSTALL_DIR/$BINARY"
    sudo chmod +x "$INSTALL_DIR/$BINARY"
fi

# Cleanup
rm -rf "$TMPDIR"

# Show version
echo ""
$BINARY version
echo ""
ok "UWAS installed to ${INSTALL_DIR}/${BINARY}"
echo ""

# Post-install: on Linux, seed /etc/uwas/uwas.yaml + register and start the
# systemd service automatically. macOS skips this entire block (no systemd).
#
# `uwas install` itself runs the full setup:
#   - seeds /etc/uwas/uwas.yaml + /etc/uwas/.env with a generated API key + pin
#   - writes /etc/systemd/system/uwas.service
#   - systemctl daemon-reload + enable
#   - starts the service (or asks on a TTY; --yes for non-interactive auto)
#   - prints the dashboard URL + credentials so the operator can log in
#
# Skip with UWAS_NO_SERVICE=1 to install only the binary.
if [ "$OS" = "linux" ] && [ -z "$UWAS_NO_SERVICE" ]; then
    INSTALL_FLAGS="--yes"
    if [ -t 0 ]; then
        # Interactive shell — let uwas install ask before starting.
        INSTALL_FLAGS=""
    fi

    if [ "$(id -u)" = "0" ]; then
        info "Running 'uwas install' to register the systemd service..."
        echo ""
        $BINARY install $INSTALL_FLAGS
    elif command -v sudo >/dev/null 2>&1; then
        info "Running 'sudo uwas install' to register the systemd service..."
        echo "  (you'll be prompted for your sudo password)"
        echo ""
        sudo $BINARY install $INSTALL_FLAGS
    else
        echo ""
        printf "${BOLD}Finish setup manually:${NC}\n"
        echo "  sudo uwas install          # creates /etc/uwas/uwas.yaml, registers + starts service"
        echo ""
        printf "${BOLD}Dashboard:${NC}\n"
        echo "  http://127.0.0.1:9443/_uwas/dashboard/  (credentials printed by 'uwas install')"
        echo ""
        echo "Docs: https://github.com/$REPO"
        exit 0
    fi
else
    # macOS or UWAS_NO_SERVICE=1 — print manual guidance only.
    printf "${BOLD}Quick start:${NC}\n"
    echo "  uwas                    # Auto-setup + start server (config in ~/.uwas/)"
    echo "  uwas serve -c uwas.yaml # Start with specific config"
    echo "  uwas doctor             # System diagnostics"
    echo ""
    if [ "$OS" = "linux" ]; then
        printf "${BOLD}Systemd service:${NC}\n"
        echo "  sudo uwas install       # /etc/uwas/uwas.yaml + systemd service + autostart"
        echo ""
    fi
    printf "${BOLD}Dashboard:${NC}\n"
    echo "  http://127.0.0.1:9443/_uwas/dashboard/"
    echo ""
fi

echo "Docs: https://github.com/$REPO"
echo ""
