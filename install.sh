#!/bin/sh
set -e

# UWAS Installer
# Usage: curl -fsSL https://uwaserver.com/install.sh | sh

REPO="uwaserver/uwas"
INSTALL_DIR="/usr/local/bin"
BINARY="uwas"

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

case "$OS" in
    linux)  PLATFORM="linux"  ; EXT="tar.gz" ;;
    darwin) PLATFORM="darwin" ; EXT="tar.gz" ;;
    *)      echo "Unsupported OS: $OS"; exit 1 ;;
esac

# Get latest version
VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | sed 's/.*"v\(.*\)".*/\1/')

if [ -z "$VERSION" ]; then
    echo "Failed to fetch latest version"
    exit 1
fi

FILENAME="${BINARY}_${VERSION}_${PLATFORM}_${ARCH}.${EXT}"
URL="https://github.com/$REPO/releases/download/v${VERSION}/${FILENAME}"

echo "Installing UWAS v${VERSION} for ${PLATFORM}/${ARCH}..."

# Download
TMPDIR=$(mktemp -d)
curl -fsSL "$URL" -o "$TMPDIR/$FILENAME"

# Extract
cd "$TMPDIR"
tar xzf "$FILENAME"

# Install
if [ -w "$INSTALL_DIR" ]; then
    mv "$BINARY" "$INSTALL_DIR/$BINARY"
else
    sudo mv "$BINARY" "$INSTALL_DIR/$BINARY"
fi

chmod +x "$INSTALL_DIR/$BINARY"

# Cleanup
rm -rf "$TMPDIR"

# Verify
echo ""
$BINARY version
echo ""
echo "UWAS installed successfully to $INSTALL_DIR/$BINARY"
echo ""
echo "Quick start:"
echo "  uwas serve -c uwas.yaml"
echo ""
echo "Documentation: https://github.com/$REPO"
