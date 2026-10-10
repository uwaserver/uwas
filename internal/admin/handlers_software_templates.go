package admin

import (
	"fmt"
	"strconv"
	"strings"
)

// composeEnv renders one "environment:" list entry as a double-quoted YAML
// scalar so a user-supplied value cannot change how the compose file parses
// ("a: b", " #", newlines), and doubles "$" so compose does not interpolate it.
func composeEnv(key, value string) string {
	return strconv.Quote(key + "=" + strings.ReplaceAll(value, "$", "$$"))
}

func composeHeader(_ string) string {
	return "services:\n"
}

func composeUptimeKuma(req softwareInstallRequest, tpl softwareTemplate) string {
	return composeHeader(req.Name) + fmt.Sprintf(`  uptime-kuma:
    image: louislam/uptime-kuma:1
    restart: unless-stopped
    ports:
      - "127.0.0.1:%d:%d"
    volumes:
      - uptime-kuma-data:/app/data
volumes:
  uptime-kuma-data:
`, req.HostPort, tpl.WebPort)
}

func composeN8N(req softwareInstallRequest, tpl softwareTemplate) string {
	webhook := ""
	if req.Domain != "" {
		webhook = "https://" + req.Domain + "/"
	}
	return composeHeader(req.Name) + fmt.Sprintf(`  n8n:
    image: n8nio/n8n:latest
    restart: unless-stopped
    ports:
      - "127.0.0.1:%d:%d"
    environment:
      - N8N_HOST=%s
      - WEBHOOK_URL=%s
      - N8N_BASIC_AUTH_ACTIVE=true
      - %s
      - %s
    volumes:
      - n8n-data:/home/node/.n8n
volumes:
  n8n-data:
`, req.HostPort, tpl.WebPort, req.Domain, webhook, composeEnv("N8N_BASIC_AUTH_USER", envValue(req, "N8N_BASIC_AUTH_USER", "admin")), composeEnv("N8N_BASIC_AUTH_PASSWORD", envValue(req, "N8N_BASIC_AUTH_PASSWORD", "")))
}

func composeVaultwarden(req softwareInstallRequest, tpl softwareTemplate) string {
	return composeHeader(req.Name) + fmt.Sprintf(`  vaultwarden:
    image: vaultwarden/server:latest
    restart: unless-stopped
    ports:
      - "127.0.0.1:%d:%d"
    volumes:
      - vaultwarden-data:/data
volumes:
  vaultwarden-data:
`, req.HostPort, tpl.WebPort)
}

func composeGitea(req softwareInstallRequest, tpl softwareTemplate) string {
	return composeHeader(req.Name) + fmt.Sprintf(`  gitea:
    image: gitea/gitea:latest
    restart: unless-stopped
    ports:
      - "127.0.0.1:%d:%d"
    volumes:
      - gitea-data:/data
volumes:
  gitea-data:
`, req.HostPort, tpl.WebPort)
}

func composePostgresAdminer(req softwareInstallRequest, tpl softwareTemplate) string {
	return composeHeader(req.Name) + fmt.Sprintf(`  postgres:
    image: postgres:16-alpine
    restart: unless-stopped
    environment:
      - %s
      - %s
      - %s
    volumes:
      - postgres-data:/var/lib/postgresql/data
  adminer:
    image: adminer:latest
    restart: unless-stopped
    depends_on:
      - postgres
    ports:
      - "127.0.0.1:%d:%d"
volumes:
  postgres-data:
`, composeEnv("POSTGRES_DB", envValue(req, "POSTGRES_DB", "app")), composeEnv("POSTGRES_USER", envValue(req, "POSTGRES_USER", "app")), composeEnv("POSTGRES_PASSWORD", envValue(req, "POSTGRES_PASSWORD", "")), req.HostPort, tpl.WebPort)
}

func composePostgres(req softwareInstallRequest, tpl softwareTemplate) string {
	return composeHeader(req.Name) + fmt.Sprintf(`  postgres:
    image: postgres:16-alpine
    restart: unless-stopped
    environment:
      - %s
      - %s
      - %s
    volumes:
      - postgres-data:/var/lib/postgresql/data
volumes:
  postgres-data:
`, composeEnv("POSTGRES_DB", envValue(req, "POSTGRES_DB", "app")), composeEnv("POSTGRES_USER", envValue(req, "POSTGRES_USER", "app")), composeEnv("POSTGRES_PASSWORD", envValue(req, "POSTGRES_PASSWORD", "")))
}

func composeMySQL(req softwareInstallRequest, tpl softwareTemplate) string {
	return composeHeader(req.Name) + fmt.Sprintf(`  mysql:
    image: mysql:8
    restart: unless-stopped
    environment:
      - %s
      - %s
      - %s
      - %s
    volumes:
      - mysql-data:/var/lib/mysql
volumes:
  mysql-data:
`, composeEnv("MYSQL_DATABASE", envValue(req, "MYSQL_DATABASE", "app")), composeEnv("MYSQL_USER", envValue(req, "MYSQL_USER", "app")), composeEnv("MYSQL_PASSWORD", envValue(req, "MYSQL_PASSWORD", "")), composeEnv("MYSQL_ROOT_PASSWORD", envValue(req, "MYSQL_ROOT_PASSWORD", "")))
}

func composeMariaDB(req softwareInstallRequest, tpl softwareTemplate) string {
	return composeHeader(req.Name) + fmt.Sprintf(`  mariadb:
    image: mariadb:11
    restart: unless-stopped
    environment:
      - %s
      - %s
      - %s
      - %s
    volumes:
      - mariadb-data:/var/lib/mysql
volumes:
  mariadb-data:
`, composeEnv("MARIADB_DATABASE", envValue(req, "MARIADB_DATABASE", "app")), composeEnv("MARIADB_USER", envValue(req, "MARIADB_USER", "app")), composeEnv("MARIADB_PASSWORD", envValue(req, "MARIADB_PASSWORD", "")), composeEnv("MARIADB_ROOT_PASSWORD", envValue(req, "MARIADB_ROOT_PASSWORD", "")))
}

func composeMinIO(req softwareInstallRequest, tpl softwareTemplate) string {
	return composeHeader(req.Name) + fmt.Sprintf(`  minio:
    image: minio/minio:latest
    restart: unless-stopped
    command: server /data --console-address ":9001"
    ports:
      - "127.0.0.1:%d:9001"
    environment:
      - %s
      - %s
    volumes:
      - minio-data:/data
volumes:
  minio-data:
`, req.HostPort, composeEnv("MINIO_ROOT_USER", envValue(req, "MINIO_ROOT_USER", "admin")), composeEnv("MINIO_ROOT_PASSWORD", envValue(req, "MINIO_ROOT_PASSWORD", "")))
}

func composeRedis(req softwareInstallRequest, tpl softwareTemplate) string {
	return composeHeader(req.Name) + `  redis:
    image: redis:7-alpine
    restart: unless-stopped
    command: ["redis-server", "--appendonly", "yes"]
    volumes:
      - redis-data:/data
volumes:
  redis-data:
`
}

func composeMemcached(req softwareInstallRequest, tpl softwareTemplate) string {
	return composeHeader(req.Name) + `  memcached:
    image: memcached:1.6-alpine
    restart: unless-stopped
    command: ["memcached", "-m", "128"]
`
}
