import { useState, useEffect, useCallback, useRef } from 'react';
import { FileText, Search, Pause, Play, RefreshCw } from 'lucide-react';
import { fetchLogs, type LogEntry } from '@/lib/api';

const statusColor = (status: number): string => {
  if (status >= 500) return 'text-red-400 bg-red-500/15';
  if (status >= 400) return 'text-amber-400 bg-amber-500/15';
  if (status >= 300) return 'text-blue-400 bg-blue-500/15';
  if (status >= 200) return 'text-emerald-400 bg-emerald-500/15';
  return 'text-slate-400 bg-slate-500/15';
};

type StatusFilter = 'all' | '2xx' | '3xx' | '4xx' | '5xx';

const statusFilters: { label: string; value: StatusFilter }[] = [
  { label: 'All', value: 'all' },
  { label: '2xx', value: '2xx' },
  { label: '3xx', value: '3xx' },
  { label: '4xx', value: '4xx' },
  { label: '5xx', value: '5xx' },
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
        </div>
      </div>

      {error && (
        <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">
          {error}
        </div>
      )}

      {/* Filters */}
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
        {/* Status filter */}
        <div className="flex gap-1 rounded-md border border-[#334155] bg-[#1e293b] p-1">
          {statusFilters.map((f) => (
            <button
              key={f.value}
              onClick={() => setFilter(f.value)}
              className={`rounded px-3 py-1.5 text-xs font-medium transition ${
                filter === f.value
                  ? 'bg-blue-600 text-white'
                  : 'text-slate-400 hover:bg-[#334155] hover:text-slate-200'
              }`}
            >
              {f.label}
            </button>
          ))}
        </div>

        {/* Search */}
        <div className="relative flex-1">
          <Search
            size={14}
            className="absolute top-1/2 left-3 -translate-y-1/2 text-slate-500"
          />
          <input
            type="text"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Filter by path..."
            className="w-full rounded-md border border-[#334155] bg-[#1e293b] py-2 pr-3 pl-9 text-sm text-slate-200 placeholder-slate-500 outline-none transition focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
          />
        </div>

        <span className="text-xs text-slate-500">
          {filtered.length} of {logs.length} entries
        </span>
      </div>

      {/* Log table */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] shadow-md">
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-[#334155] text-slate-400">
                <th className="px-5 py-3 font-medium">Time</th>
                <th className="px-5 py-3 font-medium">Method</th>
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
                    colSpan={7}
                    className="px-5 py-8 text-center text-slate-500"
                  >
                    Loading...
                  </td>
                </tr>
              )}
              {!loading && filtered.length === 0 && (
                <tr>
                  <td
                    colSpan={7}
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
