import { Link } from 'react-router-dom'
import {
  Shield,
  Zap,
  Code2,
  LayoutDashboard,
  Network,
  Brain,
  Star,
  Terminal as TerminalIcon,
  ArrowRight,
  Check,
  X as XIcon,
  Minus,
} from 'lucide-react'
import Terminal from '@/components/Terminal'
import CodeBlock from '@/components/CodeBlock'

/* ------------------------------------------------------------------ */
/*  Data                                                               */
/* ------------------------------------------------------------------ */

const features = [
  {
    icon: Shield,
    title: 'Auto HTTPS',
    description: 'Automatic TLS certificates via Let\'s Encrypt and ZeroSSL. HTTPS just works, zero config.',
    color: 'text-uwas-green',
    bg: 'bg-uwas-green/10',
  },
  {
    icon: Zap,
    title: 'Built-in Cache',
    description: 'Integrated Varnish-class caching engine. 75M cache operations per second, in-process.',
    color: 'text-uwas-orange',
    bg: 'bg-uwas-orange/10',
  },
  {
    icon: Code2,
    title: 'PHP Ready',
    description: 'Native PHP support with FastCGI. Serve WordPress, Laravel, and any PHP app out of the box.',
    color: 'text-uwas-purple',
    bg: 'bg-uwas-purple/10',
  },
  {
    icon: Network,
    title: 'Load Balancer',
    description: 'Built-in reverse proxy and load balancer with health checks, round-robin, and sticky sessions.',
    color: 'text-uwas-blue-light',
    bg: 'bg-uwas-blue-light/10',
  },
  {
    icon: LayoutDashboard,
    title: 'Web Dashboard',
    description: 'Real-time admin dashboard to monitor traffic, manage domains, and configure settings live.',
    color: 'text-uwas-cyan',
    bg: 'bg-uwas-cyan/10',
  },
  {
    icon: Brain,
    title: 'AI-Native (MCP)',
    description: 'Model Context Protocol built in. Let AI assistants manage your server configuration natively.',
    color: 'text-pink-400',
    bg: 'bg-pink-400/10',
  },
]

const stats = [
  { value: '7K', label: 'req/sec', sub: 'HTTP throughput' },
  { value: '75M', label: 'cache ops/sec', sub: 'Cache engine' },
  { value: '15MB', label: 'binary', sub: 'Single executable' },
  { value: '1213', label: 'tests', sub: 'Test coverage' },
]

interface ComparisonRow {
  feature: string
  uwas: boolean | string
  nginx: boolean | string
  caddy: boolean | string
  apache: boolean | string
  litespeed: boolean | string
}

const comparison: ComparisonRow[] = [
  { feature: 'Auto HTTPS', uwas: true, nginx: false, caddy: true, apache: false, litespeed: false },
  { feature: 'Built-in Cache', uwas: true, nginx: false, caddy: false, apache: false, litespeed: true },
  { feature: 'PHP Support', uwas: true, nginx: 'External', caddy: 'Plugin', apache: true, litespeed: true },
  { feature: 'Load Balancer', uwas: true, nginx: true, caddy: true, apache: 'Module', litespeed: true },
  { feature: 'Web Dashboard', uwas: true, nginx: 'Paid', caddy: false, apache: false, litespeed: true },
  { feature: 'AI/MCP Support', uwas: true, nginx: false, caddy: false, apache: false, litespeed: false },
  { feature: 'Single Binary', uwas: true, nginx: false, caddy: true, apache: false, litespeed: false },
  { feature: 'Zero-Config TLS', uwas: true, nginx: false, caddy: true, apache: false, litespeed: false },
  { feature: 'URL Rewriting', uwas: true, nginx: true, caddy: true, apache: true, litespeed: true },
  { feature: 'Gzip/Brotli', uwas: true, nginx: true, caddy: true, apache: true, litespeed: true },
]

