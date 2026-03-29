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
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">Dashboard</h1>
          <p className="text-sm text-muted-foreground">UWAS server overview</p>
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

      {/* First-run setup wizard — shown when no domains configured */}
      {domains.length === 0 && (
        <div className="rounded-xl border-2 border-dashed border-blue-500/30 bg-blue-500/5 p-8">
          <div className="text-center mb-6">
            <div className="mx-auto mb-3 flex h-14 w-14 items-center justify-center rounded-2xl bg-blue-500/15">
              <Zap size={24} className="text-blue-400" />
            </div>
            <h2 className="text-lg font-semibold text-foreground">Welcome to UWAS</h2>
            <p className="mt-1 text-sm text-muted-foreground">Let's get your first site online in under 5 minutes.</p>
          </div>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-3 max-w-2xl mx-auto">
            <a href="/_uwas/dashboard/domains" className="flex flex-col items-center gap-2 rounded-lg border border-border bg-card p-5 hover:border-blue-500/50 transition-colors group">
              <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-emerald-500/15 group-hover:bg-emerald-500/25 transition-colors">
                <Globe size={18} className="text-emerald-400" />
              </div>
              <span className="text-sm font-medium text-foreground">1. Add Domain</span>
              <span className="text-[10px] text-muted-foreground text-center">Point your domain's DNS A record to this server, then add it here</span>
            </a>
            <a href="/_uwas/dashboard/certificates" className="flex flex-col items-center gap-2 rounded-lg border border-border bg-card p-5 hover:border-blue-500/50 transition-colors group">
              <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-purple-500/15 group-hover:bg-purple-500/25 transition-colors">
                <Lock size={18} className="text-purple-400" />
              </div>
              <span className="text-sm font-medium text-foreground">2. SSL Certificate</span>
              <span className="text-[10px] text-muted-foreground text-center">Auto-issued via Let's Encrypt when domain is added with ssl: auto</span>
            </a>
            <a href="/_uwas/dashboard/php" className="flex flex-col items-center gap-2 rounded-lg border border-border bg-card p-5 hover:border-blue-500/50 transition-colors group">
              <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-cyan-500/15 group-hover:bg-cyan-500/25 transition-colors">
                <Server size={18} className="text-cyan-400" />
              </div>
              <span className="text-sm font-medium text-foreground">3. Install PHP</span>
              <span className="text-[10px] text-muted-foreground text-center">One-click PHP install for WordPress, Laravel, and other apps</span>
            </a>
          </div>
          <div className="mt-6 text-center">
            <a href="/_uwas/dashboard/migration" className="text-xs text-blue-400 hover:text-blue-300 transition-colors">
              Migrating from cPanel? Use the cPanel Import tool →
            </a>
          </div>
        </div>
      )}

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
        <div className="rounded-lg border border-border bg-card p-4">
          <div className="flex items-center gap-2 text-sm font-semibold text-card-foreground mb-3">
            <Lock size={15} className="text-emerald-400" /> SSL Certificates
          </div>
          <div className="space-y-2">
            <div className="flex justify-between text-xs">
              <span className="text-muted-foreground">Active</span>
              <span className="text-emerald-400 font-medium">{activeCerts}</span>
            </div>
            <div className="flex justify-between text-xs">
              <span className="text-muted-foreground">Pending</span>
              <span className="text-blue-400 font-medium">{pendingCerts}</span>
            </div>
            <div className="flex justify-between text-xs">
              <span className="text-muted-foreground">Total domains</span>
              <span className="text-card-foreground font-medium">{certs.length}</span>
            </div>
          </div>
        </div>

        {/* PHP Status */}
        <div className="rounded-lg border border-border bg-card p-4">
          <div className="flex items-center gap-2 text-sm font-semibold text-card-foreground mb-3">
            <Cpu size={15} className="text-purple-400" /> PHP Engines
          </div>
          <div className="space-y-2">
            <div className="flex justify-between text-xs">
              <span className="text-muted-foreground">Detected</span>
              <span className="text-card-foreground font-medium">{php.length}</span>
            </div>
            <div className="flex justify-between text-xs">
              <span className="text-muted-foreground">Running</span>
              <span className="text-emerald-400 font-medium">{phpRunning}</span>
            </div>
            {php.length > 0 && (
              <p className="text-[10px] text-muted-foreground mt-1">
                {php.map(p => `${p.version} (${p.sapi === 'cgi-fcgi' ? 'CGI' : 'FPM'})`).join(', ')}
              </p>
            )}
            {php.length === 0 && (
              <p className="text-[10px] text-amber-400 mt-1">No PHP detected — install from PHP page</p>
            )}
          </div>
        </div>

        {/* Security */}
        <div className="rounded-lg border border-border bg-card p-4">
          <div className="flex items-center gap-2 text-sm font-semibold text-card-foreground mb-3">
            <ShieldAlert size={15} className="text-red-400" /> Security
          </div>
          {security ? (
            <div className="space-y-2">
              <div className="flex justify-between text-xs">
                <span className="text-muted-foreground">WAF blocks</span>
                <span className="text-red-400 font-medium">{security.waf_blocked}</span>
              </div>
              <div className="flex justify-between text-xs">
                <span className="text-muted-foreground">Bot blocks</span>
                <span className="text-orange-400 font-medium">{security.bot_blocked}</span>
              </div>
              <div className="flex justify-between text-xs">
                <span className="text-muted-foreground">Rate limited</span>
                <span className="text-amber-400 font-medium">{security.rate_blocked}</span>
              </div>
            </div>
          ) : (
            <p className="text-xs text-muted-foreground">Loading...</p>
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
      <div className="rounded-lg border border-border bg-card p-5 shadow-md">
        <h2 className="mb-4 text-sm font-semibold text-card-foreground">Requests Over Time</h2>
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

      {/* Resource gauges */}
      {sysInfo && (
        <div className="grid grid-cols-3 gap-3">
          {[
            { label: 'CPU Load', value: sysInfo.load_1m ? parseFloat(sysInfo.load_1m) : 0, max: sysInfo.cpus || 1, unit: '', color: 'blue' },
            { label: 'RAM Used', value: sysInfo.memory_alloc ? sysInfo.memory_alloc / (1024 * 1024 * 1024) : 0, max: sysInfo.ram_total_human ? parseFloat(sysInfo.ram_total_human) : 1, unit: 'GB', color: 'purple' },
            { label: 'Disk Used', value: sysInfo.disk_total_human && sysInfo.disk_free_human ? parseFloat(sysInfo.disk_total_human) - parseFloat(sysInfo.disk_free_human) : 0, max: sysInfo.disk_total_human ? parseFloat(sysInfo.disk_total_human) : 1, unit: 'GB', color: 'emerald' },
          ].map(g => {
            const pct = g.max > 0 ? Math.min((g.value / g.max) * 100, 100) : 0;
            return (
              <div key={g.label} className="rounded-lg border border-border bg-card p-4">
                <div className="flex items-center justify-between mb-2">
                  <span className="text-xs font-medium text-muted-foreground">{g.label}</span>
                  <span className={`text-xs font-semibold text-${g.color}-400`}>{g.value.toFixed(1)}{g.unit} / {g.max.toFixed?.(1) ?? g.max}{g.unit}</span>
                </div>
                <div className={`h-2 rounded-full bg-${g.color}-500/10 overflow-hidden`}>
                  <div className={`h-full rounded-full bg-${g.color}-500 transition-all duration-500`} style={{ width: `${pct}%` }} />
                </div>
                <div className="flex justify-end mt-1"><span className="text-[9px] text-muted-foreground">{pct.toFixed(0)}%</span></div>
              </div>
            );
          })}
        </div>
      )}

      {/* Domains table */}
      <div className="rounded-lg border border-border bg-card shadow-md">
        <div className="border-b border-border px-5 py-4">
          <h2 className="text-sm font-semibold text-card-foreground">Domains ({domains.length})</h2>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-border text-muted-foreground">
                <th className="px-5 py-3 font-medium">Host</th>
                <th className="px-5 py-3 font-medium">Type</th>
                <th className="px-5 py-3 font-medium">SSL</th>
                <th className="px-5 py-3 font-medium">Status</th>
                <th className="px-5 py-3 font-medium">IP</th>
              </tr>
            </thead>
            <tbody>
              {domains.map(d => (
                <tr key={d.host} className="border-b border-border/50 text-card-foreground transition hover:bg-accent/30">
                  <td className="px-5 py-3 font-mono text-xs">{d.host}</td>
                  <td className="px-5 py-3">
                    <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${
                      d.type === 'php' ? 'bg-purple-500/15 text-purple-400' :
                      d.type === 'proxy' ? 'bg-orange-500/15 text-orange-400' :
                      d.type === 'redirect' ? 'bg-slate-500/15 text-muted-foreground' :
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
                      if (!h) return <span className="text-xs text-muted-foreground">--</span>;
                      const isUp = h.status === 'up';
                      return (
                        <span className="flex items-center gap-1.5" title={!isUp && h.error ? h.error : undefined}>
                          <span className={`inline-block h-2 w-2 rounded-full ${isUp ? 'bg-emerald-400' : 'bg-red-400'}`} />
                          <span className={`text-xs font-medium ${isUp ? 'text-emerald-400' : 'text-red-400'}`}>
                            {h.status}
                          </span>
                          <span className="text-[10px] text-muted-foreground">{h.ms}ms</span>
                        </span>
                      );
                    })()}
                  </td>
                  <td className="px-5 py-3 font-mono text-xs text-muted-foreground">{d.ip || 'shared'}</td>
                </tr>
              ))}
              {domains.length === 0 && (
                <tr><td colSpan={5} className="px-5 py-8 text-center text-muted-foreground">No domains configured</td></tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
