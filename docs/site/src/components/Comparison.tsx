import { Check, X, Minus } from 'lucide-react'

type CellValue = true | false | string

interface ComparisonRow {
  feature: string
  uwas: CellValue
  nginx: CellValue
  caddy: CellValue
  apache: CellValue
  litespeed: CellValue
}

const rows: ComparisonRow[] = [
  { feature: 'Single Binary',      uwas: true,  nginx: false,      caddy: true,     apache: false,    litespeed: false },
  { feature: 'Auto HTTPS',         uwas: true,  nginx: false,      caddy: true,     apache: false,    litespeed: false },
  { feature: 'Built-in Cache',     uwas: true,  nginx: false,      caddy: false,    apache: false,    litespeed: true },
  { feature: 'PHP / FastCGI',      uwas: true,  nginx: 'External', caddy: 'Plugin', apache: true,     litespeed: true },
  { feature: '.htaccess Support',  uwas: true,  nginx: false,      caddy: false,    apache: true,     litespeed: true },
  { feature: 'Load Balancer',      uwas: true,  nginx: true,       caddy: true,     apache: 'Module', litespeed: true },
  { feature: 'WAF',                uwas: true,  nginx: 'Module',   caddy: false,    apache: 'Module', litespeed: true },
  { feature: 'Web Dashboard',      uwas: true,  nginx: 'Paid',     caddy: false,    apache: false,    litespeed: true },
  { feature: 'HTTP/3',             uwas: true,  nginx: 'Partial',  caddy: true,     apache: false,    litespeed: true },
  { feature: 'Backup / Restore',   uwas: true,  nginx: false,      caddy: false,    apache: false,    litespeed: false },
  { feature: 'MCP / AI',           uwas: true,  nginx: false,      caddy: false,    apache: false,    litespeed: false },
  { feature: 'Migration Tool',     uwas: true,  nginx: false,      caddy: false,    apache: false,    litespeed: false },
  { feature: 'WebSocket Proxy',    uwas: true,  nginx: true,       caddy: true,     apache: 'Module', litespeed: true },
]

function CellIcon({ value }: { value: CellValue }) {
  if (value === true) return <Check className="mx-auto h-5 w-5 text-emerald-500" />
  if (value === false) return <X className="mx-auto h-5 w-5 opacity-30" style={{ color: 'var(--accent-red)' }} />
  return (
    <span className="flex items-center justify-center gap-1 text-xs font-medium" style={{ color: 'var(--accent-orange)' }}>
      <Minus className="h-3 w-3" /> {value}
    </span>
  )
}

export default function Comparison() {
  return (
    <section id="compare" className="scroll-mt-20 py-24">
      <div className="mx-auto max-w-6xl px-4 sm:px-6 lg:px-8">
        {/* Section header */}
        <div className="mb-16 text-center">
          <h2 className="text-3xl font-bold tracking-tight sm:text-4xl" style={{ color: 'var(--text-primary)' }}>
            How UWAS compares
          </h2>
          <p className="mt-4 text-lg" style={{ color: 'var(--text-secondary)' }}>
            Feature-by-feature against the most popular web servers.
          </p>
        </div>

        {/* Table */}
        <div className="overflow-x-auto rounded-xl border" style={{ borderColor: 'var(--border)' }}>
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b" style={{ borderColor: 'var(--border)', backgroundColor: 'var(--bg-secondary)' }}>
                <th className="sticky left-0 px-5 py-4 text-left font-semibold" style={{ color: 'var(--text-primary)', backgroundColor: 'var(--bg-secondary)' }}>
                  Feature
                </th>
                <th className="px-5 py-4 text-center font-semibold" style={{ color: 'var(--accent-blue)' }}>
                  UWAS
                </th>
                <th className="px-5 py-4 text-center font-semibold" style={{ color: 'var(--text-secondary)' }}>
                  Nginx
                </th>
                <th className="px-5 py-4 text-center font-semibold" style={{ color: 'var(--text-secondary)' }}>
                  Caddy
                </th>
                <th className="px-5 py-4 text-center font-semibold" style={{ color: 'var(--text-secondary)' }}>
                  Apache
                </th>
                <th className="px-5 py-4 text-center font-semibold" style={{ color: 'var(--text-secondary)' }}>
                  LiteSpeed
                </th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row, i) => (
                <tr
                  key={row.feature}
                  className="border-b last:border-0"
                  style={{
                    borderColor: 'var(--border)',
                    backgroundColor: i % 2 === 0 ? 'var(--bg-card)' : 'transparent',
                  }}
                >
                  <td
                    className="sticky left-0 px-5 py-3.5 font-medium"
                    style={{
                      color: 'var(--text-primary)',
                      backgroundColor: i % 2 === 0 ? 'var(--bg-card)' : 'var(--bg-primary)',
                    }}
                  >
                    {row.feature}
                  </td>
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
      </div>
    </section>
  )
}
