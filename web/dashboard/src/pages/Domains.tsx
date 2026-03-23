import { useState, useEffect, useCallback, type FormEvent, type ReactNode } from 'react';
import {
  Globe, X, Plus, Trash2, CheckCircle, XCircle, ChevronDown, ChevronRight,
  Shield, Lock, Database, Server, ArrowRight, FileCode, Zap, RefreshCw,
  AlertTriangle, Layers, Settings, Link, Pencil,
} from 'lucide-react';
import {
  fetchDomains, addDomain, updateDomain, deleteDomain, fetchDomainDetail, fetchCerts, triggerPurge,
  fetchPHP, type DomainData, type DomainDetail, type CertInfo, type PHPInstall,
} from '@/lib/api';

/* ------------------------------------------------------------------ */
/*  Types                                                              */
/* ------------------------------------------------------------------ */

interface DomainFormState {
  host: string;
  type: string;
  root: string;
  ssl: string;
  cacheEnabled: boolean;
  cacheTTL: string;
  phpFpmAddress: string;
  phpIndexFiles: string;
  proxyUpstreams: string;
  proxyAlgorithm: string;
  redirectTarget: string;
  redirectCode: string;
  blockedPaths: string;
  wafEnabled: boolean;
  htaccessEnabled: boolean;
}

type TemplateName = 'wordpress' | 'static' | 'proxy' | 'redirect' | null;

/* ------------------------------------------------------------------ */
/*  Constants                                                          */
/* ------------------------------------------------------------------ */

const domainTypes = ['static', 'php', 'proxy', 'redirect'] as const;
const sslModes = ['auto', 'manual', 'off'] as const;
const proxyAlgorithms = ['round-robin', 'least-conn', 'ip-hash', 'random'] as const;
const redirectCodes = ['301', '302', '307', '308'] as const;

const emptyForm: DomainFormState = {
  host: '',
  type: 'static',
  root: '',
  ssl: 'auto',
  cacheEnabled: false,
  cacheTTL: '3600',
  phpFpmAddress: '',
  phpIndexFiles: 'index.php,index.html',
  proxyUpstreams: '',
  proxyAlgorithm: 'round-robin',
  redirectTarget: '',
  redirectCode: '301',
  blockedPaths: '',
  wafEnabled: false,
  htaccessEnabled: false,
};

interface TemplateConfig {
  label: string;
  description: string;
  icon: ReactNode;
  color: string;
  form: Partial<DomainFormState>;
}

const templates: Record<string, TemplateConfig> = {
  wordpress: {
    label: 'WordPress',
    description: 'PHP site with WAF, cache bypass for wp-admin, blocked paths',
    icon: <FileCode size={20} />,
    color: 'text-purple-400 bg-purple-500/15 border-purple-500/30',
    form: {
      type: 'php',
      ssl: 'auto',
      root: '/var/www/html',
      htaccessEnabled: true,
      cacheEnabled: true,
      cacheTTL: '3600',
      phpFpmAddress: '127.0.0.1:9000',
      phpIndexFiles: 'index.php,index.html',
      wafEnabled: true,
      blockedPaths: 'wp-config.php,xmlrpc.php,.env,.git',
    },
  },
  static: {
    label: 'Static Site',
    description: 'Static files with compression and caching',
    icon: <Layers size={20} />,
    color: 'text-blue-400 bg-blue-500/15 border-blue-500/30',
    form: {
      type: 'static',
      ssl: 'auto',
      root: '/var/www/html',
      cacheEnabled: true,
      cacheTTL: '86400',
    },
  },
  proxy: {
    label: 'Reverse Proxy',
    description: 'Forward traffic to an upstream backend',
    icon: <Server size={20} />,
    color: 'text-orange-400 bg-orange-500/15 border-orange-500/30',
    form: {
      type: 'proxy',
      ssl: 'auto',
      proxyUpstreams: 'http://localhost:3000',
      proxyAlgorithm: 'round-robin',
    },
  },
  redirect: {
    label: 'Redirect',
    description: 'Redirect all traffic to another URL',
    icon: <ArrowRight size={20} />,
    color: 'text-slate-300 bg-slate-500/15 border-slate-500/30',
    form: {
      type: 'redirect',
      ssl: 'auto',
      redirectTarget: '',
      redirectCode: '301',
    },
  },
};

/* ------------------------------------------------------------------ */
/*  Badge helpers                                                      */
/* ------------------------------------------------------------------ */

const typeBadgeStyles: Record<string, string> = {
  static: 'bg-blue-500/15 text-blue-400',
  php: 'bg-purple-500/15 text-purple-400',
  proxy: 'bg-orange-500/15 text-orange-400',
  redirect: 'bg-slate-500/15 text-slate-400',
};

function TypeBadge({ type }: { type: string }) {
  return (
    <span className={`rounded-full px-2.5 py-0.5 text-xs font-medium ${typeBadgeStyles[type] ?? 'bg-slate-500/15 text-slate-400'}`}>
      {type}
    </span>
  );
}

