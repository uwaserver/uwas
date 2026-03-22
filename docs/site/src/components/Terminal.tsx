import { useState, useEffect } from 'react'

const lines = [
  { prompt: true, text: 'curl -fsSL https://uwas.dev/install.sh | sh' },
  { prompt: false, text: '  Installing UWAS v1.0.0...' },
  { prompt: false, text: '  Downloading binary (15MB)... done' },
  { prompt: false, text: '  UWAS installed to /usr/local/bin/uwas' },
  { prompt: false, text: '' },
  { prompt: true, text: 'uwas serve' },
  { prompt: false, text: '  [UWAS] Starting server...' },
  { prompt: false, text: '  [UWAS] Auto-HTTPS enabled (Let\'s Encrypt)' },
  { prompt: false, text: '  [UWAS] Listening on :80, :443' },
  { prompt: false, text: '  [UWAS] Built-in cache: active (75M ops/sec)' },
  { prompt: false, text: '  [UWAS] Ready to serve.' },
]

export default function Terminal() {
  const [visibleLines, setVisibleLines] = useState(0)

  useEffect(() => {
    if (visibleLines < lines.length) {
      const delay = lines[visibleLines]?.prompt ? 600 : 150
      const timer = setTimeout(() => {
        setVisibleLines((prev) => prev + 1)
      }, delay)
      return () => clearTimeout(timer)
    }
  }, [visibleLines])

  return (
    <div className="glow-blue overflow-hidden rounded-xl border border-uwas-border bg-uwas-code-bg">
      {/* Title bar */}
      <div className="flex items-center gap-2 border-b border-uwas-border px-4 py-3">
        <div className="flex gap-1.5">
          <div className="h-3 w-3 rounded-full bg-red-500/70" />
          <div className="h-3 w-3 rounded-full bg-yellow-500/70" />
          <div className="h-3 w-3 rounded-full bg-green-500/70" />
        </div>
        <span className="ml-2 text-xs font-medium text-uwas-text-muted">Terminal</span>
      </div>

      {/* Content */}
      <div className="p-4 font-mono text-sm leading-relaxed">
        {lines.slice(0, visibleLines).map((line, i) => (
          <div key={i} className="min-h-[1.5em]">
            {line.prompt ? (
              <>
                <span className="text-uwas-green">$</span>{' '}
                <span className="text-uwas-text">{line.text}</span>
              </>
            ) : (
              <span className="text-uwas-text-muted">{line.text}</span>
            )}
          </div>
        ))}
        {visibleLines < lines.length && (
          <div className="min-h-[1.5em]">
            {lines[visibleLines]?.prompt && (
              <>
                <span className="text-uwas-green">$</span>{' '}
              </>
            )}
            <span className="terminal-cursor inline-block h-4 w-2 bg-uwas-text" />
          </div>
        )}
        {visibleLines >= lines.length && (
          <div className="min-h-[1.5em]">
            <span className="text-uwas-green">$</span>{' '}
            <span className="terminal-cursor inline-block h-4 w-2 bg-uwas-text" />
          </div>
        )}
      </div>
    </div>
  )
}
