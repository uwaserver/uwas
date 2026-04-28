# Dependency Audit — UWAS v0.0.54

## Direct Dependencies (5)

| Module | Version | Purpose | Known CVEs | Risk |
|--------|---------|---------|------------|------|
| `gopkg.in/yaml.v3` | v3.0.1 | YAML config parsing | None | Low |
| `github.com/andybalholm/brotli` | v1.2.0 | Brotli compression | None | Low |
| `github.com/quic-go/quic-go` | v0.59.0 | HTTP/3 (QUIC) | None | Medium |
| `golang.org/x/crypto` | v0.49.0 | bcrypt, SSH, ed25519 | None | Low |
| `golang.org/x/sync` | v0.20.0 | Concurrency primitives | None | Low |

## Indirect Dependencies (5)

| Module | Version | Purpose |
|--------|---------|---------|
| `github.com/kr/text` | v0.2.0 | Text formatting |
| `github.com/quic-go/qpack` | v0.6.0 | QPACK header compression |
| `golang.org/x/net` | v0.52.0 | Extended networking |
| `golang.org/x/sys` | v0.42.0 | OS-level calls |
| `golang.org/x/text` | v0.35.0 | Unicode/text processing |

## Assessment

**Very minimal dependency footprint.** Only 5 direct dependencies, all from reputable sources (Go team, well-known libraries). No web frameworks, no ORMs, no logging frameworks. This significantly reduces supply chain attack surface.

## External Network Integrations

| Service | Auth Method | File |
|---------|-------------|------|
| GitHub (self-update) | HTTPS + domain allowlist | `selfupdate/updater.go` |
| Cloudflare API | API token | `dnsmanager/cloudflare.go` |
| DigitalOcean API | API token | `dnsmanager/digitalocean.go` |
| Hetzner DNS API | API token | `dnsmanager/hetzner.go` |
| AWS Route53 | HMAC-SHA256 (Sig V4) | `dnsmanager/route53.go` |
| S3 Storage | HMAC-SHA256 (Sig V4) | `backup/s3.go` |
| ACME/Let's Encrypt | JWS (ECDSA) | `tls/acme/client.go` |
| WordPress.org | HTTPS (no checksum) | `wordpress/installer.go` |
| WP-CLI (GitHub) | HTTPS (no checksum) | `admin/handlers_hosting.go` |
| Webhook delivery | HMAC-SHA256 | `webhook/manager.go` |

## Supply Chain Recommendations

1. **Pin WP-CLI version** and verify SHA256 checksum after download
2. **Verify WordPress checksum** against `latest.tar.gz.sha256`
3. **Add binary signing** for self-update releases (ed25519 or minisign)
4. **Run `go mod verify`** in CI to detect dependency tampering
5. **Consider `govulncheck`** in CI pipeline for Go vulnerability scanning
