import { useState, useEffect, useCallback } from 'react';
import {
  Database, Trash2, Tag, CheckCircle, XCircle, RefreshCw,
  HardDrive, Zap, Clock, Globe, Shield,
} from 'lucide-react';
import {
  AreaChart, Area, XAxis, YAxis, Tooltip, ResponsiveContainer,
  PieChart, Pie, Cell,
} from 'recharts';
import { useStats } from '@/hooks/useStats';
import { triggerPurge, fetchCacheStats as fetchCacheStatsAPI, type CacheStatsData } from '@/lib/api';
import Card from '@/components/Card';

function formatBytes(b: number): string {
  if (b >= 1 << 30) return `${(b / (1 << 30)).toFixed(1)} GB`;
  if (b >= 1 << 20) return `${(b / (1 << 20)).toFixed(1)} MB`;
  if (b >= 1 << 10) return `${(b / (1 << 10)).toFixed(1)} KB`;
  return `${b} B`;
}

const COLORS = ['#10b981', '#ef4444', '#f59e0b'];

export default function Cache() {
  const { history } = useStats(3000);
  const [cacheStats, setCacheStats] = useState<CacheStatsData | null>(null);
  const [tag, setTag] = useState('');
  const [purgeHost, setPurgeHost] = useState('');
  const [confirmAll, setConfirmAll] = useState(false);
  const [status, setStatus] = useState<{ ok: boolean; message: string } | null>(null);
  const [purging, setPurging] = useState(false);
  const [purgeLog, setPurgeLog] = useState<{ time: string; action: string; ok: boolean }[]>([]);

  const fetchCacheStats = useCallback(async () => {
    try {
      const data = await fetchCacheStatsAPI();
      setCacheStats(data);
    } catch { /* ignore */ }
  }, []);

  useEffect(() => {
    fetchCacheStats();
    const id = setInterval(fetchCacheStats, 5000);
    return () => clearInterval(id);
  }, [fetchCacheStats]);

  const total = (cacheStats?.hits ?? 0) + (cacheStats?.misses ?? 0);
  const hitRate = total > 0 ? ((cacheStats!.hits / total) * 100).toFixed(1) : '0.0';

  const addPurgeLog = (action: string, ok: boolean) => {
    setPurgeLog(prev => [{ time: new Date().toLocaleTimeString(), action, ok }, ...prev].slice(0, 20));
  };

  const handlePurgeTag = async () => {
    if (!tag.trim()) return;
    setPurging(true); setStatus(null);
    try {
      await triggerPurge(tag.trim());
      setStatus({ ok: true, message: `Purged tag "${tag.trim()}"` });
      addPurgeLog(`Purge tag: ${tag.trim()}`, true);
      setTag(''); fetchCacheStats();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
      addPurgeLog(`Purge tag: ${tag.trim()} — FAILED`, false);
    } finally { setPurging(false); }
  };

  const handlePurgeDomain = async () => {
    if (!purgeHost) return;
    setPurging(true); setStatus(null);
    try {
      const domainTag = `site:${purgeHost.replace(/[^a-zA-Z0-9.-]/g, '')}`;
      await triggerPurge(domainTag);
      setStatus({ ok: true, message: `Purged cache for ${purgeHost}` });
      addPurgeLog(`Purge domain: ${purgeHost}`, true);
      setPurgeHost(''); fetchCacheStats();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
      addPurgeLog(`Purge domain: ${purgeHost} — FAILED`, false);
    } finally { setPurging(false); }
  };

  const handlePurgeAll = async () => {
    setPurging(true); setStatus(null);
    try {
      await triggerPurge();
      setStatus({ ok: true, message: 'All cache entries purged' });
      addPurgeLog('Purge ALL', true); fetchCacheStats();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
      addPurgeLog('Purge ALL — FAILED', false);
    } finally { setPurging(false); setConfirmAll(false); }
  };

  const pieData = [
    { name: 'Hits', value: cacheStats?.hits ?? 0 },
    { name: 'Misses', value: cacheStats?.misses ?? 0 },
    { name: 'Stale', value: cacheStats?.stales ?? 0 },
  ].filter(d => d.value > 0);

  const chartData = history.map(h => ({ time: h.time, hits: h.cacheHits }));

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">Cache</h1>
          <p className="text-sm text-muted-foreground">
            {cacheStats?.enabled ? `${cacheStats.entries} entries, ${formatBytes(cacheStats.used_bytes)} used` : 'Cache not enabled'}
          </p>
        </div>
        <button onClick={fetchCacheStats} className="flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-xs text-card-foreground hover:bg-[#475569]">
          <RefreshCw size={12} /> Refresh
        </button>
      </div>

      {/* Stats */}
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-5">
        <Card icon={<Zap size={20} />} label="Hit Rate" value={`${hitRate}%`} />
        <Card icon={<CheckCircle size={20} />} label="Hits" value={(cacheStats?.hits ?? 0).toLocaleString()} />
        <Card icon={<XCircle size={20} />} label="Misses" value={(cacheStats?.misses ?? 0).toLocaleString()} />
        <Card icon={<Database size={20} />} label="Entries" value={(cacheStats?.entries ?? 0).toLocaleString()} />
        <Card icon={<HardDrive size={20} />} label="Memory" value={formatBytes(cacheStats?.used_bytes ?? 0)} />
      </div>

      {/* Charts */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <div className="rounded-lg border border-border bg-card p-4">
          <h3 className="mb-3 text-sm font-semibold text-card-foreground">Cache Hits Over Time</h3>
          <ResponsiveContainer width="100%" height={200}>
            <AreaChart data={chartData}>
              <XAxis dataKey="time" tick={{ fill: '#64748b', fontSize: 10 }} />
              <YAxis tick={{ fill: '#64748b', fontSize: 10 }} />
              <Tooltip contentStyle={{ backgroundColor: '#1e293b', border: '1px solid #334155', borderRadius: 8 }} labelStyle={{ color: '#94a3b8' }} />
              <Area type="monotone" dataKey="hits" stroke="#10b981" fill="#10b981" fillOpacity={0.15} />
            </AreaChart>
          </ResponsiveContainer>
        </div>
        <div className="rounded-lg border border-border bg-card p-4">
          <h3 className="mb-3 text-sm font-semibold text-card-foreground">Distribution</h3>
          {pieData.length > 0 ? (
            <ResponsiveContainer width="100%" height={200}>
              <PieChart><Pie data={pieData} cx="50%" cy="50%" innerRadius={50} outerRadius={80} paddingAngle={3} dataKey="value">
                {pieData.map((_, i) => <Cell key={i} fill={COLORS[i % COLORS.length]} />)}
              </Pie><Tooltip contentStyle={{ backgroundColor: '#1e293b', border: '1px solid #334155', borderRadius: 8 }} /></PieChart>
            </ResponsiveContainer>
          ) : (
            <div className="flex h-[200px] items-center justify-center text-sm text-muted-foreground">No cache data yet</div>
          )}
          <div className="mt-2 flex justify-center gap-4 text-xs">
            <span className="flex items-center gap-1"><span className="inline-block h-2.5 w-2.5 rounded-full bg-emerald-500" /> Hits</span>
            <span className="flex items-center gap-1"><span className="inline-block h-2.5 w-2.5 rounded-full bg-red-500" /> Misses</span>
            <span className="flex items-center gap-1"><span className="inline-block h-2.5 w-2.5 rounded-full bg-amber-500" /> Stale</span>
          </div>
        </div>
      </div>

      {/* Per-domain */}
      {cacheStats?.domains && cacheStats.domains.length > 0 && (
        <div className="rounded-lg border border-border bg-card p-4">
          <h3 className="mb-3 flex items-center gap-2 text-sm font-semibold text-card-foreground"><Globe size={14} /> Per-Domain Cache</h3>
          <div className="overflow-x-auto">
            <table className="w-full text-left text-sm">
              <thead><tr className="border-b border-border text-xs text-muted-foreground">
                <th className="pb-2 pr-4">Domain</th><th className="pb-2 pr-4">Status</th><th className="pb-2 pr-4">TTL</th><th className="pb-2 pr-4">Tags</th><th className="pb-2 pr-4">Rules</th><th className="pb-2">Actions</th>
              </tr></thead>
              <tbody>{(cacheStats.domains ?? []).map(d => (
                <tr key={d.host} className="border-b border-border/50 hover:bg-background/30">
                  <td className="py-2.5 pr-4 font-medium text-foreground">{d.host}</td>
                  <td className="py-2.5 pr-4">
                    <span className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs ${d.enabled ? 'bg-emerald-500/15 text-emerald-400' : 'bg-slate-500/15 text-muted-foreground'}`}>
                      {d.enabled ? <CheckCircle size={10} /> : <XCircle size={10} />}{d.enabled ? 'On' : 'Off'}
                    </span>
                  </td>
                  <td className="py-2.5 pr-4 text-muted-foreground">{d.ttl > 0 ? `${d.ttl}s` : '—'}</td>
                  <td className="py-2.5 pr-4">{d.tags?.map(t => <span key={t} className="mr-1 rounded bg-blue-500/15 px-1.5 py-0.5 text-xs text-blue-400">{t}</span>) || <span className="text-muted-foreground">—</span>}</td>
                  <td className="py-2.5 pr-4 text-muted-foreground">{d.rules ? (
                    <span title={d.rules.map(r => `${r.match} ${r.bypass ? '(bypass)' : `TTL:${r.ttl}s`}`).join('\n')}>{d.rules.length} rules</span>
                  ) : '—'}</td>
                  <td className="py-2.5">{d.enabled && d.tags && d.tags.length > 0 && (
                    <button onClick={() => { setTag(d.tags![0]); }} className="rounded bg-red-500/10 px-2 py-1 text-xs text-red-400 hover:bg-red-500/20">Purge</button>
                  )}</td>
                </tr>
              ))}</tbody>
            </table>
          </div>
        </div>
      )}

      {/* Purge controls */}
      <div className="rounded-lg border border-border bg-card p-5">
        <h2 className="mb-4 flex items-center gap-2 text-sm font-semibold text-card-foreground"><Shield size={14} /> Purge Controls</h2>
        {status && (
          <div className={`mb-4 flex items-center gap-2 rounded-md px-3 py-2 text-sm ${status.ok ? 'bg-emerald-500/10 text-emerald-400' : 'bg-red-500/10 text-red-400'}`}>
            {status.ok ? <CheckCircle size={14} /> : <XCircle size={14} />}{status.message}
          </div>
        )}
        <div className="mb-4">
          <label className="mb-1.5 block text-xs text-muted-foreground">Purge by Cache Tag</label>
          <div className="flex gap-3">
            <div className="relative flex-1">
              <Tag size={14} className="absolute top-1/2 left-3 -translate-y-1/2 text-muted-foreground" />
              <input type="text" value={tag} onChange={e => setTag(e.target.value)} placeholder='e.g., site:blog'
                className="w-full rounded-md border border-border bg-background py-2 pr-3 pl-9 text-sm text-foreground placeholder-slate-500 outline-none focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
                onKeyDown={e => e.key === 'Enter' && handlePurgeTag()} />
            </div>
            <button onClick={handlePurgeTag} disabled={purging || !tag.trim()} className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50">Purge Tag</button>
          </div>
        </div>
        <div className="mb-4">
          <label className="mb-1.5 block text-xs text-muted-foreground">Purge by Domain</label>
          <div className="flex gap-3">
            <select value={purgeHost} onChange={e => setPurgeHost(e.target.value)} className="flex-1 rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground outline-none focus:border-blue-500">
              <option value="">Select domain...</option>
              {cacheStats?.domains?.filter(d => d.enabled).map(d => <option key={d.host} value={d.host}>{d.host}</option>)}
            </select>
            <button onClick={handlePurgeDomain} disabled={purging || !purgeHost} className="rounded-md bg-amber-600 px-4 py-2 text-sm font-medium text-white hover:bg-amber-700 disabled:cursor-not-allowed disabled:opacity-50">Purge Domain</button>
          </div>
        </div>
        <div className="border-t border-border pt-4">
          {!confirmAll ? (
            <button onClick={() => setConfirmAll(true)} className="flex items-center gap-2 rounded-md bg-red-600/15 px-4 py-2 text-sm font-medium text-red-400 hover:bg-red-600/25">
              <Trash2 size={14} /> Purge All Cache
            </button>
          ) : (
            <div className="flex items-center gap-3">
              <span className="text-sm text-muted-foreground">Clear all {cacheStats?.entries ?? 0} entries?</span>
              <button onClick={handlePurgeAll} disabled={purging} className="rounded-md bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700 disabled:opacity-50">{purging ? 'Purging...' : 'Yes, Purge All'}</button>
              <button onClick={() => setConfirmAll(false)} className="rounded-md bg-accent px-4 py-2 text-sm text-card-foreground hover:bg-[#475569]">Cancel</button>
            </div>
          )}
        </div>
      </div>

      {/* Purge history */}
      {purgeLog.length > 0 && (
        <div className="rounded-lg border border-border bg-card p-4">
          <h3 className="mb-3 flex items-center gap-2 text-sm font-semibold text-card-foreground"><Clock size={14} /> Purge History</h3>
          <div className="space-y-1.5">
            {purgeLog.map((entry, i) => (
              <div key={i} className="flex items-center gap-3 text-xs">
                <span className="font-mono text-muted-foreground">{entry.time}</span>
                {entry.ok ? <CheckCircle size={12} className="text-emerald-400" /> : <XCircle size={12} className="text-red-400" />}
                <span className={entry.ok ? 'text-card-foreground' : 'text-red-400'}>{entry.action}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Redis L3 Cache */}
      <div className="rounded-lg border border-border bg-card p-4">
        <h3 className="mb-4 flex items-center gap-2 text-base font-semibold text-card-foreground">
          <Database className="h-5 w-5 text-purple-500" />
          Redis L3 Cache
        </h3>
        <p className="text-sm text-muted-foreground mb-4">
          Configure Redis as an L3 cache layer for distributed caching across multiple UWAS instances.
        </p>
        <div className="space-y-4">
          <div className="flex items-center gap-4">
            <label className="flex items-center gap-2">
              <input
                type="checkbox"
                className="rounded border-border"
                defaultChecked={false}
              />
              <span className="text-sm text-foreground">Enable Redis Cache</span>
            </label>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-foreground mb-1">Redis Address</label>
              <input
                type="text"
                defaultValue="localhost:6379"
                className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                placeholder="localhost:6379"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-foreground mb-1">Database (DB)</label>
              <input
                type="number"
                defaultValue={0}
                min={0}
                max={15}
                className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
              />
            </div>
          </div>
          <div>
            <label className="block text-sm font-medium text-foreground mb-1">Password (optional)</label>
            <input
              type="password"
              className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
              placeholder="Redis password"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-foreground mb-1">Key Prefix</label>
            <input
              type="text"
              defaultValue="uwas"
              className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
              placeholder="uwas"
            />
            <p className="text-xs text-muted-foreground mt-1">Prefix for all cache keys in Redis</p>
          </div>
          <div className="flex gap-2">
            <button
              className="inline-flex items-center gap-2 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground transition hover:bg-primary/90 disabled:opacity-50"
            >
              <RefreshCw className="h-4 w-4" />
              Test Connection
            </button>
            <button
              className="inline-flex items-center gap-2 rounded-md border border-border bg-card px-4 py-2 text-sm font-medium text-card-foreground transition hover:bg-accent disabled:opacity-50"
            >
              Save Configuration
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
