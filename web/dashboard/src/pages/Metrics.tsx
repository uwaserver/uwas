import { useState, useCallback, useMemo } from 'react';
import { BarChart3, RefreshCw, Search, X } from 'lucide-react';
import { fetchMetrics } from '@/lib/api';
import Card from '@/components/Card';
import { usePolling } from '@/hooks/usePolling';

interface ParsedMetric {
  name: string;
  help: string;
  value: string;
}

function parsePrometheus(raw: string): ParsedMetric[] {
  const metrics: ParsedMetric[] = [];
  const helpMap = new Map<string, string>();
  const lines = raw.split('\n');

  for (const line of lines) {
    if (line.startsWith('# HELP ')) {
      const rest = line.slice(7);
      const spaceIdx = rest.indexOf(' ');
      if (spaceIdx > 0) {
        helpMap.set(rest.slice(0, spaceIdx), rest.slice(spaceIdx + 1));
      }
    }
  }

  for (const line of lines) {
    if (line.startsWith('#') || line.trim() === '') continue;
    const parts = line.split(' ');
    if (parts.length >= 2) {
      const nameRaw = parts[0];
      const value = parts[1];
      // Strip labels for display name
      const baseName = nameRaw.replace(/\{.*\}/, '');
      metrics.push({
        name: nameRaw,
        help: helpMap.get(baseName) ?? '',
        value,
      });
    }
  }

  return metrics;
}

export default function Metrics() {
  const [raw, setRaw] = useState('');
  const [metrics, setMetrics] = useState<ParsedMetric[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [filter, setFilter] = useState('');

  const load = useCallback(async () => {
    try {
      const text = await fetchMetrics();
      setRaw(text);
      setMetrics(parsePrometheus(text));
      setError('');
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);

  usePolling(load, 5000);

  // Pick a few key metrics for cards
  const keyMetrics = metrics.filter(
    (m) =>
      !m.name.includes('{') &&
      (m.name.includes('total') ||
        m.name.includes('bytes') ||
        m.name.includes('connections') ||
        m.name.includes('uptime')),
  );

  // Case-insensitive filter on metric name and help text. Memoized so the
  // 5s polling doesn't re-filter the entire table on every keystroke.
  const visibleMetrics = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) return metrics;
    return metrics.filter(m =>
      m.name.toLowerCase().includes(q) || m.help.toLowerCase().includes(q),
    );
  }, [metrics, filter]);

  // Render counter values nicely. Number("+Inf") -> Infinity which prints
  // as "Infinity" via toLocaleString — show the raw +Inf / -Inf / NaN.
  const renderValue = (v: string): string => {
    const n = Number(v);
    if (!Number.isFinite(n)) return v;
    return n.toLocaleString();
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">Metrics</h1>
          <p className="text-sm text-muted-foreground">
            Prometheus-format server metrics
          </p>
        </div>
        <button
          onClick={load}
          disabled={loading}
          className="flex items-center gap-2 rounded-md bg-card border border-border px-3 py-2 text-sm text-card-foreground transition hover:bg-accent disabled:opacity-50"
        >
          <RefreshCw size={14} className={loading ? 'animate-spin' : ''} />
          Refresh
        </button>
      </div>

      {error && (
        <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">
          {error}
        </div>
      )}

      {/* Key metrics cards */}
      {keyMetrics.length > 0 && (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
          {keyMetrics.slice(0, 8).map((m) => (
            <Card
              key={m.name}
              icon={<BarChart3 size={20} />}
              label={m.name.replace(/_/g, ' ')}
              value={renderValue(m.value)}
            />
          ))}
        </div>
      )}

      {/* All metrics table */}
      <div className="rounded-lg border border-border bg-card shadow-md">
        <div className="flex items-center justify-between gap-4 border-b border-border px-5 py-4">
          <h2 className="text-sm font-semibold text-card-foreground">
            All Metrics ({filter ? `${visibleMetrics.length} of ${metrics.length}` : metrics.length})
          </h2>
          <div className="relative w-72 max-w-full">
            <Search size={12} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-muted-foreground" />
            <input
              type="text"
              value={filter}
              onChange={e => setFilter(e.target.value)}
              placeholder="Filter by name or description…"
              className="w-full rounded-md border border-border bg-background py-1.5 pl-7 pr-7 text-xs text-foreground outline-none focus:border-blue-500"
            />
            {filter && (
              <button
                onClick={() => setFilter('')}
                className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                title="Clear filter"
              >
                <X size={12} />
              </button>
            )}
          </div>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-border text-muted-foreground">
                <th className="px-5 py-3 font-medium">Metric</th>
                <th className="px-5 py-3 font-medium">Value</th>
                <th className="hidden px-5 py-3 font-medium md:table-cell">
                  Description
                </th>
              </tr>
            </thead>
            <tbody>
              {visibleMetrics.map((m, i) => (
                <tr
                  key={`${m.name}-${i}`}
                  className="border-b border-border/50 text-card-foreground transition hover:bg-accent/30"
                >
                  <td className="px-5 py-2.5 font-mono text-xs">{m.name}</td>
                  <td className="px-5 py-2.5 font-mono text-xs text-blue-400">
                    {m.value}
                  </td>
                  <td className="hidden px-5 py-2.5 text-xs text-muted-foreground md:table-cell">
                    {m.help}
                  </td>
                </tr>
              ))}
              {visibleMetrics.length === 0 && !loading && (
                <tr>
                  <td
                    colSpan={3}
                    className="px-5 py-8 text-center text-muted-foreground"
                  >
                    {filter ? `No metrics match "${filter}"` : 'No metrics available'}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Raw output */}
      <details className="rounded-lg border border-border bg-card shadow-md">
        <summary className="cursor-pointer px-5 py-4 text-sm font-semibold text-card-foreground">
          Raw Prometheus Output
        </summary>
        <pre className="max-h-96 overflow-auto border-t border-border p-5 font-mono text-xs text-muted-foreground">
          {raw || 'No data'}
        </pre>
      </details>
    </div>
  );
}
