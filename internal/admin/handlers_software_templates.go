package admin

import "fmt"

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
      - N8N_BASIC_AUTH_USER=%s
      - N8N_BASIC_AUTH_PASSWORD=%s
    volumes:
      - n8n-data:/home/node/.n8n
volumes:
  n8n-data:
`, req.HostPort, tpl.WebPort, req.Domain, webhook, envValue(req, "N8N_BASIC_AUTH_USER", "admin"), envValue(req, "N8N_BASIC_AUTH_PASSWORD", ""))
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
      - POSTGRES_DB=%s
      - POSTGRES_USER=%s
      - POSTGRES_PASSWORD=%s
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
`, envValue(req, "POSTGRES_DB", "app"), envValue(req, "POSTGRES_USER", "app"), envValue(req, "POSTGRES_PASSWORD", ""), req.HostPort, tpl.WebPort)
}

func composePostgres(req softwareInstallRequest, tpl softwareTemplate) string {
	return composeHeader(req.Name) + fmt.Sprintf(`  postgres:
    image: postgres:16-alpine
    restart: unless-stopped
    environment:
      - POSTGRES_DB=%s
      - POSTGRES_USER=%s
      - POSTGRES_PASSWORD=%s
    volumes:
      - postgres-data:/var/lib/postgresql/data
volumes:
  postgres-data:
`, envValue(req, "POSTGRES_DB", "app"), envValue(req, "POSTGRES_USER", "app"), envValue(req, "POSTGRES_PASSWORD", ""))
}

func composeMySQL(req softwareInstallRequest, tpl softwareTemplate) string {
	return composeHeader(req.Name) + fmt.Sprintf(`  mysql:
    image: mysql:8
    restart: unless-stopped
    environment:
      - MYSQL_DATABASE=%s
      - MYSQL_USER=%s
      - MYSQL_PASSWORD=%s
      - MYSQL_ROOT_PASSWORD=%s
    volumes:
      - mysql-data:/var/lib/mysql
volumes:
  mysql-data:
`, envValue(req, "MYSQL_DATABASE", "app"), envValue(req, "MYSQL_USER", "app"), envValue(req, "MYSQL_PASSWORD", ""), envValue(req, "MYSQL_ROOT_PASSWORD", ""))
}

func composeMariaDB(req softwareInstallRequest, tpl softwareTemplate) string {
	return composeHeader(req.Name) + fmt.Sprintf(`  mariadb:
    image: mariadb:11
    restart: unless-stopped
    environment:
      - MARIADB_DATABASE=%s
      - MARIADB_USER=%s
      - MARIADB_PASSWORD=%s
      - MARIADB_ROOT_PASSWORD=%s
    volumes:
      - mariadb-data:/var/lib/mysql
volumes:
  mariadb-data:
`, envValue(req, "MARIADB_DATABASE", "app"), envValue(req, "MARIADB_USER", "app"), envValue(req, "MARIADB_PASSWORD", ""), envValue(req, "MARIADB_ROOT_PASSWORD", ""))
}

func composeMinIO(req softwareInstallRequest, tpl softwareTemplate) string {
	return composeHeader(req.Name) + fmt.Sprintf(`  minio:
    image: minio/minio:latest
    restart: unless-stopped
    command: server /data --console-address ":9001"
    ports:
      - "127.0.0.1:%d:9001"
    environment:
      - MINIO_ROOT_USER=%s
      - MINIO_ROOT_PASSWORD=%s
    volumes:
      - minio-data:/data
volumes:
  minio-data:
`, req.HostPort, envValue(req, "MINIO_ROOT_USER", "admin"), envValue(req, "MINIO_ROOT_PASSWORD", ""))
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
