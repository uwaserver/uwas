import { useState, useEffect, useCallback } from 'react';
import { ShieldAlert, ShieldOff, ShieldCheck, Trash2, RefreshCw } from 'lucide-react';
import {
  fetchUnknownDomains, blockUnknownDomain, unblockUnknownDomain, dismissUnknownDomain,
  type UnknownDomainEntry,
} from '@/lib/api';

function timeAgo(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return 'just now';
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

export default function UnknownDomains() {
  const [entries, setEntries] = useState<UnknownDomainEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [acting, setActing] = useState<string | null>(null);

  const load = useCallback(() => {
    fetchUnknownDomains()
      .then(d => setEntries(d ?? []))
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    load();
    const iv = setInterval(load, 10000);
    return () => clearInterval(iv);
  }, [load]);

  const act = async (host: string, fn: (h: string) => Promise<unknown>) => {
    setActing(host);
    try {
      await fn(host);
      load();
    } catch {
      /* ignore */
    } finally {
      setActing(null);
    }
  };

  const blocked = entries.filter(e => e.blocked);
  const unblocked = entries.filter(e => !e.blocked);

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">Unknown Domains</h1>
          <p className="mt-1 text-sm text-slate-400">
            Hostnames hitting your server that aren't configured. Blocked hosts get 403, others get 421.
          </p>
        </div>
        <button
          onClick={load}
          className="flex items-center gap-2 rounded-md border border-[#334155] bg-[#1e293b] px-3 py-2 text-sm text-slate-300 hover:bg-[#334155]"
        >
          <RefreshCw size={14} />
          Refresh
        </button>
      </div>

      {loading && (
        <div className="text-center text-slate-500 py-12">Loading...</div>
      )}

      {!loading && entries.length === 0 && (
        <div className="rounded-lg border border-[#334155] bg-[#1e293b] px-6 py-12 text-center">
          <ShieldCheck size={40} className="mx-auto mb-3 text-green-400" />
          <p className="text-slate-300 font-medium">No unknown domains detected</p>
          <p className="text-sm text-slate-500 mt-1">All incoming requests match a configured domain.</p>
        </div>
      )}

      {/* Unblocked — candidate domains */}
      {unblocked.length > 0 && (
        <div>
          <h2 className="text-sm font-semibold uppercase tracking-wider text-slate-400 mb-3">
            Unconfigured Hosts ({unblocked.length})
          </h2>
          <div className="overflow-hidden rounded-lg border border-[#334155]">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-[#334155] bg-[#1e293b]/50 text-left text-xs uppercase tracking-wider text-slate-500">
                  <th className="px-4 py-3">Hostname</th>
                  <th className="px-4 py-3 text-right">Hits</th>
                  <th className="px-4 py-3">First Seen</th>
                  <th className="px-4 py-3">Last Seen</th>
                  <th className="px-4 py-3 text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[#334155]">
                {unblocked.map(e => (
                  <tr key={e.host} className="bg-[#0f172a] hover:bg-[#1e293b]/50 transition-colors">
                    <td className="px-4 py-3 font-mono text-slate-200">{e.host}</td>
                    <td className="px-4 py-3 text-right">
                      <span className="rounded-full bg-amber-500/15 px-2.5 py-0.5 text-xs font-medium text-amber-400">
                        {e.hits.toLocaleString()}
                      </span>
                    </td>
                    <td className="px-4 py-3 text-slate-400">{timeAgo(e.first_seen)}</td>
                    <td className="px-4 py-3 text-slate-400">{timeAgo(e.last_seen)}</td>
                    <td className="px-4 py-3 text-right">
                      <div className="flex items-center justify-end gap-2">
                        <button
                          onClick={() => act(e.host, blockUnknownDomain)}
                          disabled={acting === e.host}
                          className="flex items-center gap-1 rounded-md bg-red-500/15 px-2.5 py-1.5 text-xs font-medium text-red-400 hover:bg-red-500/25 disabled:opacity-50"
                          title="Block this domain (403)"
                        >
                          <ShieldAlert size={13} />
                          Block
                        </button>
                        <button
                          onClick={() => act(e.host, dismissUnknownDomain)}
                          disabled={acting === e.host}
                          className="flex items-center gap-1 rounded-md bg-slate-500/15 px-2.5 py-1.5 text-xs font-medium text-slate-400 hover:bg-slate-500/25 disabled:opacity-50"
                          title="Dismiss from list"
                        >
                          <Trash2 size={13} />
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Blocked domains */}
      {blocked.length > 0 && (
        <div>
          <h2 className="text-sm font-semibold uppercase tracking-wider text-red-400 mb-3">
            Blocked ({blocked.length})
          </h2>
          <div className="overflow-hidden rounded-lg border border-red-500/30">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-red-500/20 bg-red-500/5 text-left text-xs uppercase tracking-wider text-slate-500">
                  <th className="px-4 py-3">Hostname</th>
                  <th className="px-4 py-3 text-right">Hits</th>
                  <th className="px-4 py-3">Last Seen</th>
                  <th className="px-4 py-3 text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-red-500/10">
                {blocked.map(e => (
                  <tr key={e.host} className="bg-[#0f172a] hover:bg-red-500/5 transition-colors">
                    <td className="px-4 py-3 font-mono text-slate-300 line-through decoration-red-500/40">{e.host}</td>
                    <td className="px-4 py-3 text-right">
                      <span className="rounded-full bg-red-500/15 px-2.5 py-0.5 text-xs font-medium text-red-400">
                        {e.hits.toLocaleString()}
                      </span>
                    </td>
                    <td className="px-4 py-3 text-slate-400">{timeAgo(e.last_seen)}</td>
                    <td className="px-4 py-3 text-right">
                      <div className="flex items-center justify-end gap-2">
                        <button
                          onClick={() => act(e.host, unblockUnknownDomain)}
                          disabled={acting === e.host}
                          className="flex items-center gap-1 rounded-md bg-green-500/15 px-2.5 py-1.5 text-xs font-medium text-green-400 hover:bg-green-500/25 disabled:opacity-50"
                          title="Unblock"
                        >
                          <ShieldOff size={13} />
                          Unblock
                        </button>
                        <button
                          onClick={() => act(e.host, dismissUnknownDomain)}
                          disabled={acting === e.host}
                          className="flex items-center gap-1 rounded-md bg-slate-500/15 px-2.5 py-1.5 text-xs font-medium text-slate-400 hover:bg-slate-500/25 disabled:opacity-50"
                          title="Remove from list"
                        >
                          <Trash2 size={13} />
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}
