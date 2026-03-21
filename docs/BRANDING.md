# UWAS — Branding Guide

---

## 1. Identity

### Name
- **Full**: UWAS — Unified Web Application Server
- **Short**: UWAS
- **Pronunciation**: "you-wass" (IPA: /juːwæs/)
- **CLI**: `uwas` (lowercase)
- **Stylized**: **UWAS** (all caps in headings, logos), `uwas` (lowercase in code/CLI context)

### Tagline Options
- **Primary**: "One binary to serve them all"
- **Technical**: "Apache + Nginx + Varnish + Caddy in a single Go binary"
- **Simple**: "The unified web server"
- **Developer**: "Stop configuring five services. Start with one."

### Elevator Pitch
> UWAS replaces your entire web server stack — Apache, Nginx, Varnish, Certbot —
> with a single Go binary. Auto HTTPS, built-in caching, PHP support, .htaccess
> compatibility, reverse proxy with load balancing, and an AI-ready MCP interface.
> One binary. Zero hassle. Production ready.

---

## 2. Visual Identity

### Logo Concept
Minimal geometric mark: **dört katmanın birleştiği tek yapı**.

- Primary symbol: Stacked horizontal layers merging into one unified block
- Layers represent: TLS, HTTP, Cache, Handler — merging into one
- Style: flat, geometric, no gradients
- Mono version: works in single color (white on dark, dark on light)

### Color Palette

| Name | Hex | Usage |
|------|-----|-------|
| **UWAS Blue** | `#2563EB` | Primary brand, links, CTAs |
| **UWAS Dark** | `#0F172A` | Text, dark backgrounds |
| **UWAS Slate** | `#475569` | Secondary text, borders |
| **UWAS Light** | `#F1F5F9` | Light backgrounds, cards |
| **UWAS White** | `#FFFFFF` | White space, light mode bg |
| **Accent Green** | `#10B981` | Success, healthy, cache HIT |
| **Accent Amber** | `#F59E0B` | Warning, stale, pending |
| **Accent Red** | `#EF4444` | Error, unhealthy, cache MISS |

### Typography
- **Headings**: Inter (or system sans-serif fallback)
- **Body**: Inter
- **Code/CLI**: JetBrains Mono (or system monospace fallback)
- **Logo type**: Custom letterspacing on Inter Bold

---

## 3. Messaging

### Core Value Propositions

**1. Unified Stack**
- Before: Nginx + Varnish + Certbot + PHP-FPM configs everywhere
- After: One YAML file, one binary, done
- Proof: "UWAS replaces 5 services with 1"

**2. WordPress Just Works™**
- .htaccess compatibility — no migration needed
- PHP-FPM built-in — FastCGI connection pooling
- Pretty permalinks — rewrite engine handles it
- Proof: "Install WordPress, point UWAS, it works"