const sslBadgeStyles: Record<string, string> = {
  auto: 'bg-emerald-500/15 text-emerald-400',
  manual: 'bg-amber-500/15 text-amber-400',
  off: 'bg-red-500/15 text-red-400',
};

function SslBadge({ ssl }: { ssl: string }) {
  return (
    <span className={`rounded-full px-2.5 py-0.5 text-xs font-medium ${sslBadgeStyles[ssl] ?? 'bg-slate-500/15 text-slate-400'}`}>
      {ssl}
    </span>
  );
}

function StatusDot({ active }: { active: boolean }) {
  return (
    <span className="flex items-center gap-1.5">
      <span className={`inline-block h-2 w-2 rounded-full ${active ? 'bg-emerald-400' : 'bg-red-400'}`} />
      <span className={`text-xs ${active ? 'text-emerald-400' : 'text-red-400'}`}>{active ? 'Active' : 'Inactive'}</span>
    </span>
  );
}

/* ------------------------------------------------------------------ */
/*  Small reusable components                                          */
/* ------------------------------------------------------------------ */

function InfoCard({ icon, title, children }: { icon: ReactNode; title: string; children: ReactNode }) {
  return (
    <div className="rounded-lg border border-[#334155] bg-[#0f172a] p-4">
      <div className="mb-3 flex items-center gap-2">
        <span className="text-slate-400">{icon}</span>
        <h4 className="text-sm font-semibold text-slate-300">{title}</h4>
      </div>
      <div className="space-y-2 text-sm">{children}</div>
    </div>
  );
}

function DetailRow({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-4">
      <span className="shrink-0 text-xs font-medium uppercase text-slate-500">{label}</span>
      <span className="text-right text-xs text-slate-200">{value}</span>
    </div>
  );
}

function FormField({ label, htmlFor, children }: { label: string; htmlFor: string; children: ReactNode }) {
  return (
    <div>
      <label htmlFor={htmlFor} className="mb-1.5 block text-sm font-medium text-slate-300">{label}</label>
      {children}
    </div>
  );
}

const inputCls = 'w-full rounded-md border border-[#334155] bg-[#1e293b] px-3 py-2.5 text-sm text-slate-200 placeholder-slate-500 outline-none transition focus:border-blue-500 focus:ring-1 focus:ring-blue-500';
const selectCls = inputCls;

/* ------------------------------------------------------------------ */
/*  Main component                                                     */
/* ------------------------------------------------------------------ */

