import { useState } from 'react'
import { Copy, Check } from 'lucide-react'

interface Tab {
  id: string
  label: string
  code: string
}

const tabs: Tab[] = [
  {
    id: 'zero',
    label: 'Zero Config',
    code: `$ uwas

[UWAS] Starting server...
[UWAS] Auto-detecting sites in /var/www/
[UWAS] Found: example.com (static)
[UWAS] Auto-HTTPS enabled (Let's Encrypt)
[UWAS] Cache engine: active (L1 memory + L2 disk)
[UWAS] Dashboard: http://localhost:2019
[UWAS] Listening on :80, :443
[UWAS] Ready to serve.`,
  },
  {
    id: 'docker',
    label: 'Docker',
    code: `$ docker run -p 80:80 -p 443:443 \\
    -v ./uwas.yaml:/etc/uwas/uwas.yaml \\
    ghcr.io/uwaserver/uwas:latest

[UWAS] Starting server...
[UWAS] Config loaded: /etc/uwas/uwas.yaml
[UWAS] Listening on :80, :443
[UWAS] Ready to serve.`,
  },
  {
    id: 'config',
    label: 'Config',
    code: `# uwas.yaml
domains:
  - name: example.com
    type: static
    root: /var/www/html
    tls:
      auto: true
    cache:
      enabled: true
      ttl: 3600

  - name: app.example.com
    type: php
    root: /var/www/app
    php:
      fastcgi: 127.0.0.1:9000
      index: index.php
    tls:
      auto: true`,
  },
  {
    id: 'migrate',
    label: 'Migrate',
    code: `$ uwas migrate nginx /etc/nginx/sites-enabled/default

[UWAS] Parsing nginx config...
[UWAS] Found 3 server blocks
[UWAS] Converting to UWAS format...
[UWAS] Output: uwas.yaml

# Pipe directly to config file:
$ uwas migrate nginx /etc/nginx/sites-enabled/default > uwas.yaml
$ uwas migrate apache /etc/apache2/sites-enabled/000-default.conf >> uwas.yaml`,
  },
]

function CodeBlock({ code, onCopy, copied }: { code: string; onCopy: () => void; copied: boolean }) {
  return (
    <div className="relative overflow-hidden rounded-b-xl" style={{ backgroundColor: '#0d1117' }}>
      {/* Copy button */}
      <button
        onClick={onCopy}
        className="absolute right-3 top-3 rounded-md p-1.5 transition-all hover:opacity-80"
        style={{ color: 'var(--text-secondary)' }}
        title="Copy to clipboard"
      >
        {copied ? <Check className="h-4 w-4 text-emerald-400" /> : <Copy className="h-4 w-4" />}
      </button>

      <pre className="overflow-x-auto p-5">
        <code className="text-sm leading-relaxed" style={{ fontFamily: "'JetBrains Mono', 'Fira Code', monospace" }}>
          {code.split('\n').map((line, i) => {
            // Comment lines
            if (line.trimStart().startsWith('#')) {
              return (
                <div key={i}>
                  <span style={{ color: '#6a737d' }}>{line}</span>
                </div>
              )
            }
            // Prompt lines
            if (line.startsWith('$')) {
              return (
                <div key={i}>
                  <span style={{ color: '#10b981' }}>$</span>
                  <span style={{ color: '#e6edf3' }}>{line.slice(1)}</span>
                </div>
              )
            }
            // [UWAS] log lines
            if (line.includes('[UWAS]')) {
              return (
                <div key={i}>
                  <span style={{ color: '#3b82f6' }}>[UWAS]</span>
                  <span style={{ color: '#8b949e' }}>{line.split('[UWAS]')[1]}</span>
                </div>
              )
            }
            // YAML keys
            if (line.match(/^\s*[\w-]+:/)) {
              const colonIdx = line.indexOf(':')
              return (
                <div key={i}>
                  <span style={{ color: '#7ee787' }}>{line.slice(0, colonIdx)}</span>
                  <span style={{ color: '#e6edf3' }}>:</span>
                  <span style={{ color: '#a5d6ff' }}>{line.slice(colonIdx + 1)}</span>
                </div>
              )
            }
            // Fallback
            return (
              <div key={i}>
                <span style={{ color: '#8b949e' }}>{line}</span>
              </div>
            )
          })}
        </code>
      </pre>
    </div>
  )
}

export default function QuickStart() {
  const [activeTab, setActiveTab] = useState('zero')
  const [copied, setCopied] = useState(false)

  const activeContent = tabs.find((t) => t.id === activeTab)

  function handleCopy() {
    if (activeContent) {
      navigator.clipboard.writeText(activeContent.code).then(() => {
        setCopied(true)
        setTimeout(() => setCopied(false), 2000)
      })
    }
  }

  return (
    <section id="quickstart" className="scroll-mt-20 py-24">
      <div className="mx-auto max-w-4xl px-4 sm:px-6 lg:px-8">
        {/* Section header */}
        <div className="mb-16 text-center">
          <h2 className="text-3xl font-bold tracking-tight sm:text-4xl" style={{ color: 'var(--text-primary)' }}>
            Up and running in seconds
          </h2>
          <p className="mt-4 text-lg" style={{ color: 'var(--text-secondary)' }}>
            Four ways to get started — pick the one that fits your workflow.
          </p>
        </div>

        {/* Terminal window */}
        <div className="overflow-hidden rounded-xl border" style={{ borderColor: 'var(--border)' }}>
          {/* Tab bar */}
          <div
            className="flex items-center gap-0 border-b"
            style={{ backgroundColor: '#161b22', borderColor: 'var(--border)' }}
          >
            {/* Traffic light dots */}
            <div className="flex gap-1.5 px-4 py-3">
              <div className="h-3 w-3 rounded-full" style={{ backgroundColor: 'rgba(239,68,68,0.7)' }} />
              <div className="h-3 w-3 rounded-full" style={{ backgroundColor: 'rgba(245,158,11,0.7)' }} />
              <div className="h-3 w-3 rounded-full" style={{ backgroundColor: 'rgba(16,185,129,0.7)' }} />
            </div>

            {/* Tabs */}
            <div className="flex flex-1 overflow-x-auto">
              {tabs.map((tab) => (
                <button
                  key={tab.id}
                  onClick={() => { setActiveTab(tab.id); setCopied(false) }}
                  className="relative px-4 py-3 text-xs font-medium transition-colors sm:text-sm"
                  style={{
                    color: activeTab === tab.id ? '#e6edf3' : '#8b949e',
                    backgroundColor: activeTab === tab.id ? '#0d1117' : 'transparent',
                  }}
                >
                  {tab.label}
                  {activeTab === tab.id && (
                    <div
                      className="absolute bottom-0 left-0 right-0 h-0.5"
                      style={{ backgroundColor: 'var(--accent-blue)' }}
                    />
                  )}
                </button>
              ))}
            </div>
          </div>

          {/* Code content */}
          {activeContent && (
            <CodeBlock code={activeContent.code} onCopy={handleCopy} copied={copied} />
          )}
        </div>
      </div>
    </section>
  )
}
