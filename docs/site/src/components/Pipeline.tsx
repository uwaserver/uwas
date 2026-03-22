import { useState } from 'react'
import {
  Wifi, Lock, FileText, Shield, Globe, Database,
  Server, HardDrive, Send, Layers, ChevronRight,
} from 'lucide-react'

interface Stage {
  id: string
  label: string
  icon: typeof Wifi
  color: string
  glow: string
  description: string
  items?: string[]
  category: 'ingress' | 'security' | 'routing' | 'handler' | 'egress'
}

const stages: Stage[] = [
  {
    id: 'tcp', label: 'TCP Accept', icon: Wifi, color: '#3b82f6', glow: '#3b82f680',
    description: 'Raw TCP connection accepted. Connection limiting and Slowloris protection applied.',
    category: 'ingress',
  },
  {
    id: 'tls', label: 'TLS / SNI', icon: Lock, color: '#06b6d4', glow: '#06b6d480',
    description: 'TLS termination with SNI-based certificate selection. HTTP/2 + HTTP/3 ALPN negotiation.',
    category: 'ingress',
  },
  {
    id: 'http', label: 'HTTP Parse', icon: FileText, color: '#8b5cf6', glow: '#8b5cf680',
    description: 'HTTP request parsing, method validation, URL normalization, header extraction.',
    category: 'ingress',
  },
  {
    id: 'middleware', label: 'Middleware', icon: Shield, color: '#f59e0b', glow: '#f59e0b80',
    description: '8-layer middleware chain. Each layer can be independently configured per domain.',
    items: ['Recovery', 'Request ID', 'Security Headers', 'Rate Limit', 'WAF', 'CORS', 'Compression', 'Access Log'],
    category: 'security',
  },
  {
    id: 'vhost', label: 'VHost Lookup', icon: Globe, color: '#14b8a6', glow: '#14b8a680',
    description: 'Virtual host routing: exact match → alias → wildcard → fallback. Sub-millisecond lookup.',
    category: 'routing',
  },
  {
    id: 'cache-check', label: 'Cache Check', icon: Database, color: '#10b981', glow: '#10b98180',
    description: 'L1 memory (256-shard LRU) → L2 disk lookup. Grace mode serves stale content during revalidation.',
    category: 'routing',
  },
  {
    id: 'handler', label: 'Handler', icon: Server, color: '#6366f1', glow: '#6366f180',
    description: 'Request dispatched to the appropriate handler based on domain type.',
    items: ['Static', 'PHP/FastCGI', 'Proxy', 'Redirect'],
    category: 'handler',
  },
  {
    id: 'cache-store', label: 'Cache Store', icon: HardDrive, color: '#10b981', glow: '#10b98180',
    description: 'Cacheable responses stored in L1 memory + L2 disk. TTL and tag-based invalidation.',
    category: 'egress',
  },
  {
    id: 'response', label: 'Response', icon: Send, color: '#ec4899', glow: '#ec489980',
    description: 'Response delivered to client. Metrics recorded, audit logged, analytics tracked.',
    category: 'egress',
  },
]

const categoryLabels: Record<string, { label: string; color: string }> = {
  ingress: { label: 'Ingress', color: '#3b82f6' },
  security: { label: 'Security', color: '#f59e0b' },
  routing: { label: 'Routing', color: '#10b981' },
  handler: { label: 'Processing', color: '#6366f1' },
  egress: { label: 'Egress', color: '#ec4899' },
}