export default function Domains() {
  /* list state */
  const [domains, setDomains] = useState<DomainData[]>([]);
  const [loading, setLoading] = useState(true);
  const [status, setStatus] = useState<{ ok: boolean; message: string } | null>(null);

  /* expanded row state */
  const [expandedHost, setExpandedHost] = useState<string | null>(null);
  const [detail, setDetail] = useState<DomainDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [certMap, setCertMap] = useState<Record<string, CertInfo>>({});

  /* delete confirmation */
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);

  /* add/edit form state */
  const [showAdd, setShowAdd] = useState(false);
  const [selectedTemplate, setSelectedTemplate] = useState<TemplateName>(null);
  const [form, setForm] = useState<DomainFormState>({ ...emptyForm });
  const [submitting, setSubmitting] = useState(false);
  const [editingHost, setEditingHost] = useState<string | null>(null);

  /* PHP installs for FPM dropdown */
  const [phpInstalls, setPhpInstalls] = useState<PHPInstall[]>([]);
  const [phpCustomInput, setPhpCustomInput] = useState(false);

  /* purge state */
  const [purgingHost, setPurgingHost] = useState<string | null>(null);

  /* -------- data loading -------- */

  const loadDomains = useCallback(() => {
    fetchDomains()
      .then(setDomains)
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  const loadCerts = useCallback(() => {
    fetchCerts()
      .then(certs => {
        const map: Record<string, CertInfo> = {};
        for (const c of certs) map[c.host] = c;
        setCertMap(map);
      })
      .catch(() => {});
  }, []);

  const loadPHP = useCallback(() => {
    fetchPHP()
      .then(setPhpInstalls)
      .catch(() => {});
  }, []);

  useEffect(() => {
    loadDomains();
    loadCerts();
    loadPHP();
  }, [loadDomains, loadCerts, loadPHP]);

  /* -------- expand row -------- */

  const toggleExpand = (host: string) => {
    if (expandedHost === host) {
      setExpandedHost(null);
      setDetail(null);
      return;
    }
    setExpandedHost(host);
    setDetail(null);
    setDetailLoading(true);
    fetchDomainDetail(host)
      .then(setDetail)
      .catch(() => {
        /* fall back to list-level data */
        const found = domains.find(d => d.host === host);
        if (found) {
          setDetail({ ...found, cache: undefined, security: undefined, php: undefined, proxy: undefined, redirect: undefined, htaccess: undefined });
        }
      })
      .finally(() => setDetailLoading(false));
  };

  /* -------- delete -------- */

  const handleDelete = async (host: string) => {
    setStatus(null);
    try {
      await deleteDomain(host);
      setStatus({ ok: true, message: `Domain "${host}" deleted successfully` });
      setConfirmDelete(null);
      if (expandedHost === host) { setExpandedHost(null); setDetail(null); }
      loadDomains();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
      setConfirmDelete(null);
    }
  };

  /* -------- purge cache for single domain -------- */

  const handlePurgeDomain = async (host: string) => {
    setPurgingHost(host);
    setStatus(null);
    try {
      const domainTag = `site:${host.replace(/[^a-zA-Z0-9.-]/g, '')}`;
      await triggerPurge(domainTag);
      setStatus({ ok: true, message: `Cache purged for ${host}` });
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setPurgingHost(null);
    }
  };

  /* -------- template selection -------- */

  const selectTemplate = (name: TemplateName) => {
    setSelectedTemplate(name);
    if (name && templates[name]) {
      setForm({ ...emptyForm, ...templates[name].form });
    } else {
      setForm({ ...emptyForm });
    }
  };

  const openAddModal = () => {
    setShowAdd(true);
    setEditingHost(null);
    setSelectedTemplate(null);
    setForm({ ...emptyForm });
    setPhpCustomInput(false);
    setStatus(null);
  };

  const startEdit = async (host: string) => {
    setStatus(null);
    try {
      const d = await fetchDomainDetail(host);
      const editForm: DomainFormState = {
        host: d.host,
        type: d.type,
        root: d.root || '',
        ssl: d.ssl,
        cacheEnabled: d.cache?.enabled ?? false,
        cacheTTL: String(d.cache?.ttl ?? 3600),
        phpFpmAddress: d.php?.fpm_address ?? '',
        phpIndexFiles: d.php?.index_files?.join(', ') ?? 'index.php,index.html',
        proxyUpstreams: d.proxy?.upstreams?.join(', ') ?? '',
        proxyAlgorithm: d.proxy?.algorithm ?? 'round-robin',
        redirectTarget: d.redirect?.target ?? '',
        redirectCode: String(d.redirect?.status_code ?? 301),
        blockedPaths: d.security?.blocked_paths?.join(', ') ?? '',
        wafEnabled: d.security?.waf ?? false,
        htaccessEnabled: d.htaccess?.enabled ?? false,
      };
      /* Determine if the PHP address matches a known install */
      const knownAddr = phpInstalls.some(p => p.listen_addr === editForm.phpFpmAddress);
      setPhpCustomInput(!knownAddr && editForm.phpFpmAddress !== '');
      setForm(editForm);
      setEditingHost(host);
      setSelectedTemplate(editForm.type === 'php' ? 'wordpress' : editForm.type === 'proxy' ? 'proxy' : editForm.type === 'redirect' ? 'redirect' : 'static');
      setShowAdd(true);
    } catch (e) {
      setStatus({ ok: false, message: `Failed to load domain details: ${(e as Error).message}` });
    }
  };

  /* -------- add domain -------- */

  const patchField = <K extends keyof DomainFormState>(key: K, value: DomainFormState[K]) => {
    setForm(prev => ({ ...prev, [key]: value }));
  };

  const handleAdd = async (e: FormEvent) => {
    e.preventDefault();
    if (!form.host.trim()) return;
    setSubmitting(true);
    setStatus(null);

    /* Build API payload */
    const payload: Record<string, unknown> = {
      host: form.host.trim(),
      type: form.type,
      root: form.root.trim() || undefined,
      ssl: { mode: form.ssl },
    };

    if (form.cacheEnabled) {
      payload.cache = { enabled: true, ttl: parseInt(form.cacheTTL, 10) || 3600 };
    }

    if (form.type === 'php') {
      payload.php = {
        fpm_address: form.phpFpmAddress.trim() || '127.0.0.1:9000',
        index_files: form.phpIndexFiles.split(',').map(s => s.trim()).filter(Boolean),
      };
    }

    if (form.type === 'proxy') {
      payload.proxy = {
        upstreams: form.proxyUpstreams.split(',').map(s => s.trim()).filter(Boolean).map(addr => ({ address: addr, weight: 1 })),
        algorithm: form.proxyAlgorithm,
      };
    }

    if (form.type === 'redirect') {
      payload.redirect = {
        target: form.redirectTarget.trim(),
        status: parseInt(form.redirectCode, 10) || 301,
      };
    }

    if (form.wafEnabled || form.blockedPaths.trim()) {
      payload.security = {
        waf: { enabled: form.wafEnabled },
        blocked_paths: form.blockedPaths.split(',').map(s => s.trim()).filter(Boolean),
      };
    }

    if (form.htaccessEnabled) {
      payload.htaccess = { mode: "import" };
    }

    try {
      if (editingHost) {
        await updateDomain(editingHost, payload);
        setStatus({ ok: true, message: `Domain "${editingHost}" updated successfully` });
      } else {
        await addDomain(payload);
        setStatus({ ok: true, message: `Domain "${form.host.trim()}" added successfully` });
      }
      setForm({ ...emptyForm });
      setSelectedTemplate(null);
      setEditingHost(null);
      setPhpCustomInput(false);
      setShowAdd(false);
      loadDomains();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setSubmitting(false);
    }
  };

  /* ---------------------------------------------------------------- */
  /*  Render                                                           */
  /* ---------------------------------------------------------------- */

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">Domains</h1>
          <p className="text-sm text-slate-400">
            {loading ? 'Loading...' : `${domains.length} domain${domains.length !== 1 ? 's' : ''} configured`}
          </p>
        </div>
        <button
          onClick={openAddModal}
          className="flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700"
        >
          <Plus size={14} />
          Add Domain
        </button>
      </div>

      {/* Status toast */}
      {status && (
        <div className={`flex items-center gap-2 rounded-md px-4 py-3 text-sm ${status.ok ? 'bg-emerald-500/10 text-emerald-400' : 'bg-red-500/10 text-red-400'}`}>
          {status.ok ? <CheckCircle size={14} /> : <XCircle size={14} />}
          {status.message}
        </div>
      )}

      {/* Domain table */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] shadow-md">
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-[#334155] text-slate-400">
                <th className="w-8 px-3 py-3" />
                <th className="px-5 py-3 font-medium">Host</th>
                <th className="px-5 py-3 font-medium">Type</th>
                <th className="px-5 py-3 font-medium">SSL</th>
                <th className="px-5 py-3 font-medium">Root / Target</th>
                <th className="px-5 py-3 font-medium">Status</th>
                <th className="px-5 py-3 font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {loading && (
                <tr><td colSpan={7} className="px-5 py-8 text-center text-slate-500">Loading...</td></tr>
              )}
              {!loading && domains.length === 0 && (
                <tr><td colSpan={7} className="px-5 py-8 text-center text-slate-500">No domains configured</td></tr>
              )}
              {domains.map(d => {
                const isExpanded = expandedHost === d.host;
                return (
                  <DomainRow
                    key={d.host}
                    domain={d}
                    isExpanded={isExpanded}
                    detail={isExpanded ? detail : null}
                    detailLoading={isExpanded && detailLoading}
                    certInfo={certMap[d.host] ?? null}
                    confirmDelete={confirmDelete}
                    purgingHost={purgingHost}
                    onToggle={() => toggleExpand(d.host)}
                    onEdit={startEdit}
                    onDelete={handleDelete}
                    onConfirmDelete={setConfirmDelete}
                    onPurge={handlePurgeDomain}
                  />
                );
              })}
            </tbody>
          </table>
        </div>
      </div>

      {/* ============ Add Domain Modal ============ */}
      {showAdd && (
        <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto py-10">
          <div className="absolute inset-0 bg-black/60" onClick={() => { setShowAdd(false); setEditingHost(null); setPhpCustomInput(false); }} />
          <div className="relative z-10 w-full max-w-2xl rounded-xl border border-[#334155] bg-[#0f172a] p-6 shadow-2xl">
            {/* Modal header */}
            <div className="mb-5 flex items-center justify-between">
              <h2 className="text-lg font-bold text-slate-100">{editingHost ? 'Edit Domain' : 'Add Domain'}</h2>
              <button onClick={() => { setShowAdd(false); setEditingHost(null); setPhpCustomInput(false); }} className="rounded-md p-1 text-slate-400 hover:text-slate-200">
                <X size={18} />
              </button>
            </div>

            {/* Template quick-add (hidden when editing) */}
            {!selectedTemplate && !editingHost && (
              <>
                <p className="mb-4 text-sm text-slate-400">Quick Add &mdash; choose a template or start from scratch</p>
                <div className="mb-6 grid grid-cols-2 gap-3">
                  {(Object.entries(templates) as [string, TemplateConfig][]).map(([key, tpl]) => (
                    <button
                      key={key}
                      onClick={() => selectTemplate(key as TemplateName)}
                      className={`flex items-center gap-3 rounded-lg border p-4 text-left transition hover:ring-1 hover:ring-blue-500 ${tpl.color}`}
                    >
                      {tpl.icon}
                      <div>
                        <p className="text-sm font-semibold">{tpl.label}</p>
                        <p className="text-xs opacity-70">{tpl.description}</p>
                      </div>
                    </button>
                  ))}
                </div>
                <button
                  onClick={() => selectTemplate('static')}
                  className="text-xs text-slate-500 underline hover:text-slate-300"
                >
                  Skip template, start with blank form
                </button>
              </>
            )}

            {/* Full form (shown after template selection) */}
            {selectedTemplate !== null && (
              <form onSubmit={handleAdd} className="space-y-5">
                {/* Template indicator */}
                <div className="flex items-center gap-2 rounded-md bg-[#1e293b] px-3 py-2 text-xs text-slate-400">
                  <Settings size={12} />
                  {editingHost ? (
                    <>Editing: <span className="font-mono font-medium text-slate-200">{editingHost}</span></>
                  ) : (
                    <>Template: <span className="font-medium text-slate-200">{templates[selectedTemplate]?.label ?? 'Custom'}</span>
                    <button type="button" onClick={() => { setSelectedTemplate(null); setForm({ ...emptyForm }); }} className="ml-auto text-slate-500 hover:text-slate-300">Change</button></>
                  )}
                </div>

                {/* Host */}
                <FormField label="Host" htmlFor="add-host">
                  <input id="add-host" type="text" value={form.host} onChange={e => patchField('host', e.target.value)}
                    placeholder="example.com" required autoFocus disabled={!!editingHost}
                    className={`${inputCls}${editingHost ? ' opacity-60 cursor-not-allowed' : ''}`} />
                </FormField>

                {/* Type + SSL row */}
                <div className="grid grid-cols-2 gap-4">
                  <FormField label="Type" htmlFor="add-type">
                    <select id="add-type" value={form.type} onChange={e => patchField('type', e.target.value)} className={selectCls}>
                      {domainTypes.map(t => <option key={t} value={t}>{t}</option>)}
                    </select>
                  </FormField>
                  <FormField label="SSL Mode" htmlFor="add-ssl">
                    <select id="add-ssl" value={form.ssl} onChange={e => patchField('ssl', e.target.value)} className={selectCls}>
                      {sslModes.map(m => <option key={m} value={m}>{m}</option>)}
                    </select>
                  </FormField>
                </div>

                {/* Root */}
                <FormField label="Root Directory" htmlFor="add-root">
                  <input id="add-root" type="text" value={form.root} onChange={e => patchField('root', e.target.value)}
                    placeholder="/var/www/html" className={inputCls} />
                </FormField>

                {/* Cache */}
                <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-4">
                  <div className="flex items-center justify-between">
                    <span className="flex items-center gap-2 text-sm font-medium text-slate-300"><Database size={14} /> Cache</span>
                    <label className="relative inline-flex cursor-pointer items-center">
                      <input type="checkbox" checked={form.cacheEnabled} onChange={e => patchField('cacheEnabled', e.target.checked)} className="peer sr-only" />
                      <div className="peer h-5 w-9 rounded-full bg-[#334155] after:absolute after:left-[2px] after:top-[2px] after:h-4 after:w-4 after:rounded-full after:bg-slate-400 after:transition-all peer-checked:bg-blue-600 peer-checked:after:translate-x-full peer-checked:after:bg-white" />
                    </label>
                  </div>
                  {form.cacheEnabled && (
                    <div className="mt-3">
                      <FormField label="TTL (seconds)" htmlFor="add-cache-ttl">
                        <input id="add-cache-ttl" type="number" min="0" value={form.cacheTTL} onChange={e => patchField('cacheTTL', e.target.value)} className={inputCls} />
                      </FormField>
                    </div>
                  )}
                </div>

                {/* PHP section */}
                {form.type === 'php' && (
                  <div className="rounded-lg border border-purple-500/20 bg-purple-500/5 p-4">
                    <h3 className="mb-3 flex items-center gap-2 text-sm font-semibold text-purple-400"><FileCode size={14} /> PHP Configuration</h3>
                    <div className="space-y-3">
                      <FormField label="FPM Address" htmlFor="add-php-fpm">
                        {phpCustomInput ? (
                          <div className="flex gap-2">
                            <input id="add-php-fpm" type="text" value={form.phpFpmAddress} onChange={e => patchField('phpFpmAddress', e.target.value)}
                              placeholder="127.0.0.1:9000" className={inputCls} />
                            {phpInstalls.length > 0 && (
                              <button type="button" onClick={() => { setPhpCustomInput(false); patchField('phpFpmAddress', phpInstalls[0]?.listen_addr ?? ''); }}
                                className="shrink-0 rounded-md bg-[#334155] px-3 py-2 text-xs font-medium text-slate-300 transition hover:bg-[#475569]">
                                List
                              </button>
                            )}
                          </div>
                        ) : (
                          <select id="add-php-fpm" value={form.phpFpmAddress} onChange={e => {
                            if (e.target.value === '__custom__') {
                              setPhpCustomInput(true);
                              patchField('phpFpmAddress', '');
                            } else {
                              patchField('phpFpmAddress', e.target.value);
                            }
                          }} className={selectCls}>
                            {phpInstalls.length === 0 && <option value="">No PHP detected</option>}
                            {phpInstalls.map(p => (
                              <option key={p.listen_addr} value={p.listen_addr}>
                                PHP {p.version} ({p.listen_addr})
                              </option>
                            ))}
                            <option value="__custom__">Custom...</option>
                          </select>
                        )}
                      </FormField>
                      <FormField label="Index Files (comma-separated)" htmlFor="add-php-index">
                        <input id="add-php-index" type="text" value={form.phpIndexFiles} onChange={e => patchField('phpIndexFiles', e.target.value)}
                          placeholder="index.php,index.html" className={inputCls} />
                      </FormField>
                      <label className="flex items-center gap-2 text-sm text-slate-300">
                        <input type="checkbox" checked={form.htaccessEnabled} onChange={e => patchField('htaccessEnabled', e.target.checked)}
                          className="rounded border-[#334155] bg-[#1e293b] text-blue-600 focus:ring-blue-500" />
                        Import .htaccess rules
                      </label>
                    </div>
                  </div>
                )}

                {/* Proxy section */}
                {form.type === 'proxy' && (
                  <div className="rounded-lg border border-orange-500/20 bg-orange-500/5 p-4">
                    <h3 className="mb-3 flex items-center gap-2 text-sm font-semibold text-orange-400"><Server size={14} /> Proxy Configuration</h3>
                    <div className="space-y-3">
                      <FormField label="Upstream URLs (comma-separated)" htmlFor="add-proxy-upstreams">
                        <input id="add-proxy-upstreams" type="text" value={form.proxyUpstreams} onChange={e => patchField('proxyUpstreams', e.target.value)}
                          placeholder="http://localhost:3000,http://localhost:3001" className={inputCls} />
                      </FormField>
                      <FormField label="Algorithm" htmlFor="add-proxy-algo">
                        <select id="add-proxy-algo" value={form.proxyAlgorithm} onChange={e => patchField('proxyAlgorithm', e.target.value)} className={selectCls}>
                          {proxyAlgorithms.map(a => <option key={a} value={a}>{a}</option>)}
                        </select>
                      </FormField>
                    </div>
                  </div>
                )}

                {/* Redirect section */}
                {form.type === 'redirect' && (
                  <div className="rounded-lg border border-slate-500/20 bg-slate-500/5 p-4">
                    <h3 className="mb-3 flex items-center gap-2 text-sm font-semibold text-slate-300"><ArrowRight size={14} /> Redirect Configuration</h3>
                    <div className="space-y-3">
                      <FormField label="Target URL" htmlFor="add-redirect-target">
                        <input id="add-redirect-target" type="text" value={form.redirectTarget} onChange={e => patchField('redirectTarget', e.target.value)}
                          placeholder="https://new-domain.com" className={inputCls} />
                      </FormField>
                      <FormField label="Status Code" htmlFor="add-redirect-code">
                        <select id="add-redirect-code" value={form.redirectCode} onChange={e => patchField('redirectCode', e.target.value)} className={selectCls}>
                          {redirectCodes.map(c => <option key={c} value={c}>{c}</option>)}
                        </select>
                      </FormField>
                    </div>
                  </div>
                )}

                {/* Security section */}
                <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-4">
                  <h3 className="mb-3 flex items-center gap-2 text-sm font-semibold text-slate-300"><Shield size={14} /> Security</h3>
                  <div className="space-y-3">
                    <label className="flex items-center gap-2 text-sm text-slate-300">
                      <input type="checkbox" checked={form.wafEnabled} onChange={e => patchField('wafEnabled', e.target.checked)}
                        className="rounded border-[#334155] bg-[#1e293b] text-blue-600 focus:ring-blue-500" />
                      Enable WAF
                    </label>
                    <FormField label="Blocked Paths (comma-separated)" htmlFor="add-blocked-paths">
                      <input id="add-blocked-paths" type="text" value={form.blockedPaths} onChange={e => patchField('blockedPaths', e.target.value)}
                        placeholder=".env,.git,wp-config.php" className={inputCls} />
                    </FormField>
                  </div>
                </div>

                {/* Submit */}
                <div className="flex justify-end gap-3 pt-2">
                  <button type="button" onClick={() => { setShowAdd(false); setEditingHost(null); setPhpCustomInput(false); }}
                    className="rounded-md bg-[#334155] px-4 py-2 text-sm font-medium text-slate-300 transition hover:bg-[#475569]">
                    Cancel
                  </button>
                  <button type="submit" disabled={submitting || !form.host.trim()}
                    className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50">
                    {submitting ? (editingHost ? 'Updating...' : 'Adding...') : (editingHost ? 'Update Domain' : 'Add Domain')}
                  </button>
                </div>
              </form>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

/* ------------------------------------------------------------------ */
/*  Domain row + inline detail                                         */
/* ------------------------------------------------------------------ */

interface DomainRowProps {
  domain: DomainData;
  isExpanded: boolean;
  detail: DomainDetail | null;
  detailLoading: boolean;
  certInfo: CertInfo | null;
  confirmDelete: string | null;
  purgingHost: string | null;
  onToggle: () => void;
  onEdit: (host: string) => void;
  onDelete: (host: string) => void;
  onConfirmDelete: (host: string | null) => void;
  onPurge: (host: string) => void;
}

function DomainRow({
  domain: d,
  isExpanded,
  detail,
  detailLoading,
  certInfo,
  confirmDelete,
  purgingHost,
  onToggle,
  onEdit,
  onDelete,
  onConfirmDelete,
  onPurge,
}: DomainRowProps) {
  return (
    <>
      {/* Main row */}
      <tr
        onClick={onToggle}
        className={`cursor-pointer border-b border-[#334155]/50 text-slate-300 transition hover:bg-[#334155]/30 ${isExpanded ? 'bg-[#334155]/20' : ''}`}
      >
        <td className="px-3 py-3 text-slate-500">
          {isExpanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
        </td>
        <td className="px-5 py-3">
          <div className="flex items-center gap-2">
            <Globe size={14} className="text-slate-500" />
            <span className="font-mono text-xs">{d.host}</span>
          </div>
        </td>
        <td className="px-5 py-3"><TypeBadge type={d.type} /></td>
        <td className="px-5 py-3"><SslBadge ssl={d.ssl} /></td>
        <td className="px-5 py-3 font-mono text-xs text-slate-400">{d.root || '--'}</td>
        <td className="px-5 py-3"><StatusDot active={true} /></td>
        <td className="px-5 py-3">
          {confirmDelete === d.host ? (
            <div className="flex items-center gap-2" onClick={e => e.stopPropagation()}>
              <button onClick={() => onDelete(d.host)} className="rounded bg-red-600 px-2 py-1 text-xs font-medium text-white transition hover:bg-red-700">Confirm</button>
              <button onClick={() => onConfirmDelete(null)} className="rounded bg-[#334155] px-2 py-1 text-xs font-medium text-slate-300 transition hover:bg-[#475569]">Cancel</button>
            </div>
          ) : (
            <div className="flex items-center gap-1">
              <button
                onClick={e => { e.stopPropagation(); onEdit(d.host); }}
                className="rounded p-1.5 text-slate-500 transition hover:bg-blue-500/10 hover:text-blue-400"
                title="Edit domain"
              >
                <Pencil size={14} />
              </button>
              <button
                onClick={e => { e.stopPropagation(); onConfirmDelete(d.host); }}
                className="rounded p-1.5 text-slate-500 transition hover:bg-red-500/10 hover:text-red-400"
                title="Delete domain"
              >
                <Trash2 size={14} />
              </button>
            </div>
          )}
        </td>
      </tr>

      {/* Expanded detail panel */}
      {isExpanded && (
        <tr>
          <td colSpan={7} className="border-b border-[#334155] bg-[#0f172a]/60 p-0">
            <div className="px-6 py-5">
              {detailLoading ? (
                <div className="flex items-center gap-2 py-6 text-sm text-slate-500">
                  <RefreshCw size={14} className="animate-spin" />
                  Loading domain details...
                </div>
              ) : detail ? (
                <DomainDetailPanel detail={detail} certInfo={certInfo} purgingHost={purgingHost} onPurge={onPurge} onDelete={onDelete} onConfirmDelete={onConfirmDelete} confirmDelete={confirmDelete} />
              ) : (
                <p className="py-4 text-sm text-slate-500">Could not load domain details.</p>
              )}
            </div>
          </td>
        </tr>
      )}
    </>
  );
}

/* ------------------------------------------------------------------ */
/*  Domain detail panel (rendered inside expanded row)                  */
/* ------------------------------------------------------------------ */

interface DomainDetailPanelProps {
  detail: DomainDetail;
  certInfo: CertInfo | null;
  purgingHost: string | null;
  onPurge: (host: string) => void;
  onDelete: (host: string) => void;
  onConfirmDelete: (host: string | null) => void;
  confirmDelete: string | null;
}

function DomainDetailPanel({ detail, certInfo, purgingHost, onPurge }: DomainDetailPanelProps) {
  return (
    <div className="space-y-4">
      {/* Quick actions bar */}
      <div className="flex items-center gap-3">
        <button
          onClick={() => onPurge(detail.host)}
          disabled={purgingHost === detail.host}
          className="flex items-center gap-1.5 rounded-md bg-amber-600/15 px-3 py-1.5 text-xs font-medium text-amber-400 transition hover:bg-amber-600/25 disabled:opacity-50"
        >
          <Zap size={12} />
          {purgingHost === detail.host ? 'Purging...' : 'Purge Cache'}
        </button>
        <span className="text-xs text-slate-500">Host: <span className="font-mono text-slate-300">{detail.host}</span></span>
        {detail.aliases && detail.aliases.length > 0 && (
          <span className="text-xs text-slate-500">Aliases: <span className="font-mono text-slate-400">{detail.aliases.join(', ')}</span></span>
        )}
      </div>

      {/* Info cards grid */}
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">

        {/* SSL card */}
        <InfoCard icon={<Lock size={16} />} title="SSL / TLS">
          <DetailRow label="Mode" value={<SslBadge ssl={detail.ssl} />} />
          {certInfo ? (
            <>
              <DetailRow label="Status" value={
                <span className={`inline-flex items-center gap-1 text-xs ${certInfo.status === 'valid' ? 'text-emerald-400' : 'text-amber-400'}`}>
                  {certInfo.status === 'valid' ? <CheckCircle size={10} /> : <AlertTriangle size={10} />}
                  {certInfo.status}
                </span>
              } />
              <DetailRow label="Issuer" value={certInfo.issuer || '--'} />
            </>
          ) : (
            <DetailRow label="Certificate" value={<span className="text-slate-500">No cert info</span>} />
          )}
        </InfoCard>

        {/* Cache card */}
        <InfoCard icon={<Database size={16} />} title="Cache">
          {detail.cache ? (
            <>
              <DetailRow label="Enabled" value={
                <span className={`inline-flex items-center gap-1 text-xs ${detail.cache.enabled ? 'text-emerald-400' : 'text-slate-400'}`}>
                  {detail.cache.enabled ? <CheckCircle size={10} /> : <XCircle size={10} />}
                  {detail.cache.enabled ? 'Yes' : 'No'}
                </span>
              } />
              <DetailRow label="TTL" value={detail.cache.ttl > 0 ? `${detail.cache.ttl}s` : '--'} />
              {detail.cache.rules && detail.cache.rules.length > 0 && (
                <div className="mt-2 space-y-1">
                  <span className="text-xs font-medium uppercase text-slate-500">Rules</span>
                  {detail.cache.rules.map((r, i) => (
                    <div key={i} className="flex items-center gap-2 text-xs">
                      <span className="font-mono text-slate-400">{r.match}</span>
                      {r.bypass ? (
                        <span className="rounded bg-red-500/15 px-1.5 py-0.5 text-red-400">bypass</span>
                      ) : (
                        <span className="rounded bg-emerald-500/15 px-1.5 py-0.5 text-emerald-400">TTL:{r.ttl}s</span>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </>
          ) : (
            <p className="text-xs text-slate-500">Not configured</p>
          )}
        </InfoCard>

        {/* Security card */}
        <InfoCard icon={<Shield size={16} />} title="Security">
          {detail.security ? (
            <>
              <DetailRow label="WAF" value={
                <span className={`inline-flex items-center gap-1 text-xs ${detail.security.waf ? 'text-emerald-400' : 'text-slate-400'}`}>
                  {detail.security.waf ? <CheckCircle size={10} /> : <XCircle size={10} />}
                  {detail.security.waf ? 'Enabled' : 'Disabled'}
                </span>
              } />
              {detail.security.rate_limit && (
                <DetailRow label="Rate Limit" value={`${detail.security.rate_limit.requests}/${detail.security.rate_limit.window}`} />
              )}
              {detail.security.blocked_paths && detail.security.blocked_paths.length > 0 && (
                <div className="mt-2 space-y-1">
                  <span className="text-xs font-medium uppercase text-slate-500">Blocked Paths</span>
                  <div className="flex flex-wrap gap-1">
                    {detail.security.blocked_paths.map(p => (
                      <span key={p} className="rounded bg-red-500/15 px-1.5 py-0.5 font-mono text-xs text-red-400">{p}</span>
                    ))}
                  </div>
                </div>
              )}
            </>
          ) : (
            <p className="text-xs text-slate-500">Not configured</p>
          )}
        </InfoCard>

        {/* PHP card (only for php type) */}
        {detail.type === 'php' && detail.php && (
          <InfoCard icon={<FileCode size={16} />} title="PHP / FPM">
            <DetailRow label="FPM Address" value={<span className="font-mono">{detail.php.fpm_address}</span>} />
            <DetailRow label="Timeout" value={detail.php.timeout > 0 ? `${detail.php.timeout}s` : '--'} />
            <DetailRow label="Upload Max" value={detail.php.upload_max_size || '--'} />
            {detail.php.index_files && detail.php.index_files.length > 0 && (
              <DetailRow label="Index Files" value={detail.php.index_files.join(', ')} />
            )}
            {detail.htaccess && (
              <DetailRow label=".htaccess" value={
                <span className={`text-xs ${detail.htaccess.enabled ? 'text-emerald-400' : 'text-slate-400'}`}>
                  {detail.htaccess.enabled ? 'Imported' : 'Disabled'}
                </span>
              } />
            )}
          </InfoCard>
        )}

        {/* Proxy card (only for proxy type) */}
        {detail.type === 'proxy' && detail.proxy && (
          <InfoCard icon={<Link size={16} />} title="Reverse Proxy">
            <DetailRow label="Algorithm" value={detail.proxy.algorithm || '--'} />
            {detail.proxy.health_check && (
              <>
                <DetailRow label="Health Path" value={detail.proxy.health_check.path || '--'} />
                <DetailRow label="Check Interval" value={detail.proxy.health_check.interval || '--'} />
              </>
            )}
            {detail.proxy.upstreams && detail.proxy.upstreams.length > 0 && (
              <div className="mt-2 space-y-1">
                <span className="text-xs font-medium uppercase text-slate-500">Upstreams</span>
                {detail.proxy.upstreams.map((u, i) => (
                  <div key={i} className="flex items-center gap-2 text-xs">
                    <Server size={10} className="text-orange-400" />
                    <span className="font-mono text-slate-300">{u}</span>
                  </div>
                ))}
              </div>
            )}
          </InfoCard>
        )}

        {/* Redirect card (only for redirect type) */}
        {detail.type === 'redirect' && detail.redirect && (
          <InfoCard icon={<ArrowRight size={16} />} title="Redirect">
            <DetailRow label="Target" value={<span className="font-mono">{detail.redirect.target}</span>} />
            <DetailRow label="Status Code" value={
              <span className="rounded bg-slate-500/15 px-1.5 py-0.5 font-mono text-xs text-slate-300">{detail.redirect.status_code}</span>
            } />
          </InfoCard>
        )}
      </div>
    </div>
  );
}
