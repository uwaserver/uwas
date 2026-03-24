import { useState, useEffect, useCallback, useRef } from 'react';
import { FileText, Search, Pause, Play, RefreshCw, Filter, Download } from 'lucide-react';
import { fetchLogs, type LogEntry } from '@/lib/api';

const statusColor = (status: number): string => {
  if (status >= 500) return 'text-red-400 bg-red-500/15';
  if (status >= 400) return 'text-amber-400 bg-amber-500/15';
  if (status >= 300) return 'text-blue-400 bg-blue-500/15';
  if (status >= 200) return 'text-emerald-400 bg-emerald-500/15';
  return 'text-slate-400 bg-slate-500/15';
};

type StatusFilter = 'all' | '2xx' | '3xx' | '4xx' | '5xx';
type MethodFilter = 'all' | 'GET' | 'POST' | 'PUT' | 'DELETE';

const statusFilters: { label: string; value: StatusFilter }[] = [
  { label: 'All', value: 'all' },
  { label: '2xx', value: '2xx' },
  { label: '3xx', value: '3xx' },
  { label: '4xx', value: '4xx' },
  { label: '5xx', value: '5xx' },
];

const methodFilters: { label: string; value: MethodFilter }[] = [
  { label: 'All', value: 'all' },
  { label: 'GET', value: 'GET' },
  { label: 'POST', value: 'POST' },
  { label: 'PUT', value: 'PUT' },
  { label: 'DELETE', value: 'DELETE' },
];

