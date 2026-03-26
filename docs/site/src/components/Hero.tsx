import { ArrowRight, Github } from 'lucide-react'

const stats = [
  { value: '0', label: 'Dependencies', sub: 'ext' },
  { value: '45', label: 'Test Packages', sub: 'all passing' },
  { value: '35', label: 'Dashboard Pages', sub: 'built-in' },
  { value: '170+', label: 'API Endpoints', sub: 'RESTful' },
  { value: '~14MB', label: 'Binary', sub: 'statically linked' },
]

export default function Hero() {
  return (
    <section className="relative overflow-hidden pb-20 pt-28 sm:pt-36">
      {/* Background gradient decoration */}
      <div className="pointer-events-none absolute inset-0 overflow-hidden">
        <div
          className="absolute -top-40 left-1/2 h-[600px] w-[900px] -translate-x-1/2 rounded-full blur-3xl"
          style={{ background: 'radial-gradient(ellipse, rgba(59,130,246,0.08), rgba(6,182,212,0.05), transparent 70%)' }}
        />
      </div>

      <div className="relative mx-auto max-w-5xl px-4 text-center sm:px-6 lg:px-8">
        {/* Badge */}
        <div
          className="mb-8 inline-flex items-center gap-2 rounded-full border px-4 py-1.5 text-sm"
          style={{ borderColor: 'var(--border)', color: 'var(--text-secondary)', backgroundColor: 'var(--bg-secondary)' }}
        >
          <span className="inline-block h-2 w-2 animate-pulse rounded-full bg-emerald-500" />
          v1.0 — Production Ready
        </div>

        {/* Heading */}
        <h1 className="text-5xl font-extrabold leading-tight tracking-tight sm:text-7xl">
          <span className="bg-gradient-to-r from-blue-400 via-cyan-400 to-emerald-400 bg-clip-text text-transparent">
            Unified web server.
          </span>
          <br />
          <span style={{ color: 'var(--text-primary)' }}>One binary. Total control.</span>
        </h1>

        {/* Subtitle */}
        <p
          className="mx-auto mt-6 max-w-3xl text-lg leading-relaxed sm:text-xl"
          style={{ color: 'var(--text-secondary)' }}
        >
          A high-performance web server + hosting panel replacing Apache + Nginx + Varnish + Caddy + cPanel.
          Auto HTTPS, built-in cache, PHP/FastCGI, reverse proxy, WordPress management, WAF,
          and a 35-page dashboard — all in a single binary. Written in pure Go.
        </p>

        {/* CTA Buttons */}
        <div className="mt-10 flex flex-col items-center justify-center gap-4 sm:flex-row">
          <a
            href="https://github.com/uwaserver/uwas/releases"
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-6 py-3 font-medium text-white no-underline shadow-lg shadow-blue-600/25 transition-all hover:bg-blue-700 hover:shadow-xl hover:shadow-blue-600/30"
          >
            Download
            <ArrowRight className="h-4 w-4" />
          </a>
          <a
            href="https://github.com/uwaserver/uwas"
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-2 rounded-lg border bg-transparent px-6 py-3 font-medium no-underline transition-all hover:border-blue-500/50"
            style={{ borderColor: 'var(--border)', color: 'var(--text-primary)' }}
          >
            <Github className="h-4 w-4" />
            Star on GitHub
          </a>
        </div>

        {/* Stats row */}
        <div className="mt-16 flex flex-wrap items-center justify-center gap-y-8">
          {stats.map((stat, i) => (
            <div key={stat.label} className="flex items-center">
              {i > 0 && (
                <div
                  className="mx-6 hidden h-10 w-px sm:block"
                  style={{ backgroundColor: 'var(--border)' }}
                />
              )}
              <div className="min-w-[120px] text-center">
                <div className="text-3xl font-extrabold tracking-tight">
                  <span className="bg-gradient-to-r from-blue-400 to-cyan-400 bg-clip-text text-transparent">
                    {stat.value}
                  </span>
                </div>
                <div className="mt-1 text-sm font-semibold" style={{ color: 'var(--text-primary)' }}>
                  {stat.label}
                </div>
                <div className="text-xs" style={{ color: 'var(--text-secondary)' }}>
                  {stat.sub}
                </div>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  )
}
