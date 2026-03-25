import { useState, useEffect, useCallback } from 'react';
import { BarChart3, RefreshCw } from 'lucide-react';
import { fetchMetrics } from '@/lib/api';
import Card from '@/components/Card';

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

  useEffect(() => {
    load();
    const id = setInterval(load, 5000);
    return () => clearInterval(id);
  }, [load]);

  // Pick a few key metrics for cards
  const keyMetrics = metrics.filter(
    (m) =>
      !m.name.includes('{') &&
      (m.name.includes('total') ||
        m.name.includes('bytes') ||
        m.name.includes('connections') ||
        m.name.includes('uptime')),
  );

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
              value={Number(m.value).toLocaleString()}
            />
          ))}
        </div>
      )}

      {/* All metrics table */}
      <div className="rounded-lg border border-border bg-card shadow-md">
        <div className="border-b border-border px-5 py-4">
          <h2 className="text-sm font-semibold text-card-foreground">
            All Metrics ({metrics.length})
          </h2>
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
              {metrics.map((m, i) => (
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
              {metrics.length === 0 && !loading && (
                <tr>
                  <td
                    colSpan={3}
                    className="px-5 py-8 text-center text-muted-foreground"
                  >
                    No metrics available
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
