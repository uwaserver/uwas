import { useState } from 'react'
import { Link } from 'react-router-dom'
import {
  BookOpen,
  Settings,
  Globe,
  Lock,
  Code2,
  Database,
  ArrowLeftRight,
  RefreshCw,
  ShieldCheck,
  Terminal,
  Monitor,
  Container,
  ArrowRightLeft,
  ChevronRight,
  Menu,
  X,
} from 'lucide-react'
import CodeBlock from '@/components/CodeBlock'

/* ------------------------------------------------------------------ */
/*  Sidebar sections                                                   */
/* ------------------------------------------------------------------ */

interface SidebarItem {
  id: string
  label: string
  icon: React.ComponentType<{ className?: string }>
}

const sections: SidebarItem[] = [
  { id: 'getting-started', label: 'Getting Started', icon: BookOpen },
  { id: 'configuration', label: 'Configuration', icon: Settings },
  { id: 'domains', label: 'Domains', icon: Globe },
  { id: 'tls', label: 'TLS / HTTPS', icon: Lock },
  { id: 'php', label: 'PHP', icon: Code2 },
  { id: 'caching', label: 'Caching', icon: Database },
  { id: 'proxy', label: 'Proxy', icon: ArrowLeftRight },
  { id: 'rewrite', label: 'Rewrite Rules', icon: RefreshCw },
  { id: 'security', label: 'Security', icon: ShieldCheck },
  { id: 'admin-api', label: 'Admin API', icon: Monitor },
  { id: 'cli', label: 'CLI Reference', icon: Terminal },
  { id: 'docker', label: 'Docker', icon: Container },
  { id: 'migration', label: 'Migration', icon: ArrowRightLeft },
]

/* ------------------------------------------------------------------ */
/*  Page                                                               */
/* ------------------------------------------------------------------ */