**3. Auto Everything**
- Auto HTTPS (Let's Encrypt)
- Auto HTTP→HTTPS redirect
- Auto security headers
- Auto cert renewal
- Proof: "Add a domain, UWAS handles the rest"

**4. Built-in Caching (No Varnish Needed)**
- Memory + disk cache
- Grace mode (serve stale while revalidating)
- Tag-based purge
- ESI support
- Proof: "300x faster responses, zero extra services"

**5. AI-Native Server Management**
- MCP server built-in
- "Hey Claude, add a domain" → done
- "Purge cache for the blog" → done
- Proof: "First web server with native AI management"

### Target Audiences

| Audience | Pain Point | UWAS Message |
|----------|-----------|--------------|
| Solo developers | Too many services to manage | "One binary, one config, done" |
| WordPress hosts | Apache is slow, migration is hard | "WordPress works out of the box" |
| SaaS builders | Multi-tenant SSL management | "On-Demand TLS scales to 100K domains" |
| DevOps engineers | Config fragmentation | "Unified config, REST API, Prometheus metrics" |
| Hosting providers | LiteSpeed is expensive | "Open source alternative with equivalent features" |

### Competitive Positioning

```
                    Simple ────────────────── Complex
                      │                         │
          Feature-    │   Caddy                  │   Apache
          light       │                          │
                      │          UWAS ←── here   │
                      │                          │   Nginx + Varnish
          Feature-    │                          │
          rich        │   LiteSpeed              │   HAProxy
                      │                         │
                    Simple ────────────────── Complex
```

UWAS sits at: **Feature-rich + Simple**. That's the gap.

---

## 4. README Hero Section

```markdown
# UWAS

**Unified Web Application Server**

One binary to serve them all.

Apache + Nginx + Varnish + Caddy → UWAS

---

- 🔒 **Auto HTTPS** — Let's Encrypt certificates, zero config
- ⚡ **Built-in Cache** — Varnish-level caching, grace mode, ESI
- 🐘 **PHP Ready** — FastCGI with connection pooling, .htaccess support
- 🔄 **Load Balancer** — 6 algorithms, health checks, circuit breaker
- 📊 **Observable** — Prometheus metrics, JSON logs, admin dashboard
- 🤖 **AI-Native** — MCP server for LLM-driven management
- 📦 **Single Binary** — No dependencies, just download and run

### Quick Start

​```bash
# Install
curl -fsSL https://uwaserver.com/install.sh | sh

# Serve a static site with auto HTTPS
uwas serve -c uwas.yaml

# Or just:
echo "domains:
  - host: example.com
    root: /var/www/html
    ssl: { mode: auto }" > uwas.yaml
uwas serve
​```

### WordPress in 30 Seconds

​```yaml
# uwas.yaml
global:
  acme:
    email: you@example.com

domains:
  - host: blog.example.com
    root: /var/www/wordpress
    type: php
    ssl: { mode: auto }
    htaccess: { mode: import }
    cache:
      enabled: true
      ttl: 3600
​```

That's it. HTTPS, caching, .htaccess, PHP — all handled.
```

---

## 5. Social Media / Launch Content

### GitHub Description
"Unified Web Application Server — Apache + Nginx + Varnish + Caddy in a single Go binary. Auto HTTPS, built-in caching, PHP/FastCGI, .htaccess support, reverse proxy, load balancing, and MCP server."

### GitHub Topics
`web-server`, `go`, `golang`, `http-server`, `reverse-proxy`, `load-balancer`, `cache`, `php`, `fastcgi`, `https`, `lets-encrypt`, `acme`, `htaccess`, `wordpress`, `mcp`, `devops`, `single-binary`

### X/Twitter Launch Thread Angles

**Thread 1: Problem/Solution**
- Hook: "I was tired of configuring 5 different services to host a PHP site."
- Problem: Nginx config + Varnish VCL + Certbot cron + PHP-FPM pool + access logs
- Solution: UWAS — one binary, one YAML file
- Demo: WordPress running on UWAS
- CTA: GitHub link

**Thread 2: Feature Comparison**
- Hook: "What if Caddy and Varnish had a baby that understood PHP?"
- Visual: Comparison table (UWAS vs Nginx vs Caddy vs LiteSpeed)
- Key differentiator: built-in cache + PHP support + .htaccess
- CTA: Star on GitHub

**Thread 3: Technical Deep Dive**
- Hook: "I wrote an ACME client and FastCGI protocol implementation from scratch in Go."
- Technical journey: what I learned building each module
- Performance benchmarks
- Architecture diagram
- CTA: Contribute

**Thread 4: AI-Native**
- Hook: "My web server has an MCP interface."
- Demo: Claude managing UWAS domains, purging cache, checking health
- "First web server you can manage by talking to an AI"
- CTA: Try it

### Hacker News Title Options
- "UWAS – Unified Web Application Server: Apache+Nginx+Varnish in one Go binary"
- "Show HN: UWAS – Single binary web server with auto HTTPS, caching, and PHP support"
- "I built a web server that replaces Nginx, Varnish, and Certbot with one Go binary"

### Product Hunt Tagline
"The web server that replaces your entire stack"

---

## 6. Documentation Site Structure (uwaserver.com)

```
uwaserver.com/
├── /                      → Hero + quick start + features
├── /docs/
│   ├── getting-started    → Installation, first site, 5-minute guide
│   ├── configuration      → Full YAML reference
│   ├── domains            → Virtual hosts, aliases, wildcards
│   ├── tls                → Auto HTTPS, manual certs, On-Demand TLS
│   ├── php                → PHP-FPM setup, WordPress, Laravel
│   ├── caching            → Cache engine, grace mode, ESI, purge
│   ├── proxy              → Reverse proxy, load balancing, health checks
│   ├── rewrite            → URL rewriting, .htaccess compatibility
│   ├── security           → WAF, rate limiting, blocked paths
│   ├── admin-api          → REST API reference
│   ├── mcp                → MCP server, AI management
│   ├── cli                → CLI command reference
│   ├── metrics            → Prometheus metrics, monitoring
│   └── migration/
│       ├── from-apache    → Apache → UWAS migration
│       ├── from-nginx     → Nginx → UWAS migration
│       └── from-litespeed → LiteSpeed → UWAS migration
├── /blog/                 → Release notes, tutorials, benchmarks
├── /benchmark/            → Performance comparisons
└── /community/            → GitHub, Discord, contributing guide
```

---

## 7. Infographic Prompt (Nano Banana 2)

### Architecture Overview Infographic
```
Style: Clean technical infographic with isometric layered design.
Background: Dark navy (#0F172A) with subtle grid pattern.
Title: "UWAS — One Binary, Complete Stack" in white Inter Bold.

Visual: Vertical stack of translucent glass-like layers, each labeled:
- Top layer (blue glow): "TLS / Auto HTTPS" with lock icon
- Second layer (purple): "HTTP/1.1 + HTTP/2 + HTTP/3" with speed lines
- Third layer (orange): "Rewrite Engine + .htaccess" with regex symbols
- Fourth layer (green): "Cache Engine" with clock/lightning icons
- Fifth layer (teal): "Middleware Chain" with chain links
- Bottom layer (red): Three blocks side by side: "Static" "PHP" "Proxy"

Right side: Small icons showing what UWAS replaces:
- Apache logo → crossed out
- Nginx logo → crossed out  
- Varnish logo → crossed out
- Certbot logo → crossed out
- Arrow pointing to single UWAS logo

Bottom: "go install github.com/uwaserver/uwas@latest"
Footer: uwaserver.com | Apache 2.0 | github.com/uwaserver/uwas
```

### Comparison Infographic
```
Style: Clean split-screen comparison.
Left side (red tint, "BEFORE"): 
  5 separate boxes with config files scattered:
  Nginx + Varnish + Certbot + PHP-FPM + custom scripts
  Label: "5 services, 5 configs, 5 problems"

Right side (green tint, "AFTER"):
  Single unified box with UWAS logo:
  One uwas.yaml file
  Label: "1 binary, 1 config, 0 hassle"

Center divider: Arrow pointing right
Bottom: Feature checkmarks for UWAS
```

---

## 8. Community & Ecosystem

### GitHub Organization
- `github.com/uwaserver/uwas` — main repository
- `github.com/uwaserver/uwas-docker` — official Docker images
- `github.com/uwaserver/uwas-helm` — Kubernetes Helm chart
- `github.com/uwaserver/uwas-ansible` — Ansible role
- `github.com/uwaserver/uwas-docs` — documentation site source

### Community Channels
- GitHub Discussions (primary)
- Discord server (real-time chat)
- X/Twitter: @uwaserver

### Contributing Guide Principles
- Issues first: discuss before coding
- Small PRs: one feature/fix per PR
- Tests required: every PR must include tests
- Go style: `gofmt`, `go vet`, `staticcheck` pass
- Documentation: every new feature needs docs
