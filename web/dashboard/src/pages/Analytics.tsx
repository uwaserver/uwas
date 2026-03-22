import { useState, useEffect, useCallback } from 'react';
import {
  Eye, Users, HardDrive, ChevronDown, ChevronRight, RefreshCw,
} from 'lucide-react';
import {
  PieChart, Pie, Cell, Tooltip, ResponsiveContainer,
  BarChart, Bar, XAxis, YAxis,
} from 'recharts';
import { fetchAnalytics, type DomainAnalytics } from '@/lib/api';
import Card from '@/components/Card';

function formatBytes(b: number): string {
  if (b >= 1 << 30) return `${(b / (1 << 30)).toFixed(1)} GB`;
  if (b >= 1 << 20) return `${(b / (1 << 20)).toFixed(1)} MB`;
  if (b >= 1 << 10) return `${(b / (1 << 10)).toFixed(1)} KB`;
  return `${b} B`;
}

const PIE_COLORS = ['#10b981', '#3b82f6', '#f59e0b', '#ef4444', '#8b5cf6'];
const tooltipStyle = { backgroundColor: '#1e293b', border: '1px solid #334155', borderRadius: 8 };

function DomainRow({ d }: { d: DomainAnalytics }) {
  const [open, setOpen] = useState(false);

  const topPaths = Object.entries(d.top_paths || {})
    .map(([path, views]) => ({ path, views }))
    .sort((a, b) => b.views - a.views)
    .slice(0, 10);

  const statusData = Object.entries(d.status_codes || {})
    .map(([code, count]) => ({ code, count }));

  const hourlyData = (d.hourly_views || []).map((v, i) => ({ hour: `${i}h`, views: v }));

  const referrers = Object.entries(d.top_referrers || {})
    .map(([domain, count]) => ({ domain, count }))
    .sort((a, b) => b.count - a.count)
    .slice(0, 10);

  const uaData = Object.entries(d.user_agents || {})
    .map(([name, count]) => ({ name, count }))
    .sort((a, b) => b.count - a.count);

  return (
    <>
      <tr className="cursor-pointer border-b border-[#334155]/50 text-slate-300 hover:bg-[#334155]/30" onClick={() => setOpen(!open)}>
        <td className="px-5 py-3">
          <span className="mr-2 inline-block text-slate-500">{open ? <ChevronDown size={14} /> : <ChevronRight size={14} />}</span>
          <span className="font-mono text-xs">{d.host}</span>
        </td>
        <td className="px-5 py-3 text-right">{(d.page_views ?? 0).toLocaleString()}</td>
        <td className="px-5 py-3 text-right">{(d.unique_ips ?? 0).toLocaleString()}</td>
        <td className="px-5 py-3 text-right">{formatBytes(d.bytes_sent ?? 0)}</td>
        <td className="px-5 py-3 text-right">{(d.views_last_hour ?? 0).toLocaleString()}</td>
      </tr>
      {open && (
        <tr className="border-b border-[#334155]/50 bg-[#0f172a]/40">
          <td colSpan={5} className="px-5 py-4">
            {/* Row 1: Paths, Status Codes, Hourly Traffic */}
            <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
              <div>
                <h4 className="mb-2 text-xs font-semibold text-slate-400">Top Paths</h4>
                <div className="space-y-1">
                  {topPaths.map(p => (
                    <div key={p.path} className="flex items-center justify-between text-xs">
                      <span className="truncate font-mono text-slate-300">{p.path}</span>
                      <span className="ml-2 text-slate-500">{p.views}</span>
                    </div>
                  ))}
                  {topPaths.length === 0 && <span className="text-xs text-slate-500">No data</span>}
                </div>
              </div>
              <div>
                <h4 className="mb-2 text-xs font-semibold text-slate-400">Status Codes</h4>
                {statusData.length > 0 ? (
                  <ResponsiveContainer width="100%" height={140}>
                    <PieChart>
                      <Pie data={statusData} cx="50%" cy="50%" innerRadius={30} outerRadius={55} paddingAngle={3} dataKey="count" nameKey="code">
                        {statusData.map((_, i) => <Cell key={i} fill={PIE_COLORS[i % PIE_COLORS.length]} />)}
                      </Pie>
                      <Tooltip contentStyle={tooltipStyle} />
                    </PieChart>
                  </ResponsiveContainer>
                ) : <div className="flex h-[140px] items-center justify-center text-xs text-slate-500">No data</div>}
              </div>
              <div>
                <h4 className="mb-2 text-xs font-semibold text-slate-400">Hourly Traffic (24h)</h4>
                <ResponsiveContainer width="100%" height={140}>
                  <BarChart data={hourlyData}>
                    <XAxis dataKey="hour" tick={{ fill: '#64748b', fontSize: 9 }} interval={3} />
                    <YAxis tick={{ fill: '#64748b', fontSize: 9 }} width={30} />
                    <Tooltip contentStyle={tooltipStyle} />
                    <Bar dataKey="views" fill="#3b82f6" radius={[2, 2, 0, 0]} />
                  </BarChart>
                </ResponsiveContainer>
              </div>
            </div>
            {/* Row 2: Referrers and User Agents */}
            <div className="mt-4 grid grid-cols-1 gap-6 lg:grid-cols-2">
              <div>
                <h4 className="mb-2 text-xs font-semibold text-slate-400">Top Referrers</h4>
                <div className="space-y-1">
                  {referrers.map(r => (
                    <div key={r.domain} className="flex items-center justify-between text-xs">
                      <span className="truncate font-mono text-slate-300">{r.domain}</span>
                      <span className="ml-2 text-slate-500">{r.count}</span>
                    </div>
                  ))}
                  {referrers.length === 0 && <span className="text-xs text-slate-500">No referrer data</span>}
                </div>
              </div>
              <div>
                <h4 className="mb-2 text-xs font-semibold text-slate-400">Browsers / User Agents</h4>
                {uaData.length > 0 ? (
                  <ResponsiveContainer width="100%" height={140}>
                    <BarChart data={uaData} layout="vertical">
                      <XAxis type="number" tick={{ fill: '#64748b', fontSize: 9 }} />
                      <YAxis type="category" dataKey="name" tick={{ fill: '#94a3b8', fontSize: 9 }} width={60} />
                      <Tooltip contentStyle={tooltipStyle} />
                      <Bar dataKey="count" fill="#8b5cf6" radius={[0, 2, 2, 0]} />
                    </BarChart>
                  </ResponsiveContainer>
                ) : <span className="text-xs text-slate-500">No user agent data</span>}
              </div>
            </div>
          </td>
        </tr>
      )}
    </>
  );
}

