import { useState, useEffect } from 'react';
import { Globe, X } from 'lucide-react';
import { fetchDomains, type DomainData } from '@/lib/api';

const typeBadge = (type: string) => {
  const styles: Record<string, string> = {
    static: 'bg-blue-500/15 text-blue-400',
    php: 'bg-purple-500/15 text-purple-400',
    proxy: 'bg-orange-500/15 text-orange-400',
    redirect: 'bg-slate-500/15 text-slate-400',
  };
  return (
    <span
      className={`rounded-full px-2.5 py-0.5 text-xs font-medium ${styles[type] ?? 'bg-slate-500/15 text-slate-400'}`}
    >
      {type}
    </span>
  );
};

const sslBadge = (ssl: string) => {
  const styles: Record<string, string> = {
    auto: 'bg-emerald-500/15 text-emerald-400',
    manual: 'bg-amber-500/15 text-amber-400',
    off: 'bg-red-500/15 text-red-400',
  };
  return (
    <span
      className={`rounded-full px-2.5 py-0.5 text-xs font-medium ${styles[ssl] ?? 'bg-slate-500/15 text-slate-400'}`}
    >
      {ssl}
    </span>
  );
};

export default function Domains() {
  const [domains, setDomains] = useState<DomainData[]>([]);
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<DomainData | null>(null);

  useEffect(() => {
    fetchDomains()
      .then(setDomains)
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold text-slate-100">Domains</h1>
        <p className="text-sm text-slate-400">
          Manage your server domains and routing
        </p>
      </div>

      {/* Table */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] shadow-md">
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-[#334155] text-slate-400">
                <th className="px-5 py-3 font-medium">Host</th>
                <th className="px-5 py-3 font-medium">Type</th>
                <th className="px-5 py-3 font-medium">SSL</th>
                <th className="px-5 py-3 font-medium">Root / Target</th>
              </tr>
            </thead>
            <tbody>
              {loading && (
                <tr>
                  <td
                    colSpan={4}
                    className="px-5 py-8 text-center text-slate-500"
                  >
                    Loading...
                  </td>
                </tr>
              )}
              {!loading && domains.length === 0 && (
                <tr>
                  <td
                    colSpan={4}
                    className="px-5 py-8 text-center text-slate-500"
                  >
                    No domains configured
                  </td>
                </tr>
              )}
              {domains.map((d) => (
                <tr
                  key={d.host}
                  onClick={() => setSelected(d)}
                  className="cursor-pointer border-b border-[#334155]/50 text-slate-300 transition hover:bg-[#334155]/30"
                >
                  <td className="px-5 py-3">
                    <div className="flex items-center gap-2">
                      <Globe size={14} className="text-slate-500" />
                      <span className="font-mono text-xs">{d.host}</span>
                    </div>
                  </td>
                  <td className="px-5 py-3">{typeBadge(d.type)}</td>
                  <td className="px-5 py-3">{sslBadge(d.ssl)}</td>
                  <td className="px-5 py-3 font-mono text-xs text-slate-400">
                    {d.root || '--'}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      {/* Detail slide-over */}
      {selected && (
        <div className="fixed inset-0 z-50 flex justify-end">
          <div
            className="absolute inset-0 bg-black/50"
            onClick={() => setSelected(null)}
          />
          <div className="relative z-10 w-full max-w-md overflow-y-auto border-l border-[#334155] bg-[#0f172a] p-6 shadow-2xl">
            <div className="mb-6 flex items-center justify-between">
              <h2 className="text-lg font-bold text-slate-100">
                Domain Details
              </h2>
              <button
                onClick={() => setSelected(null)}
                className="rounded-md p-1 text-slate-400 hover:text-slate-200"
              >
                <X size={18} />
              </button>
            </div>
            <dl className="space-y-4">
              {([
                ['Host', selected.host],
                ['Type', selected.type],
                ['SSL', selected.ssl],
                ['Root / Target', selected.root || '--'],
                [
                  'Aliases',
                  selected.aliases?.length
                    ? selected.aliases.join(', ')
                    : 'None',
                ],
              ] as const).map(([label, value]) => (
                <div key={label}>
                  <dt className="text-xs font-medium text-slate-500 uppercase">
                    {label}
                  </dt>
                  <dd className="mt-1 text-sm text-slate-200">{value}</dd>
                </div>
              ))}
            </dl>
          </div>
        </div>
      )}
    </div>
  );
}
