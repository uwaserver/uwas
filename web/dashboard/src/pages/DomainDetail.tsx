import { useState, useEffect, useCallback } from 'react';
import { useParams, Link } from 'react-router-dom';
import {
  ExternalLink, RefreshCw, Shield, HardDrive, BarChart3, ArrowRight,
  FileText, Settings, Lock, Plug, Save, Plus, Trash2, ArrowLeft, Eye, Server,
} from 'lucide-react';
import {
  fetchDomainDetail, updateDomain, fetchDomainStats, fetchDiskUsage,
  fetchAnalytics, fetchWPSites,
  wpSecurityStatus, wpHarden, wpListUsers, wpChangePassword,
  wpUpdateCore, wpUpdatePlugins, wpFixPermissions, wpToggleDebug,
  wpErrorLog, wpOptimizeDB, wpPluginAction,
  type DomainDetail as DDType, type DomainAnalytics, type WPSite,
  type WPSecurityStatus, type WPUserInfo, type WPPlugin,
} from '@/lib/api';

type Tab = 'overview' | 'settings' | 'security' | 'routes' | 'wordpress' | 'analytics' | 'files';

export default function DomainDetail() {
  const { host } = useParams<{ host: string }>();
  const [detail, setDetail] = useState<DDType | null>(null);
  const [loading, setLoading] = useState(true);
  const [tab, setTab] = useState<Tab>('overview');
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [actionLoading, setActionLoading] = useState('');

  // Overview
  const [stats, setStats] = useState<{ requests: number; bytes_out: number; status_2xx: number; status_4xx: number; status_5xx: number } | null>(null);
  const [diskUsage, setDiskUsage] = useState<{ bytes: number; human: string } | null>(null);
  const [analytics, setAnalytics] = useState<DomainAnalytics | null>(null);

  // Security
  const [wafEnabled, setWafEnabled] = useState(false);
  const [hotlinkEnabled, setHotlinkEnabled] = useState(false);
  const [rateLimitReqs, setRateLimitReqs] = useState(0);
  const [rateLimitWindow, setRateLimitWindow] = useState('1m');
  const [blockedPaths, setBlockedPaths] = useState<string[]>([]);
  const [newBlockedPath, setNewBlockedPath] = useState('');
  const [ipBlacklist, setIpBlacklist] = useState<string[]>([]);
  const [geoBlock, setGeoBlock] = useState('');
  const [geoAllow, setGeoAllow] = useState('');
  const [cpuPercent, setCpuPercent] = useState(0);
  const [memoryMB, setMemoryMB] = useState(0);
  const [pidMax, setPidMax] = useState(0);
  const [newBlacklistIP, setNewBlacklistIP] = useState('');

  // WordPress
  const [wpSite, setWpSite] = useState<WPSite | null>(null);
  const [wpSecurity, setWpSecurity] = useState<WPSecurityStatus | null>(null);
  const [wpUsers, setWpUsers] = useState<WPUserInfo[]>([]);
  const [pwUser, setPwUser] = useState('');
  const [newPw, setNewPw] = useState('');
  const [wpResult, setWpResult] = useState('');

  const load = useCallback(async () => {
    if (!host) return;
    setLoading(true);
    try {
      const [d, statsMap, an] = await Promise.all([
        fetchDomainDetail(host),
        fetchDomainStats().catch(() => ({})),
        fetchAnalytics().catch(() => []),
      ]);
      setDetail(d);
      setStats((statsMap as Record<string, typeof stats>)[host] ?? null);
      setAnalytics((an as DomainAnalytics[])?.find(a => a.host === host) ?? null);

      // Security state
      setWafEnabled(d.security?.waf?.enabled ?? false);
      setHotlinkEnabled(d.security?.hotlink_protection?.enabled ?? false);
      setRateLimitReqs(d.security?.rate_limit?.requests ?? 0);
      setRateLimitWindow(d.security?.rate_limit?.window ?? '1m');
      setBlockedPaths(d.security?.blocked_paths ?? []);
      setIpBlacklist(d.security?.ip_blacklist ?? []);
      setGeoBlock((d.security?.geo_block_countries ?? []).join(', '));
      setGeoAllow((d.security?.geo_allow_countries ?? []).join(', '));
      setCpuPercent(d.resources?.cpu_percent ?? 0);
      setMemoryMB(d.resources?.memory_mb ?? 0);
      setPidMax(d.resources?.pid_max ?? 0);

      // Disk usage
      fetchDiskUsage(host).then(setDiskUsage).catch(() => {});

      // WordPress
      fetchWPSites().then(sites => {
        const wp = sites?.find(s => s.domain === host);
        setWpSite(wp ?? null);
        if (wp) {
          wpSecurityStatus(host).then(setWpSecurity).catch(() => {});
          wpListUsers(host).then(setWpUsers).catch(() => setWpUsers([]));
        }
      }).catch(() => {});
    } catch { /* ignore */ }
    finally { setLoading(false); }
  }, [host]);

  useEffect(() => { load(); }, [load]);

  const doAction = async (label: string, fn: () => Promise<unknown>) => {
    setActionLoading(label);
    setMsg(null);
    try {
      await fn();
      await load();
      setMsg({ ok: true, text: label + ' completed' });
    } catch (e) { setMsg({ ok: false, text: (e as Error).message }); }
    finally { setActionLoading(''); }
  };

  const saveSecurity = async () => {
    if (!host) return;
    setSaving(true);
    setMsg(null);
    try {
      const geoBlockList = geoBlock.split(',').map(s => s.trim().toUpperCase()).filter(Boolean);
      const geoAllowList = geoAllow.split(',').map(s => s.trim().toUpperCase()).filter(Boolean);
      await updateDomain(host, {
        security: {
          waf: { enabled: wafEnabled },
          rate_limit: rateLimitReqs > 0 ? { requests: rateLimitReqs, window: rateLimitWindow } : undefined,
          blocked_paths: blockedPaths.length > 0 ? blockedPaths : undefined,
          ip_blacklist: ipBlacklist.length > 0 ? ipBlacklist : undefined,
          hotlink_protection: { enabled: hotlinkEnabled },
          geo_block_countries: geoBlockList.length > 0 ? geoBlockList : undefined,
          geo_allow_countries: geoAllowList.length > 0 ? geoAllowList : undefined,
        },
        resources: (cpuPercent > 0 || memoryMB > 0 || pidMax > 0) ? {
          cpu_percent: cpuPercent || undefined,
          memory_mb: memoryMB || undefined,
          pid_max: pidMax || undefined,
        } : undefined,
      });
      setMsg({ ok: true, text: 'Security & resource settings saved' });
    } catch (e) { setMsg({ ok: false, text: (e as Error).message }); }
    finally { setSaving(false); }
  };

  const Badge = ({ ok, label }: { ok: boolean; label: string }) => (
    <span className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-medium ${ok ? 'bg-emerald-500/15 text-emerald-400' : 'bg-red-500/15 text-red-400'}`}>
      <span className={`h-1.5 w-1.5 rounded-full ${ok ? 'bg-emerald-400' : 'bg-red-400'}`} />{label}
    </span>
  );

  const Stat = ({ label, value, sub }: { label: string; value: string | number; sub?: string }) => (
    <div className="rounded-lg bg-background px-4 py-3">
      <p className="text-[10px] text-muted-foreground">{label}</p>
      <p className="text-lg font-bold text-foreground">{value}</p>
      {sub && <p className="text-[10px] text-muted-foreground">{sub}</p>}
    </div>
  );

  if (loading) return <div className="text-center py-20 text-muted-foreground">Loading domain...</div>;
  if (!detail || !host) return <div className="text-center py-20 text-muted-foreground">Domain not found</div>;

  const siteUrl = `https://${host}`;
  const tabs: { id: Tab; label: string; icon: React.ReactNode }[] = [
    { id: 'overview', label: 'Overview', icon: <Eye size={13} /> },
    { id: 'settings', label: 'Settings', icon: <Settings size={13} /> },
    { id: 'security', label: 'Security', icon: <Shield size={13} /> },
    { id: 'routes', label: 'Routes', icon: <ArrowRight size={13} /> },
    ...(wpSite ? [{ id: 'wordpress' as Tab, label: 'WordPress', icon: <Plug size={13} /> }] : []),
    { id: 'analytics', label: 'Analytics', icon: <BarChart3 size={13} /> },
    { id: 'files', label: 'Files', icon: <FileText size={13} /> },
  ];

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-start justify-between">
        <div className="flex items-center gap-4">
          <Link to="/domains" className="rounded-md p-2 text-muted-foreground hover:bg-accent hover:text-foreground"><ArrowLeft size={18} /></Link>
          <div>
            <div className="flex items-center gap-3">
              <h1 className="text-xl font-bold sm:text-2xl text-foreground">{host}</h1>
              <a href={siteUrl} target="_blank" rel="noopener" className="flex items-center gap-1 rounded-md bg-blue-600 px-2.5 py-1 text-xs font-medium text-white hover:bg-blue-700">
                Visit <ExternalLink size={10} />
              </a>
            </div>
            <div className="mt-1 flex items-center gap-2 text-sm text-muted-foreground">
              <span className="rounded bg-accent px-1.5 py-0.5 text-[10px] font-medium">{detail.type}</span>
              <span>{detail.ssl?.mode === 'auto' ? 'Auto SSL' : detail.ssl?.mode}</span>
              {detail.root && <span className="font-mono text-[10px]">{detail.root}</span>}
            </div>
          </div>
        </div>
        <button onClick={load} className="flex items-center gap-2 rounded-md border border-border bg-card px-3 py-2 text-sm text-card-foreground hover:bg-accent">
          <RefreshCw size={14} /> Refresh
        </button>
      </div>

      {/* Site link bar */}
      <div className="rounded-lg border border-border bg-card overflow-hidden">
        <div className="flex items-center gap-2 px-4 py-2.5 bg-background">
          <div className="flex gap-1.5">
            <span className="h-2.5 w-2.5 rounded-full bg-red-500/60" />
            <span className="h-2.5 w-2.5 rounded-full bg-amber-500/60" />
            <span className="h-2.5 w-2.5 rounded-full bg-emerald-500/60" />
          </div>
          <div className="flex-1 flex items-center gap-2 rounded-md bg-card px-3 py-1.5 text-xs text-muted-foreground font-mono">
            <Lock size={10} className="text-emerald-400" />{siteUrl}
          </div>
          <a href={siteUrl} target="_blank" rel="noopener" className="flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-blue-700">
            <ExternalLink size={11} /> Visit Site
          </a>
          {wpSite && (
            <a href={wpSite.admin_url} target="_blank" rel="noopener" className="flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-xs text-card-foreground hover:bg-accent">
              WP Admin
            </a>
          )}
        </div>
      </div>

      {/* Quick stats */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-cols-6">
        <Stat label="Requests" value={stats?.requests?.toLocaleString() ?? '0'} sub="total" />
        <Stat label="Bandwidth" value={formatBytes(stats?.bytes_out ?? 0)} sub="total sent" />
        <Stat label="Success" value={stats?.status_2xx?.toLocaleString() ?? '0'} sub="2xx responses" />
        <Stat label="Errors" value={((stats?.status_4xx ?? 0) + (stats?.status_5xx ?? 0)).toLocaleString()} sub="4xx + 5xx" />
        <Stat label="Disk Usage" value={diskUsage?.human ?? '—'} sub={detail.root} />
        {wpSite && <Stat label="WordPress" value={wpSite.version} sub={`${wpSite.plugins?.length ?? 0} plugins`} />}
      </div>

      {msg && <div className={`rounded-md px-4 py-2.5 text-sm ${msg.ok ? 'bg-emerald-500/10 text-emerald-400' : 'bg-red-500/10 text-red-400'}`}>{msg.text}</div>}

      {/* Tabs */}
      <div className="flex gap-1 border-b border-border">
        {tabs.map(t => (
          <button key={t.id} onClick={() => setTab(t.id)}
            className={`flex items-center gap-1.5 px-4 py-2.5 text-xs font-medium transition border-b-2 ${tab === t.id
              ? 'border-blue-500 text-blue-400' : 'border-transparent text-muted-foreground hover:text-foreground'}`}>
            {t.icon} {t.label}
          </button>
        ))}
      </div>

      {/* ═══ Overview ═══ */}
      {tab === 'overview' && (
        <div className="space-y-5">
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">
            <div className="rounded-lg bg-card border border-border px-4 py-3">
              <p className="text-[10px] text-muted-foreground">Type</p>
              <p className="text-sm font-medium text-foreground">{detail.type}</p>
            </div>
            <div className="rounded-lg bg-card border border-border px-4 py-3">
              <p className="text-[10px] text-muted-foreground">SSL</p>
              <p className="text-sm font-medium text-foreground">{detail.ssl?.mode}</p>
            </div>
            <div className="rounded-lg bg-card border border-border px-4 py-3">
              <p className="text-[10px] text-muted-foreground">Root</p>
              <p className="text-xs font-mono text-foreground truncate">{detail.root}</p>
            </div>
            {detail.php && (
              <div className="rounded-lg bg-card border border-border px-4 py-3">
                <p className="text-[10px] text-muted-foreground">PHP FPM</p>
                <p className="text-xs font-mono text-foreground truncate">{detail.php.fpm_address}</p>
              </div>
            )}
            {detail.cache?.enabled && (
              <div className="rounded-lg bg-card border border-border px-4 py-3">
                <p className="text-[10px] text-muted-foreground">Cache TTL</p>
                <p className="text-sm font-medium text-foreground">{detail.cache.ttl}s</p>
              </div>
            )}
            {detail.proxy && (
              <div className="rounded-lg bg-card border border-border px-4 py-3">
                <p className="text-[10px] text-muted-foreground">Proxy</p>
                <p className="text-xs font-mono text-foreground truncate">{detail.proxy.algorithm} &middot; {detail.proxy.upstreams?.length ?? 0} upstreams</p>
              </div>
            )}
          </div>

          {/* Status badges */}
          <div className="flex flex-wrap gap-2">
            <Badge ok={detail.security?.waf?.enabled ?? false} label={detail.security?.waf?.enabled ? 'WAF Active' : 'No WAF'} />
            <Badge ok={(detail.security?.rate_limit?.requests ?? 0) > 0} label={(detail.security?.rate_limit?.requests ?? 0) > 0 ? `Rate: ${detail.security!.rate_limit!.requests}/min` : 'No Rate Limit'} />
            <Badge ok={detail.cache?.enabled ?? false} label={detail.cache?.enabled ? 'Cache On' : 'No Cache'} />
            {wpSite && <Badge ok={true} label={`WP ${wpSite.version}`} />}
          </div>

          {/* Traffic chart placeholder */}
          {analytics && analytics.hourly_views && (
            <div className="rounded-lg border border-border bg-card p-5">
              <h3 className="text-sm font-semibold text-card-foreground mb-3">24h Traffic</h3>
              <div className="flex items-end gap-[2px] h-16">
                {analytics.hourly_views.map((v, i) => {
                  const max = Math.max(...analytics.hourly_views, 1);
                  return <div key={i} className="flex-1 bg-blue-500/60 rounded-t" style={{ height: `${(v / max) * 100}%`, minHeight: v > 0 ? 2 : 0 }} title={`${v} views`} />;
                })}
              </div>
              <div className="flex justify-between mt-1 text-[9px] text-muted-foreground">
                <span>24h ago</span><span>now</span>
              </div>
            </div>
          )}
        </div>
      )}

      {/* ═══ Settings ═══ */}
      {tab === 'settings' && (
        <div className="rounded-lg border border-border bg-card p-5">
          <p className="text-sm text-muted-foreground mb-3">
            Edit domain settings in the <Link to="/domains" className="text-blue-400 hover:underline">Domains</Link> page or use the <Link to="/config-editor" className="text-blue-400 hover:underline">Config Editor</Link>.
          </p>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            {Object.entries({
              'Type': detail.type,
              'Root': detail.root,
              'SSL Mode': detail.ssl?.mode,
              'SSL Min Version': detail.ssl?.min_version || 'default',
              'Cache': detail.cache?.enabled ? `Enabled (TTL: ${detail.cache.ttl}s)` : 'Disabled',
              'PHP FPM': detail.php?.fpm_address || '—',
              '.htaccess': detail.htaccess?.mode || 'disabled',
            }).map(([k, v]) => (
              <div key={k} className="rounded bg-background px-4 py-3">
                <p className="text-[10px] text-muted-foreground">{k}</p>
                <p className="text-sm font-mono text-foreground">{v}</p>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* ═══ Security ═══ */}
      {tab === 'security' && (
        <div className="space-y-4">
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            {/* WAF */}
            <div className="flex items-center justify-between rounded-lg bg-card border border-border px-4 py-3">
              <div>
                <p className="text-sm font-medium text-card-foreground">WAF</p>
                <p className="text-[10px] text-muted-foreground">SQL injection, XSS, shell, RCE detection</p>
              </div>
              <button onClick={() => setWafEnabled(!wafEnabled)}
                className={`relative h-6 w-11 rounded-full transition ${wafEnabled ? 'bg-emerald-500' : 'bg-slate-600'}`}>
                <span className={`absolute top-0.5 h-5 w-5 rounded-full bg-white shadow transition ${wafEnabled ? 'left-[22px]' : 'left-0.5'}`} />
              </button>
            </div>
            {/* Hotlink */}
            <div className="flex items-center justify-between rounded-lg bg-card border border-border px-4 py-3">
              <div>
                <p className="text-sm font-medium text-card-foreground">Hotlink Protection</p>
                <p className="text-[10px] text-muted-foreground">Block direct linking to images/files</p>
              </div>
              <button onClick={() => setHotlinkEnabled(!hotlinkEnabled)}
                className={`relative h-6 w-11 rounded-full transition ${hotlinkEnabled ? 'bg-emerald-500' : 'bg-slate-600'}`}>
                <span className={`absolute top-0.5 h-5 w-5 rounded-full bg-white shadow transition ${hotlinkEnabled ? 'left-[22px]' : 'left-0.5'}`} />
              </button>
            </div>
          </div>

          {/* Rate Limit */}
          <div className="rounded-lg bg-card border border-border px-4 py-3">
            <p className="text-sm font-medium text-card-foreground mb-2">Rate Limiting</p>
            <div className="flex gap-3">
              <div className="flex-1">
                <label className="text-[10px] text-muted-foreground">Requests</label>
                <input type="number" value={rateLimitReqs} onChange={e => setRateLimitReqs(parseInt(e.target.value) || 0)} min={0}
                  className="w-full rounded border border-border bg-background px-2 py-1.5 text-sm text-foreground outline-none" placeholder="0 = off" />
              </div>
              <div className="flex-1">
                <label className="text-[10px] text-muted-foreground">Window</label>
                <select value={rateLimitWindow} onChange={e => setRateLimitWindow(e.target.value)}
                  className="w-full rounded border border-border bg-background px-2 py-1.5 text-sm text-foreground outline-none">
                  <option value="10s">10s</option><option value="30s">30s</option><option value="1m">1m</option><option value="5m">5m</option>
                </select>
              </div>
            </div>
          </div>

          {/* Blocked Paths */}
          <div className="rounded-lg bg-card border border-border px-4 py-3">
            <p className="text-sm font-medium text-card-foreground mb-2">Blocked Paths</p>
            <div className="flex flex-wrap gap-1.5 mb-2">
              {blockedPaths.map((p, i) => (
                <span key={i} className="inline-flex items-center gap-1 rounded bg-red-500/10 px-2 py-0.5 text-xs text-red-400 font-mono">
                  {p} <button onClick={() => setBlockedPaths(blockedPaths.filter((_, j) => j !== i))}><Trash2 size={10} /></button>
                </span>
              ))}
            </div>
            <div className="flex gap-2">
              <input value={newBlockedPath} onChange={e => setNewBlockedPath(e.target.value)} placeholder=".env" onKeyDown={e => { if (e.key === 'Enter' && newBlockedPath.trim()) { setBlockedPaths([...blockedPaths, newBlockedPath.trim()]); setNewBlockedPath(''); } }}
                className="flex-1 rounded border border-border bg-background px-2 py-1.5 text-sm text-foreground outline-none font-mono" />
              <button onClick={() => { if (newBlockedPath.trim()) { setBlockedPaths([...blockedPaths, newBlockedPath.trim()]); setNewBlockedPath(''); } }}
                className="rounded bg-red-600 px-2 py-1.5 text-xs text-white hover:bg-red-700"><Plus size={12} /></button>
            </div>
          </div>

          {/* IP Blacklist */}
          <div className="rounded-lg bg-card border border-border px-4 py-3">
            <p className="text-sm font-medium text-card-foreground mb-2">IP Blacklist</p>
            <div className="flex flex-wrap gap-1.5 mb-2">
              {ipBlacklist.map((ip, i) => (
                <span key={i} className="inline-flex items-center gap-1 rounded bg-red-500/10 px-2 py-0.5 text-xs text-red-400 font-mono">
                  {ip} <button onClick={() => setIpBlacklist(ipBlacklist.filter((_, j) => j !== i))}><Trash2 size={10} /></button>
                </span>
              ))}
            </div>
            <div className="flex gap-2">
              <input value={newBlacklistIP} onChange={e => setNewBlacklistIP(e.target.value)} placeholder="10.0.0.0/8" onKeyDown={e => { if (e.key === 'Enter' && newBlacklistIP.trim()) { setIpBlacklist([...ipBlacklist, newBlacklistIP.trim()]); setNewBlacklistIP(''); } }}
                className="flex-1 rounded border border-border bg-background px-2 py-1.5 text-sm text-foreground outline-none font-mono" />
              <button onClick={() => { if (newBlacklistIP.trim()) { setIpBlacklist([...ipBlacklist, newBlacklistIP.trim()]); setNewBlacklistIP(''); } }}
                className="rounded bg-red-600 px-2 py-1.5 text-xs text-white hover:bg-red-700"><Plus size={12} /></button>
            </div>
          </div>

          {/* GeoIP */}
          <div>
            <p className="text-sm font-medium text-card-foreground mb-2">GeoIP Blocking</p>
            <div className="grid gap-2 sm:grid-cols-2">
              <div>
                <label className="text-[10px] text-muted-foreground">Block Countries (comma-separated ISO codes)</label>
                <input value={geoBlock} onChange={e => setGeoBlock(e.target.value)} placeholder="CN, RU, KP"
                  className="w-full rounded border border-border bg-background px-2 py-1.5 text-sm text-foreground outline-none font-mono" />
              </div>
              <div>
                <label className="text-[10px] text-muted-foreground">Allow Only (whitelist mode, leave empty for all)</label>
                <input value={geoAllow} onChange={e => setGeoAllow(e.target.value)} placeholder="US, TR, DE"
                  className="w-full rounded border border-border bg-background px-2 py-1.5 text-sm text-foreground outline-none font-mono" />
              </div>
            </div>
          </div>

          {/* Resource Limits */}
          <div>
            <p className="text-sm font-medium text-card-foreground mb-2">Resource Limits (Linux cgroups)</p>
            <div className="grid gap-2 sm:grid-cols-3">
              <div>
                <label className="text-[10px] text-muted-foreground">CPU % (0 = unlimited)</label>
                <input type="number" min={0} max={400} value={cpuPercent} onChange={e => setCpuPercent(Number(e.target.value))}
                  className="w-full rounded border border-border bg-background px-2 py-1.5 text-sm text-foreground outline-none" />
              </div>
              <div>
                <label className="text-[10px] text-muted-foreground">Memory MB (0 = unlimited)</label>
                <input type="number" min={0} value={memoryMB} onChange={e => setMemoryMB(Number(e.target.value))}
                  className="w-full rounded border border-border bg-background px-2 py-1.5 text-sm text-foreground outline-none" />
              </div>
              <div>
                <label className="text-[10px] text-muted-foreground">Max PIDs (0 = unlimited)</label>
                <input type="number" min={0} value={pidMax} onChange={e => setPidMax(Number(e.target.value))}
                  className="w-full rounded border border-border bg-background px-2 py-1.5 text-sm text-foreground outline-none" />
              </div>
            </div>
          </div>

          <button onClick={saveSecurity} disabled={saving}
            className="flex items-center gap-2 rounded-md bg-blue-600 px-5 py-2.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50">
            {saving ? <RefreshCw size={14} className="animate-spin" /> : <Save size={14} />} Save Security Settings
          </button>
        </div>
      )}

      {/* ═══ WordPress ═══ */}
      {tab === 'wordpress' && wpSite && (
        <div className="space-y-5">
          {/* Info grid */}
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
            <Stat label="WP Version" value={wpSite.version} />
            <Stat label="PHP" value={wpSecurity?.php_version || '—'} />
            <Stat label="Plugins" value={wpSite.plugins?.length ?? 0} sub={`${wpSite.health.plugin_updates} updates`} />
            <Stat label="Themes" value={wpSite.themes?.length ?? 0} />
          </div>

          {/* WP quick actions */}
          <div className="flex flex-wrap gap-2">
            <a href={wpSite.admin_url} target="_blank" rel="noopener" className="flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-blue-700">WP Admin <ExternalLink size={11} /></a>
            <button onClick={() => doAction('Update Core', () => wpUpdateCore(host))} disabled={!!actionLoading} className="flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-xs text-card-foreground hover:bg-accent disabled:opacity-50">
              {actionLoading === 'Update Core' ? <RefreshCw size={11} className="animate-spin" /> : null} Update Core
            </button>
            <button onClick={() => doAction('Update Plugins', () => wpUpdatePlugins(host))} disabled={!!actionLoading} className="flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-xs text-card-foreground hover:bg-accent disabled:opacity-50">
              {actionLoading === 'Update Plugins' ? <RefreshCw size={11} className="animate-spin" /> : null} Update Plugins
            </button>
            <button onClick={() => doAction('Fix Permissions', () => wpFixPermissions(host))} disabled={!!actionLoading} className="flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-xs text-card-foreground hover:bg-accent disabled:opacity-50">Fix Permissions</button>
            <button onClick={() => doAction('Toggle Debug', () => wpToggleDebug(host, !wpSite.health.debug))} disabled={!!actionLoading}
              className={`flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-xs disabled:opacity-50 ${wpSite.health.debug ? 'border-amber-500/30 bg-amber-500/10 text-amber-400' : 'border-border text-card-foreground hover:bg-accent'}`}>
              Debug {wpSite.health.debug ? 'ON' : 'OFF'}
            </button>
            <button onClick={() => doAction('Optimize DB', async () => { const r = await wpOptimizeDB(host); setWpResult(r.output); })} disabled={!!actionLoading} className="flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-xs text-card-foreground hover:bg-accent disabled:opacity-50">Optimize DB</button>
            <button onClick={async () => { const r = await wpErrorLog(host); setWpResult(r.log || r.message || 'No log'); }} className="flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-xs text-card-foreground hover:bg-accent">Error Log</button>
          </div>

          {wpResult && (
            <div>
              <div className="flex justify-between mb-1"><span className="text-xs text-muted-foreground">Output</span><button onClick={() => setWpResult('')} className="text-[10px] text-muted-foreground hover:text-foreground">Close</button></div>
              <pre className="max-h-40 overflow-auto rounded bg-background p-3 font-mono text-[11px] text-muted-foreground whitespace-pre-wrap">{wpResult}</pre>
            </div>
          )}

          {/* WP Security toggles */}
          {wpSecurity && (
            <div>
              <h3 className="text-sm font-semibold text-card-foreground mb-2">WordPress Security</h3>
              <div className="flex flex-wrap gap-2 mb-3">
                <Badge ok={wpSecurity.xmlrpc_disabled} label={wpSecurity.xmlrpc_disabled ? 'XML-RPC off' : 'XML-RPC on'} />
                <Badge ok={wpSecurity.file_edit_disabled} label={wpSecurity.file_edit_disabled ? 'Editor off' : 'Editor on'} />
                <Badge ok={wpSecurity.ssl_forced} label={wpSecurity.ssl_forced ? 'SSL forced' : 'SSL not forced'} />
                <Badge ok={!wpSecurity.debug_enabled} label={wpSecurity.debug_enabled ? 'DEBUG' : 'No debug'} />
                <Badge ok={wpSecurity.table_prefix !== 'wp_'} label={`Prefix: ${wpSecurity.table_prefix}`} />
              </div>
              <button onClick={() => doAction('Harden WP', async () => {
                  await wpHarden(host, { disable_xmlrpc: true, disable_file_edit: true, force_ssl_admin: true, block_dir_listing: true });
                  setWpSecurity(await wpSecurityStatus(host));
                })} disabled={!!actionLoading}
                className="flex items-center gap-2 rounded-md bg-emerald-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-emerald-700 disabled:opacity-50">
                <Lock size={11} /> Harden All
              </button>
            </div>
          )}

          {/* Plugins */}
          {(wpSite.plugins ?? []).length > 0 && (
            <div>
              <h3 className="text-sm font-semibold text-card-foreground mb-2">Plugins ({wpSite.plugins.length})</h3>
              <div className="space-y-1">
                {wpSite.plugins.map((p: WPPlugin) => (
                  <div key={p.name} className="flex items-center justify-between rounded bg-background px-3 py-2">
                    <div className="flex items-center gap-2">
                      <span className={`h-1.5 w-1.5 rounded-full ${p.status === 'active' ? 'bg-emerald-400' : 'bg-slate-500'}`} />
                      <span className="text-xs text-card-foreground">{p.name}</span>
                      <span className="text-[10px] text-muted-foreground">v{p.version}</span>
                      {p.update && p.update !== 'none' && <span className="rounded bg-amber-500/15 px-1.5 py-0.5 text-[9px] text-amber-400">{p.update}</span>}
                    </div>
                    <div className="flex gap-1">
                      {p.update && p.update !== 'none' && <button onClick={() => doAction(`Update ${p.name}`, () => wpPluginAction(host, 'update', p.name))} disabled={!!actionLoading} className="rounded px-2 py-0.5 text-[10px] text-blue-400 hover:bg-blue-500/10 disabled:opacity-50">Update</button>}
                      {p.status === 'active'
                        ? <button onClick={() => doAction(`Deactivate ${p.name}`, () => wpPluginAction(host, 'deactivate', p.name))} disabled={!!actionLoading} className="rounded px-2 py-0.5 text-[10px] text-amber-400 hover:bg-amber-500/10 disabled:opacity-50">Deactivate</button>
                        : <button onClick={() => doAction(`Activate ${p.name}`, () => wpPluginAction(host, 'activate', p.name))} disabled={!!actionLoading} className="rounded px-2 py-0.5 text-[10px] text-emerald-400 hover:bg-emerald-500/10 disabled:opacity-50">Activate</button>
                      }
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* Users + password change */}
          {wpUsers.length > 0 && (
            <div>
              <h3 className="text-sm font-semibold text-card-foreground mb-2">Users ({wpUsers.length})</h3>
              <div className="space-y-1">
                {wpUsers.map(u => (
                  <div key={u.id} className="rounded bg-background px-3 py-2">
                    <div className="flex items-center justify-between">
                      <div className="flex items-center gap-2">
                        <span className="text-xs font-medium text-card-foreground">{u.login}</span>
                        <span className="text-[10px] text-muted-foreground">{u.email}</span>
                        <span className={`rounded-full px-1.5 py-0.5 text-[9px] font-medium ${u.role === 'administrator' ? 'bg-red-500/15 text-red-400' : 'bg-blue-500/15 text-blue-400'}`}>{u.role}</span>
                      </div>
                      <button onClick={() => { setPwUser(pwUser === u.login ? '' : u.login); setNewPw(''); }} className="text-[10px] text-muted-foreground hover:text-foreground">Change Password</button>
                    </div>
                    {pwUser === u.login && (
                      <div className="flex gap-2 mt-2">
                        <input type="password" value={newPw} onChange={e => setNewPw(e.target.value)} placeholder="New password (min 8)"
                          className="flex-1 rounded border border-border bg-card px-2 py-1.5 text-sm text-foreground outline-none" />
                        <button onClick={() => doAction('Change password', async () => { await wpChangePassword(host, u.login, newPw); setPwUser(''); setNewPw(''); })}
                          disabled={!!actionLoading || newPw.length < 8} className="rounded bg-blue-600 px-3 py-1.5 text-xs text-white disabled:opacity-50">Save</button>
                      </div>
                    )}
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      )}

      {/* ═══ Analytics ═══ */}
      {tab === 'analytics' && (
        <div className="space-y-4">
          {analytics ? (
            <>
              <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                <Stat label="Page Views (24h)" value={analytics.views_last_24h?.toLocaleString() ?? 0} />
                <Stat label="Page Views (7d)" value={analytics.views_last_7d?.toLocaleString() ?? 0} />
                <Stat label="Unique IPs" value={analytics.unique_ips?.toLocaleString() ?? 0} />
                <Stat label="Bandwidth" value={formatBytes(analytics.bytes_sent ?? 0)} />
              </div>
              {/* Top paths */}
              {analytics.top_paths && Object.keys(analytics.top_paths).length > 0 && (
                <div className="rounded-lg border border-border bg-card p-4">
                  <h3 className="text-sm font-semibold text-card-foreground mb-2">Top Pages</h3>
                  <div className="space-y-1">
                    {Object.entries(analytics.top_paths).sort(([,a],[,b]) => b - a).slice(0, 10).map(([path, count]) => (
                      <div key={path} className="flex justify-between rounded bg-background px-3 py-1.5">
                        <span className="text-xs font-mono text-card-foreground truncate max-w-[70%]">{path}</span>
                        <span className="text-xs text-muted-foreground">{count.toLocaleString()}</span>
                      </div>
                    ))}
                  </div>
                </div>
              )}
              {/* Top referrers */}
              {analytics.top_referrers && Object.keys(analytics.top_referrers).length > 0 && (
                <div className="rounded-lg border border-border bg-card p-4">
                  <h3 className="text-sm font-semibold text-card-foreground mb-2">Top Referrers</h3>
                  <div className="space-y-1">
                    {Object.entries(analytics.top_referrers).sort(([,a],[,b]) => b - a).slice(0, 10).map(([ref, count]) => (
                      <div key={ref} className="flex justify-between rounded bg-background px-3 py-1.5">
                        <span className="text-xs text-card-foreground truncate max-w-[70%]">{ref}</span>
                        <span className="text-xs text-muted-foreground">{count.toLocaleString()}</span>
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </>
          ) : (
            <div className="text-center py-12 text-muted-foreground">
              <BarChart3 size={32} className="mx-auto mb-3 opacity-30" />
              <p className="text-sm">No analytics data yet</p>
            </div>
          )}
        </div>
      )}

      {/* ═══ Routes (Location Blocks) + Aliases ═══ */}
      {tab === 'routes' && (
        <RoutesEditor host={host} detail={detail} onSave={async (locs, aliases) => {
          setSaving(true);
          try {
            await updateDomain(host, { locations: locs, aliases } as any);
            setMsg({ ok: true, text: 'Routes saved. Reload applied.' });
            load();
          } catch (e) { setMsg({ ok: false, text: (e as Error).message }); }
          finally { setSaving(false); }
        }} saving={saving} />
      )}

      {/* ═══ Files ═══ */}
      {tab === 'files' && (
        <div className="rounded-lg border border-border bg-card p-5 text-center">
          <HardDrive size={32} className="mx-auto mb-3 text-muted-foreground opacity-30" />
          <p className="text-sm text-card-foreground mb-1">File Manager</p>
          <p className="text-xs text-muted-foreground mb-3">Disk: {diskUsage?.human ?? '—'} &middot; Root: {detail.root}</p>
          <Link to="/file-manager" className="inline-flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700">
            <FileText size={14} /> Open File Manager
          </Link>
        </div>
      )}
    </div>
  );
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
}

/* ═══ Routes + Aliases Editor ═══ */

interface RouteRow {
  match: string;
  type: 'proxy' | 'static' | 'redirect' | 'headers';
  target: string; // proxy_pass URL, root path, redirect URL, or cache_control
  stripPrefix: boolean;
  redirectCode: number;
}

function RoutesEditor({ host, detail, onSave, saving }: {
  host: string;
  detail: any;
  onSave: (locations: any[], aliases: string[]) => Promise<void>;
  saving: boolean;
}) {
  const [routes, setRoutes] = useState<RouteRow[]>([]);
  const [aliases, setAliases] = useState<string[]>([]);
  const [newAlias, setNewAlias] = useState('');

  // Init from detail
  useState(() => {
    const locs = (detail.locations || []) as any[];
    setRoutes(locs.map((l: any) => ({
      match: l.match || '',
      type: l.proxy_pass ? 'proxy' : l.root ? 'static' : l.redirect ? 'redirect' : 'headers',
      target: l.proxy_pass || l.root || l.redirect || l.cache_control || '',
      stripPrefix: l.strip_prefix || false,
      redirectCode: l.redirect_code || 301,
    })));
    setAliases(detail.aliases || []);
  });

  const addRoute = () => setRoutes([...routes, { match: '/', type: 'proxy', target: '', stripPrefix: false, redirectCode: 301 }]);
  const removeRoute = (i: number) => setRoutes(routes.filter((_, j) => j !== i));
  const updateRoute = (i: number, patch: Partial<RouteRow>) => {
    setRoutes(routes.map((r, j) => j === i ? { ...r, ...patch } : r));
  };

  const handleSave = () => {
    const locs = routes.filter(r => r.match && r.target).map(r => {
      const loc: any = { match: r.match };
      if (r.type === 'proxy') { loc.proxy_pass = r.target; loc.strip_prefix = r.stripPrefix; }
      else if (r.type === 'static') { loc.root = r.target; }
      else if (r.type === 'redirect') { loc.redirect = r.target; loc.redirect_code = r.redirectCode; }
      else { loc.cache_control = r.target; }
      return loc;
    });
    onSave(locs, aliases);
  };

  const typeColors: Record<string, string> = {
    proxy: 'text-orange-400 bg-orange-500/10 border-orange-500/30',
    static: 'text-green-400 bg-green-500/10 border-green-500/30',
    redirect: 'text-purple-400 bg-purple-500/10 border-purple-500/30',
    headers: 'text-blue-400 bg-blue-500/10 border-blue-500/30',
  };

  return (
    <div className="space-y-5">
      {/* Aliases */}
      <div className="rounded-lg border border-border bg-card p-5">
        <h3 className="text-sm font-semibold text-card-foreground mb-1">Domain Aliases</h3>
        <p className="text-xs text-muted-foreground mb-3">Additional hostnames that point to this domain (e.g. www.{host})</p>
        <div className="flex flex-wrap gap-2 mb-3">
          {aliases.map((a, i) => (
            <span key={i} className="flex items-center gap-1 rounded-full bg-accent px-3 py-1 text-xs text-foreground">
              {a}
              <button onClick={() => setAliases(aliases.filter((_, j) => j !== i))} className="text-muted-foreground hover:text-red-400 ml-1">
                <Trash2 size={10} />
              </button>
            </span>
          ))}
          {aliases.length === 0 && <span className="text-xs text-muted-foreground">No aliases</span>}
        </div>
        <div className="flex gap-2">
          <input value={newAlias} onChange={e => setNewAlias(e.target.value)} placeholder={`www.${host}`}
            onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); if (newAlias.trim()) { setAliases([...aliases, newAlias.trim()]); setNewAlias(''); } } }}
            className="flex-1 rounded-md border border-border bg-background px-3 py-1.5 text-xs font-mono text-foreground outline-none focus:border-blue-500/50" />
          <button type="button" onClick={() => { if (newAlias.trim()) { setAliases([...aliases, newAlias.trim()]); setNewAlias(''); } }}
            className="rounded-md bg-accent px-3 py-1.5 text-xs font-medium text-card-foreground hover:bg-accent/80">
            <Plus size={12} />
          </button>
        </div>
      </div>

      {/* Routes */}
      <div className="rounded-lg border border-border bg-card p-5">
        <div className="flex items-center justify-between mb-3">
          <div>
            <h3 className="text-sm font-semibold text-card-foreground">Sub-Path Routes</h3>
            <p className="text-xs text-muted-foreground mt-0.5">First match wins. Unmatched paths go to default handler.</p>
          </div>
          <button onClick={addRoute} className="flex items-center gap-1 rounded-md bg-blue-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-blue-700">
            <Plus size={12} /> Add Route
          </button>
        </div>

        {routes.length === 0 ? (
          <div className="rounded-lg border border-dashed border-border bg-background/50 p-6 text-center">
            <Server size={24} className="mx-auto mb-2 text-muted-foreground opacity-30" />
            <p className="text-xs text-muted-foreground">No routes. All paths use the domain's default handler.</p>
          </div>
        ) : (
          <div className="space-y-3">
            {routes.map((r, i) => (
              <div key={i} className={`rounded-lg border p-3 space-y-2 ${typeColors[r.type]}`}>
                <div className="flex items-center gap-2">
                  <span className="text-[10px] font-bold uppercase">{r.type}</span>
                  <div className="flex-1" />
                  <button onClick={() => removeRoute(i)} className="text-muted-foreground hover:text-red-400"><Trash2 size={12} /></button>
                </div>
                <div className="grid grid-cols-[1fr_100px_1fr] gap-2 items-center">
                  <input value={r.match} onChange={e => updateRoute(i, { match: e.target.value })} placeholder="/path/"
                    className="rounded-md border border-border bg-background px-2 py-1.5 text-xs font-mono text-foreground outline-none" />
                  <select value={r.type} onChange={e => updateRoute(i, { type: e.target.value as RouteRow['type'] })}
                    className="rounded-md border border-border bg-background px-2 py-1.5 text-xs text-foreground outline-none">
                    <option value="proxy">Proxy</option>
                    <option value="static">Static</option>
                    <option value="redirect">Redirect</option>
                    <option value="headers">Cache</option>
                  </select>
                  <input value={r.target} onChange={e => updateRoute(i, { target: e.target.value })}
                    placeholder={r.type === 'proxy' ? 'http://127.0.0.1:4000' : r.type === 'static' ? '/var/www/docs' : r.type === 'redirect' ? 'https://example.com/new' : 'public, max-age=3600'}
                    className="rounded-md border border-border bg-background px-2 py-1.5 text-xs font-mono text-foreground outline-none" />
                </div>
                {r.type === 'proxy' && (
                  <label className="flex items-center gap-1.5 text-[10px]">
                    <input type="checkbox" checked={r.stripPrefix} onChange={e => updateRoute(i, { stripPrefix: e.target.checked })} />
                    Strip prefix ({r.match}users → /users)
                  </label>
                )}
                {r.type === 'redirect' && (
                  <div className="flex items-center gap-2 text-[10px]">
                    Status:
                    {[301, 302, 307, 308].map(c => (
                      <button key={c} type="button" onClick={() => updateRoute(i, { redirectCode: c })}
                        className={`rounded px-1.5 py-0.5 ${r.redirectCode === c ? 'bg-purple-500/30 text-purple-300' : 'text-muted-foreground hover:text-foreground'}`}>
                        {c}
                      </button>
                    ))}
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Save */}
      <div className="flex justify-end">
        <button onClick={handleSave} disabled={saving}
          className="flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50">
          {saving ? <RefreshCw size={14} className="animate-spin" /> : <Save size={14} />}
          Save Routes & Aliases
        </button>
      </div>
    </div>
  );
}
