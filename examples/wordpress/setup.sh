#!/bin/bash
set -e

# UWAS + WordPress — One-Command VPS Setup
# Usage: curl -fsSL https://raw.githubusercontent.com/uwaserver/uwas/main/examples/wordpress/setup.sh | bash -s yourdomain.com

DOMAIN="${1:-}"
if [ -z "$DOMAIN" ]; then
  echo "Usage: $0 <domain>"
  echo "Example: $0 myblog.com"
  exit 1
fi

echo "================================================"
echo "  UWAS + WordPress Setup"
echo "  Domain: $DOMAIN"
echo "================================================"
echo ""

# Check Docker
if ! command -v docker &>/dev/null; then
  echo "Installing Docker..."
  curl -fsSL https://get.docker.com | sh
  systemctl enable docker
  systemctl start docker
fi

if ! command -v docker &>/dev/null; then
  echo "ERROR: Docker installation failed"
  exit 1
fi

echo "Docker: $(docker --version)"

# Create directory
INSTALL_DIR="/opt/uwas-wordpress"
mkdir -p "$INSTALL_DIR"
cd "$INSTALL_DIR"

# Download files
echo "Downloading configuration..."
BASE_URL="https://raw.githubusercontent.com/uwaserver/uwas/main/examples/wordpress"
mkdir -p config

curl -fsSL "$BASE_URL/docker-compose.yml" -o docker-compose.yml
curl -fsSL "$BASE_URL/config/uwas.yaml" -o config/uwas.yaml
curl -fsSL "$BASE_URL/config/php.ini" -o config/php.ini
curl -fsSL "$BASE_URL/config/mariadb.cnf" -o config/mariadb.cnf

# Generate secure passwords
DB_ROOT_PASS=$(openssl rand -base64 24 | tr -d '/+=' | head -c 32)
DB_PASS=$(openssl rand -base64 24 | tr -d '/+=' | head -c 32)
ADMIN_KEY=$(openssl rand -base64 24 | tr -d '/+=' | head -c 32)

# Create .env
cat > .env << EOF
DOMAIN=$DOMAIN
ADMIN_EMAIL=admin@$DOMAIN
DB_ROOT_PASSWORD=$DB_ROOT_PASS
DB_NAME=wordpress
DB_USER=wordpress
DB_PASSWORD=$DB_PASS
UWAS_ADMIN_KEY=$ADMIN_KEY
ADMIN_PORT=9443
PHP_MEMORY_LIMIT=256M
EOF

echo ""
echo "Starting services..."
docker compose up -d

echo ""
echo "Waiting for WordPress to initialize..."
sleep 30

echo ""
echo "================================================"
echo "  Setup Complete!"
echo "================================================"
echo ""
echo "  WordPress:  https://$DOMAIN"
echo "  Dashboard:  https://$DOMAIN:9443/_uwas/dashboard/"
echo "  Admin Key:  $ADMIN_KEY"
echo ""
echo "  Database:"
echo "    Root password: $DB_ROOT_PASS"
echo "    WP user:       wordpress"
echo "    WP password:   $DB_PASS"
echo ""
echo "  Saved to: $INSTALL_DIR/.env"
echo ""
echo "  Open https://$DOMAIN to complete WordPress setup."
echo "================================================"
