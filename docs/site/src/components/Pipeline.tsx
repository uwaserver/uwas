import { ArrowRight } from 'lucide-react'

interface PipelineStage {
  label: string
  color: string
  items?: string[]
}

const stages: PipelineStage[] = [
  { label: 'TCP', color: 'var(--accent-blue)' },
  { label: 'TLS / SNI', color: 'var(--accent-cyan)' },
  { label: 'HTTP Parse', color: 'var(--accent-purple)' },
  {
    label: 'Middleware',
    color: 'var(--accent-orange)',
    items: ['Recovery', 'RequestID', 'SecurityHeaders', 'RateLimit', 'WAF', 'CORS', 'Compression', 'AccessLog'],
  },
  { label: 'VHost Lookup', color: 'var(--accent-teal)' },
  { label: 'Cache Check', color: 'var(--accent-green)' },
  {
    label: 'Handler',
    color: 'var(--accent-blue)',
    items: ['Static', 'PHP', 'Proxy', 'Redirect'],
  },
  { label: 'Cache Store', color: 'var(--accent-green)' },
  { label: 'Response', color: 'var(--accent-cyan)' },
]

export default function Pipeline() {
  return (
    <section
      id="architecture"
      className="scroll-mt-20 border-y py-24"
      style={{ borderColor: 'var(--border)', backgroundColor: 'var(--bg-secondary)' }}
    >
      <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
        {/* Section header */}
        <div className="mb-16 text-center">
          <h2 className="text-3xl font-bold tracking-tight sm:text-4xl" style={{ color: 'var(--text-primary)' }}>
            Request Pipeline
          </h2>
          <p className="mt-4 text-lg" style={{ color: 'var(--text-secondary)' }}>
            Every request flows through a layered pipeline — from TCP accept to response delivery.
          </p>
        </div>

        {/* Desktop pipeline (horizontal flow) */}
        <div className="hidden lg:block">
          <div className="flex items-start justify-center gap-2">
            {stages.map((stage, i) => (
              <div key={stage.label} className="flex items-start">
                <div className="flex flex-col items-center">
                  {/* Stage box */}
                  <div
                    className="rounded-lg border px-4 py-3 text-center transition-all hover:scale-105"
                    style={{
                      borderColor: stage.color,
                      backgroundColor: 'var(--bg-card)',
                      boxShadow: `0 0 12px ${stage.color}15`,
                      minWidth: stage.items ? '140px' : '100px',
                    }}
                  >
                    <div className="text-xs font-bold uppercase tracking-wider" style={{ color: stage.color }}>
                      {stage.label}
                    </div>
                  </div>
                  {/* Sub-items */}
                  {stage.items && (
                    <div className="mt-3 flex flex-col gap-1">
                      {stage.items.map((item) => (
                        <div
                          key={item}
                          className="rounded px-2 py-0.5 text-center text-[10px] font-medium"
                          style={{ backgroundColor: `${stage.color}15`, color: stage.color }}
                        >
                          {item}
                        </div>
                      ))}
                    </div>
                  )}
                </div>
                {/* Arrow connector */}
                {i < stages.length - 1 && (
                  <div className="mt-3 flex items-center px-1">
                    <ArrowRight className="h-4 w-4" style={{ color: 'var(--text-secondary)' }} />
                  </div>
                )}
              </div>
            ))}
          </div>
        </div>

        {/* Mobile pipeline (vertical flow) */}
        <div className="lg:hidden">
          <div className="mx-auto flex max-w-sm flex-col items-center gap-3">
            {stages.map((stage, i) => (
              <div key={stage.label} className="flex w-full flex-col items-center">
                <div
                  className="w-full rounded-lg border px-4 py-3 text-center"
                  style={{
                    borderColor: stage.color,
                    backgroundColor: 'var(--bg-card)',
                  }}
                >
                  <div className="text-xs font-bold uppercase tracking-wider" style={{ color: stage.color }}>
                    {stage.label}
                  </div>
                  {stage.items && (
                    <div className="mt-2 flex flex-wrap justify-center gap-1">
                      {stage.items.map((item) => (
                        <span
                          key={item}
                          className="rounded px-2 py-0.5 text-[10px] font-medium"
                          style={{ backgroundColor: `${stage.color}15`, color: stage.color }}
                        >
                          {item}
                        </span>
                      ))}
                    </div>
                  )}
                </div>
                {i < stages.length - 1 && (
                  <div className="my-1" style={{ color: 'var(--text-secondary)' }}>
                    <ArrowRight className="h-4 w-4 rotate-90" />
                  </div>
                )}
              </div>
            ))}
          </div>
        </div>
      </div>
    </section>
  )
}