function matchesFilter(status: number, filter: StatusFilter): boolean {
  if (filter === 'all') return true;
  const base = parseInt(filter) * 100;
  return status >= base && status < base + 100;
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function formatDuration(ms: number): string {
  if (ms < 1) return `${(ms * 1000).toFixed(0)} us`;
  if (ms < 1000) return `${ms.toFixed(1)} ms`;
  return `${(ms / 1000).toFixed(2)} s`;
}

export default function Logs() {
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [paused, setPaused] = useState(false);
  const [filter, setFilter] = useState<StatusFilter>('all');
  const [search, setSearch] = useState('');
  const [domainFilter, setDomainFilter] = useState('');
  const [methodFilter, setMethodFilter] = useState<MethodFilter>('all');
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const load = useCallback(async () => {
    try {
      const data = await fetchLogs();
      setLogs((data ?? []).slice(-100));
      setError('');
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  useEffect(() => {
    if (paused) {
      if (intervalRef.current) {
        clearInterval(intervalRef.current);
        intervalRef.current = null;
      }
      return;
    }
    intervalRef.current = setInterval(load, 2000);
    return () => {
      if (intervalRef.current) {
        clearInterval(intervalRef.current);
        intervalRef.current = null;
      }
    };
  }, [paused, load]);

  const filtered = logs.filter((entry) => {
    if (!matchesFilter(entry.status, filter)) return false;
    if (search && !entry.path.toLowerCase().includes(search.toLowerCase())) return false;
    if (domainFilter && !entry.host.toLowerCase().includes(domainFilter.toLowerCase())) return false;
    if (methodFilter !== 'all' && entry.method !== methodFilter) return false;
    return true;
  });

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">Logs</h1>
          <p className="text-sm text-slate-400">
            Real-time request log viewer
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => setPaused(!paused)}
            className={`flex items-center gap-2 rounded-md border px-3 py-2 text-sm font-medium transition ${
              paused
                ? 'border-emerald-500/50 bg-emerald-500/10 text-emerald-400 hover:bg-emerald-500/20'
                : 'border-amber-500/50 bg-amber-500/10 text-amber-400 hover:bg-amber-500/20'
            }`}
          >
            {paused ? <Play size={14} /> : <Pause size={14} />}
            {paused ? 'Resume' : 'Pause'}
          </button>
          <button
            onClick={load}
            disabled={loading}
            className="flex items-center gap-2 rounded-md border border-[#334155] bg-[#1e293b] px-3 py-2 text-sm text-slate-300 transition hover:bg-[#334155] disabled:opacity-50"
          >
            <RefreshCw size={14} className={loading ? 'animate-spin' : ''} />
            Refresh
          </button>
          <div className="relative group">
            <button
              className="flex items-center gap-2 rounded-md border border-[#334155] bg-[#1e293b] px-3 py-2 text-sm text-slate-300 transition hover:bg-[#334155]"
            >
              <Download size={14} />
              Export
            </button>
            <div className="absolute right-0 top-full mt-1 hidden group-hover:block rounded-md border border-[#334155] bg-[#1e293b] shadow-lg z-10">
              <button
                onClick={() => {
                  const csv = ['Time,Host,Method,Path,Status,Duration_ms,Remote'].concat(
                    filtered.map(e => `${e.time},${e.host},${e.method},"${e.path}",${e.status},${e.duration_ms},${e.remote}`)
                  ).join('\n');
                  const blob = new Blob([csv], { type: 'text/csv' });
                  const url = URL.createObjectURL(blob);
                  const a = document.createElement('a'); a.href = url; a.download = 'uwas-logs.csv';
                  document.body.appendChild(a); a.click(); document.body.removeChild(a); URL.revokeObjectURL(url);
                }}
                className="block w-full px-4 py-2 text-left text-sm text-slate-300 hover:bg-[#334155]"
              >
                CSV
              </button>
              <button
                onClick={() => {
                  const blob = new Blob([JSON.stringify(filtered, null, 2)], { type: 'application/json' });
                  const url = URL.createObjectURL(blob);
                  const a = document.createElement('a'); a.href = url; a.download = 'uwas-logs.json';
                  document.body.appendChild(a); a.click(); document.body.removeChild(a); URL.revokeObjectURL(url);
                }}
                className="block w-full px-4 py-2 text-left text-sm text-slate-300 hover:bg-[#334155]"
              >
                JSON
              </button>
            </div>
          </div>
        </div>
      </div>

      {error && (
        <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">
          {error}
        </div>
      )}

      {/* Filters */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-4 space-y-3">
        <div className="flex items-center gap-2 text-xs font-semibold text-slate-400 mb-1">
          <Filter size={13} /> Filters
          <span className="ml-auto text-slate-500 font-normal">
            {filtered.length} of {logs.length} entries
          </span>
        </div>

        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
          {/* Domain filter */}
          <div>
            <label className="mb-1 block text-[11px] font-medium text-slate-500">Domain</label>
            <input
              type="text"
              value={domainFilter}
              onChange={(e) => setDomainFilter(e.target.value)}
              placeholder="Filter by host..."
              className="w-full rounded-md border border-[#334155] bg-[#0f172a] py-2 px-3 text-sm text-slate-200 placeholder-slate-500 outline-none transition focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
            />
          </div>

          {/* Path search */}
          <div>
            <label className="mb-1 block text-[11px] font-medium text-slate-500">Path</label>
            <div className="relative">
              <Search
                size={14}
                className="absolute top-1/2 left-3 -translate-y-1/2 text-slate-500"
              />
              <input
                type="text"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Filter by path..."
                className="w-full rounded-md border border-[#334155] bg-[#0f172a] py-2 pr-3 pl-9 text-sm text-slate-200 placeholder-slate-500 outline-none transition focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
              />
            </div>
          </div>

          {/* Status filter */}
          <div>
            <label className="mb-1 block text-[11px] font-medium text-slate-500">Status</label>
            <select
              value={filter}
              onChange={(e) => setFilter(e.target.value as StatusFilter)}
              className="w-full rounded-md border border-[#334155] bg-[#0f172a] py-2 px-3 text-sm text-slate-200 outline-none transition focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
            >
              {statusFilters.map((f) => (
                <option key={f.value} value={f.value}>{f.label}</option>
              ))}
            </select>
          </div>

          {/* Method filter */}
          <div>
            <label className="mb-1 block text-[11px] font-medium text-slate-500">Method</label>
            <select
              value={methodFilter}
              onChange={(e) => setMethodFilter(e.target.value as MethodFilter)}
              className="w-full rounded-md border border-[#334155] bg-[#0f172a] py-2 px-3 text-sm text-slate-200 outline-none transition focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
            >
              {methodFilters.map((f) => (
                <option key={f.value} value={f.value}>{f.label}</option>
              ))}
            </select>
          </div>
        </div>
      </div>

      {/* Log table */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] shadow-md">
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-[#334155] text-slate-400">
                <th className="px-5 py-3 font-medium">Time</th>
                <th className="px-5 py-3 font-medium">Method</th>
                <th className="px-5 py-3 font-medium">Host</th>
                <th className="px-5 py-3 font-medium">Path</th>
                <th className="px-5 py-3 font-medium">Status</th>
                <th className="px-5 py-3 font-medium">Bytes</th>
                <th className="px-5 py-3 font-medium">Duration</th>
                <th className="hidden px-5 py-3 font-medium lg:table-cell">Remote</th>
              </tr>
            </thead>
            <tbody>
              {loading && (
                <tr>
                  <td
                    colSpan={8}
                    className="px-5 py-8 text-center text-slate-500"
                  >
                    Loading...
                  </td>
                </tr>
              )}
              {!loading && filtered.length === 0 && (
                <tr>
                  <td
                    colSpan={8}
                    className="px-5 py-8 text-center text-slate-500"
                  >
                    <FileText size={20} className="mx-auto mb-2 opacity-50" />
                    No log entries found
                  </td>
                </tr>
              )}
              {filtered.map((entry, i) => (
                <tr
                  key={`${entry.time}-${i}`}
                  className="border-b border-[#334155]/50 text-slate-300 transition hover:bg-[#334155]/30"
                >
                  <td className="whitespace-nowrap px-5 py-2.5 font-mono text-xs text-slate-400">
                    {entry.time}
                  </td>
                  <td className="px-5 py-2.5">
                    <span className="rounded bg-slate-500/15 px-2 py-0.5 font-mono text-xs font-medium text-slate-300">
                      {entry.method}
                    </span>
                  </td>
                  <td className="whitespace-nowrap px-5 py-2.5 font-mono text-xs text-slate-400">
                    {entry.host}
                  </td>
                  <td className="max-w-xs truncate px-5 py-2.5 font-mono text-xs">
                    {entry.path}
                  </td>
                  <td className="px-5 py-2.5">
                    <span
                      className={`rounded-full px-2.5 py-0.5 text-xs font-medium ${statusColor(entry.status)}`}
                    >
                      {entry.status}
                    </span>
                  </td>
                  <td className="whitespace-nowrap px-5 py-2.5 font-mono text-xs text-slate-400">
                    {formatBytes(entry.bytes)}
                  </td>
                  <td className="whitespace-nowrap px-5 py-2.5 font-mono text-xs text-slate-400">
                    {formatDuration(entry.duration_ms)}
                  </td>
                  <td className="hidden whitespace-nowrap px-5 py-2.5 font-mono text-xs text-slate-500 lg:table-cell">
                    {entry.remote}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
