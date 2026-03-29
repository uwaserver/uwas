import { ArrowRight, GitBranch } from 'lucide-react'

export default function CTA() {
  return (
    <section className="py-24">
      <div className="mx-auto max-w-4xl px-4 sm:px-6 lg:px-8">
        <div
          className="relative overflow-hidden rounded-2xl border p-12 text-center sm:p-16"
          style={{ borderColor: 'var(--border)' }}
        >
          {/* Gradient background */}
          <div
            className="pointer-events-none absolute inset-0"
            style={{
              background: 'linear-gradient(135deg, rgba(59,130,246,0.1), rgba(6,182,212,0.05), rgba(16,185,129,0.1))',
            }}
          />

          <div className="relative">
            <h2 className="text-3xl font-bold tracking-tight sm:text-4xl" style={{ color: 'var(--text-primary)' }}>
              Ready to simplify your stack?
            </h2>
            <p className="mx-auto mt-4 max-w-xl text-lg" style={{ color: 'var(--text-secondary)' }}>
              Replace your entire web server stack with a single 18 MB binary.
              Install in seconds, configure in minutes.
            </p>

            <div className="mt-10 flex flex-col items-center justify-center gap-4 sm:flex-row">
              <a
                href="https://github.com/uwaserver/uwas/releases"
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-6 py-3 font-medium text-white no-underline shadow-lg shadow-blue-600/25 transition-all hover:bg-blue-700 hover:shadow-xl hover:shadow-blue-600/30"
              >
                Download UWAS
                <ArrowRight className="h-4 w-4" />
              </a>
              <a
                href="https://github.com/uwaserver/uwas"
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-2 rounded-lg border bg-transparent px-6 py-3 font-medium no-underline transition-all hover:border-blue-500/50"
                style={{ borderColor: 'var(--border)', color: 'var(--text-primary)' }}
              >
            <GitBranch className="h-4 w-4" />
                View Source
              </a>
            </div>
          </div>
        </div>
      </div>
    </section>
  )
}
