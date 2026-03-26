import {
  Shield,
  Zap,
  Code2,
  Network,
  LayoutDashboard,
  Wifi,
  HardDrive,
  ArrowRightLeft,
  BarChart3,
  Brain,
  Activity,
} from 'lucide-react'
import type { LucideIcon } from 'lucide-react'

interface Feature {
  icon: LucideIcon
  title: string
  description: string
  color: string
  bgColor: string
}

const features: Feature[] = [
  {
    icon: Shield,
    title: 'Auto HTTPS',
    description: "Let's Encrypt ACME, SNI routing, automatic certificate renewal. HTTPS just works with zero configuration.",
    color: 'var(--accent-green)',
    bgColor: 'rgba(16, 185, 129, 0.1)',
  },
  {
    icon: Zap,
    title: 'Built-in Cache',
    description: 'L1 memory + L2 disk caching engine. Grace mode, tag-based purge, 75M operations per second.',
    color: 'var(--accent-orange)',
    bgColor: 'rgba(245, 158, 11, 0.1)',
  },
  {
    icon: Code2,
    title: 'PHP Ready',
    description: 'Native FastCGI support, .htaccess compatibility, per-domain PHP versions, connection pooling.',
    color: 'var(--accent-purple)',
    bgColor: 'rgba(139, 92, 246, 0.1)',
  },
  {
    icon: Network,
    title: 'Load Balancer',
    description: '5 load balancing algorithms, active health checks, circuit breaker, WebSocket proxy support.',
    color: 'var(--accent-cyan)',
    bgColor: 'rgba(6, 182, 212, 0.1)',
  },
  {
    icon: LayoutDashboard,
    title: 'Web Dashboard',
    description: '35 pages, real-time SSE updates, built with React 19. Monitor and manage everything from the browser.',
    color: 'var(--accent-blue)',
    bgColor: 'rgba(59, 130, 246, 0.1)',
  },
  {
    icon: Wifi,
    title: 'HTTP/3',
    description: 'QUIC protocol support with automatic Alt-Svc header advertisement for modern clients.',
    color: 'var(--accent-green)',
    bgColor: 'rgba(16, 185, 129, 0.1)',
  },
  {
    icon: HardDrive,
    title: 'Backup / Restore',
    description: 'Local, S3, and SFTP backup targets. Scheduled backups with automatic pruning and one-click restore.',
    color: 'var(--accent-indigo)',
    bgColor: 'rgba(99, 102, 241, 0.1)',
  },
  {
    icon: Shield,
    title: 'WAF Security',
    description: 'SQL injection, XSS, path traversal protection. Configurable rate limiting and IP blocking.',
    color: 'var(--accent-red)',
    bgColor: 'rgba(239, 68, 68, 0.1)',
  },
  {
    icon: ArrowRightLeft,
    title: 'Migration Tool',
    description: 'uwas migrate nginx/apache — automatically convert existing configs to UWAS format.',
    color: 'var(--accent-teal)',
    bgColor: 'rgba(20, 184, 166, 0.1)',
  },
  {
    icon: BarChart3,
    title: 'Analytics',
    description: 'Per-domain analytics with referrer tracking, user agent stats, and hourly breakdowns.',
    color: 'var(--accent-violet)',
    bgColor: 'rgba(139, 92, 246, 0.1)',
  },
  {
    icon: Brain,
    title: 'AI-Native MCP',
    description: 'Model Context Protocol built in. Let AI assistants manage and configure your server natively.',
    color: 'var(--accent-pink)',
    bgColor: 'rgba(236, 72, 153, 0.1)',
  },
  {
    icon: Activity,
    title: 'Observability',
    description: 'Prometheus metrics, p50/p95/p99 latency tracking, audit log, structured JSON logging.',
    color: 'var(--accent-amber)',
    bgColor: 'rgba(245, 158, 11, 0.1)',
  },
]

export default function Features() {
  return (
    <section id="features" className="scroll-mt-20 py-24">
      <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
        {/* Section header */}
        <div className="mb-16 text-center">
          <h2 className="text-3xl font-bold tracking-tight sm:text-4xl" style={{ color: 'var(--text-primary)' }}>
            Everything you need. Nothing you don't.
          </h2>
          <p className="mt-4 text-lg" style={{ color: 'var(--text-secondary)' }}>
            Twelve capabilities that make UWAS the last server you'll ever install.
          </p>
        </div>

        {/* 4x3 grid */}
        <div className="grid gap-5 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {features.map((f) => (
            <div
              key={f.title}
              className="group rounded-xl border p-6 transition-all hover:border-blue-500/30"
              style={{ backgroundColor: 'var(--bg-card)', borderColor: 'var(--border)' }}
            >
              <div
                className="mb-4 inline-flex rounded-xl p-3"
                style={{ backgroundColor: f.bgColor }}
              >
                <f.icon className="h-6 w-6" style={{ color: f.color }} />
              </div>
              <h3 className="mb-2 text-lg font-semibold" style={{ color: 'var(--text-primary)' }}>
                {f.title}
              </h3>
              <p className="text-sm leading-relaxed" style={{ color: 'var(--text-secondary)' }}>
                {f.description}
              </p>
            </div>
          ))}
        </div>
      </div>
    </section>
  )
}
