import { Link } from 'react-router-dom'
import {
  Download,
  FileText,
  Play,
  Globe,
  Rocket,
  CheckCircle2,
  ArrowRight,
} from 'lucide-react'
import CodeBlock from '@/components/CodeBlock'

/* ------------------------------------------------------------------ */
/*  Steps                                                              */
/* ------------------------------------------------------------------ */

interface Step {
  number: number
  icon: React.ComponentType<{ className?: string }>
  title: string
  description: string
}

const steps: Step[] = [
  {
    number: 1,
    icon: Download,
    title: 'Install UWAS',
    description: 'Download and install the single binary — no dependencies required.',
  },
  {
    number: 2,
    icon: FileText,
    title: 'Create a config',
    description: 'Write a minimal uwas.conf or use CLI flags to configure your server.',
  },
  {
    number: 3,
    icon: Play,
    title: 'Start the server',
    description: 'Run uwas serve and your site is live — with auto HTTPS if you have a domain.',
  },
  {
    number: 4,
    icon: Globe,
    title: 'Go live',
    description: 'Point your DNS, let UWAS handle TLS, caching, and everything else.',
  },
]

/* ------------------------------------------------------------------ */
/*  Page                                                               */
/* ------------------------------------------------------------------ */

export default function QuickStart() {
  return (
    <div className="min-h-screen pt-24 pb-16">
      <div className="mx-auto max-w-4xl px-4 sm:px-6 lg:px-8">
        {/* Header */}
        <div className="mb-16 text-center">
          <div className="mb-6 inline-flex items-center gap-2 rounded-full border border-uwas-border bg-uwas-bg-light/50 px-4 py-1.5 text-sm text-uwas-text-muted">
            <Rocket className="h-4 w-4 text-uwas-blue-light" />
            Quick Start Guide
          </div>
          <h1 className="text-4xl font-bold tracking-tight sm:text-5xl">
            Up and running in <span className="gradient-text">under a minute</span>
          </h1>
          <p className="mx-auto mt-4 max-w-xl text-lg text-uwas-text-muted">
            Follow these four steps to go from zero to a fully working web server with auto-HTTPS.
          </p>
        </div>

        {/* Step overview cards */}
        <div className="mb-20 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {steps.map((step) => (
            <div
              key={step.number}
              className="rounded-2xl border border-uwas-border bg-uwas-bg-card p-5 text-center transition-colors hover:border-uwas-blue/40"
            >
              <div className="mx-auto mb-3 flex h-10 w-10 items-center justify-center rounded-xl bg-uwas-blue/10 text-sm font-bold text-uwas-blue-light">
                {step.number}
              </div>
              <h3 className="text-sm font-semibold text-uwas-text">{step.title}</h3>
              <p className="mt-1 text-xs leading-relaxed text-uwas-text-muted">{step.description}</p>
            </div>
          ))}
        </div>

        {/* Step 1 */}
        <section className="mb-16">
          <div className="mb-6 flex items-center gap-3">
            <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-uwas-blue text-sm font-bold text-white">
              1
            </div>
            <h2 className="text-2xl font-bold tracking-tight">Install UWAS</h2>
          </div>
          <p className="mb-6 text-uwas-text-muted">
            Choose your preferred installation method. UWAS ships as a single binary with zero runtime dependencies.
          </p>

          <div className="space-y-4">
            <CodeBlock
              title="Linux / macOS — one-liner"
              code="curl -fsSL https://uwas.dev/install.sh | sh"
            />

            <CodeBlock
              title="Manual download"
              code={`# Linux (amd64)
wget https://github.com/avrahambenaram/uwas/releases/latest/download/uwas-linux-amd64
chmod +x uwas-linux-amd64
sudo mv uwas-linux-amd64 /usr/local/bin/uwas

# macOS (Apple Silicon)
wget https://github.com/avrahambenaram/uwas/releases/latest/download/uwas-darwin-arm64
chmod +x uwas-darwin-arm64
sudo mv uwas-darwin-arm64 /usr/local/bin/uwas`}
            />

            <CodeBlock
              title="Docker"
              code="docker run -d -p 80:80 -p 443:443 uwas/uwas:latest"
            />
          </div>

          <div className="mt-6 flex items-start gap-3 rounded-xl border border-uwas-green/30 bg-uwas-green/5 p-5">
            <CheckCircle2 className="mt-0.5 h-5 w-5 shrink-0 text-uwas-green" />
            <div>
              <p className="font-medium text-uwas-text">Verify the installation</p>
              <p className="mt-1 text-sm text-uwas-text-muted">
                Run <code className="rounded bg-uwas-code-bg px-1.5 py-0.5 text-xs text-uwas-text">uwas --version</code> to
                confirm UWAS is installed correctly. You should see something like{' '}
                <code className="rounded bg-uwas-code-bg px-1.5 py-0.5 text-xs text-uwas-text">uwas v1.0.0 (zig 0.14)</code>.
              </p>
            </div>
          </div>
        </section>

        {/* Step 2 */}
        <section className="mb-16">
          <div className="mb-6 flex items-center gap-3">
            <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-uwas-blue text-sm font-bold text-white">
              2
            </div>
            <h2 className="text-2xl font-bold tracking-tight">Create a configuration</h2>
          </div>
          <p className="mb-6 text-uwas-text-muted">
            Create a <code className="rounded bg-uwas-code-bg px-1.5 py-0.5 text-xs text-uwas-text">uwas.conf</code> file.
            Here are three starter templates:
          </p>

          <div className="space-y-6">
            <div>
              <h3 className="mb-3 text-sm font-semibold uppercase tracking-wider text-uwas-blue-light">
                Static Site
              </h3>
              <CodeBlock
                title="uwas.conf"
                showLineNumbers
                code={`server {
    domain   example.com
    root     /var/www/html

    tls {
        auto true
    }
}`}
              />
            </div>

            <div>
              <h3 className="mb-3 text-sm font-semibold uppercase tracking-wider text-uwas-purple">
                WordPress / PHP
              </h3>
              <CodeBlock
                title="uwas.conf"
                showLineNumbers
                code={`server {
    domain   blog.example.com
    root     /var/www/wordpress

    php {
        fastcgi  /run/php/php-fpm.sock
        index    index.php
        try      $uri $uri/ /index.php?$query
    }

    tls {
        auto true
    }
}`}
              />
            </div>

            <div>
              <h3 className="mb-3 text-sm font-semibold uppercase tracking-wider text-uwas-cyan">
                Reverse Proxy
              </h3>
              <CodeBlock
                title="uwas.conf"
                showLineNumbers
                code={`server {
    domain   app.example.com

    proxy {
        upstream  127.0.0.1:3000
        upstream  127.0.0.1:3001
        balance   round-robin
        health    /health
    }

    tls {
        auto true
    }
}`}
              />
            </div>
          </div>
        </section>

        {/* Step 3 */}
        <section className="mb-16">
          <div className="mb-6 flex items-center gap-3">
            <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-uwas-blue text-sm font-bold text-white">
              3
            </div>
            <h2 className="text-2xl font-bold tracking-tight">Start the server</h2>
          </div>
          <p className="mb-6 text-uwas-text-muted">
            Launch UWAS with your configuration file.
          </p>

          <CodeBlock
            title="Terminal"
            code={`# Start with config file
uwas serve -c uwas.conf

# Or serve a directory directly (development)
uwas serve --root ./dist --port 8080

# Run as a systemd service
sudo uwas install   # Installs systemd unit
sudo systemctl enable uwas
sudo systemctl start uwas`}
          />

          <div className="mt-6 flex items-start gap-3 rounded-xl border border-uwas-blue/30 bg-uwas-blue/5 p-5">
            <CheckCircle2 className="mt-0.5 h-5 w-5 shrink-0 text-uwas-blue-light" />
            <div>
              <p className="font-medium text-uwas-text">Validate before starting</p>
              <p className="mt-1 text-sm text-uwas-text-muted">
                Run <code className="rounded bg-uwas-code-bg px-1.5 py-0.5 text-xs text-uwas-text">uwas validate -c uwas.conf</code> to
                check your configuration for errors before launching.
              </p>
            </div>
          </div>
        </section>

        {/* Step 4 */}
        <section className="mb-16">
          <div className="mb-6 flex items-center gap-3">
            <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-uwas-blue text-sm font-bold text-white">
              4
            </div>
            <h2 className="text-2xl font-bold tracking-tight">Go live</h2>
          </div>
          <p className="mb-6 text-uwas-text-muted">
            Point your domain's A/AAAA record to your server IP. UWAS will automatically obtain a TLS certificate from Let's Encrypt and begin serving over HTTPS.
          </p>

          <CodeBlock
            title="DNS setup"
            code={`# Point your domain to your server
# A     example.com    →  203.0.113.10
# AAAA  example.com    →  2001:db8::1

# UWAS handles the rest:
#   ✓ Obtains TLS certificate automatically
#   ✓ Redirects HTTP → HTTPS
#   ✓ Enables caching
#   ✓ Starts serving your content`}
          />

          <div className="mt-6 flex items-start gap-3 rounded-xl border border-uwas-green/30 bg-uwas-green/5 p-5">
            <CheckCircle2 className="mt-0.5 h-5 w-5 shrink-0 text-uwas-green" />
            <div>
              <p className="font-medium text-uwas-text">That's it!</p>
              <p className="mt-1 text-sm text-uwas-text-muted">
                Your site is live with auto-HTTPS, caching, and all of UWAS's features. Check the{' '}
                <Link to="/docs" className="text-uwas-blue-light underline underline-offset-2">
                  full documentation
                </Link>{' '}
                for advanced configuration.
              </p>
            </div>
          </div>
        </section>

        {/* Next steps */}
        <section className="rounded-2xl border border-uwas-border bg-uwas-bg-card p-8">
          <h2 className="text-xl font-bold">Next steps</h2>
          <div className="mt-6 grid gap-4 sm:grid-cols-2">
            <Link
              to="/docs#configuration"
              className="flex items-center gap-3 rounded-xl border border-uwas-border p-4 no-underline transition-colors hover:border-uwas-blue/40 hover:bg-uwas-bg-light/50"
            >
              <FileText className="h-5 w-5 shrink-0 text-uwas-blue-light" />
              <div>
                <p className="text-sm font-medium text-uwas-text">Configuration Reference</p>
                <p className="text-xs text-uwas-text-muted">Full config syntax and options</p>
              </div>
              <ArrowRight className="ml-auto h-4 w-4 text-uwas-text-muted" />
            </Link>
            <Link
              to="/docs#tls"
              className="flex items-center gap-3 rounded-xl border border-uwas-border p-4 no-underline transition-colors hover:border-uwas-blue/40 hover:bg-uwas-bg-light/50"
            >
              <Globe className="h-5 w-5 shrink-0 text-uwas-green" />
              <div>
                <p className="text-sm font-medium text-uwas-text">TLS / HTTPS</p>
                <p className="text-xs text-uwas-text-muted">Custom certs and TLS settings</p>
              </div>
              <ArrowRight className="ml-auto h-4 w-4 text-uwas-text-muted" />
            </Link>
            <Link
              to="/docs#caching"
              className="flex items-center gap-3 rounded-xl border border-uwas-border p-4 no-underline transition-colors hover:border-uwas-blue/40 hover:bg-uwas-bg-light/50"
            >
              <Rocket className="h-5 w-5 shrink-0 text-uwas-orange" />
              <div>
                <p className="text-sm font-medium text-uwas-text">Caching</p>
                <p className="text-xs text-uwas-text-muted">Built-in high-performance cache</p>
              </div>
              <ArrowRight className="ml-auto h-4 w-4 text-uwas-text-muted" />
            </Link>
            <Link
              to="/docs#docker"
              className="flex items-center gap-3 rounded-xl border border-uwas-border p-4 no-underline transition-colors hover:border-uwas-blue/40 hover:bg-uwas-bg-light/50"
            >
              <Download className="h-5 w-5 shrink-0 text-uwas-purple" />
              <div>
                <p className="text-sm font-medium text-uwas-text">Docker</p>
                <p className="text-xs text-uwas-text-muted">Containerized deployments</p>
              </div>
              <ArrowRight className="ml-auto h-4 w-4 text-uwas-text-muted" />
            </Link>
          </div>
        </section>
      </div>
    </div>
  )
}
