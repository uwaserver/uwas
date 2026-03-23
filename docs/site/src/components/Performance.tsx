interface Metric {
  value: string
  label: string
  sub: string
  color: string
}

const metrics: Metric[] = [
  { value: '7K', label: 'req/sec', sub: 'HTTP throughput', color: 'var(--accent-blue)' },
  { value: '75M', label: 'cache ops/sec', sub: 'Cache engine', color: 'var(--accent-cyan)' },
  { value: '<1ms', label: 'p99 latency', sub: 'Overhead', color: 'var(--accent-green)' },
  { value: '18MB', label: 'Binary', sub: 'Statically linked', color: 'var(--accent-purple)' },
  { value: '1,718', label: 'Tests', sub: '100% pass', color: 'var(--accent-orange)' },
  { value: '<2s', label: 'Cold Start', sub: 'Ready to serve', color: 'var(--accent-pink)' },
]

export default function Performance() {
  return (
    <section
      className="border-y py-24"
      style={{ borderColor: 'var(--border)', backgroundColor: 'var(--bg-secondary)' }}
    >
      <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
        {/* Section header */}
        <div className="mb-16 text-center">
          <h2 className="text-3xl font-bold tracking-tight sm:text-4xl" style={{ color: 'var(--text-primary)' }}>
            Performance that speaks for itself
          </h2>
          <p className="mt-4 text-lg" style={{ color: 'var(--text-secondary)' }}>
            Built in pure Go for speed, safety, and minimal footprint.
          </p>
        </div>

        {/* 3x2 grid */}
        <div className="grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
          {metrics.map((m) => (
            <div
              key={m.label}
              className="rounded-xl border p-8 text-center transition-all hover:border-blue-500/30"
              style={{ backgroundColor: 'var(--bg-card)', borderColor: 'var(--border)' }}
            >
              <div className="text-4xl font-extrabold tracking-tight" style={{ color: m.color }}>
                {m.value}
              </div>
              <div className="mt-2 text-sm font-semibold" style={{ color: 'var(--text-primary)' }}>
                {m.label}
              </div>
              <div className="mt-1 text-xs" style={{ color: 'var(--text-secondary)' }}>
                {m.sub}
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  )
}
