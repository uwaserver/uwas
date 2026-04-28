# UWAS Security Architecture Map

## Entry Points

| Entry Point | Type | Auth | Risk |
|-------------|------|------|------|
| `POST /api/v1/auth/login` | HTTP | None | Login endpoint, rate-limited |
| `POST /api/v1/apps/{domain}/webhook` | HTTP | Secret (HMAC/token) | Deploy trigger |
| `GET /api/v1/health` | HTTP | None | Health check |
| `GET /_uwas/dashboard/*` | HTTP | None | Static dashboard files |
| `GET /api/v1/terminal` | WebSocket | Session + PIN | PTY shell access |
| CLI commands | CLI | Root required | System management |
| `.env` auto-load | File | File access | API keys loaded from disk |

## Authentication Chain

```
Request → Auth Middleware
  ├── Skip: /health, /dashboard, /webhook (POST)
  ├── Check: Auth ticket (single-use, short-lived)
  ├── Check: X-Session-Token → ValidateSession()
  ├── Check: X-API-Key → AuthenticateAPIKey()
  ├── Check: Authorization: Bearer <token>
  ├── Check: WebSocket token/ticket query param
  ├── If TOTP enabled: X-TOTP-Code header required
  └── Role check: admin > reseller > user
```

## Key Security Middleware Stack

```
Request
  → Recovery (panic catch)
  → RequestID (crypto/rand)
  → Security Headers
  → Global Rate Limit (per-IP, sharded)
  → Access Log
  → SecurityGuard (block .env, .git, /etc, /proc)
  → BotGuard (25+ malicious UA patterns)
  → Virtual Host Lookup
  → Per-Domain: IP ACL → Rate Limit → BasicAuth → CORS → Header Transform
  → DomainWAF (SQL/XSS/shell/PHP injection patterns)
  → Bandwidth Check
  → Rewrite Engine
  → Cache Lookup (L1 memory → L2 disk)
  → Handler: Static | FastCGI | Proxy | Redirect
```

## Sensitive File Permissions

| File | Permissions | Content |
|------|-------------|---------|
| `~/.uwas/users.json` | 0600 | User credentials, API keys |
| `~/.uwas/config.yaml` | 0600 | Server config, secrets |
| `domains.d/*.yaml` | 0600 | Domain configs |
| TLS certs | 0600 | PEM certificates |
| Backup files | 0755 dir | tar.gz archives |

## exec.Command Surface

| Command | Risk | Input Source |
|---------|------|-------------|
| `sh -c <command>` | CRITICAL | Deploy hook commands |
| `sshpass -p <pass>` | HIGH | SSH migration passwords |
| `mysql/mysqldump` | MEDIUM | DB operations (escaped) |
| `apt install/remove` | HIGH | Package management |
| `curl \| bash` | CRITICAL | WP-CLI download |
| `tar xzf` | MEDIUM | Backup restore, WordPress |
| `crontab -l/-` | MEDIUM | Cron job management |
| `ufw allow/deny` | MEDIUM | Firewall rules |
| `systemctl` | MEDIUM | Service management |
