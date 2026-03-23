import { useState, useEffect } from 'react';
import {
  Activity,
  Zap,
  HardDrive,
  Clock,
  CheckCircle,
  AlertTriangle,
  Gauge,
  AlertCircle,
} from 'lucide-react';
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  Legend,
} from 'recharts';
import { useStats } from '@/hooks/useStats';
import { fetchDomains, type DomainData } from '@/lib/api';
import Card from '@/components/Card';

export default function Dashboard() {
  const { stats, health, history } = useStats(3000);
  const [domains, setDomains] = useState<DomainData[]>([]);

  useEffect(() => {
    fetchDomains()
      .then(setDomains)
      .catch(() => {});
  }, []);

  const hitRate =
    stats && stats.cache_hits + stats.cache_misses > 0
      ? ((stats.cache_hits / (stats.cache_hits + stats.cache_misses)) * 100).toFixed(1)
      : '0';

  const sslBadge = (ssl: string) => {
    switch (ssl) {
      case 'auto':
        return (
          <span className="rounded-full bg-emerald-500/15 px-2 py-0.5 text-xs font-medium text-emerald-400">
            Auto
          </span>
        );
      case 'manual':
        return (
          <span className="rounded-full bg-amber-500/15 px-2 py-0.5 text-xs font-medium text-amber-400">
            Manual
          </span>
        );
      default:
        return (
          <span className="rounded-full bg-red-500/15 px-2 py-0.5 text-xs font-medium text-red-400">
            Off
          </span>
        );
    }
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">Dashboard</h1>
          <p className="text-sm text-slate-400">UWAS server overview</p>
        </div>
        {health && (
          <div
            className={`flex items-center gap-2 rounded-full px-3 py-1.5 text-sm font-medium ${
              health.status === 'ok'
                ? 'bg-emerald-500/15 text-emerald-400'
                : 'bg-amber-500/15 text-amber-400'
            }`}
          >
            {health.status === 'ok' ? (
              <CheckCircle size={14} />
            ) : (
              <AlertTriangle size={14} />
            )}
            {health.status === 'ok' ? 'Healthy' : health.status}
          </div>
        )}
      </div>

      {/* Stat cards */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <Card
          icon={<Activity size={20} />}
          label="Total Requests"
          value={stats?.requests_total.toLocaleString() ?? '--'}
        />
        <Card
          icon={<Zap size={20} />}
          label="Cache Hit Rate"
          value={`${hitRate}%`}
        />
        <Card
          icon={<HardDrive size={20} />}
          label="Active Connections"
          value={stats?.active_conns.toLocaleString() ?? '--'}
        />
        <Card
          icon={<Clock size={20} />}
          label="Uptime"
          value={stats?.uptime ?? '--'}
        />
      </div>

      {/* Latency cards */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-5">
        <Card
          icon={<Gauge size={20} />}
          label="p50 Latency"
          value={stats?.latency_p50_ms != null ? `${stats.latency_p50_ms.toFixed(1)}ms` : '--'}
        />
        <Card
          icon={<Gauge size={20} />}
          label="p95 Latency"
          value={stats?.latency_p95_ms != null ? `${stats.latency_p95_ms.toFixed(1)}ms` : '--'}
        />
        <Card
          icon={<Gauge size={20} />}
          label="p99 Latency"
          value={stats?.latency_p99_ms != null ? `${stats.latency_p99_ms.toFixed(1)}ms` : '--'}
        />
        <Card
          icon={<Gauge size={20} />}
          label="Max Latency"
          value={stats?.latency_max_ms != null ? `${stats.latency_max_ms.toFixed(1)}ms` : '--'}
        />
        <Card
          icon={<AlertCircle size={20} />}
          label="Slow Requests"
          value={stats?.slow_requests?.toLocaleString() ?? '--'}
        />
      </div>

      {/* Chart */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5 shadow-md">
        <h2 className="mb-4 text-sm font-semibold text-slate-300">
          Requests Over Time
        </h2>
        <div className="h-64">
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={history}>
              <CartesianGrid strokeDasharray="3 3" stroke="#334155" />
              <XAxis dataKey="time" stroke="#64748b" fontSize={12} />
              <YAxis stroke="#64748b" fontSize={12} />
              <YAxis yAxisId="right" orientation="right" stroke="#f59e0b" fontSize={12} />
              <Tooltip
                contentStyle={{
                  backgroundColor: '#1e293b',
                  border: '1px solid #334155',
                  borderRadius: '0.375rem',
                  color: '#e2e8f0',
                  fontSize: '0.875rem',
                }}
              />
              <Legend />
              <Line
                type="monotone"
                dataKey="requests"
                name="Requests"
                stroke="#2563eb"
                strokeWidth={2}
                dot={false}
              />
              <Line
                type="monotone"
                dataKey="cacheHits"
                name="Cache Hits"
                stroke="#10b981"
                strokeWidth={2}
                dot={false}
              />
              <Line
                type="monotone"
                dataKey="p95"
                name="p95 Latency (ms)"
                stroke="#f59e0b"
                strokeWidth={2}
                dot={false}
                yAxisId="right"
              />
            </LineChart>
          </ResponsiveContainer>
        </div>
      </div>

      {/* Domains table */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] shadow-md">
        <div className="border-b border-[#334155] px-5 py-4">
          <h2 className="text-sm font-semibold text-slate-300">
            Domains ({domains.length})
          </h2>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-[#334155] text-slate-400">
                <th className="px-5 py-3 font-medium">Host</th>
                <th className="px-5 py-3 font-medium">Type</th>
                <th className="px-5 py-3 font-medium">SSL</th>
              </tr>
            </thead>
            <tbody>
              {domains.map((d) => (
                <tr
                  key={d.host}
                  className="border-b border-[#334155]/50 text-slate-300 transition hover:bg-[#334155]/30"
                >
                  <td className="px-5 py-3 font-mono text-xs">{d.host}</td>
                  <td className="px-5 py-3 capitalize">{d.type}</td>
                  <td className="px-5 py-3">{sslBadge(d.ssl)}</td>
                </tr>
              ))}
              {domains.length === 0 && (
                <tr>
                  <td
                    colSpan={3}
                    className="px-5 py-8 text-center text-slate-500"
                  >
                    No domains configured
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