export default function Analytics() {
  const [domains, setDomains] = useState<DomainAnalytics[]>([]);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    try {
      const result = await fetchAnalytics();
      setDomains(result || []);
      setError('');
    } catch (e) { setError((e as Error).message); }
  }, []);

  useEffect(() => { load(); const id = setInterval(load, 5000); return () => clearInterval(id); }, [load]);

  const totalViews = domains.reduce((s, d) => s + (d.page_views ?? 0), 0);
  const totalUniqueIPs = domains.reduce((s, d) => s + (d.unique_ips ?? 0), 0);
  const totalBandwidth = domains.reduce((s, d) => s + (d.bytes_sent ?? 0), 0);
  const totalLastHour = domains.reduce((s, d) => s + (d.views_last_hour ?? 0), 0);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">Analytics</h1>
          <p className="text-sm text-slate-400">Real-time traffic analytics ({domains.length} domains)</p>
        </div>
        <button onClick={load} className="flex items-center gap-1.5 rounded-md bg-[#334155] px-3 py-1.5 text-xs text-slate-300 hover:bg-[#475569]">
          <RefreshCw size={12} /> Refresh
        </button>
      </div>

      {error && <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">{error}</div>}

      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <Card icon={<Eye size={20} />} label="Total Page Views" value={totalViews.toLocaleString()} />
        <Card icon={<Users size={20} />} label="Unique Visitors" value={totalUniqueIPs.toLocaleString()} />
        <Card icon={<HardDrive size={20} />} label="Bandwidth" value={formatBytes(totalBandwidth)} />
        <Card icon={<Eye size={20} />} label="Last Hour" value={totalLastHour.toLocaleString()} />
      </div>

      <div className="rounded-lg border border-[#334155] bg-[#1e293b] shadow-md">
        <div className="border-b border-[#334155] px-5 py-4">
          <h2 className="text-sm font-semibold text-slate-300">Per-Domain Statistics ({domains.length})</h2>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead><tr className="border-b border-[#334155] text-slate-400">
              <th className="px-5 py-3">Host</th>
              <th className="px-5 py-3 text-right">Page Views</th>
              <th className="px-5 py-3 text-right">Unique IPs</th>
              <th className="px-5 py-3 text-right">Bandwidth</th>
              <th className="px-5 py-3 text-right">Last Hour</th>
            </tr></thead>
            <tbody>
              {domains.map(d => <DomainRow key={d.host} d={d} />)}
              {domains.length === 0 && (
                <tr><td colSpan={5} className="px-5 py-8 text-center text-slate-500">No analytics data yet</td></tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
