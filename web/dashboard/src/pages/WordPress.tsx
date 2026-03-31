import { useState, useEffect, useCallback, useRef } from 'react';
import { Zap, RefreshCw, Check, Copy, ExternalLink, Shield, Download, Plug, Palette, ChevronDown, ChevronUp, Bug, FileText, Users, Key, Lock, Database, Trash2 } from 'lucide-react';
import {
  fetchDomains, installWordPress, fetchWPInstallStatus, fetchDBStatus, fetchDockerDBs,
  type DockerDBContainer,
  fetchWPSites, fetchWPSiteDetail, wpUpdateCore, wpUpdatePlugins, wpPluginAction, wpFixPermissions,
  wpToggleDebug, wpErrorLog, wpListUsers, wpChangePassword, wpSecurityStatus,
  wpHarden, wpOptimizeDB,
  type DomainData, type WPInstallStatus, type WPSite, type WPPlugin,
  type WPUserInfo, type WPSecurityStatus,
} from '@/lib/api';

type Tab = 'sites' | 'install';

export default function WordPress() {
  const [tab, setTab] = useState<Tab>('sites');
  const [domains, setDomains] = useState<DomainData[]>([]);
  const [sites, setSites] = useState<WPSite[]>([]);
  const [loadingSites, setLoadingSites] = useState(true);
  const [selectedDomain, setSelectedDomain] = useState('');
  const [dbHost, setDbHost] = useState('localhost');
  const [installing, setInstalling] = useState(false);
  const [status, setStatus] = useState<WPInstallStatus | null>(null);
  const [error, setError] = useState('');
  const [mysqlOk, setMysqlOk] = useState(false);
  const [dockerDBs, setDockerDBs] = useState<DockerDBContainer[]>([]);
  const [copied, setCopied] = useState('');
  const [expandedSite, setExpandedSite] = useState('');
  const [actionLoading, setActionLoading] = useState('');
  const [actionResult, setActionResult] = useState('');
  // New: users, security, password
  const [siteUsers, setSiteUsers] = useState<WPUserInfo[]>([]);
  const [siteUsersError, setSiteUsersError] = useState('');
  const [security, setSecurity] = useState<WPSecurityStatus | null>(null);
  const [showPasswordForm, setShowPasswordForm] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [siteTab, setSiteTab] = useState<'overview' | 'security' | 'users' | 'optimize'>('overview');
  const activeSiteRef = useRef('');

  const loadSites = useCallback(async () => {
    try {
      const s = await fetchWPSites();
      setSites(s ?? []);
      if ((s ?? []).length > 0) setTab('sites');
      else setTab('install');
    } catch { setSites([]); }
    finally { setLoadingSites(false); }
  }, []);

  useEffect(() => {
    loadSites().then(() => {
      // After sites loaded, load domains for install tab
      fetchDomains().then(d => {
        const list = d ?? [];
        setDomains(list);
        // Select first PHP domain that doesn't have WordPress installed
        setSites(prev => {
          const wpHosts = new Set(prev.map(s => s.domain));
          const available = list.filter(dd => dd.type === 'php' && !wpHosts.has(dd.host));
          if (available.length > 0) setSelectedDomain(available[0].host);
          return prev;
        });
      }).catch(() => {});
    });
    fetchDBStatus().then(s => setMysqlOk(s?.installed && s?.running)).catch(() => {});
    fetchDockerDBs().then(r => setDockerDBs((r?.containers ?? []).filter(c => c.running))).catch(() => {});
  }, [loadSites]);

  const phpDomains = domains.filter(d => d.type === 'php');
  const wpHosts = new Set(sites.map(s => s.domain));
  const installableDomains = phpDomains.filter(d => !wpHosts.has(d.host));

  const handleInstall = async () => {
    if (!selectedDomain) return;
    setInstalling(true);
    setError('');
    setStatus(null);
    try {
      await installWordPress(selectedDomain, dbHost);
      const poll = setInterval(async () => {
        try {
          const st = await fetchWPInstallStatus();
          setStatus(st);
          if (st.status !== 'running') {
            clearInterval(poll);
            setInstalling(false);
            if (st.status === 'done') loadSites();
          }
        } catch { clearInterval(poll); setInstalling(false); }
      }, 2000);
    } catch (e) {
      setError((e as Error).message);
      setInstalling(false);
    }
  };

  const doAction = async (label: string, fn: () => Promise<unknown>) => {
    setActionLoading(label);
    try { await fn(); await loadSites(); }
    catch (e) { setError((e as Error).message); }
    finally { setActionLoading(''); }
  };

  const copy = (text: string, label: string) => {
    navigator.clipboard.writeText(text);
    setCopied(label);
    setTimeout(() => setCopied(''), 2000);
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
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">WordPress</h1>
          <p className="mt-1 text-sm text-muted-foreground">Manage WordPress installations across your domains</p>
        </div>
        <button onClick={loadSites} disabled={loadingSites} className="flex items-center gap-2 rounded-md border border-border bg-card px-3 py-2 text-sm text-card-foreground hover:bg-accent disabled:opacity-50">
          <RefreshCw size={14} className={loadingSites ? 'animate-spin' : ''} />Refresh
        </button>
      </div>

      {/* Tabs */}
      <div className="flex gap-1 rounded-lg bg-background p-1">
        {(['sites', 'install'] as Tab[]).map(t => (
          <button key={t} onClick={() => setTab(t)}
            className={`flex-1 rounded-md py-2 text-sm font-medium transition ${tab === t ? 'bg-card text-foreground shadow' : 'text-muted-foreground hover:text-card-foreground'}`}>
            {t === 'sites' ? `Sites (${sites.length})` : 'Install New'}
          </button>
        ))}
      </div>

      {error && <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">{error}</div>}

      {/* ═══ Sites Tab ═══ */}
      {tab === 'sites' && (
        <>
          {loadingSites && <p className="text-sm text-muted-foreground text-center py-8">Scanning domains for WordPress...</p>}
          {!loadingSites && sites.length === 0 && (
            <div className="text-center py-12 text-muted-foreground">
              <Zap size={32} className="mx-auto mb-3 opacity-30" />
              <p className="text-sm">No WordPress sites found</p>
              <p className="text-xs mt-1">Install WordPress on a PHP domain using the Install tab</p>
            </div>
          )}
          {sites.map(site => (
            <div key={site.domain} className="rounded-lg border border-border bg-card overflow-hidden">
              {/* Site header */}
              <div className="flex items-center justify-between px-5 py-4 cursor-pointer hover:bg-accent/30"
                onClick={() => {
                  const next = expandedSite === site.domain ? '' : site.domain;
                  activeSiteRef.current = next;
                  setExpandedSite(next);
                  setSiteTab('overview');
                  setShowPasswordForm('');
                  setNewPassword('');
                  setSiteUsers([]);
                  setSiteUsersError('');
                  setSecurity(null);
                  if (next) {
                    // Lazy load: enrich with wp-cli detail (plugins/themes with update info)
                    fetchWPSiteDetail(next).then(enriched => {
                      setSites(prev => prev.map(s => s.domain === next ? enriched : s));
                    }).catch(() => {});
                    wpSecurityStatus(next).then((sec) => {
                      if (activeSiteRef.current === next) {
                        setSecurity(sec);
                      }
                    }).catch(() => {
                      if (activeSiteRef.current === next) {
                        setSecurity(null);
                      }
                    });
                    wpListUsers(next)
                      .then(users => {
                        if (activeSiteRef.current === next) {
                          setSiteUsers(users);
                          setSiteUsersError('');
                        }
                      })
                      .catch((e) => {
                        if (activeSiteRef.current === next) {
                          const msg = (e as Error).message || 'Failed to load WordPress users';
                          setSiteUsers([]);
                          setSiteUsersError(msg);
                        }
                      });
                  }
                }}>
                <div className="flex items-center gap-3">
                  <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-blue-500/10 text-blue-400 font-bold text-sm">WP</div>
                  <div>
                    <p className="text-sm font-medium text-foreground">{site.domain}</p>
                    <p className="text-xs text-muted-foreground">WordPress {site.version} &middot; DB: {site.db_name}</p>
                  </div>
                </div>
                <div className="flex items-center gap-2">
                  {site.health.plugin_updates > 0 && <Badge ok={false} label={`${site.health.plugin_updates} updates`} />}
                  {site.health.core_update && <Badge ok={false} label="Core update" />}
                  <Badge ok={site.health.ssl} label={site.health.ssl ? 'SSL' : 'No SSL'} />
                  <Badge ok={!site.health.debug} label={site.health.debug ? 'DEBUG ON' : 'Debug off'} />
                  {expandedSite === site.domain ? <ChevronUp size={16} className="text-muted-foreground" /> : <ChevronDown size={16} className="text-muted-foreground" />}
                </div>
              </div>

              {/* Expanded detail */}
              {expandedSite === site.domain && (
                <div className="border-t border-border">
                  {/* Sub-tabs */}
                  <div className="flex border-b border-border">
                    {(['overview', 'security', 'users', 'optimize'] as const).map(t => (
                      <button key={t} onClick={() => setSiteTab(t)}
                        className={`px-4 py-2.5 text-xs font-medium transition border-b-2 ${siteTab === t
                          ? 'border-blue-500 text-blue-400' : 'border-transparent text-muted-foreground hover:text-foreground'}`}>
                        {t === 'overview' && 'Overview'}
                        {t === 'security' && <span className="flex items-center gap-1"><Shield size={11} /> Security</span>}
                        {t === 'users' && <span className="flex items-center gap-1"><Users size={11} /> Users</span>}
                        {t === 'optimize' && <span className="flex items-center gap-1"><Database size={11} /> DB Optimize</span>}
                      </button>
                    ))}
                  </div>

                  <div className="p-5 space-y-5">
                  {/* ═══ Overview Tab ═══ */}
                  {siteTab === 'overview' && (<>
                    {/* Version info */}
                    <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                      {[
                        ['WP Version', site.version || '—'],
                        ['PHP', security?.php_version || site.health.php_version || '—'],
                        ['Database', site.db_name],
                        ['Prefix', security?.table_prefix || 'wp_'],
                      ].map(([label, val]) => (
                        <div key={label as string} className="rounded bg-background px-3 py-2">
                          <p className="text-[10px] text-muted-foreground">{label}</p>
                          <p className="font-mono text-xs text-card-foreground">{val}</p>
                        </div>
                      ))}
                    </div>

                    {/* Quick actions */}
                    <div className="flex flex-wrap gap-2">
                      <a href={site.admin_url} target="_blank" rel="noopener noreferrer"
                        className="flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-blue-700">
                        WP Admin <ExternalLink size={11} />
                      </a>
                      <button onClick={() => doAction('core-' + site.domain, () => wpUpdateCore(site.domain))}
                        disabled={!!actionLoading}
                        className="flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-xs text-card-foreground hover:bg-accent disabled:opacity-50">
                        {actionLoading === 'core-' + site.domain ? <RefreshCw size={11} className="animate-spin" /> : <Download size={11} />}
                        Update Core
                      </button>
                      <button onClick={() => doAction('plugins-' + site.domain, () => wpUpdatePlugins(site.domain))}
                        disabled={!!actionLoading}
                        className="flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-xs text-card-foreground hover:bg-accent disabled:opacity-50">
                        {actionLoading === 'plugins-' + site.domain ? <RefreshCw size={11} className="animate-spin" /> : <Plug size={11} />}
                        Update All Plugins
                      </button>
                      <button onClick={() => doAction('perms-' + site.domain, () => wpFixPermissions(site.domain))}
                        disabled={!!actionLoading}
                        className="flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-xs text-card-foreground hover:bg-accent disabled:opacity-50">
                        {actionLoading === 'perms-' + site.domain ? <RefreshCw size={11} className="animate-spin" /> : <Shield size={11} />}
                        Fix Permissions
                      </button>
                      <button onClick={() => doAction('debug-' + site.domain, () => wpToggleDebug(site.domain, !site.health.debug))}
                        disabled={!!actionLoading}
                        className={`flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-xs disabled:opacity-50 ${
                          site.health.debug ? 'border-amber-500/30 bg-amber-500/10 text-amber-400 hover:bg-amber-500/20' : 'border-border text-card-foreground hover:bg-accent'}`}>
                        {actionLoading === 'debug-' + site.domain ? <RefreshCw size={11} className="animate-spin" /> : <Bug size={11} />}
                        {site.health.debug ? 'Debug ON' : 'Debug OFF'}
                      </button>
                      <button onClick={() => doAction('errlog-' + site.domain, async () => {
                          const res = await wpErrorLog(site.domain);
                          setActionResult(res.log || res.message || 'No log content');
                          return { status: 'ok', output: '' };
                        })}
                        disabled={!!actionLoading}
                        className="flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-xs text-card-foreground hover:bg-accent disabled:opacity-50">
                        {actionLoading === 'errlog-' + site.domain ? <RefreshCw size={11} className="animate-spin" /> : <FileText size={11} />}
                        Error Log
                      </button>
                    </div>

                    {/* Error Log */}
                    {actionResult && (
                      <div>
                        <div className="flex items-center justify-between mb-2">
                          <h3 className="text-xs font-semibold text-muted-foreground flex items-center gap-1"><Bug size={12} /> Debug Log</h3>
                          <button onClick={() => setActionResult('')} className="text-[10px] text-muted-foreground hover:text-foreground">Close</button>
                        </div>
                        <pre className="max-h-48 overflow-auto rounded bg-background p-3 font-mono text-[11px] text-muted-foreground whitespace-pre-wrap">{actionResult}</pre>
                      </div>
                    )}

                    {/* Permissions */}
                    <div>
                      <h3 className="text-xs font-semibold text-muted-foreground mb-2">Permissions</h3>
                      <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                        {[['wp-config.php', site.permissions.wp_config], ['wp-content/', site.permissions.wp_content], ['uploads/', site.permissions.uploads], ['.htaccess', site.permissions.htaccess]]
                          .map(([label, val]) => (
                            <div key={label as string} className="rounded bg-background px-3 py-2">
                              <p className="text-[10px] text-muted-foreground">{label}</p>
                              <p className="font-mono text-xs text-card-foreground">{val || '—'}</p>
                            </div>
                          ))}
                      </div>
                      {site.permissions.owner && (
                        <p className="mt-1.5 text-[10px] text-muted-foreground">Owner: <span className="font-mono">{site.permissions.owner}</span>
                          {site.permissions.writable ? <span className="ml-2 text-emerald-400">wp-content writable</span> : <span className="ml-2 text-red-400">wp-content NOT writable</span>}
                        </p>
                      )}
                    </div>

                    {/* Plugins */}
                    {(site.plugins ?? []).length > 0 && (
                      <div>
                        <h3 className="text-xs font-semibold text-muted-foreground mb-2 flex items-center gap-1"><Plug size={12} /> Plugins ({site.plugins.length})</h3>
                        <div className="space-y-1">
                          {site.plugins.map((p: WPPlugin) => (
                            <div key={p.name} className="flex items-center justify-between rounded bg-background px-3 py-2">
                              <div className="flex items-center gap-2">
                                <span className={`h-1.5 w-1.5 rounded-full ${p.status === 'active' ? 'bg-emerald-400' : 'bg-slate-500'}`} />
                                <span className="text-xs text-card-foreground">{p.name}</span>
                                <span className="text-[10px] text-muted-foreground">v{p.version}</span>
                                {p.update && p.update !== 'none' && <span className="rounded bg-amber-500/15 px-1.5 py-0.5 text-[9px] font-medium text-amber-400">{p.update} available</span>}
                              </div>
                              <div className="flex items-center gap-1">
                                {p.update && p.update !== 'none' && (
                                  <button onClick={() => doAction(`update-${p.name}`, () => wpPluginAction(site.domain, 'update', p.name))}
                                    disabled={!!actionLoading} className="rounded px-2 py-0.5 text-[10px] text-blue-400 hover:bg-blue-500/10 disabled:opacity-50">Update</button>
                                )}
                                {p.status === 'active' ? (
                                  <button onClick={() => doAction(`deactivate-${p.name}`, () => wpPluginAction(site.domain, 'deactivate', p.name))}
                                    disabled={!!actionLoading} className="rounded px-2 py-0.5 text-[10px] text-amber-400 hover:bg-amber-500/10 disabled:opacity-50">Deactivate</button>
                                ) : (
                                  <button onClick={() => doAction(`activate-${p.name}`, () => wpPluginAction(site.domain, 'activate', p.name))}
                                    disabled={!!actionLoading} className="rounded px-2 py-0.5 text-[10px] text-emerald-400 hover:bg-emerald-500/10 disabled:opacity-50">Activate</button>
                                )}
                              </div>
                            </div>
                          ))}
                        </div>
                      </div>
                    )}

                    {/* Themes */}
                    {(site.themes ?? []).length > 0 && (
                      <div>
                        <h3 className="text-xs font-semibold text-muted-foreground mb-2 flex items-center gap-1"><Palette size={12} /> Themes ({site.themes.length})</h3>
                        <div className="flex flex-wrap gap-2">
                          {site.themes.map(t => (
                            <div key={t.name} className={`rounded px-3 py-1.5 text-xs ${t.status === 'active' ? 'bg-blue-500/10 text-blue-400 border border-blue-500/30' : 'bg-background text-muted-foreground'}`}>
                              {t.name} v{t.version}
                              {t.update && t.update !== 'none' && <span className="ml-1 text-amber-400">({t.update})</span>}
                            </div>
                          ))}
                        </div>
                      </div>
                    )}
                  </>)}

                  {/* ═══ Security Tab ═══ */}
                  {siteTab === 'security' && (<>
                    {security ? (
                      <div className="space-y-4">
                        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                          {/* Security toggles */}
                          {([
                            { key: 'xmlrpc', label: 'XML-RPC', desc: 'Disable XML-RPC (prevents brute-force & DDoS)', on: security.xmlrpc_disabled, field: 'disable_xmlrpc' },
                            { key: 'fileedit', label: 'File Editor', desc: 'Disable theme/plugin editor in dashboard', on: security.file_edit_disabled, field: 'disable_file_edit' },
                            { key: 'ssl', label: 'Force SSL Admin', desc: 'Require HTTPS for wp-admin', on: security.ssl_forced, field: 'force_ssl_admin' },
                            { key: 'cron', label: 'Disable WP-Cron', desc: 'Use system cron instead of WP-Cron', on: security.wp_cron_disabled, field: 'disable_wp_cron' },
                            { key: 'dirlist', label: 'Block Directory Listing', desc: 'Prevent browsing directory contents', on: security.directory_listing_blocked, field: 'block_dir_listing' },
                          ] as const).map(item => (
                            <div key={item.key} className="flex items-center justify-between rounded-lg bg-background px-4 py-3">
                              <div>
                                <p className="text-sm font-medium text-card-foreground">{item.label}</p>
                                <p className="text-[10px] text-muted-foreground">{item.desc}</p>
                              </div>
                              <button
                                onClick={() => doAction('harden-' + item.key, async () => {
                                  await wpHarden(site.domain, { [item.field]: !item.on });
                                  const s = await wpSecurityStatus(site.domain);
                                  setSecurity(s);
                                })}
                                disabled={!!actionLoading}
                                className={`relative h-6 w-11 rounded-full transition ${item.on ? 'bg-emerald-500' : 'bg-slate-600'}`}>
                                <span className={`absolute top-0.5 h-5 w-5 rounded-full bg-white shadow transition ${item.on ? 'left-[22px]' : 'left-0.5'}`} />
                              </button>
                            </div>
                          ))}
                        </div>

                        {/* Security status badges */}
                        <div>
                          <h3 className="text-xs font-semibold text-muted-foreground mb-2">Status</h3>
                          <div className="flex flex-wrap gap-2">
                            <Badge ok={security.xmlrpc_disabled} label={security.xmlrpc_disabled ? 'XML-RPC disabled' : 'XML-RPC enabled'} />
                            <Badge ok={security.file_edit_disabled} label={security.file_edit_disabled ? 'File editor disabled' : 'File editor enabled'} />
                            <Badge ok={security.ssl_forced} label={security.ssl_forced ? 'SSL forced' : 'SSL not forced'} />
                            <Badge ok={!security.debug_enabled} label={security.debug_enabled ? 'DEBUG on' : 'DEBUG off'} />
                            <Badge ok={security.directory_listing_blocked} label={security.directory_listing_blocked ? 'Dir listing blocked' : 'Dir listing open'} />
                            <Badge ok={security.table_prefix !== 'wp_'} label={`Prefix: ${security.table_prefix}`} />
                          </div>
                        </div>

                        {/* Quick harden all */}
                        <button
                          onClick={() => doAction('harden-all', async () => {
                            await wpHarden(site.domain, {
                              disable_xmlrpc: true, disable_file_edit: true,
                              force_ssl_admin: true, block_dir_listing: true,
                            });
                            const s = await wpSecurityStatus(site.domain);
                            setSecurity(s);
                          })}
                          disabled={!!actionLoading}
                          className="flex items-center gap-2 rounded-md bg-emerald-600 px-4 py-2 text-sm font-medium text-white hover:bg-emerald-700 disabled:opacity-50">
                          {actionLoading === 'harden-all' ? <RefreshCw size={13} className="animate-spin" /> : <Lock size={13} />}
                          Harden All
                        </button>
                      </div>
                    ) : (
                      <p className="text-sm text-muted-foreground py-4">Loading security status...</p>
                    )}
                  </>)}

                  {/* ═══ Users Tab ═══ */}
                  {siteTab === 'users' && (<>
                    <div className="space-y-3">
                      {siteUsersError ? (
                        <div className="rounded-md border border-red-500/30 bg-red-500/10 px-4 py-3 text-xs text-red-300">
                          Failed to load users: {siteUsersError}
                        </div>
                      ) : siteUsers.length === 0 ? (
                        <p className="text-sm text-muted-foreground py-4">No users found (requires wp-cli)</p>
                      ) : (
                        <div className="space-y-1">
                          {siteUsers.map(u => (
                            <div key={u.id} className="flex items-center justify-between rounded bg-background px-4 py-3">
                              <div className="flex items-center gap-3">
                                <div className="flex h-8 w-8 items-center justify-center rounded-full bg-blue-500/10 text-blue-400 text-xs font-bold">
                                  {u.login[0]?.toUpperCase()}
                                </div>
                                <div>
                                  <p className="text-sm font-medium text-card-foreground">{u.login}</p>
                                  <p className="text-[10px] text-muted-foreground">{u.email} &middot; {u.role}</p>
                                </div>
                              </div>
                              <div className="flex items-center gap-2">
                                <span className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${
                                  u.role === 'administrator' ? 'bg-red-500/15 text-red-400' :
                                  u.role === 'editor' ? 'bg-blue-500/15 text-blue-400' :
                                  'bg-slate-500/15 text-slate-400'
                                }`}>{u.role}</span>
                                <button
                                  onClick={() => { setShowPasswordForm(showPasswordForm === u.login ? '' : u.login); setNewPassword(''); }}
                                  className="flex items-center gap-1 rounded px-2 py-1 text-[10px] text-muted-foreground hover:bg-accent hover:text-foreground">
                                  <Key size={10} /> Change Password
                                </button>
                              </div>
                            </div>
                          ))}
                          {/* Password change form */}
                          {showPasswordForm && (
                            <div className="rounded-lg border border-blue-500/30 bg-blue-500/5 p-4 mt-2">
                              <p className="text-xs text-blue-400 mb-2">Change password for <span className="font-bold">{showPasswordForm}</span></p>
                              <div className="flex gap-2">
                                <input type="password" value={newPassword} onChange={e => setNewPassword(e.target.value)}
                                  placeholder="New password (min 8 chars)"
                                  className="flex-1 rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground outline-none focus:border-blue-500" />
                                <button
                                  onClick={async () => {
                                    if (newPassword.length < 8) { setError('Password must be at least 8 characters'); return; }
                                    await doAction('pw-change', async () => {
                                      await wpChangePassword(site.domain, showPasswordForm, newPassword);
                                      setShowPasswordForm('');
                                      setNewPassword('');
                                    });
                                  }}
                                  disabled={!!actionLoading || newPassword.length < 8}
                                  className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50">
                                  {actionLoading === 'pw-change' ? <RefreshCw size={13} className="animate-spin" /> : 'Save'}
                                </button>
                              </div>
                            </div>
                          )}
                        </div>
                      )}
                    </div>
                  </>)}

                  {/* ═══ DB Optimize Tab ═══ */}
                  {siteTab === 'optimize' && (<>
                    <div className="space-y-4">
                      <div className="rounded-lg bg-background p-4">
                        <h3 className="text-sm font-medium text-card-foreground mb-1">Database Optimization</h3>
                        <p className="text-xs text-muted-foreground mb-3">
                          Clean up post revisions, spam comments, trashed items, expired transients, and optimize database tables.
                        </p>
                        <button
                          onClick={() => doAction('optimize-db', async () => {
                            const res = await wpOptimizeDB(site.domain);
                            setActionResult(res.output || 'Optimization complete');
                          })}
                          disabled={!!actionLoading}
                          className="flex items-center gap-2 rounded-md bg-amber-600 px-4 py-2 text-sm font-medium text-white hover:bg-amber-700 disabled:opacity-50">
                          {actionLoading === 'optimize-db' ? <RefreshCw size={13} className="animate-spin" /> : <Trash2 size={13} />}
                          Optimize Database
                        </button>
                      </div>

                      {actionResult && (
                        <div>
                          <div className="flex items-center justify-between mb-2">
                            <h3 className="text-xs font-semibold text-muted-foreground">Result</h3>
                            <button onClick={() => setActionResult('')} className="text-[10px] text-muted-foreground hover:text-foreground">Close</button>
                          </div>
                          <pre className="max-h-48 overflow-auto rounded bg-background p-3 font-mono text-[11px] text-muted-foreground whitespace-pre-wrap">{actionResult}</pre>
                        </div>
                      )}

                      <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
                        <div className="rounded bg-background px-3 py-2">
                          <p className="text-[10px] text-muted-foreground">Database</p>
                          <p className="font-mono text-xs text-card-foreground">{site.db_name}</p>
                        </div>
                        <div className="rounded bg-background px-3 py-2">
                          <p className="text-[10px] text-muted-foreground">DB User</p>
                          <p className="font-mono text-xs text-card-foreground">{site.db_user}</p>
                        </div>
                        <div className="rounded bg-background px-3 py-2">
                          <p className="text-[10px] text-muted-foreground">DB Host</p>
                          <p className="font-mono text-xs text-card-foreground">{site.db_host}</p>
                        </div>
                      </div>
                    </div>
                  </>)}
                  </div>
                </div>
              )}
            </div>
          ))}
        </>
      )}

      {/* ═══ Install Tab ═══ */}
      {tab === 'install' && (
        <>
          <div className="rounded-lg border border-border bg-card p-5">
            <h2 className="text-sm font-semibold text-card-foreground mb-3">Prerequisites</h2>
            <div className="space-y-2">
              <div className="flex items-center gap-2 text-sm">
                <span className={`h-2.5 w-2.5 rounded-full ${phpDomains.length > 0 ? 'bg-emerald-400' : 'bg-red-400'}`} />
                <span className={phpDomains.length > 0 ? 'text-emerald-400' : 'text-red-400'}>
                  {phpDomains.length > 0
                    ? `PHP domains (${phpDomains.length})`
                    : <>No PHP domains — <a href="/_uwas/dashboard/domains" className="underline">add a PHP domain</a> first</>}
                </span>
              </div>
              <div className="flex items-center gap-2 text-sm">
                <span className={`h-2.5 w-2.5 rounded-full ${installableDomains.length > 0 ? 'bg-emerald-400' : 'bg-amber-400'}`} />
                <span className={installableDomains.length > 0 ? 'text-emerald-400' : 'text-amber-400'}>
                  {installableDomains.length > 0
                    ? `${installableDomains.length} domain${installableDomains.length > 1 ? 's' : ''} ready for WordPress`
                    : 'All PHP domains already have WordPress'}
                </span>
              </div>
              {(() => {
                const hasDB = mysqlOk || dockerDBs.filter(c => c.engine !== 'postgresql').length > 0;
                return (
                  <div className="flex items-center gap-2 text-sm">
                    <span className={`h-2.5 w-2.5 rounded-full ${hasDB ? 'bg-emerald-400' : 'bg-red-400'}`} />
                    <span className={hasDB ? 'text-emerald-400' : 'text-red-400'}>
                      {mysqlOk ? 'MySQL/MariaDB running' : dockerDBs.filter(c => c.engine !== 'postgresql').length > 0 ? `Docker DB available (${dockerDBs.filter(c => c.engine !== 'postgresql').map(c => c.name).join(', ')})` : 'MySQL/MariaDB — install from Database page'}
                    </span>
                  </div>
                );
              })()}
            </div>
          </div>

          {installableDomains.length > 0 && (mysqlOk || dockerDBs.filter(c => c.engine !== 'postgresql').length > 0) && (
            <div className="rounded-lg border border-border bg-card p-5">
              <h2 className="text-sm font-semibold text-card-foreground mb-4">Install WordPress</h2>
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <div>
                  <label className="mb-1.5 block text-xs text-muted-foreground">Domain</label>
                  <select value={selectedDomain} onChange={e => setSelectedDomain(e.target.value)}
                    className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none focus:border-blue-500">
                    {installableDomains.map(d => <option key={d.host} value={d.host}>{d.host}</option>)}
                  </select>
                </div>
                <div>
                  <label className="mb-1.5 block text-xs text-muted-foreground">Database Host</label>
                  {dockerDBs.length > 0 ? (
                    <select value={dbHost} onChange={e => setDbHost(e.target.value)}
                      className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none focus:border-blue-500">
                      {mysqlOk && <option value="localhost">localhost (Native MariaDB/MySQL)</option>}
                      {dockerDBs.filter(c => c.engine !== 'postgresql').map(c => (
                        <option key={c.name} value={`127.0.0.1:${c.port}`}>
                          {c.name} (Docker {c.engine} — port {c.port})
                        </option>
                      ))}
                    </select>
                  ) : (
                    <input value={dbHost} onChange={e => setDbHost(e.target.value)}
                      className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none focus:border-blue-500" placeholder="localhost" />
                  )}
                </div>
              </div>
              <button onClick={handleInstall} disabled={installing || !selectedDomain}
                className="mt-4 flex items-center gap-2 rounded-md bg-amber-600 px-5 py-2.5 text-sm font-medium text-white hover:bg-amber-700 disabled:opacity-50">
                {installing ? <><RefreshCw size={14} className="animate-spin" /> Installing...</> : <><Zap size={14} /> Install WordPress</>}
              </button>
            </div>
          )}

          {status && status.status === 'running' && (
            <div className="rounded-lg border border-blue-500/30 bg-blue-500/5 p-5">
              <p className="text-sm text-blue-400 mb-2">Installing WordPress on {status.domain}...</p>
              <div className="h-1.5 w-full bg-accent rounded-full overflow-hidden">
                <div className="h-full bg-blue-500 rounded-full animate-pulse" style={{ width: '60%' }} />
              </div>
            </div>
          )}

          {status && status.status === 'done' && (
            <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-5">
              <div className="flex items-center gap-2 text-emerald-400 font-medium mb-3"><Check size={16} /> WordPress installed!</div>
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                {([['Database', status.db_name], ['DB User', status.db_user], ['DB Password', status.db_pass], ['Admin URL', status.admin_url]] as [string, string][])
                  .filter(([,v]) => v).map(([label, value]) => (
                    <div key={label} className="flex items-center justify-between rounded bg-background px-3 py-2">
                      <div><span className="text-xs text-muted-foreground">{label}</span><p className="font-mono text-xs text-foreground">{value}</p></div>
                      <button onClick={() => copy(value, label)} className="ml-2 rounded p-1 text-muted-foreground hover:text-card-foreground">
                        {copied === label ? <Check size={12} className="text-emerald-400" /> : <Copy size={12} />}
                      </button>
                    </div>
                  ))}
              </div>
              <a href={status.admin_url} target="_blank" rel="noopener noreferrer"
                className="mt-3 inline-flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700">
                Open WordPress Setup <ExternalLink size={13} />
              </a>
            </div>
          )}

          {status && status.status === 'error' && (
            <div className="rounded-lg border border-red-500/30 bg-red-500/5 p-5">
              <p className="text-sm text-red-400 mb-2">Failed: {status.error}</p>
              {status.output && <pre className="mt-2 max-h-40 overflow-auto rounded bg-background p-3 text-[10px] text-muted-foreground whitespace-pre-wrap">{status.output}</pre>}
            </div>
          )}
        </>
      )}
    </div>
  );
}
