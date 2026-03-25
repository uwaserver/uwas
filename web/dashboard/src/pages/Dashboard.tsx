import { useState, useEffect, useRef } from 'react';
import {
  Activity, Zap, HardDrive, Clock, CheckCircle, AlertTriangle,
  Gauge, AlertCircle, Shield, Globe, Lock, Cpu, ShieldAlert,
  MemoryStick, Server,
} from 'lucide-react';
import {
  LineChart, Line, XAxis, YAxis, CartesianGrid, Tooltip,
  ResponsiveContainer, Legend,
} from 'recharts';
import { useStats } from '@/hooks/useStats';
import {
  fetchDomains, fetchCerts, fetchSecurityStats, fetchPHP,
  fetchSystem, fetchDomainHealth,
  type DomainData, type CertInfo, type SecurityStats, type PHPInstall,
  type DomainHealth, type SystemInfo,
} from '@/lib/api';
import Card from '@/components/Card';

export default function Dashboard() {
  const { stats, health, history } = useStats(3000);
  const [domains, setDomains] = useState<DomainData[]>([]);
  const [certs, setCerts] = useState<CertInfo[]>([]);
  const [security, setSecurity] = useState<SecurityStats | null>(null);
  const [php, setPhp] = useState<PHPInstall[]>([]);
  const [sysInfo, setSysInfo] = useState<SystemInfo | null>(null);
  const [domainHealth, setDomainHealth] = useState<DomainHealth[]>([]);
  const sysInterval = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => {
    fetchDomains().then(d => setDomains(d ?? [])).catch(() => {});
    fetchCerts().then(c => setCerts(c ?? [])).catch(() => {});
    fetchSecurityStats().then(setSecurity).catch(() => {});
    fetchPHP().then(p => setPhp(p ?? [])).catch(() => {});
    fetchDomainHealth().then(h => setDomainHealth(h ?? [])).catch(() => {});
  }, []);

  useEffect(() => {
    fetchSystem().then(setSysInfo).catch(() => {});
    sysInterval.current = setInterval(() => {
      fetchSystem().then(setSysInfo).catch(() => {});
    }, 10000);
    return () => { if (sysInterval.current) clearInterval(sysInterval.current); };
  }, []);

  const hitRate =
    stats && stats.cache_hits + stats.cache_misses > 0
      ? ((stats.cache_hits / (stats.cache_hits + stats.cache_misses)) * 100).toFixed(1)
      : '0';

  const activeCerts = certs.filter(c => c.status === 'active').length;
  const pendingCerts = certs.filter(c => c.status === 'pending').length;
  const phpRunning = php.filter(p => p.running).length;

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">Dashboard</h1>
          <p className="text-sm text-slate-400">UWAS server overview</p>
        </div>
        {health && (
          <div className={`flex items-center gap-2 rounded-full px-3 py-1.5 text-sm font-medium ${
            health.status === 'ok' ? 'bg-emerald-500/15 text-emerald-400' : 'bg-amber-500/15 text-amber-400'
          }`}>
            {health.status === 'ok' ? <CheckCircle size={14} /> : <AlertTriangle size={14} />}
            {health.status === 'ok' ? 'Healthy' : health.status}
            {health.uptime && <span className="text-xs opacity-70">({health.uptime})</span>}
          </div>
        )}
      </div>

      {/* Quick stats row */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
        <Card icon={<Globe size={18} />} label="Domains" value={String(domains.length)} />
        <Card icon={<Activity size={18} />} label="Requests" value={stats?.requests_total.toLocaleString() ?? '--'} />
        <Card icon={<Zap size={18} />} label="Cache Hit" value={`${hitRate}%`} />
        <Card icon={<HardDrive size={18} />} label="Connections" value={stats?.active_conns.toLocaleString() ?? '--'} />
        <Card icon={<Shield size={18} />} label="Blocked" value={security?.total_blocked.toLocaleString() ?? '0'} />
        <Card icon={<Clock size={18} />} label="Uptime" value={stats?.uptime ?? '--'} />
      </div>

      {/* System Resources */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Card icon={<Cpu size={18} />} label={sysInfo?.os_name ? 'Server' : 'CPU Cores'} value={sysInfo?.os_name || (sysInfo ? String(sysInfo.cpus) + ' cores' : '--')} sub={sysInfo?.load_1m ? `Load: ${sysInfo.load_1m} / ${sysInfo.load_5m} / ${sysInfo.load_15m}` : undefined} />
        <Card icon={<MemoryStick size={18} />} label="RAM" value={sysInfo?.ram_total_human || '--'} sub={sysInfo?.ram_available_human ? `${sysInfo.ram_available_human} available` : undefined} />
        <Card icon={<HardDrive size={18} />} label="Disk" value={sysInfo?.disk_total_human || '--'} sub={sysInfo?.disk_free_human ? `${sysInfo.disk_free_human} free` : undefined} />
        <Card icon={<Server size={18} />} label="UWAS" value={sysInfo?.version || '--'} sub={sysInfo ? `${sysInfo.uptime} uptime` : undefined} />
      </div>

      {/* Status panels */}
      <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
        {/* SSL Status */}
        <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-4">
          <div className="flex items-center gap-2 text-sm font-semibold text-slate-300 mb-3">
            <Lock size={15} className="text-emerald-400" /> SSL Certificates
          </div>
          <div className="space-y-2">
            <div className="flex justify-between text-xs">
              <span className="text-slate-400">Active</span>
              <span className="text-emerald-400 font-medium">{activeCerts}</span>
            </div>
            <div className="flex justify-between text-xs">
              <span className="text-slate-400">Pending</span>
              <span className="text-blue-400 font-medium">{pendingCerts}</span>
            </div>
            <div className="flex justify-between text-xs">
              <span className="text-slate-400">Total domains</span>
              <span className="text-slate-300 font-medium">{certs.length}</span>
            </div>
          </div>
        </div>

        {/* PHP Status */}
        <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-4">
          <div className="flex items-center gap-2 text-sm font-semibold text-slate-300 mb-3">
            <Cpu size={15} className="text-purple-400" /> PHP Engines
          </div>
          <div className="space-y-2">
            <div className="flex justify-between text-xs">
              <span className="text-slate-400">Detected</span>
              <span className="text-slate-300 font-medium">{php.length}</span>
            </div>
            <div className="flex justify-between text-xs">
              <span className="text-slate-400">Running</span>
              <span className="text-emerald-400 font-medium">{phpRunning}</span>
            </div>
            {php.length > 0 && (
              <p className="text-[10px] text-slate-500 mt-1">
                {php.map(p => `${p.version} (${p.sapi === 'cgi-fcgi' ? 'CGI' : 'FPM'})`).join(', ')}
              </p>
            )}
            {php.length === 0 && (
              <p className="text-[10px] text-amber-400 mt-1">No PHP detected — install from PHP page</p>
            )}
          </div>
        </div>

        {/* Security */}
        <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-4">
          <div className="flex items-center gap-2 text-sm font-semibold text-slate-300 mb-3">
            <ShieldAlert size={15} className="text-red-400" /> Security
          </div>
          {security ? (
            <div className="space-y-2">
              <div className="flex justify-between text-xs">
                <span className="text-slate-400">WAF blocks</span>
                <span className="text-red-400 font-medium">{security.waf_blocked}</span>
              </div>
              <div className="flex justify-between text-xs">
                <span className="text-slate-400">Bot blocks</span>
                <span className="text-orange-400 font-medium">{security.bot_blocked}</span>
              </div>
              <div className="flex justify-between text-xs">
                <span className="text-slate-400">Rate limited</span>
                <span className="text-amber-400 font-medium">{security.rate_blocked}</span>
              </div>
            </div>
          ) : (
            <p className="text-xs text-slate-500">Loading...</p>
          )}
        </div>
      </div>

      {/* Latency cards */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-5">
        <Card icon={<Gauge size={16} />} label="p50" value={stats?.latency_p50_ms != null ? `${stats.latency_p50_ms.toFixed(1)}ms` : '--'} />
        <Card icon={<Gauge size={16} />} label="p95" value={stats?.latency_p95_ms != null ? `${stats.latency_p95_ms.toFixed(1)}ms` : '--'} />
        <Card icon={<Gauge size={16} />} label="p99" value={stats?.latency_p99_ms != null ? `${stats.latency_p99_ms.toFixed(1)}ms` : '--'} />
        <Card icon={<Gauge size={16} />} label="Max" value={stats?.latency_max_ms != null ? `${stats.latency_max_ms.toFixed(1)}ms` : '--'} />
        <Card icon={<AlertCircle size={16} />} label="Slow" value={stats?.slow_requests?.toLocaleString() ?? '--'} />
      </div>

      {/* Chart */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5 shadow-md">
        <h2 className="mb-4 text-sm font-semibold text-slate-300">Requests Over Time</h2>
        <div className="h-64">
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={history}>
              <CartesianGrid strokeDasharray="3 3" stroke="#334155" />
              <XAxis dataKey="time" stroke="#64748b" fontSize={12} />
              <YAxis stroke="#64748b" fontSize={12} />
              <YAxis yAxisId="right" orientation="right" stroke="#f59e0b" fontSize={12} />
              <Tooltip contentStyle={{ backgroundColor: '#1e293b', border: '1px solid #334155', borderRadius: '0.375rem', color: '#e2e8f0', fontSize: '0.875rem' }} />
              <Legend />
              <Line type="monotone" dataKey="requests" name="Requests" stroke="#2563eb" strokeWidth={2} dot={false} />
              <Line type="monotone" dataKey="cacheHits" name="Cache Hits" stroke="#10b981" strokeWidth={2} dot={false} />
              <Line type="monotone" dataKey="p95" name="p95 Latency (ms)" stroke="#f59e0b" strokeWidth={2} dot={false} yAxisId="right" />
            </LineChart>
          </ResponsiveContainer>
        </div>
      </div>

      {/* Domains table */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] shadow-md">
        <div className="border-b border-[#334155] px-5 py-4">
          <h2 className="text-sm font-semibold text-slate-300">Domains ({domains.length})</h2>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-[#334155] text-slate-400">
                <th className="px-5 py-3 font-medium">Host</th>
                <th className="px-5 py-3 font-medium">Type</th>
                <th className="px-5 py-3 font-medium">SSL</th>
                <th className="px-5 py-3 font-medium">Status</th>
                <th className="px-5 py-3 font-medium">IP</th>
              </tr>
            </thead>
            <tbody>
              {domains.map(d => (
                <tr key={d.host} className="border-b border-[#334155]/50 text-slate-300 transition hover:bg-[#334155]/30">
                  <td className="px-5 py-3 font-mono text-xs">{d.host}</td>
                  <td className="px-5 py-3">
                    <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${
                      d.type === 'php' ? 'bg-purple-500/15 text-purple-400' :
                      d.type === 'proxy' ? 'bg-orange-500/15 text-orange-400' :
                      d.type === 'redirect' ? 'bg-slate-500/15 text-slate-400' :
                      'bg-blue-500/15 text-blue-400'
                    }`}>{d.type}</span>
                  </td>
                  <td className="px-5 py-3">
                    <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${
                      d.ssl === 'auto' ? 'bg-emerald-500/15 text-emerald-400' :
                      d.ssl === 'manual' ? 'bg-amber-500/15 text-amber-400' :
                      'bg-red-500/15 text-red-400'
                    }`}>{d.ssl}</span>
                  </td>
                  <td className="px-5 py-3">
                    {(() => {
                      const h = domainHealth.find(dh => dh.host === d.host);
                      if (!h) return <span className="text-xs text-slate-500">--</span>;
                      const isUp = h.status === 'up';
                      return (
                        <span className="flex items-center gap-1.5" title={!isUp && h.error ? h.error : undefined}>
                          <span className={`inline-block h-2 w-2 rounded-full ${isUp ? 'bg-emerald-400' : 'bg-red-400'}`} />
                          <span className={`text-xs font-medium ${isUp ? 'text-emerald-400' : 'text-red-400'}`}>
                            {h.status}
                          </span>
                          <span className="text-[10px] text-slate-500">{h.ms}ms</span>
                        </span>
                      );
                    })()}
                  </td>
                  <td className="px-5 py-3 font-mono text-xs text-slate-500">{d.ip || 'shared'}</td>
                </tr>
              ))}
              {domains.length === 0 && (
                <tr><td colSpan={5} className="px-5 py-8 text-center text-slate-500">No domains configured</td></tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