export default function Pipeline() {
  const [active, setActive] = useState<string | null>(null)
  const activeStage = stages.find(s => s.id === active)

  return (
    <section
      id="architecture"
      className="scroll-mt-20 border-y py-24"
      style={{ borderColor: 'var(--border)', backgroundColor: 'var(--bg-secondary)' }}
    >
      <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
        {/* Header */}
        <div className="mb-16 text-center">
          <h2 className="text-3xl font-bold tracking-tight sm:text-4xl" style={{ color: 'var(--text-primary)' }}>
            Request Pipeline
          </h2>
          <p className="mt-4 text-lg" style={{ color: 'var(--text-secondary)' }}>
            Every request flows through 9 stages — from TCP accept to response delivery.
          </p>
        </div>

        {/* Category legend */}
        <div className="mb-10 flex flex-wrap items-center justify-center gap-4">
          {Object.entries(categoryLabels).map(([key, { label, color }]) => (
            <div key={key} className="flex items-center gap-2">
              <div className="h-2 w-2 rounded-full" style={{ backgroundColor: color }} />
              <span className="text-xs font-medium" style={{ color: 'var(--text-secondary)' }}>{label}</span>
            </div>
          ))}
        </div>

        {/* Desktop: horizontal pipeline */}
        <div className="hidden lg:block">
          <div className="relative flex items-center justify-center">
            {/* Animated connection line */}
            <div
              className="absolute top-8 left-[5%] right-[5%] h-[2px]"
              style={{
                background: `linear-gradient(90deg, #3b82f6, #06b6d4, #8b5cf6, #f59e0b, #14b8a6, #10b981, #6366f1, #10b981, #ec4899)`,
                opacity: 0.3,
              }}
            />
            {/* Animated pulse on line */}
            <div
              className="pipeline-pulse absolute top-[7px] left-[5%] h-1 w-20 rounded-full"
              style={{
                background: 'linear-gradient(90deg, transparent, #3b82f6, #06b6d4, transparent)',
              }}
            />

            <div className="relative z-10 flex items-start gap-1">
              {stages.map((stage, i) => {
                const Icon = stage.icon
                const isActive = active === stage.id
                return (
                  <div key={stage.id} className="flex items-start">
                    {/* Stage node */}
                    <div
                      className="group flex cursor-pointer flex-col items-center"
                      onMouseEnter={() => setActive(stage.id)}
                      onMouseLeave={() => setActive(null)}
                    >
                      {/* Circle node */}
                      <div
                        className="relative flex h-16 w-16 items-center justify-center rounded-2xl border-2 transition-all duration-300"
                        style={{
                          borderColor: isActive ? stage.color : 'var(--border)',
                          backgroundColor: isActive ? `${stage.color}15` : 'var(--bg-card)',
                          boxShadow: isActive ? `0 0 24px ${stage.glow}, 0 0 48px ${stage.color}20` : 'none',
                          transform: isActive ? 'translateY(-4px) scale(1.05)' : 'none',
                        }}
                      >
                        {/* Step number */}
                        <div
                          className="absolute -top-2 -right-2 flex h-5 w-5 items-center justify-center rounded-full text-[9px] font-bold text-white"
                          style={{ backgroundColor: stage.color }}
                        >
                          {String(i + 1).padStart(2, '0')}
                        </div>
                        <Icon
                          className="h-6 w-6 transition-colors duration-300"
                          style={{ color: isActive ? stage.color : 'var(--text-secondary)' }}
                        />
                      </div>
                      {/* Label */}
                      <div
                        className="mt-3 text-center text-[11px] font-semibold uppercase tracking-wider transition-colors duration-300"
                        style={{ color: isActive ? stage.color : 'var(--text-secondary)', maxWidth: '90px' }}
                      >
                        {stage.label}
                      </div>
                      {/* Sub-items (visible on hover) */}
                      {stage.items && (
                        <div
                          className="mt-2 flex flex-col gap-0.5 transition-all duration-300"
                          style={{ opacity: isActive ? 1 : 0.4, maxHeight: isActive ? '200px' : '0px', overflow: 'hidden' }}
                        >
                          {stage.items.map(item => (
                            <div
                              key={item}
                              className="rounded-md px-2 py-0.5 text-center text-[9px] font-medium"
                              style={{ backgroundColor: `${stage.color}15`, color: stage.color }}
                            >
                              {item}
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                    {/* Arrow */}
                    {i < stages.length - 1 && (
                      <div className="mt-5 flex items-center px-0.5">
                        <ChevronRight className="h-4 w-4" style={{ color: 'var(--text-secondary)', opacity: 0.4 }} />
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          </div>

          {/* Detail panel */}
          <div
            className="mx-auto mt-10 max-w-2xl overflow-hidden rounded-xl border transition-all duration-500"
            style={{
              borderColor: activeStage ? activeStage.color : 'var(--border)',
              backgroundColor: 'var(--bg-card)',
              opacity: activeStage ? 1 : 0,
              transform: activeStage ? 'translateY(0)' : 'translateY(8px)',
              maxHeight: activeStage ? '200px' : '0px',
            }}
          >
            {activeStage && (
              <div className="flex items-start gap-4 p-6">
                <div
                  className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg"
                  style={{ backgroundColor: `${activeStage.color}15` }}
                >
                  <activeStage.icon className="h-5 w-5" style={{ color: activeStage.color }} />
                </div>
                <div>
                  <div className="flex items-center gap-2">
                    <h3 className="text-sm font-bold" style={{ color: 'var(--text-primary)' }}>
                      {activeStage.label}
                    </h3>
                    <span
                      className="rounded-full px-2 py-0.5 text-[10px] font-medium"
                      style={{
                        backgroundColor: `${categoryLabels[activeStage.category].color}15`,
                        color: categoryLabels[activeStage.category].color,
                      }}
                    >
                      {categoryLabels[activeStage.category].label}
                    </span>
                  </div>
                  <p className="mt-1 text-sm leading-relaxed" style={{ color: 'var(--text-secondary)' }}>
                    {activeStage.description}
                  </p>
                  {activeStage.items && (
                    <div className="mt-3 flex flex-wrap gap-1.5">
                      {activeStage.items.map(item => (
                        <span
                          key={item}
                          className="rounded-md border px-2 py-0.5 text-[11px] font-medium"
                          style={{ borderColor: `${activeStage.color}30`, color: activeStage.color }}
                        >
                          {item}
                        </span>
                      ))}
                    </div>
                  )}
                </div>
              </div>
            )}
          </div>
          {!activeStage && (
            <p className="mt-6 text-center text-sm" style={{ color: 'var(--text-secondary)', opacity: 0.6 }}>
              Hover over a stage to see details
            </p>
          )}
        </div>

        {/* Mobile: vertical pipeline */}
        <div className="lg:hidden">
          <div className="relative mx-auto max-w-md">
            {/* Vertical line */}
            <div
              className="absolute top-0 bottom-0 left-8 w-[2px]"
              style={{
                background: `linear-gradient(180deg, #3b82f6, #f59e0b, #10b981, #6366f1, #ec4899)`,
                opacity: 0.2,
              }}
            />

            <div className="flex flex-col gap-4">
              {stages.map((stage, i) => {
                const Icon = stage.icon
                return (
                  <div
                    key={stage.id}
                    className="relative flex items-start gap-4 rounded-xl border p-4 transition-all"
                    style={{
                      borderColor: 'var(--border)',
                      backgroundColor: 'var(--bg-card)',
                    }}
                    onClick={() => setActive(active === stage.id ? null : stage.id)}
                  >
                    {/* Step circle */}
                    <div
                      className="relative z-10 flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border-2"
                      style={{ borderColor: stage.color, backgroundColor: `${stage.color}10` }}
                    >
                      <Icon className="h-4 w-4" style={{ color: stage.color }} />
                      <div
                        className="absolute -top-1.5 -right-1.5 flex h-4 w-4 items-center justify-center rounded-full text-[8px] font-bold text-white"
                        style={{ backgroundColor: stage.color }}
                      >
                        {i + 1}
                      </div>
                    </div>

                    <div className="flex-1">
                      <div className="flex items-center gap-2">
                        <h3 className="text-sm font-bold" style={{ color: 'var(--text-primary)' }}>
                          {stage.label}
                        </h3>
                        <span
                          className="rounded-full px-1.5 py-0.5 text-[9px] font-medium"
                          style={{
                            backgroundColor: `${categoryLabels[stage.category].color}15`,
                            color: categoryLabels[stage.category].color,
                          }}
                        >
                          {categoryLabels[stage.category].label}
                        </span>
                      </div>
                      <p className="mt-1 text-xs leading-relaxed" style={{ color: 'var(--text-secondary)' }}>
                        {stage.description}
                      </p>
                      {stage.items && active === stage.id && (
                        <div className="mt-2 flex flex-wrap gap-1">
                          {stage.items.map(item => (
                            <span
                              key={item}
                              className="rounded px-1.5 py-0.5 text-[10px] font-medium"
                              style={{ backgroundColor: `${stage.color}15`, color: stage.color }}
                            >
                              {item}
                            </span>
                          ))}
                        </div>
                      )}
                    </div>

                    {/* Layers icon for expandable */}
                    {stage.items && (
                      <Layers className="mt-1 h-3 w-3 shrink-0" style={{ color: 'var(--text-secondary)', opacity: 0.4 }} />
                    )}
                  </div>
                )
              })}
            </div>
          </div>
        </div>
      </div>
    </section>
  )
}