const staticSiteCode = `# uwas.conf — Serve a static site
server {
    domain   example.com
    root     /var/www/html

    tls {
        auto true
    }

    cache {
        enable     true
        max-age    3600
    }
}`

const wordpressCode = `# uwas.conf — WordPress
server {
    domain   blog.example.com
    root     /var/www/wordpress

    php {
        fastcgi  127.0.0.1:9000
        index    index.php
    }

    tls {
        auto true
    }
}`

const reverseProxyCode = `# uwas.conf — Reverse proxy
server {
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
}`

/* ------------------------------------------------------------------ */
/*  Helpers                                                            */
/* ------------------------------------------------------------------ */

function CellIcon({ value }: { value: boolean | string }) {
  if (value === true) return <Check className="mx-auto h-5 w-5 text-uwas-green" />
  if (value === false) return <XIcon className="mx-auto h-5 w-5 text-red-400/60" />
  return (
    <span className="flex items-center justify-center gap-1 text-xs text-uwas-orange">
      <Minus className="h-3 w-3" /> {value}
    </span>
  )
}

/* ------------------------------------------------------------------ */
/*  Page                                                               */
/* ------------------------------------------------------------------ */

export default function Home() {
  return (
    <div>
      {/* ======== Hero ======== */}
      <section className="relative overflow-hidden pb-20 pt-32 sm:pt-40">
        {/* Decorative gradient */}
        <div className="pointer-events-none absolute inset-0 overflow-hidden">
          <div className="absolute -top-40 left-1/2 h-[600px] w-[900px] -translate-x-1/2 rounded-full bg-uwas-blue/8 blur-3xl" />
        </div>

        <div className="relative mx-auto max-w-5xl px-4 text-center sm:px-6 lg:px-8">
          {/* Badge */}
          <div className="mb-8 inline-flex items-center gap-2 rounded-full border border-uwas-border bg-uwas-bg-light/50 px-4 py-1.5 text-sm text-uwas-text-muted">
            <span className="inline-block h-2 w-2 rounded-full bg-uwas-green animate-pulse" />
            v1.0 &mdash; Production Ready
          </div>

          <h1 className="text-5xl font-extrabold leading-tight tracking-tight sm:text-7xl">
            <span className="gradient-text">UWAS</span>
          </h1>
          <p className="mt-4 text-xl font-semibold text-uwas-text sm:text-2xl">
            One binary to serve them all.
          </p>
          <p className="mx-auto mt-4 max-w-2xl text-base leading-relaxed text-uwas-text-muted sm:text-lg">
            Replace Apache + Nginx + Varnish + Caddy with a single 15 MB executable.
            Auto HTTPS, built-in caching, PHP support, load balancing, web dashboard,
            and AI-native MCP — all out of the box.
          </p>

          {/* CTA */}
          <div className="mt-10 flex flex-col items-center justify-center gap-4 sm:flex-row">
            <Link
              to="/quickstart"
              className="inline-flex items-center gap-2 rounded-xl bg-uwas-blue px-7 py-3.5 text-sm font-semibold text-white no-underline shadow-lg shadow-uwas-blue/25 transition-all hover:bg-uwas-blue-light hover:shadow-xl hover:shadow-uwas-blue/30"
            >
              Get Started
              <ArrowRight className="h-4 w-4" />
            </Link>
            <a
              href="https://github.com/avrahambenaram/uwas"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 rounded-xl border border-uwas-border bg-uwas-bg-light/50 px-7 py-3.5 text-sm font-semibold text-uwas-text no-underline transition-all hover:border-uwas-blue/50 hover:bg-uwas-bg-light"
            >
              <Star className="h-4 w-4" />
              Star on GitHub
            </a>
          </div>

          {/* Install one-liner */}
          <div className="mt-8 inline-flex items-center gap-3 rounded-xl border border-uwas-border bg-uwas-code-bg px-5 py-3 font-mono text-sm text-uwas-text-muted">
            <TerminalIcon className="h-4 w-4 text-uwas-green" />
            <span>curl -fsSL https://uwas.dev/install.sh | sh</span>
          </div>
        </div>
      </section>

      {/* ======== Animated Terminal ======== */}
      <section className="mx-auto max-w-3xl px-4 pb-24 sm:px-6 lg:px-8">
        <Terminal />
      </section>

      {/* ======== Feature Grid ======== */}
      <section className="mx-auto max-w-7xl px-4 pb-24 sm:px-6 lg:px-8">
        <div className="mb-14 text-center">
          <h2 className="text-3xl font-bold tracking-tight sm:text-4xl">
            Everything you need. Nothing you don't.
          </h2>
          <p className="mt-3 text-uwas-text-muted">
            Six pillars that make UWAS the last server you'll ever install.
          </p>
        </div>

        <div className="grid gap-5 sm:grid-cols-2 lg:grid-cols-3">
          {features.map((f) => (
            <div
              key={f.title}
              className="group rounded-2xl border border-uwas-border bg-uwas-bg-card p-6 transition-all hover:border-uwas-blue/40 hover:shadow-lg hover:shadow-uwas-blue/5"
            >
              <div className={`mb-4 inline-flex rounded-xl p-3 ${f.bg}`}>
                <f.icon className={`h-6 w-6 ${f.color}`} />
              </div>
              <h3 className="mb-2 text-lg font-semibold text-uwas-text">{f.title}</h3>
              <p className="text-sm leading-relaxed text-uwas-text-muted">{f.description}</p>
            </div>
          ))}
        </div>
      </section>

      {/* ======== Performance Numbers ======== */}
      <section className="border-y border-uwas-border bg-uwas-bg-light/30 py-20">
        <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <div className="mb-14 text-center">
            <h2 className="text-3xl font-bold tracking-tight sm:text-4xl">
              Performance that speaks for itself
            </h2>
            <p className="mt-3 text-uwas-text-muted">
              Built in Zig for raw speed and minimal footprint.
            </p>
          </div>

          <div className="grid gap-6 sm:grid-cols-2 lg:grid-cols-4">
            {stats.map((s) => (
              <div
                key={s.label}
                className="rounded-2xl border border-uwas-border bg-uwas-bg-card p-8 text-center transition-colors hover:border-uwas-blue/40"
              >
                <div className="text-4xl font-extrabold tracking-tight">
                  <span className="gradient-text">{s.value}</span>
                </div>
                <div className="mt-1 text-sm font-semibold text-uwas-text">{s.label}</div>
                <div className="mt-1 text-xs text-uwas-text-muted">{s.sub}</div>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* ======== Comparison Table ======== */}
      <section className="mx-auto max-w-5xl px-4 py-24 sm:px-6 lg:px-8">
        <div className="mb-14 text-center">
          <h2 className="text-3xl font-bold tracking-tight sm:text-4xl">
            How UWAS compares
          </h2>
          <p className="mt-3 text-uwas-text-muted">
            Feature-by-feature against the most popular web servers.
          </p>
        </div>

        <div className="overflow-x-auto rounded-2xl border border-uwas-border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-uwas-border bg-uwas-bg-light/50">
                <th className="px-5 py-4 text-left font-semibold text-uwas-text">Feature</th>
                <th className="px-5 py-4 text-center font-semibold text-uwas-blue-light">UWAS</th>
                <th className="px-5 py-4 text-center font-semibold text-uwas-text-muted">Nginx</th>
                <th className="px-5 py-4 text-center font-semibold text-uwas-text-muted">Caddy</th>
                <th className="px-5 py-4 text-center font-semibold text-uwas-text-muted">Apache</th>
                <th className="px-5 py-4 text-center font-semibold text-uwas-text-muted">LiteSpeed</th>
              </tr>
            </thead>
            <tbody>
              {comparison.map((row, i) => (
                <tr
                  key={row.feature}
                  className={`border-b border-uwas-border last:border-0 ${i % 2 === 0 ? 'bg-uwas-bg-card/50' : ''}`}
                >
                  <td className="px-5 py-3.5 font-medium text-uwas-text">{row.feature}</td>
                  <td className="px-5 py-3.5"><CellIcon value={row.uwas} /></td>
                  <td className="px-5 py-3.5"><CellIcon value={row.nginx} /></td>
                  <td className="px-5 py-3.5"><CellIcon value={row.caddy} /></td>
                  <td className="px-5 py-3.5"><CellIcon value={row.apache} /></td>
                  <td className="px-5 py-3.5"><CellIcon value={row.litespeed} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      {/* ======== Quick Start Code Blocks ======== */}
      <section className="border-t border-uwas-border bg-uwas-bg-light/20 py-24">
        <div className="mx-auto max-w-5xl px-4 sm:px-6 lg:px-8">
          <div className="mb-14 text-center">
            <h2 className="text-3xl font-bold tracking-tight sm:text-4xl">
              Up and running in seconds
            </h2>
            <p className="mt-3 text-uwas-text-muted">
              Three common setups — copy, paste, done.
            </p>
          </div>

          <div className="grid gap-6 lg:grid-cols-3">
            <div>
              <h3 className="mb-3 text-sm font-semibold uppercase tracking-wider text-uwas-blue-light">
                Static Site
              </h3>
              <CodeBlock code={staticSiteCode} language="nginx" title="uwas.conf" />
            </div>
            <div>
              <h3 className="mb-3 text-sm font-semibold uppercase tracking-wider text-uwas-purple">
                WordPress / PHP
              </h3>
              <CodeBlock code={wordpressCode} language="nginx" title="uwas.conf" />
            </div>
            <div>
              <h3 className="mb-3 text-sm font-semibold uppercase tracking-wider text-uwas-cyan">
                Reverse Proxy
              </h3>
              <CodeBlock code={reverseProxyCode} language="nginx" title="uwas.conf" />
            </div>
          </div>
        </div>
      </section>

      {/* ======== Bottom CTA ======== */}
      <section className="py-24">
        <div className="mx-auto max-w-3xl px-4 text-center sm:px-6 lg:px-8">
          <h2 className="text-3xl font-bold tracking-tight sm:text-4xl">
            Ready to simplify your stack?
          </h2>
          <p className="mt-4 text-lg text-uwas-text-muted">
            Install UWAS in under 10 seconds and start serving.
          </p>

          <div className="mt-10 flex flex-col items-center justify-center gap-4 sm:flex-row">
            <Link
              to="/quickstart"
              className="inline-flex items-center gap-2 rounded-xl bg-uwas-blue px-7 py-3.5 text-sm font-semibold text-white no-underline shadow-lg shadow-uwas-blue/25 transition-all hover:bg-uwas-blue-light hover:shadow-xl hover:shadow-uwas-blue/30"
            >
              Get Started
              <ArrowRight className="h-4 w-4" />
            </Link>
            <a
              href="https://github.com/avrahambenaram/uwas"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 rounded-xl border border-uwas-border bg-uwas-bg-light/50 px-7 py-3.5 text-sm font-semibold text-uwas-text no-underline transition-all hover:border-uwas-blue/50 hover:bg-uwas-bg-light"
            >
              <Star className="h-4 w-4" />
              Star on GitHub
            </a>
          </div>

          <div className="mt-8 inline-flex items-center gap-3 rounded-xl border border-uwas-border bg-uwas-code-bg px-5 py-3 font-mono text-sm text-uwas-text-muted">
            <TerminalIcon className="h-4 w-4 text-uwas-green" />
            <span>curl -fsSL https://uwas.dev/install.sh | sh</span>
          </div>
        </div>
      </section>
    </div>
  )
}
