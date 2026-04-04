import { useState, useEffect, useCallback, useRef } from 'react';
import { Shield, ShieldAlert, Bot, Link2Off, Gauge, RefreshCw, ChevronDown, ChevronUp, Save, Plus, Trash2, Globe } from 'lucide-react';
import {
  fetchSecurityStats, fetchSecurityBlocked, fetchDomains, fetchDomainDetail, updateDomain,
  type SecurityStats, type BlockedRequest, type DomainData, type DomainDetail,
} from '@/lib/api';

function timeAgo(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  const secs = Math.floor(diff / 1000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

const reasonColors: Record<string, string> = { waf: 'bg-red-500/15 text-red-400', bot: 'bg-orange-500/15 text-orange-400', rate: 'bg-amber-500/15 text-amber-400', hotlink: 'bg-purple-500/15 text-purple-400' };
const reasonLabels: Record<string, string> = { waf: 'WAF', bot: 'Bot', rate: 'Rate Limit', hotlink: 'Hotlink' };

type Tab = 'monitor' | 'domains';

export default function Security() {
  const [tab, setTab] = useState<Tab>('monitor');
  const [stats, setStats] = useState<SecurityStats | null>(null);
  const [blocked, setBlocked] = useState<BlockedRequest[]>([]);
  const [loading, setLoading] = useState(true);
  const [domains, setDomains] = useState<DomainData[]>([]);
  const [expanded, setExpanded] = useState('');
  const expandedRef = useRef('');
  const [detail, setDetail] = useState<DomainDetail | null>(null);
  const [saving, setSaving] = useState(false);
  const [status, setStatus] = useState<{ ok: boolean; msg: string } | null>(null);
  // Editable security state
  const [wafEnabled, setWafEnabled] = useState(false);
  const [rateLimitReqs, setRateLimitReqs] = useState(0);
  const [rateLimitWindow, setRateLimitWindow] = useState('1m');
  const [blockedPaths, setBlockedPaths] = useState<string[]>([]);
  const [newBlockedPath, setNewBlockedPath] = useState('');
  const [ipWhitelist, setIpWhitelist] = useState<string[]>([]);
  const [newWhitelistIP, setNewWhitelistIP] = useState('');
  const [ipBlacklist, setIpBlacklist] = useState<string[]>([]);
  const [newBlacklistIP, setNewBlacklistIP] = useState('');
  const [hotlinkEnabled, setHotlinkEnabled] = useState(false);
  const [wafBypassPaths, setWafBypassPaths] = useState<string[]>([]);
  const [newBypassPath, setNewBypassPath] = useState('');

  const load = useCallback(() => {
    Promise.all([fetchSecurityStats(), fetchSecurityBlocked()])
      .then(([s, b]) => { setStats(s); setBlocked(b ?? []); })
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    load();
    fetchDomains().then(d => setDomains(d ?? [])).catch(() => {});
    const iv = setInterval(load, 5000);
    return () => clearInterval(iv);
  }, [load]);

  const openDomain = async (host: string) => {
    if (expanded === host) { setExpanded(''); expandedRef.current = ''; return; }
    setExpanded(host);
    expandedRef.current = host;
    setStatus(null);
    // Reset edit state immediately to prevent cross-domain data bleed
    setDetail(null);
    setWafEnabled(false);
    setRateLimitReqs(0);
    setRateLimitWindow('1m');
    setBlockedPaths([]);
    setIpWhitelist([]);
    setIpBlacklist([]);
    setHotlinkEnabled(false);
    try {
      const d = await fetchDomainDetail(host);
      // Guard: only apply if this domain is still the expanded one
      if (host !== expandedRef.current) return;
      setDetail(d);
      setWafEnabled(d.security?.waf?.enabled ?? false);
      setRateLimitReqs(d.security?.rate_limit?.requests ?? 0);
      setRateLimitWindow(d.security?.rate_limit?.window ?? '1m');
      setBlockedPaths(d.security?.blocked_paths ?? []);
      setIpWhitelist(d.security?.ip_whitelist ?? []);
      setIpBlacklist(d.security?.ip_blacklist ?? []);
      setHotlinkEnabled(d.security?.hotlink_protection?.enabled ?? false);
      setWafBypassPaths(d.security?.waf?.bypass_paths ?? []);
    } catch { setDetail(null); }
  };

  const saveSecurity = async (host: string) => {
    setSaving(true);
    setStatus(null);
    try {
      await updateDomain(host, {
        security: {
          waf: { enabled: wafEnabled, bypass_paths: wafBypassPaths.length > 0 ? wafBypassPaths : undefined },
          rate_limit: rateLimitReqs > 0 ? { requests: rateLimitReqs, window: rateLimitWindow } : undefined,
          blocked_paths: blockedPaths.length > 0 ? blockedPaths : undefined,
          ip_whitelist: ipWhitelist.length > 0 ? ipWhitelist : undefined,
          ip_blacklist: ipBlacklist.length > 0 ? ipBlacklist : undefined,
          hotlink_protection: { enabled: hotlinkEnabled },
        },
      });
      setStatus({ ok: true, msg: 'Security settings saved. Reload applied.' });
    } catch (e) {
      setStatus({ ok: false, msg: (e as Error).message });
    } finally { setSaving(false); }
  };

  const Badge = ({ ok, label }: { ok: boolean; label: string }) => (
    <span className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-medium ${ok ? 'bg-emerald-500/15 text-emerald-400' : 'bg-red-500/15 text-red-400'}`}>
      <span className={`h-1.5 w-1.5 rounded-full ${ok ? 'bg-emerald-400' : 'bg-red-400'}`} />{label}
    </span>
  );

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">Security</h1>
          <p className="mt-1 text-sm text-muted-foreground">WAF, bot protection, rate limiting, and per-domain security settings.</p>
        </div>
        <button onClick={load} className="flex items-center gap-2 rounded-md border border-border bg-card px-3 py-2 text-sm text-card-foreground hover:bg-accent">
          <RefreshCw size={14} /> Refresh
        </button>
      </div>

      {/* Tabs */}
      <div className="flex gap-1 rounded-lg bg-background p-1">
        {(['monitor', 'domains'] as Tab[]).map(t => (
          <button key={t} onClick={() => setTab(t)}
            className={`flex-1 rounded-md py-2 text-sm font-medium transition ${tab === t ? 'bg-card text-foreground shadow' : 'text-muted-foreground hover:text-card-foreground'}`}>
            {t === 'monitor' ? 'Threat Monitor' : `Per-Domain Rules (${domains.length})`}
          </button>
        ))}
      </div>

      {/* ═══ Monitor Tab ═══ */}
      {tab === 'monitor' && (<>
        {/* Stats cards */}
        {stats && (
          <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
            {([
              { label: 'Total Blocked', value: stats.total_blocked, icon: Shield, color: 'text-red-400' },
              { label: 'WAF Blocked', value: stats.waf_blocked, icon: ShieldAlert, color: 'text-red-400' },
              { label: 'Bots Blocked', value: stats.bot_blocked, icon: Bot, color: 'text-orange-400' },
              { label: 'Rate Limited', value: stats.rate_blocked, icon: Gauge, color: 'text-amber-400' },
              { label: 'Hotlinks Blocked', value: stats.hotlink_blocked, icon: Link2Off, color: 'text-purple-400' },
            ] as const).map(card => (
              <div key={card.label} className="rounded-lg border border-border bg-card p-4">
                <div className="flex items-center gap-2 text-xs text-muted-foreground mb-2"><card.icon size={14} className={card.color} />{card.label}</div>
                <p className={`text-xl font-bold sm:text-2xl ${card.color}`}>{card.value.toLocaleString()}</p>
              </div>
            ))}
          </div>
        )}

        {/* Global protections */}
        <div className="rounded-lg border border-border bg-card p-5">
          <h2 className="text-sm font-semibold text-card-foreground mb-3">Global Protections (always active)</h2>
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">
            {[
              { name: 'Bot Guard', detail: 'Blocks 25+ malicious scanners' },
              { name: 'Security Headers', detail: 'X-Frame, HSTS, nosniff, XSS' },
              { name: 'Path Traversal', detail: 'Static + FastCGI + X-Accel' },
              { name: 'Admin Rate Limit', detail: '10 fails/min = 5min IP block' },
            ].map(p => (
              <div key={p.name} className="flex items-start gap-2 rounded-md bg-background px-3 py-2.5">
                <span className="mt-0.5 h-2 w-2 shrink-0 rounded-full bg-emerald-400" />
                <div>
                  <p className="text-xs font-medium text-card-foreground">{p.name}</p>
                  <p className="text-[10px] text-muted-foreground">{p.detail}</p>
                </div>
              </div>
            ))}
          </div>
        </div>

        {/* Recent blocked requests */}
        <div>
          <h2 className="text-sm font-semibold uppercase tracking-wider text-muted-foreground mb-3">Recent Blocked Requests ({blocked.length})</h2>
          {loading ? (
            <div className="text-center text-sm text-muted-foreground py-8">Loading...</div>
          ) : blocked.length === 0 ? (
            <div className="rounded-lg border border-border bg-card px-6 py-12 text-center">
              <Shield size={40} className="mx-auto mb-3 text-emerald-400" />
              <p className="text-card-foreground font-medium">No blocked requests yet</p>
              <p className="text-sm text-muted-foreground mt-1">All traffic is clean.</p>
            </div>
          ) : (
            <div className="overflow-hidden rounded-lg border border-border">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-border bg-card/50 text-left text-xs uppercase tracking-wider text-muted-foreground">
                    <th className="px-4 py-3">Time</th><th className="px-4 py-3">IP</th><th className="px-4 py-3">Path</th><th className="px-4 py-3">Reason</th><th className="px-4 py-3">User Agent</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {blocked.slice(0, 100).map((b, i) => (
                    <tr key={i} className="bg-background hover:bg-card/50">
                      <td className="px-4 py-2.5 text-xs text-muted-foreground whitespace-nowrap">{timeAgo(b.time)}</td>
                      <td className="px-4 py-2.5 font-mono text-xs text-card-foreground">{b.ip}</td>
                      <td className="px-4 py-2.5 font-mono text-xs text-muted-foreground max-w-[200px] truncate" title={b.path}>{b.path}</td>
                      <td className="px-4 py-2.5">
                        <span className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${reasonColors[b.reason] || 'bg-slate-500/15 text-muted-foreground'}`}>
                          {reasonLabels[b.reason] || b.reason}
                        </span>
                      </td>
                      <td className="px-4 py-2.5 text-[10px] text-muted-foreground max-w-[250px] truncate" title={b.ua}>{b.ua || '-'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </>)}

      {/* ═══ Per-Domain Rules Tab ═══ */}
      {tab === 'domains' && (<>
        {domains.length === 0 ? (
          <div className="rounded-lg border border-border bg-card px-6 py-12 text-center">
            <Globe size={40} className="mx-auto mb-3 opacity-30" />
            <p className="text-card-foreground font-medium">No domains configured</p>
          </div>
        ) : (
          <div className="space-y-2">
            {domains.map(d => (
              <div key={d.host} className="rounded-lg border border-border bg-card overflow-hidden">
                {/* Domain header */}
                <div className="flex items-center justify-between px-5 py-3.5 cursor-pointer hover:bg-accent/30" onClick={() => openDomain(d.host)}>
                  <div className="flex items-center gap-3">
                    <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-blue-500/10 text-blue-400"><Shield size={14} /></div>
                    <div>
                      <p className="text-sm font-medium text-foreground">{d.host}</p>
                      <p className="text-[10px] text-muted-foreground">{d.type}</p>
                    </div>
                  </div>
                  <div className="flex items-center gap-2">
                    {expanded === d.host && detail && (<>
                      <Badge ok={detail.security?.waf?.enabled ?? false} label={detail.security?.waf?.enabled ? 'WAF' : 'No WAF'} />
                      <Badge ok={(detail.security?.rate_limit?.requests ?? 0) > 0} label={(detail.security?.rate_limit?.requests ?? 0) > 0 ? 'Rate Limit' : 'No limit'} />
                    </>)}
                    {expanded === d.host ? <ChevronUp size={16} className="text-muted-foreground" /> : <ChevronDown size={16} className="text-muted-foreground" />}
                  </div>
                </div>

                {/* Expanded domain security config */}
                {expanded === d.host && detail && (
                  <div className="border-t border-border p-5 space-y-5">
                    {status && (
                      <div className={`rounded-md px-4 py-2.5 text-sm ${status.ok ? 'bg-emerald-500/10 text-emerald-400' : 'bg-red-500/10 text-red-400'}`}>{status.msg}</div>
                    )}

                    {/* Toggle switches */}
                    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                      {/* WAF */}
                      <div className="flex items-center justify-between rounded-lg bg-background px-4 py-3">
                        <div>
                          <p className="text-sm font-medium text-card-foreground">WAF (Web Application Firewall)</p>
                          <p className="text-[10px] text-muted-foreground">SQL injection, XSS, shell, RCE detection</p>
                        </div>
                        <button onClick={() => setWafEnabled(!wafEnabled)}
                          className={`relative h-6 w-11 rounded-full transition ${wafEnabled ? 'bg-emerald-500' : 'bg-slate-600'}`}>
                          <span className={`absolute top-0.5 h-5 w-5 rounded-full bg-white shadow transition ${wafEnabled ? 'left-[22px]' : 'left-0.5'}`} />
                        </button>
                      </div>

                      {/* WAF Bypass Paths */}
                      {wafEnabled && (
                        <div className="rounded-lg bg-background px-4 py-3">
                          <div className="flex items-center justify-between mb-2">
                            <div>
                              <p className="text-sm font-medium text-card-foreground">WAF Bypass Paths</p>
                              <p className="text-[10px] text-muted-foreground">Skip WAF for these path prefixes (API webhooks, etc.)</p>
                            </div>
                          </div>
                          <div className="flex flex-wrap gap-1.5 mb-2">
                            {wafBypassPaths.map((p, i) => (
                              <span key={i} className="inline-flex items-center gap-1 rounded bg-slate-700 px-2 py-0.5 text-xs text-slate-200">
                                {p}
                                <button onClick={() => setWafBypassPaths(wafBypassPaths.filter((_, j) => j !== i))} className="text-slate-400 hover:text-red-400"><Trash2 size={10} /></button>
                              </span>
                            ))}
                          </div>
                          <div className="flex gap-2">
                            <input value={newBypassPath} onChange={e => setNewBypassPath(e.target.value)}
                              placeholder="/api/webhooks/" className="flex-1 rounded bg-slate-800 px-2 py-1 text-xs border border-border text-foreground" />
                            <button onClick={() => { if (newBypassPath) { setWafBypassPaths([...wafBypassPaths, newBypassPath]); setNewBypassPath(''); } }}
                              className="flex items-center gap-1 rounded bg-blue-600 px-2 py-1 text-xs text-white hover:bg-blue-500"><Plus size={10} /> Add</button>
                          </div>
                        </div>
                      )}

                      {/* Hotlink */}
                      <div className="flex items-center justify-between rounded-lg bg-background px-4 py-3">
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

                    {/* Rate Limiting */}
                    <div className="rounded-lg bg-background px-4 py-3">
                      <p className="text-sm font-medium text-card-foreground mb-2">Rate Limiting</p>
                      <div className="flex items-center gap-3">
                        <div className="flex-1">
                          <label className="text-[10px] text-muted-foreground">Requests</label>
                          <input type="number" value={rateLimitReqs} onChange={e => setRateLimitReqs(parseInt(e.target.value) || 0)} min={0}
                            className="w-full rounded border border-border bg-card px-2 py-1.5 text-sm text-foreground outline-none" placeholder="0 = disabled" />
                        </div>
                        <div className="flex-1">
                          <label className="text-[10px] text-muted-foreground">Window</label>
                          <select value={rateLimitWindow} onChange={e => setRateLimitWindow(e.target.value)}
                            className="w-full rounded border border-border bg-card px-2 py-1.5 text-sm text-foreground outline-none">
                            <option value="10s">10 seconds</option>
                            <option value="30s">30 seconds</option>
                            <option value="1m">1 minute</option>
                            <option value="5m">5 minutes</option>
                            <option value="15m">15 minutes</option>
                          </select>
                        </div>
                      </div>
                      <p className="text-[10px] text-muted-foreground mt-1">{rateLimitReqs > 0 ? `Max ${rateLimitReqs} requests per ${rateLimitWindow} per IP` : 'Disabled (0 = no limit)'}</p>
                    </div>

                    {/* Blocked Paths */}
                    <div className="rounded-lg bg-background px-4 py-3">
                      <p className="text-sm font-medium text-card-foreground mb-2">Blocked Paths</p>
                      <div className="flex flex-wrap gap-1.5 mb-2">
                        {blockedPaths.map((p, i) => (
                          <span key={i} className="inline-flex items-center gap-1 rounded bg-red-500/10 px-2 py-0.5 text-xs text-red-400 font-mono">
                            {p}
                            <button onClick={() => setBlockedPaths(blockedPaths.filter((_, j) => j !== i))} className="hover:text-red-300"><Trash2 size={10} /></button>
                          </span>
                        ))}
                      </div>
                      <div className="flex gap-2">
                        <input value={newBlockedPath} onChange={e => setNewBlockedPath(e.target.value)} placeholder=".env, .git, wp-config.php"
                          className="flex-1 rounded border border-border bg-card px-2 py-1.5 text-sm text-foreground outline-none font-mono" />
                        <button onClick={() => { if (newBlockedPath.trim()) { setBlockedPaths([...blockedPaths, newBlockedPath.trim()]); setNewBlockedPath(''); } }}
                          className="rounded bg-red-600 px-2 py-1.5 text-xs text-white hover:bg-red-700"><Plus size={12} /></button>
                      </div>
                    </div>

                    {/* IP Whitelist */}
                    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                      <div className="rounded-lg bg-background px-4 py-3">
                        <p className="text-sm font-medium text-card-foreground mb-2">IP Whitelist</p>
                        <div className="flex flex-wrap gap-1.5 mb-2">
                          {ipWhitelist.map((ip, i) => (
                            <span key={i} className="inline-flex items-center gap-1 rounded bg-emerald-500/10 px-2 py-0.5 text-xs text-emerald-400 font-mono">
                              {ip}
                              <button onClick={() => setIpWhitelist(ipWhitelist.filter((_, j) => j !== i))} className="hover:text-emerald-300"><Trash2 size={10} /></button>
                            </span>
                          ))}
                        </div>
                        <div className="flex gap-2">
                          <input value={newWhitelistIP} onChange={e => setNewWhitelistIP(e.target.value)} placeholder="192.168.1.0/24"
                            className="flex-1 rounded border border-border bg-card px-2 py-1.5 text-sm text-foreground outline-none font-mono" />
                          <button onClick={() => { if (newWhitelistIP.trim()) { setIpWhitelist([...ipWhitelist, newWhitelistIP.trim()]); setNewWhitelistIP(''); } }}
                            className="rounded bg-emerald-600 px-2 py-1.5 text-xs text-white hover:bg-emerald-700"><Plus size={12} /></button>
                        </div>
                      </div>

                      {/* IP Blacklist */}
                      <div className="rounded-lg bg-background px-4 py-3">
                        <p className="text-sm font-medium text-card-foreground mb-2">IP Blacklist</p>
                        <div className="flex flex-wrap gap-1.5 mb-2">
                          {ipBlacklist.map((ip, i) => (
                            <span key={i} className="inline-flex items-center gap-1 rounded bg-red-500/10 px-2 py-0.5 text-xs text-red-400 font-mono">
                              {ip}
                              <button onClick={() => setIpBlacklist(ipBlacklist.filter((_, j) => j !== i))} className="hover:text-red-300"><Trash2 size={10} /></button>
                            </span>
                          ))}
                        </div>
                        <div className="flex gap-2">
                          <input value={newBlacklistIP} onChange={e => setNewBlacklistIP(e.target.value)} placeholder="10.0.0.0/8"
                            className="flex-1 rounded border border-border bg-card px-2 py-1.5 text-sm text-foreground outline-none font-mono" />
                          <button onClick={() => { if (newBlacklistIP.trim()) { setIpBlacklist([...ipBlacklist, newBlacklistIP.trim()]); setNewBlacklistIP(''); } }}
                            className="rounded bg-red-600 px-2 py-1.5 text-xs text-white hover:bg-red-700"><Plus size={12} /></button>
                        </div>
                      </div>
                    </div>

                    {/* Save button */}
                    <button onClick={() => saveSecurity(d.host)} disabled={saving}
                      className="flex items-center gap-2 rounded-md bg-blue-600 px-5 py-2.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50">
                      {saving ? <RefreshCw size={14} className="animate-spin" /> : <Save size={14} />}
                      Save Security Settings
                    </button>
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
      </>)}
    </div>
  );
}