export default function Docs() {
  const [active, setActive] = useState('getting-started')
  const [sidebarOpen, setSidebarOpen] = useState(false)

  function handleNav(id: string) {
    setActive(id)
    setSidebarOpen(false)
    const el = document.getElementById(id)
    if (el) el.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }

  return (
    <div className="min-h-screen pt-16">
      {/* Mobile sidebar toggle */}
      <div className="sticky top-16 z-30 flex items-center border-b border-uwas-border bg-uwas-bg/90 px-4 py-3 backdrop-blur lg:hidden">
        <button
          onClick={() => setSidebarOpen(!sidebarOpen)}
          className="flex items-center gap-2 rounded-lg border border-uwas-border px-3 py-1.5 text-sm text-uwas-text-muted hover:bg-uwas-bg-light"
        >
          {sidebarOpen ? <X className="h-4 w-4" /> : <Menu className="h-4 w-4" />}
          Documentation
        </button>
      </div>

      <div className="mx-auto flex max-w-7xl">
        {/* Sidebar — desktop always visible, mobile conditional */}
        <aside
          className={`fixed top-16 z-20 h-[calc(100vh-4rem)] w-64 shrink-0 overflow-y-auto border-r border-uwas-border bg-uwas-bg px-3 py-6 transition-transform lg:sticky lg:translate-x-0 ${
            sidebarOpen ? 'translate-x-0' : '-translate-x-full'
          }`}
        >
          <nav className="space-y-0.5">
            {sections.map((s) => (
              <button
                key={s.id}
                onClick={() => handleNav(s.id)}
                className={`flex w-full items-center gap-2.5 rounded-lg px-3 py-2 text-left text-sm font-medium transition-colors ${
                  active === s.id
                    ? 'bg-uwas-blue/10 text-uwas-blue-light'
                    : 'text-uwas-text-muted hover:bg-uwas-bg-light hover:text-uwas-text'
                }`}
              >
                <s.icon className="h-4 w-4 shrink-0" />
                {s.label}
              </button>
            ))}
          </nav>
        </aside>

        {/* Backdrop for mobile */}
        {sidebarOpen && (
          <div
            className="fixed inset-0 z-10 bg-black/40 lg:hidden"
            onClick={() => setSidebarOpen(false)}
          />
        )}

        {/* Main content */}
        <main className="min-w-0 flex-1 px-6 py-10 lg:px-12">
          <div className="mx-auto max-w-3xl">
            {/* Getting Started */}
            <section id="getting-started" className="mb-16 scroll-mt-24">
              <h1 className="text-3xl font-bold tracking-tight">Getting Started</h1>
              <p className="mt-4 leading-relaxed text-uwas-text-muted">
                UWAS ships as a single binary with zero dependencies. Install it and start serving in seconds.
              </p>
              <CodeBlock
                title="Install"
                code={`# One-line install (Linux / macOS)
curl -fsSL https://uwas.dev/install.sh | sh

# Or download directly
wget https://github.com/avrahambenaram/uwas/releases/latest/download/uwas-linux-amd64
chmod +x uwas-linux-amd64
sudo mv uwas-linux-amd64 /usr/local/bin/uwas

# Verify
uwas --version`}
              />
              <div className="mt-6">
                <CodeBlock
                  title="Serve a directory"
                  code={`# Serve current directory on port 8080
uwas serve --root . --port 8080

# Serve with auto-HTTPS
uwas serve --root /var/www --domain example.com`}
                />
              </div>
              <div className="mt-6 rounded-xl border border-uwas-blue/30 bg-uwas-blue/5 p-5">
                <p className="text-sm leading-relaxed text-uwas-text-muted">
                  <strong className="text-uwas-blue-light">Tip:</strong> For production deployments, use a{' '}
                  <code className="rounded bg-uwas-code-bg px-1.5 py-0.5 text-xs text-uwas-text">uwas.conf</code>{' '}
                  configuration file. See the <button onClick={() => handleNav('configuration')} className="text-uwas-blue-light underline underline-offset-2">Configuration</button> section.
                </p>
              </div>
            </section>

            {/* Configuration */}
            <section id="configuration" className="mb-16 scroll-mt-24">
              <h2 className="text-2xl font-bold tracking-tight">Configuration</h2>
              <p className="mt-4 leading-relaxed text-uwas-text-muted">
                UWAS uses a clean, block-based configuration format inspired by Caddy and Nginx.
                The default config file is <code className="rounded bg-uwas-code-bg px-1.5 py-0.5 text-xs text-uwas-text">/etc/uwas/uwas.conf</code>.
              </p>
              <div className="mt-6">
                <CodeBlock
                  title="uwas.conf"
                  showLineNumbers
                  code={`# Global settings
global {
    admin    127.0.0.1:2019
    log      /var/log/uwas/access.log
    workers  auto
}

# Site block
server {
    domain   example.com www.example.com
    root     /var/www/html

    tls {
        auto true
    }

    cache {
        enable   true
        max-age  3600
    }

    headers {
        X-Powered-By  "UWAS"
    }
}`}
                />
              </div>
            </section>

            {/* Domains */}
            <section id="domains" className="mb-16 scroll-mt-24">
              <h2 className="text-2xl font-bold tracking-tight">Domains</h2>
              <p className="mt-4 leading-relaxed text-uwas-text-muted">
                Each <code className="rounded bg-uwas-code-bg px-1.5 py-0.5 text-xs text-uwas-text">server</code> block
                can handle one or more domains. UWAS automatically routes requests based on the Host header.
              </p>
              <div className="mt-6">
                <CodeBlock
                  title="Multi-domain setup"
                  code={`server {
    domain   example.com www.example.com
    root     /var/www/main
}

server {
    domain   api.example.com
    proxy {
        upstream  127.0.0.1:3000
    }
}

server {
    domain   *.example.com
    root     /var/www/wildcard
}`}
                />
              </div>
            </section>

            {/* TLS */}
            <section id="tls" className="mb-16 scroll-mt-24">
              <h2 className="text-2xl font-bold tracking-tight">TLS / HTTPS</h2>
              <p className="mt-4 leading-relaxed text-uwas-text-muted">
                UWAS provides automatic TLS via Let's Encrypt and ZeroSSL. Certificates are obtained
                and renewed transparently.
              </p>
              <div className="mt-6">
                <CodeBlock
                  title="TLS options"
                  code={`server {
    domain  secure.example.com

    tls {
        auto       true             # Let's Encrypt
        # Or use custom certs:
        # cert     /path/to/cert.pem
        # key      /path/to/key.pem
        min        1.2              # Minimum TLS version
        hsts       true             # Strict-Transport-Security
    }
}`}
                />
              </div>
            </section>

            {/* PHP */}
            <section id="php" className="mb-16 scroll-mt-24">
              <h2 className="text-2xl font-bold tracking-tight">PHP</h2>
              <p className="mt-4 leading-relaxed text-uwas-text-muted">
                Native FastCGI support lets you serve WordPress, Laravel, Symfony, and any PHP application
                without external glue.
              </p>
              <div className="mt-6">
                <CodeBlock
                  title="PHP / WordPress"
                  code={`server {
    domain  blog.example.com
    root    /var/www/wordpress

    php {
        fastcgi  /run/php/php-fpm.sock   # Unix socket
        # fastcgi  127.0.0.1:9000        # Or TCP
        index    index.php
        try      $uri $uri/ /index.php?$query
    }
}`}
                />
              </div>
            </section>

            {/* Caching */}
            <section id="caching" className="mb-16 scroll-mt-24">
              <h2 className="text-2xl font-bold tracking-tight">Caching</h2>
              <p className="mt-4 leading-relaxed text-uwas-text-muted">
                UWAS includes a Varnish-class in-process caching engine capable of 75 million operations per second.
              </p>
              <div className="mt-6">
                <CodeBlock
                  title="Cache configuration"
                  code={`server {
    domain  example.com
    root    /var/www/html

    cache {
        enable       true
        max-age      3600          # 1 hour
        max-size     512MB         # Cache size limit
        stale        30            # Serve stale for 30s
        bypass       /admin/*      # Skip cache for admin
        purge-key    "secret123"   # Cache purge API key
    }
}`}
                />
              </div>
            </section>

            {/* Proxy */}
            <section id="proxy" className="mb-16 scroll-mt-24">
              <h2 className="text-2xl font-bold tracking-tight">Reverse Proxy &amp; Load Balancer</h2>
              <p className="mt-4 leading-relaxed text-uwas-text-muted">
                Built-in reverse proxy with multiple load balancing strategies, health checks, and connection pooling.
              </p>
              <div className="mt-6">
                <CodeBlock
                  title="Proxy with load balancing"
                  code={`server {
    domain  app.example.com

    proxy {
        upstream  10.0.0.1:3000
        upstream  10.0.0.2:3000
        upstream  10.0.0.3:3000

        balance       round-robin   # or least-conn, ip-hash
        health        /health
        interval      10s
        timeout       30s
        retry         3
        sticky        cookie
    }
}`}
                />
              </div>
            </section>

            {/* Rewrite */}
            <section id="rewrite" className="mb-16 scroll-mt-24">
              <h2 className="text-2xl font-bold tracking-tight">Rewrite Rules</h2>
              <p className="mt-4 leading-relaxed text-uwas-text-muted">
                Flexible URL rewriting and redirection with regex support.
              </p>
              <div className="mt-6">
                <CodeBlock
                  title="Rewrites & redirects"
                  code={`server {
    domain  example.com
    root    /var/www/html

    rewrite {
        # Redirect www to non-www
        301  ^/old-page$    /new-page

        # SPA fallback
        try  $uri $uri/ /index.html

        # Regex rewrite
        rewrite  ^/blog/(\\d+)$  /post.php?id=$1
    }
}`}
                />
              </div>
            </section>

            {/* Security */}
            <section id="security" className="mb-16 scroll-mt-24">
              <h2 className="text-2xl font-bold tracking-tight">Security</h2>
              <p className="mt-4 leading-relaxed text-uwas-text-muted">
                Built-in security headers, rate limiting, IP filtering, and more.
              </p>
              <div className="mt-6">
                <CodeBlock
                  title="Security settings"
                  code={`server {
    domain  example.com

    security {
        rate-limit   100/min       # Per-IP rate limit
        block        192.168.1.0/24
        allow        10.0.0.0/8

        headers {
            X-Frame-Options          "DENY"
            X-Content-Type-Options   "nosniff"
            Referrer-Policy          "strict-origin"
            Content-Security-Policy  "default-src 'self'"
        }
    }
}`}
                />
              </div>
            </section>

            {/* Admin API */}
            <section id="admin-api" className="mb-16 scroll-mt-24">
              <h2 className="text-2xl font-bold tracking-tight">Admin API</h2>
              <p className="mt-4 leading-relaxed text-uwas-text-muted">
                RESTful admin API and real-time web dashboard for managing your UWAS instance.
              </p>
              <div className="mt-6">
                <CodeBlock
                  title="Admin API examples"
                  code={`# Get server status
curl http://localhost:2019/api/status

# List all domains
curl http://localhost:2019/api/domains

# Purge cache
curl -X POST http://localhost:2019/api/cache/purge \\
  -H "X-Purge-Key: secret123"

# Reload configuration
curl -X POST http://localhost:2019/api/reload`}
                />
              </div>
            </section>

            {/* CLI */}
            <section id="cli" className="mb-16 scroll-mt-24">
              <h2 className="text-2xl font-bold tracking-tight">CLI Reference</h2>
              <p className="mt-4 leading-relaxed text-uwas-text-muted">
                Complete command-line interface reference.
              </p>
              <div className="mt-6">
                <CodeBlock
                  title="CLI commands"
                  code={`uwas serve                   # Start the server
uwas serve -c /path/to.conf  # Custom config path
uwas validate                 # Validate configuration
uwas reload                   # Graceful reload
uwas stop                     # Graceful shutdown
uwas status                   # Show server status
uwas domains                  # List active domains
uwas certs                    # List TLS certificates
uwas cache purge              # Purge all cache
uwas version                  # Show version info`}
                />
              </div>
            </section>

            {/* Docker */}
            <section id="docker" className="mb-16 scroll-mt-24">
              <h2 className="text-2xl font-bold tracking-tight">Docker</h2>
              <p className="mt-4 leading-relaxed text-uwas-text-muted">
                Run UWAS in Docker for containerized deployments.
              </p>
              <div className="mt-6">
                <CodeBlock
                  title="Dockerfile"
                  language="dockerfile"
                  code={`FROM uwas/uwas:latest

COPY uwas.conf /etc/uwas/uwas.conf
COPY ./public /var/www/html

EXPOSE 80 443

CMD ["uwas", "serve"]`}
                />
              </div>
              <div className="mt-6">
                <CodeBlock
                  title="docker-compose.yml"
                  language="yaml"
                  code={`version: "3.8"
services:
  uwas:
    image: uwas/uwas:latest
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./uwas.conf:/etc/uwas/uwas.conf
      - ./www:/var/www/html
      - uwas-data:/var/lib/uwas
    restart: unless-stopped

volumes:
  uwas-data:`}
                />
              </div>
            </section>

            {/* Migration */}
            <section id="migration" className="mb-16 scroll-mt-24">
              <h2 className="text-2xl font-bold tracking-tight">Migration Guide</h2>
              <p className="mt-4 leading-relaxed text-uwas-text-muted">
                Switching from Nginx, Apache, or Caddy? UWAS makes it easy.
              </p>

              <h3 className="mt-8 text-lg font-semibold">From Nginx</h3>
              <div className="mt-4 grid gap-4 md:grid-cols-2">
                <CodeBlock
                  title="nginx.conf (before)"
                  language="nginx"
                  code={`server {
    listen 80;
    server_name example.com;
    root /var/www/html;

    location / {
        try_files $uri $uri/ =404;
    }
}`}
                />
                <CodeBlock
                  title="uwas.conf (after)"
                  code={`server {
    domain  example.com
    root    /var/www/html

    tls { auto true }
}`}
                />
              </div>

              <h3 className="mt-8 text-lg font-semibold">From Apache</h3>
              <div className="mt-4 grid gap-4 md:grid-cols-2">
                <CodeBlock
                  title="apache.conf (before)"
                  language="apache"
                  code={`<VirtualHost *:80>
    ServerName example.com
    DocumentRoot /var/www/html
    <Directory /var/www/html>
        AllowOverride All
    </Directory>
</VirtualHost>`}
                />
                <CodeBlock
                  title="uwas.conf (after)"
                  code={`server {
    domain  example.com
    root    /var/www/html

    tls { auto true }
}`}
                />
              </div>

              <div className="mt-8 rounded-xl border border-uwas-border bg-uwas-bg-card p-5">
                <div className="flex items-start gap-3">
                  <ChevronRight className="mt-0.5 h-5 w-5 shrink-0 text-uwas-blue-light" />
                  <div>
                    <p className="font-medium text-uwas-text">Need help migrating?</p>
                    <p className="mt-1 text-sm text-uwas-text-muted">
                      Check our{' '}
                      <Link to="/quickstart" className="text-uwas-blue-light underline underline-offset-2">
                        Quick Start guide
                      </Link>{' '}
                      or open an issue on{' '}
                      <a
                        href="https://github.com/avrahambenaram/uwas/issues"
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-uwas-blue-light underline underline-offset-2"
                      >
                        GitHub
                      </a>.
                    </p>
                  </div>
                </div>
              </div>
            </section>
          </div>
        </main>
      </div>
    </div>
  )
}
