#!/bin/sh
set -e

# ╔═══════════════════════════════════════════════════════╗
# ║  UWAS — Update Script                                ║
# ║                                                       ║
# ║  curl -fsSL https://uwaserver.com/update.sh | sh      ║
# ╚═══════════════════════════════════════════════════════╝

REPO="uwaserver/uwas"
BINARY="uwas"

RED='\033[0;31m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

info()  { printf "${CYAN}${BOLD}▸${NC} %s\n" "$1"; }
ok()    { printf "${GREEN}${BOLD}✓${NC} %s\n" "$1"; }
fail()  { printf "${RED}${BOLD}✗${NC} %s\n" "$1"; exit 1; }

verify_release_checksum() {
    asset_file=$1
    asset_name=$2
    checksum_file=$3
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
        if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
            fail "Checksum verification failed for $asset_name"
        fi
    else
        fail "Checksum verification requires sha256sum or shasum"
    fi
}

# Find current binary
BIN_PATH=$(which $BINARY 2>/dev/null || echo "/usr/local/bin/$BINARY")
if [ ! -f "$BIN_PATH" ]; then
    fail "UWAS not found. Run install.sh first."
fi

# Current version
CURRENT=$($BIN_PATH version 2>/dev/null | head -1 | awk '{print $2}' || echo "unknown")
info "Current version: ${CURRENT}"

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64)  ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) fail "Unsupported architecture: $ARCH" ;;
esac

# Get latest version
info "Checking for updates..."
VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | grep '"tag_name"' | head -1 | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\(.*\)".*/\1/')
if [ -z "$VERSION" ]; then
    VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases" 2>/dev/null | grep '"tag_name"' | head -1 | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\(.*\)".*/\1/')
fi

if [ -z "$VERSION" ]; then
    fail "Could not determine latest version"
fi

if [ "$CURRENT" = "$VERSION" ]; then
    ok "Already up to date: ${VERSION}"
    exit 0
fi

info "Updating: ${CURRENT} → ${VERSION}"

# Download
FILENAME="uwas-${OS}-${ARCH}"
URL="https://github.com/$REPO/releases/download/${VERSION}/${FILENAME}"
CHECKSUM_URL="https://github.com/$REPO/releases/download/${VERSION}/SHA256SUMS"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT HUP INT TERM
TMPFILE="$TMPDIR/$FILENAME"
HTTP_CODE=$(curl -fsSL -w "%{http_code}" -o "$TMPFILE" "$URL" 2>/dev/null || echo "000")

if [ "$HTTP_CODE" != "200" ] || [ ! -s "$TMPFILE" ]; then
    rm -rf "$TMPDIR"
    fail "Download failed (HTTP $HTTP_CODE)"
fi

CHECKSUM_HTTP_CODE=$(curl -fsSL -w "%{http_code}" -o "$TMPDIR/SHA256SUMS" "$CHECKSUM_URL" 2>/dev/null || echo "000")
if [ "$CHECKSUM_HTTP_CODE" != "200" ] || [ ! -s "$TMPDIR/SHA256SUMS" ]; then
    rm -rf "$TMPDIR"
    fail "Checksum download failed (HTTP $CHECKSUM_HTTP_CODE). Refusing to install unverifiable binary."
fi

info "Verifying checksum..."
verify_release_checksum "$TMPFILE" "$FILENAME" "$TMPDIR/SHA256SUMS"
ok "Checksum verified"

chmod +x "$TMPFILE"

# Verify binary
if ! "$TMPFILE" version >/dev/null 2>&1; then
    rm -f "$TMPFILE"
    fail "Downloaded file is not a valid UWAS binary"
fi

NEW_VER=$("$TMPFILE" version 2>/dev/null | head -1 | awk '{print $2}')

# Replace binary
info "Installing ${NEW_VER}..."
if [ -w "$(dirname "$BIN_PATH")" ]; then
    mv "$TMPFILE" "$BIN_PATH"
else
    sudo mv "$TMPFILE" "$BIN_PATH"
    sudo chmod +x "$BIN_PATH"
fi
rm -rf "$TMPDIR"

ok "Updated to ${NEW_VER}"

# Restart service if running under systemd
if systemctl is-active uwas >/dev/null 2>&1; then
    info "Restarting UWAS service..."
    sudo systemctl restart uwas
    ok "Service restarted"
elif pgrep -x uwas >/dev/null 2>&1; then
    printf "${CYAN}${BOLD}▸${NC} UWAS is running but not as a systemd service.\n"
    printf "  Restart manually: ${BOLD}kill \$(pgrep uwas) && uwas serve -d${NC}\n"
fi

echo ""
$BIN_PATH version
echo ""
ok "Update complete"
