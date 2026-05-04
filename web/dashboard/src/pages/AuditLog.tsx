import { useState, useCallback, useMemo } from 'react';
import { Shield, RefreshCw, CheckCircle, XCircle, Search, X } from 'lucide-react';
import { fetchAuditLog, type AuditEntry } from '@/lib/api';
import { usePolling } from '@/hooks/usePolling';

const ACTION_COLORS: Record<string, string> = {
  'config.reload': 'bg-blue-500/20 text-blue-400',
  'domain.create': 'bg-green-500/20 text-green-400',
  'domain.delete': 'bg-red-500/20 text-red-400',
  'domain.update': 'bg-yellow-500/20 text-yellow-400',
  'cache.purge': 'bg-purple-500/20 text-purple-400',
  'backup.create': 'bg-emerald-500/20 text-emerald-400',
  'backup.restore': 'bg-orange-500/20 text-orange-400',
  'backup.delete': 'bg-red-500/20 text-red-400',
  'backup.schedule': 'bg-cyan-500/20 text-cyan-400',
};

function formatTime(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleString();
}

export default function AuditLog() {
  const [entries, setEntries] = useState<AuditEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  // Action chip = exact-match filter on entry.action.
  // Search = substring across action/detail/ip/user.
  // Previously a single `filter` field served both purposes via .includes(),
  // which meant clicking the "user.login" chip also matched
  // "user.login.fail" — chips looked like exact filters but weren't.
  const [actionFilter, setActionFilter] = useState('');
  const [search, setSearch] = useState('');

  const load = useCallback(async () => {
    try {
      const result = await fetchAuditLog();
      setEntries((result || []).reverse()); // newest first
      setError('');
    } catch (e) { setError((e as Error).message); }
    finally { setLoading(false); }
  }, []);

  // 30s instead of 10s — audit entries arrive in bursts after admin actions,
  // not steadily. Hook also pauses when the tab is in the background.
  usePolling(load, 30_000);

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    return entries.filter(e => {
      if (actionFilter && e.action !== actionFilter) return false;
      if (!q) return true;
      return (
        e.action.toLowerCase().includes(q) ||
        e.detail.toLowerCase().includes(q) ||
        e.ip.toLowerCase().includes(q) ||
        (e.user || '').toLowerCase().includes(q)
      );
    });
  }, [entries, actionFilter, search]);

  const showUserColumn = entries.some(e => !!e.user);

  const actionTypes = useMemo(
    () => [...new Set(entries.map(e => e.action))].sort(),
    [entries],
  );

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">Audit Log</h1>
          <p className="text-sm text-muted-foreground">Admin action history ({entries.length} entries)</p>
        </div>
        <button onClick={load} className="flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-xs text-card-foreground hover:bg-[#475569]">
          <RefreshCw size={12} /> Refresh
        </button>
      </div>

      {error && <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">{error}</div>}

      {loading && entries.length === 0 && (
        <div className="text-center py-12 text-sm text-muted-foreground">Loading audit log...</div>
      )}

      {/* Search */}
      <div className="relative">
        <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground" />
        <input
          type="text"
          value={search}
          onChange={e => setSearch(e.target.value)}
          placeholder="Search action, detail, IP, or user…"
          className="w-full rounded-md border border-border bg-background py-2 pl-9 pr-9 text-sm text-foreground outline-none focus:border-blue-500"
        />
        {search && (
          <button
            onClick={() => setSearch('')}
            className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
            title="Clear search"
          >
            <X size={14} />
          </button>
        )}
      </div>

      {/* Action filter chips — exact match, not substring */}
      <div className="flex flex-wrap gap-2">
        <button
          onClick={() => setActionFilter('')}
          className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
            !actionFilter ? 'bg-blue-600 text-white' : 'bg-accent text-muted-foreground hover:text-foreground'
          }`}
        >
          All
        </button>
        {actionTypes.map(action => (
          <button
            key={action}
            onClick={() => setActionFilter(actionFilter === action ? '' : action)}
            className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
              actionFilter === action ? 'bg-blue-600 text-white' : 'bg-accent text-muted-foreground hover:text-foreground'
            }`}
          >
            {action}
          </button>
        ))}
      </div>

      {/* Table */}
      <div className="rounded-lg border border-border bg-card shadow-md">
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-border text-muted-foreground">
                <th className="px-5 py-3 w-8"></th>
                <th className="px-5 py-3">Time</th>
                <th className="px-5 py-3">Action</th>
                <th className="px-5 py-3">Detail</th>
                {showUserColumn && <th className="px-5 py-3">User</th>}
                <th className="px-5 py-3">IP</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((entry, i) => (
                <tr key={i} className="border-b border-border/50 text-card-foreground">
                  <td className="px-5 py-2.5">
                    {entry.success ? (
                      <CheckCircle size={14} className="text-green-400" />
                    ) : (
                      <XCircle size={14} className="text-red-400" />
                    )}
                  </td>
                  <td className="px-5 py-2.5 text-xs text-muted-foreground whitespace-nowrap">
                    {formatTime(entry.time)}
                  </td>
                  <td className="px-5 py-2.5">
                    <span className={`inline-block rounded-md px-2 py-0.5 text-xs font-medium ${ACTION_COLORS[entry.action] || 'bg-slate-500/20 text-muted-foreground'}`}>
                      {entry.action}
                    </span>
                  </td>
                  <td className="px-5 py-2.5 font-mono text-xs text-muted-foreground max-w-xs truncate">
                    {entry.detail || '-'}
                  </td>
                  {showUserColumn && (
                    <td className="px-5 py-2.5 font-mono text-xs text-foreground">
                      {entry.user || '-'}
                    </td>
                  )}
                  <td className="px-5 py-2.5 font-mono text-xs text-muted-foreground">
                    {entry.ip}
                  </td>
                </tr>
              ))}
              {filtered.length === 0 && (
                <tr>
                  <td colSpan={showUserColumn ? 6 : 5} className="px-5 py-8 text-center text-muted-foreground">
                    <Shield size={24} className="mx-auto mb-2 opacity-50" />
                    No audit entries yet
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
