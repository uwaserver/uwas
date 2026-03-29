import { useState, useEffect, useCallback, type FormEvent, type ReactNode } from 'react';
import { Link as RouterLink } from 'react-router-dom';
import PinModal from '@/components/PinModal';
import { setPinCode, clearPinCode } from '@/lib/api';
import {
  X, Plus, Trash2, CheckCircle, XCircle, ChevronDown, ChevronRight,
  Shield, Lock, Database, Server, ArrowRight, FileCode, Zap, RefreshCw,
  AlertTriangle, Layers, Settings, Link, Pencil, ExternalLink, Box, Code, Cpu, Upload,
} from 'lucide-react';
import {
  fetchDomains, addDomain, updateDomain, deleteDomain, fetchDomainDetail, fetchCerts, triggerPurge,
  fetchPHP, fetchServerIPs, fetchDomainHealth,
  type DomainData, type DomainDetail, type CertInfo, type PHPInstall, type ServerIPInfo, type DomainHealth,
} from '@/lib/api';

/* ------------------------------------------------------------------ */
/*  Types                                                              */
/* ------------------------------------------------------------------ */

interface DomainFormState {
  host: string;
  ip: string;
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
  appRuntime: string;
  appCommand: string;
  appPort: string;
  appEnv: string;
  blockedPaths: string;
  wafEnabled: boolean;
  htaccessEnabled: boolean;
}

type TemplateName = 'wordpress' | 'laravel' | 'nodejs' | 'python' | 'static' | 'proxy' | 'redirect' | null;

/* ------------------------------------------------------------------ */
/*  Constants                                                          */
/* ------------------------------------------------------------------ */

const domainTypes = ['static', 'php', 'proxy', 'app', 'redirect'] as const;
const sslModes = ['auto', 'manual', 'off'] as const;
// proxyAlgorithms and redirectCodes are now rendered as visual cards inline

const emptyForm: DomainFormState = {
  host: '',
  ip: '',
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
  appRuntime: 'node',
  appCommand: '',
  appPort: '3000',
  appEnv: '',
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
  laravel: {
    label: 'Laravel',
    description: 'PHP with public/ root, .env blocked, OPcache',
    icon: <Code size={20} />,
    color: 'text-red-400 bg-red-500/15 border-red-500/30',
    form: {
      type: 'php',
      ssl: 'auto',
      root: '/var/www/html/public',
      htaccessEnabled: true,
      cacheEnabled: true,
      cacheTTL: '3600',
      phpFpmAddress: '127.0.0.1:9000',
      phpIndexFiles: 'index.php',
      wafEnabled: true,
      blockedPaths: '.env,.git,composer.json,composer.lock,storage/logs',
    },
  },
  nodejs: {
    label: 'Node.js App',
    description: 'Managed Node process — auto-start, crash restart, git deploy',
    icon: <Box size={20} />,
    color: 'text-green-400 bg-green-500/15 border-green-500/30',
    form: {
      type: 'app',
      ssl: 'auto',
      appRuntime: 'node',
      appCommand: 'npm start',
      appPort: '3000',
    },
  },
  python: {
    label: 'Python App',
    description: 'Managed Python process — Gunicorn/Django/Flask with git deploy',
    icon: <Cpu size={20} />,
    color: 'text-yellow-400 bg-yellow-500/15 border-yellow-500/30',
    form: {
      type: 'app',
      ssl: 'auto',
      appRuntime: 'python',
      appCommand: 'gunicorn app:app -b 0.0.0.0:${PORT}',
      appPort: '8000',
    },
  },
  redirect: {
    label: 'Redirect',
    description: 'Redirect all traffic to another URL',
    icon: <ArrowRight size={20} />,
    color: 'text-card-foreground bg-slate-500/15 border-slate-500/30',
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
  app: 'bg-green-500/15 text-green-400',
  redirect: 'bg-slate-500/15 text-muted-foreground',
};

function TypeBadge({ type }: { type: string }) {
  return (
    <span className={`rounded-full px-2.5 py-0.5 text-xs font-medium ${typeBadgeStyles[type] ?? 'bg-slate-500/15 text-muted-foreground'}`}>
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
    <span className={`rounded-full px-2.5 py-0.5 text-xs font-medium ${sslBadgeStyles[ssl] ?? 'bg-slate-500/15 text-muted-foreground'}`}>
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
    <div className="rounded-lg border border-border bg-background p-4">
      <div className="mb-3 flex items-center gap-2">
        <span className="text-muted-foreground">{icon}</span>
        <h4 className="text-sm font-semibold text-card-foreground">{title}</h4>
      </div>
      <div className="space-y-2 text-sm">{children}</div>
    </div>
  );
}

function DetailRow({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-4">
      <span className="shrink-0 text-xs font-medium uppercase text-muted-foreground">{label}</span>
      <span className="text-right text-xs text-foreground">{value}</span>
    </div>
  );
}

function FormField({ label, htmlFor, children }: { label: string; htmlFor: string; children: ReactNode }) {
  return (
    <div>
      <label htmlFor={htmlFor} className="mb-1.5 block text-sm font-medium text-card-foreground">{label}</label>
      {children}
    </div>
  );
}

const inputCls = 'w-full rounded-md border border-border bg-card px-3 py-2.5 text-sm text-foreground placeholder-slate-500 outline-none transition focus:border-blue-500 focus:ring-1 focus:ring-blue-500';
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

  /* server IPs for IP dropdown */
  const [serverIPs, setServerIPs] = useState<ServerIPInfo[]>([]);

  /* domain health status */
  const [healthMap, setHealthMap] = useState<Record<string, DomainHealth>>({});

  /* -------- data loading -------- */

  const loadDomains = useCallback(() => {
    fetchDomains()
      .then(d => setDomains(d ?? []))
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  const loadCerts = useCallback(() => {
    fetchCerts()
      .then(certs => {
        const map: Record<string, CertInfo> = {};
        for (const c of (certs ?? [])) map[c.host] = c;
        setCertMap(map);
      })
      .catch(() => {});
  }, []);

  const loadPHP = useCallback(() => {
    fetchPHP()
      .then(d => setPhpInstalls(d ?? []))
      .catch(() => {});
  }, []);

  const loadIPs = useCallback(() => {
    fetchServerIPs()
      .then(data => setServerIPs(data?.ips ?? []))
      .catch(() => {});
  }, []);

  const loadHealth = useCallback(() => {
    fetchDomainHealth()
      .then(results => {
        const map: Record<string, DomainHealth> = {};
        for (const h of (results ?? [])) map[h.host] = h;
        setHealthMap(map);
      })
      .catch(() => {});
  }, []);

  useEffect(() => {
    loadDomains();
    loadCerts();
    loadPHP();
    loadIPs();
    loadHealth();
    const hInterval = setInterval(loadHealth, 30000); // refresh health every 30s
    return () => clearInterval(hInterval);
  }, [loadDomains, loadCerts, loadPHP, loadIPs, loadHealth]);

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
          setDetail({ ...found, ssl: { mode: found.ssl, cert: '', key: '', min_version: '' }, cache: undefined, security: undefined, php: undefined, proxy: undefined, redirect: undefined, htaccess: undefined });
        }
      })
      .finally(() => setDetailLoading(false));
  };

  /* -------- delete -------- */

  const [cleanupOnDelete, setCleanupOnDelete] = useState(true);
  const [pinModalOpen, setPinModalOpen] = useState(false);
  const [pendingDeleteHost, setPendingDeleteHost] = useState('');
  const [justCreated, setJustCreated] = useState('');

  const handleDelete = async (host: string) => {
    setStatus(null);
    try {
      await deleteDomain(host, cleanupOnDelete);
      const msg = cleanupOnDelete
        ? `Domain "${host}" deleted with all files, PHP, and SFTP user`
        : `Domain "${host}" deleted (files kept)`;
      setStatus({ ok: true, message: msg });
      setConfirmDelete(null);
      setCleanupOnDelete(true);
      clearPinCode();
      if (expandedHost === host) { setExpandedHost(null); setDetail(null); }
      loadDomains();
    } catch (e) {
      const msg = (e as Error).message;
      if (msg === 'pin_required' || msg === 'invalid_pin') {
        setPendingDeleteHost(host);
        setPinModalOpen(true);
        return;
      }
      setStatus({ ok: false, message: msg });
      setConfirmDelete(null);
      clearPinCode();
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
      const tpl = { ...emptyForm, ...templates[name].form };
      // Use detected PHP address instead of hardcoded default
      if (tpl.type === 'php' && phpInstalls.length > 0) {
        const best = phpInstalls.find(p => p.sapi === 'fpm-fcgi') ?? phpInstalls.find(p => p.sapi !== 'cli');
        if (best?.listen_addr) tpl.phpFpmAddress = best.listen_addr;
      }
      setForm(tpl);
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
        ip: d.ip ?? '',
        type: d.type,
        root: d.root || '',
        ssl: d.ssl?.mode ?? 'off',
        cacheEnabled: d.cache?.enabled ?? false,
        cacheTTL: String(d.cache?.ttl ?? 3600),
        phpFpmAddress: d.php?.fpm_address ?? '',
        phpIndexFiles: d.php?.index_files?.join(', ') ?? 'index.php,index.html',
        proxyUpstreams: d.proxy?.upstreams?.join(', ') ?? '',
        proxyAlgorithm: d.proxy?.algorithm ?? 'round-robin',
        redirectTarget: d.redirect?.target ?? '',
        redirectCode: String(d.redirect?.status ?? 301),
        appRuntime: d.app?.runtime ?? 'node',
        appCommand: d.app?.command ?? '',
        appPort: String(d.app?.port ?? 3000),
        appEnv: '',
        blockedPaths: d.security?.blocked_paths?.join(', ') ?? '',
        wafEnabled: d.security?.waf?.enabled ?? false,
        htaccessEnabled: !!d.htaccess?.mode,
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

    /* Build API payload — minimal fields, backend fills defaults */
    const payload: Record<string, unknown> = {
      host: form.host.trim(),
      type: form.type,
      ssl: { mode: form.ssl },
    };

    if (form.ip) payload.ip = form.ip;

    // Only send type-specific fields
    if (form.type === 'proxy' && form.proxyUpstreams.trim()) {
      payload.proxy = {
        upstreams: form.proxyUpstreams.split(',').map(s => s.trim()).filter(Boolean).map(addr => ({ address: addr, weight: 1 })),
        algorithm: form.proxyAlgorithm || 'round-robin',
      };
    }

    if (form.type === 'redirect' && form.redirectTarget.trim()) {
      payload.redirect = {
        target: form.redirectTarget.trim(),
        status: parseInt(form.redirectCode, 10) || 301,
      };
    }

    if (form.type === 'php') {
      const php: Record<string, unknown> = {};
      if (form.phpFpmAddress.trim()) php.fpm_address = form.phpFpmAddress.trim();
      const idx = form.phpIndexFiles.split(',').map(s => s.trim()).filter(Boolean);
      if (idx.length > 0) php.index_files = idx;
      if (Object.keys(php).length > 0) payload.php = php;
      payload.htaccess = { mode: 'import' };
    }

    if (form.type === 'app') {
      const env: Record<string, string> = {};
      form.appEnv.split('\n').forEach(line => {
        const eq = line.indexOf('=');
        if (eq > 0) env[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
      });
      payload.app = {
        runtime: form.appRuntime || 'custom',
        command: form.appCommand || undefined,
        port: parseInt(form.appPort, 10) || 3000,
        auto_restart: true,
        env: Object.keys(env).length > 0 ? env : undefined,
      };
    }

    try {
      if (editingHost) {
        await updateDomain(editingHost, payload);
        setStatus({ ok: true, message: `Domain "${editingHost}" updated successfully` });
      } else {
        await addDomain(payload);
        setJustCreated(form.host.trim());
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
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">Domains</h1>
          <p className="text-sm text-muted-foreground">
            {loading ? 'Loading...' : `${domains.length} domain${domains.length !== 1 ? 's' : ''} configured`}
          </p>
        </div>
        <div className="flex gap-2">
          <button onClick={() => {
            const input = window.prompt('Paste domain list (one per line):');
            if (!input) return;
            const hosts = input.split('\n').map(h => h.trim()).filter(Boolean);
            if (hosts.length === 0) return;
            import('@/lib/api').then(({ bulkImportDomains }) => {
              bulkImportDomains(hosts.map(h => ({ host: h }))).then(res => {
                setStatus({ ok: true, message: `Added ${res.added.length}, skipped ${res.skipped.length}` });
                loadDomains();
              }).catch(e => setStatus({ ok: false, message: (e as Error).message }));
            });
          }} className="flex items-center gap-1.5 rounded-md border border-border bg-card px-3 py-2 text-sm text-card-foreground hover:bg-accent">
            <Upload size={14} /> Bulk Import
          </button>
          <button onClick={openAddModal}
            className="flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700">
            <Plus size={14} /> Add Domain
          </button>
        </div>
      </div>

      {/* Status toast */}
      {status && (
        <div className={`flex items-center gap-2 rounded-md px-4 py-3 text-sm ${status.ok ? 'bg-emerald-500/10 text-emerald-400' : 'bg-red-500/10 text-red-400'}`}>
          {status.ok ? <CheckCircle size={14} /> : <XCircle size={14} />}
          {status.message}
        </div>
      )}

      {/* Next Steps after domain creation */}
      {justCreated && (
        <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-5">
          <div className="flex items-center justify-between mb-3">
            <h3 className="text-sm font-semibold text-emerald-400">Domain Created: {justCreated}</h3>
            <button onClick={() => setJustCreated('')} className="text-xs text-muted-foreground hover:text-foreground">Dismiss</button>
          </div>
          <p className="text-xs text-muted-foreground mb-3">Complete these steps to get your site live:</p>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
            <div className="flex items-start gap-2 rounded-md bg-background p-3">
              <span className="flex h-5 w-5 items-center justify-center rounded-full bg-blue-500/15 text-[10px] font-bold text-blue-400 shrink-0">1</span>
              <div>
                <p className="text-xs font-medium text-foreground">Point DNS</p>
                <p className="text-[10px] text-muted-foreground">Add an A record for <code className="bg-accent px-1 rounded">{justCreated}</code> pointing to your server IP</p>
              </div>
            </div>
            <div className="flex items-start gap-2 rounded-md bg-background p-3">
              <span className="flex h-5 w-5 items-center justify-center rounded-full bg-purple-500/15 text-[10px] font-bold text-purple-400 shrink-0">2</span>
              <div>
                <p className="text-xs font-medium text-foreground">SSL Certificate</p>
                <p className="text-[10px] text-muted-foreground">Auto-issued via Let's Encrypt once DNS propagates (2-5 min)</p>
              </div>
            </div>
            <div className="flex items-start gap-2 rounded-md bg-background p-3">
              <span className="flex h-5 w-5 items-center justify-center rounded-full bg-emerald-500/15 text-[10px] font-bold text-emerald-400 shrink-0">3</span>
              <div>
                <p className="text-xs font-medium text-foreground">Upload Files</p>
                <p className="text-[10px] text-muted-foreground">Use File Manager, SFTP, or git deploy to upload your site</p>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Domain table */}
      <div className="rounded-lg border border-border bg-card shadow-md">
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-border text-muted-foreground">
                <th className="w-8 px-3 py-3" />
                <th className="px-5 py-3 font-medium">Host</th>
                <th className="px-5 py-3 font-medium">Type</th>
                <th className="px-5 py-3 font-medium">SSL</th>
                <th className="px-5 py-3 font-medium">IP</th>
                <th className="px-5 py-3 font-medium">Status</th>
                <th className="px-5 py-3 font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {loading && (
                <tr><td colSpan={7} className="px-5 py-8 text-center text-muted-foreground">Loading...</td></tr>
              )}
              {!loading && domains.length === 0 && (
                <tr><td colSpan={7} className="px-5 py-8 text-center text-muted-foreground">No domains configured</td></tr>
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
                    health={healthMap[d.host] ?? null}
                    confirmDelete={confirmDelete}
                    purgingHost={purgingHost}
                    cleanupOnDelete={cleanupOnDelete}
                    onToggle={() => toggleExpand(d.host)}
                    onEdit={startEdit}
                    onDelete={handleDelete}
                    onConfirmDelete={setConfirmDelete}
                    onPurge={handlePurgeDomain}
                    onCleanupChange={setCleanupOnDelete}
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
          <div className="relative z-10 w-full max-w-2xl rounded-xl border border-border bg-background p-6 shadow-2xl">
            {/* Modal header */}
            <div className="mb-5 flex items-center justify-between">
              <h2 className="text-lg font-bold text-foreground">{editingHost ? 'Edit Domain' : 'Add Domain'}</h2>
              <button onClick={() => { setShowAdd(false); setEditingHost(null); setPhpCustomInput(false); }} className="rounded-md p-1 text-muted-foreground hover:text-foreground">
                <X size={18} />
              </button>
            </div>

            {/* Template quick-add (hidden when editing) */}
            {!selectedTemplate && !editingHost && (
              <>
                <p className="mb-4 text-sm text-muted-foreground">Quick Add &mdash; choose a template or start from scratch</p>
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
                  className="text-xs text-muted-foreground underline hover:text-card-foreground"
                >
                  Skip template, start with blank form
                </button>
              </>
            )}

            {/* Full form (shown after template selection) */}
            {selectedTemplate !== null && (
              <form onSubmit={handleAdd} className="space-y-5">
                {/* Template indicator */}
                <div className="flex items-center gap-2 rounded-md bg-card px-3 py-2 text-xs text-muted-foreground">
                  <Settings size={12} />
                  {editingHost ? (
                    <>Editing: <span className="font-mono font-medium text-foreground">{editingHost}</span></>
                  ) : (
                    <>Template: <span className="font-medium text-foreground">{templates[selectedTemplate]?.label ?? 'Custom'}</span>
                    <button type="button" onClick={() => { setSelectedTemplate(null); setForm({ ...emptyForm }); }} className="ml-auto text-muted-foreground hover:text-card-foreground">Change</button></>
                  )}
                </div>

                {/* Host */}
                <FormField label="Host" htmlFor="add-host">
                  <input id="add-host" type="text" value={form.host} onChange={e => patchField('host', e.target.value)}
                    placeholder="example.com" required autoFocus disabled={!!editingHost}
                    className={`${inputCls}${editingHost ? ' opacity-60 cursor-not-allowed' : ''}`} />
                </FormField>

                {/* Type + SSL + IP row */}
                <div className="grid grid-cols-3 gap-4">
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
                  <FormField label="IP Address" htmlFor="add-ip">
                    <select id="add-ip" value={form.ip} onChange={e => patchField('ip', e.target.value)} className={selectCls}>
                      <option value="">Shared (all IPs)</option>
                      {serverIPs.filter(ip => ip.version === 4).map(ip => (
                        <option key={ip.ip} value={ip.ip}>
                          {ip.ip}{ip.primary ? ' (primary)' : ''} — {ip.interface}
                        </option>
                      ))}
                    </select>
                  </FormField>
                </div>

                {/* Root — auto-generated, shown as info */}
                {form.host.trim() && form.type !== 'proxy' && form.type !== 'redirect' && (
                  <div className="rounded-md bg-card border border-border px-3 py-2.5 text-sm">
                    <span className="text-xs text-muted-foreground">Web Root (auto-created)</span>
                    <p className="font-mono text-card-foreground text-xs mt-0.5">/var/www/{form.host.trim()}/public_html/</p>
                  </div>
                )}

                {/* Cache */}
                <div className="rounded-lg border border-border bg-card p-4">
                  <div className="flex items-center justify-between">
                    <span className="flex items-center gap-2 text-sm font-medium text-card-foreground"><Database size={14} /> Cache</span>
                    <label className="relative inline-flex cursor-pointer items-center">
                      <input type="checkbox" checked={form.cacheEnabled} onChange={e => patchField('cacheEnabled', e.target.checked)} className="peer sr-only" />
                      <div className="peer h-5 w-9 rounded-full bg-accent after:absolute after:left-[2px] after:top-[2px] after:h-4 after:w-4 after:rounded-full after:bg-slate-400 after:transition-all peer-checked:bg-blue-600 peer-checked:after:translate-x-full peer-checked:after:bg-white" />
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
                                className="shrink-0 rounded-md bg-accent px-3 py-2 text-xs font-medium text-card-foreground transition hover:bg-[#475569]">
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
                            {(() => {
                              // Group by major.minor, prefer FPM over CGI
                              const seen = new Map<string, typeof phpInstalls[0]>();
                              for (const p of phpInstalls.filter(p => p.sapi !== 'cli')) {
                                const major = p.version.split('.').slice(0, 2).join('.');
                                const existing = seen.get(major);
                                if (!existing || (p.sapi === 'fpm-fcgi' && existing.sapi !== 'fpm-fcgi')) {
                                  seen.set(major, p);
                                }
                              }
                              return [...seen.entries()].map(([major, p]) => {
                                const addr = p.listen_addr || '127.0.0.1:9000';
                                return (
                                  <option key={major} value={addr}>
                                    PHP {major}{p.sapi === 'fpm-fcgi' ? ' (FPM)' : ''}
                                  </option>
                                );
                              });
                            })()}
                            <option value="__custom__">Custom...</option>
                          </select>
                        )}
                      </FormField>
                      <FormField label="Index Files (comma-separated)" htmlFor="add-php-index">
                        <input id="add-php-index" type="text" value={form.phpIndexFiles} onChange={e => patchField('phpIndexFiles', e.target.value)}
                          placeholder="index.php,index.html" className={inputCls} />
                      </FormField>
                      <label className="flex items-center gap-2 text-sm text-card-foreground">
                        <input type="checkbox" checked={form.htaccessEnabled} onChange={e => patchField('htaccessEnabled', e.target.checked)}
                          className="rounded border-border bg-card text-blue-600 focus:ring-blue-500" />
                        Import .htaccess rules
                      </label>
                    </div>
                  </div>
                )}

                {/* Proxy section */}
                {form.type === 'proxy' && (
                  <div className="rounded-lg border border-orange-500/20 bg-orange-500/5 p-4 space-y-4">
                    <h3 className="flex items-center gap-2 text-sm font-semibold text-orange-400"><Server size={14} /> Reverse Proxy</h3>

                    {/* Visual routing */}
                    <div className="flex items-center gap-2 rounded-md bg-background/50 border border-border/50 px-3 py-2 text-[10px]">
                      <span className="font-mono text-foreground">{form.host || 'domain.com'}</span>
                      <ArrowRight size={10} className="text-muted-foreground" />
                      <span className="rounded bg-purple-500/15 px-1.5 py-0.5 text-purple-400 font-medium">UWAS</span>
                      <ArrowRight size={10} className="text-muted-foreground" />
                      <span className="rounded bg-orange-500/15 px-1.5 py-0.5 text-orange-400 font-mono">
                        {form.proxyUpstreams.split(',').filter(Boolean).length || 0} upstream{form.proxyUpstreams.split(',').filter(Boolean).length !== 1 ? 's' : ''}
                      </span>
                    </div>

                    {/* Upstream list — row-based */}
                    <div>
                      <label className="text-xs font-medium text-muted-foreground mb-2 block">Upstreams</label>
                      {(() => {
                        const ups = form.proxyUpstreams ? form.proxyUpstreams.split(',').map(s => s.trim()).filter(Boolean) : [];
                        if (ups.length === 0) ups.push('');
                        const update = (newUps: string[]) => patchField('proxyUpstreams', newUps.filter(Boolean).join(','));
                        return (
                          <div className="space-y-1.5">
                            {ups.map((u, i) => (
                              <div key={i} className="flex gap-1.5 items-center">
                                <input value={u} onChange={e => { ups[i] = e.target.value; update(ups); }}
                                  placeholder="http://127.0.0.1:3000" className={inputCls + ' font-mono text-xs flex-1'} />
                                <button type="button" onClick={() => { ups.splice(i, 1); if (ups.length === 0) ups.push(''); update(ups); }}
                                  className="rounded p-1.5 text-muted-foreground hover:text-red-400 hover:bg-red-500/10 shrink-0">
                                  <X size={12} />
                                </button>
                              </div>
                            ))}
                            <button type="button" onClick={() => { ups.push(''); update(ups); }}
                              className="flex items-center gap-1 rounded border border-dashed border-border px-2.5 py-1.5 text-[10px] text-muted-foreground hover:text-foreground hover:border-foreground/30 transition-colors w-full justify-center">
                              <Plus size={10} /> Add Upstream
                            </button>
                          </div>
                        );
                      })()}
                    </div>

                    {/* Algorithm */}
                    <div>
                      <label className="text-xs font-medium text-muted-foreground mb-2 block">Load Balancing</label>
                      <div className="grid grid-cols-4 gap-2">
                        {[
                          { value: 'round-robin', label: 'Round Robin', desc: 'Rotate evenly' },
                          { value: 'least-conn', label: 'Least Conn', desc: 'Fewest active' },
                          { value: 'ip-hash', label: 'IP Hash', desc: 'Session affinity' },
                          { value: 'random', label: 'Random', desc: 'Power of 2' },
                        ].map(algo => (
                          <button key={algo.value} type="button" onClick={() => patchField('proxyAlgorithm', algo.value)}
                            className={`rounded-lg border p-2 text-center transition ${
                              form.proxyAlgorithm === algo.value
                                ? 'border-orange-500/50 bg-orange-500/10 ring-1 ring-orange-500/30'
                                : 'border-border hover:border-foreground/20'
                            }`}>
                            <p className={`text-xs font-medium ${form.proxyAlgorithm === algo.value ? 'text-orange-400' : 'text-foreground'}`}>{algo.label}</p>
                            <p className="text-[9px] text-muted-foreground">{algo.desc}</p>
                          </button>
                        ))}
                      </div>
                    </div>
                  </div>
                )}

                {/* App section */}
                {form.type === 'app' && (
                  <div className="rounded-lg border border-green-500/20 bg-green-500/5 p-4 space-y-4">
                    <h3 className="flex items-center gap-2 text-sm font-semibold text-green-400"><Box size={14} /> Application Configuration</h3>

                    {/* Visual routing diagram */}
                    <div className="flex items-center gap-2 rounded-md bg-background/50 border border-border/50 px-3 py-2 text-[10px]">
                      <span className="font-mono text-foreground">{form.host || 'domain.com'}</span>
                      <ArrowRight size={10} className="text-muted-foreground" />
                      <span className="rounded bg-purple-500/15 px-1.5 py-0.5 text-purple-400 font-medium">UWAS</span>
                      <ArrowRight size={10} className="text-muted-foreground" />
                      <span className="rounded bg-green-500/15 px-1.5 py-0.5 text-green-400 font-mono">127.0.0.1:{form.appPort || '3000'}</span>
                    </div>

                    {/* Runtime selector — visual cards */}
                    <div>
                      <label className="text-xs font-medium text-muted-foreground mb-2 block">Runtime</label>
                      <div className="grid grid-cols-5 gap-2">
                        {[
                          { value: 'node', label: 'Node.js', icon: 'N', color: 'green', cmd: 'npm start', port: '3000' },
                          { value: 'python', label: 'Python', icon: 'Py', color: 'yellow', cmd: 'gunicorn app:app -b 0.0.0.0:${PORT}', port: '8000' },
                          { value: 'ruby', label: 'Ruby', icon: 'Rb', color: 'red', cmd: 'bundle exec puma -p ${PORT}', port: '3000' },
                          { value: 'go', label: 'Go', icon: 'Go', color: 'cyan', cmd: './main', port: '8080' },
                          { value: 'custom', label: 'Custom', icon: '?', color: 'slate', cmd: '', port: '3000' },
                        ].map(rt => (
                          <button key={rt.value} type="button"
                            onClick={() => {
                              patchField('appRuntime', rt.value);
                              if (!form.appCommand || form.appCommand === 'npm start' || form.appCommand === 'gunicorn app:app -b 0.0.0.0:${PORT}' || form.appCommand === 'bundle exec puma -p ${PORT}' || form.appCommand === './main') {
                                patchField('appCommand', rt.cmd);
                              }
                              if (form.appPort === '3000' || form.appPort === '8000' || form.appPort === '8080') {
                                patchField('appPort', rt.port);
                              }
                            }}
                            className={`flex flex-col items-center gap-1 rounded-lg border p-2.5 transition ${
                              form.appRuntime === rt.value
                                ? `border-${rt.color}-500/50 bg-${rt.color}-500/10 ring-1 ring-${rt.color}-500/30`
                                : 'border-border hover:border-foreground/20'
                            }`}>
                            <span className={`text-xs font-bold ${form.appRuntime === rt.value ? `text-${rt.color}-400` : 'text-muted-foreground'}`}>{rt.icon}</span>
                            <span className="text-[10px] text-muted-foreground">{rt.label}</span>
                          </button>
                        ))}
                      </div>
                    </div>

                    {/* Command + Port */}
                    <div className="grid grid-cols-[1fr_100px] gap-3">
                      <FormField label="Start Command" htmlFor="add-app-cmd">
                        <input id="add-app-cmd" type="text" value={form.appCommand} onChange={e => patchField('appCommand', e.target.value)}
                          placeholder="Leave empty for auto-detection" className={inputCls + ' font-mono text-xs'} />
                        <p className="mt-1 text-[9px] text-muted-foreground">
                          {form.appRuntime === 'node' && 'Detected from package.json scripts.start or server.js/index.js'}
                          {form.appRuntime === 'python' && 'Detected from manage.py, app.py, or requirements.txt'}
                          {form.appRuntime === 'ruby' && 'Detected from config.ru (Puma) or Gemfile'}
                          {form.appRuntime === 'go' && 'Runs compiled binary from project root'}
                          {form.appRuntime === 'custom' && 'Specify the exact command to run your application'}
                        </p>
                      </FormField>
                      <FormField label="Port" htmlFor="add-app-port">
                        <input id="add-app-port" type="number" value={form.appPort} onChange={e => patchField('appPort', e.target.value)}
                          placeholder="3000" className={inputCls} />
                      </FormField>
                    </div>

                    {/* Environment Variables — key-value rows */}
                    <div>
                      <label className="text-xs font-medium text-muted-foreground mb-2 block">Environment Variables</label>
                      {(() => {
                        const rows = form.appEnv ? form.appEnv.split('\n').map(line => {
                          const eq = line.indexOf('=');
                          return eq > 0 ? { key: line.slice(0, eq), value: line.slice(eq + 1) } : { key: line, value: '' };
                        }) : [];
                        if (rows.length === 0) rows.push({ key: '', value: '' });

                        const updateEnv = (newRows: typeof rows) => {
                          patchField('appEnv', newRows.filter(r => r.key || r.value).map(r => `${r.key}=${r.value}`).join('\n'));
                        };

                        return (
                          <div className="space-y-1.5">
                            {rows.map((row, i) => (
                              <div key={i} className="grid grid-cols-[1fr_1fr_28px] gap-1.5 items-center">
                                <input value={row.key} onChange={e => { rows[i] = { ...row, key: e.target.value }; updateEnv(rows); }}
                                  placeholder="KEY" className={inputCls + ' font-mono text-xs py-1.5'} />
                                <input value={row.value} onChange={e => { rows[i] = { ...row, value: e.target.value }; updateEnv(rows); }}
                                  placeholder="value" className={inputCls + ' font-mono text-xs py-1.5'} />
                                <button type="button" onClick={() => { rows.splice(i, 1); if (rows.length === 0) rows.push({ key: '', value: '' }); updateEnv(rows); }}
                                  className="rounded p-1 text-muted-foreground hover:text-red-400 hover:bg-red-500/10">
                                  <X size={12} />
                                </button>
                              </div>
                            ))}
                            <button type="button" onClick={() => { rows.push({ key: '', value: '' }); updateEnv(rows); }}
                              className="flex items-center gap-1 rounded border border-dashed border-border px-2.5 py-1.5 text-[10px] text-muted-foreground hover:text-foreground hover:border-foreground/30 transition-colors w-full justify-center">
                              <Plus size={10} /> Add Variable
                            </button>
                          </div>
                        );
                      })()}
                    </div>

                    <div className="rounded-md bg-background/50 border border-border/50 px-3 py-2 text-[10px] text-muted-foreground">
                      Process auto-starts on domain creation. Use the <span className="text-foreground font-medium">Apps</span> page to deploy from Git, manage builds, and monitor resources.
                    </div>
                  </div>
                )}

                {/* Redirect section */}
                {form.type === 'redirect' && (
                  <div className="rounded-lg border border-slate-500/20 bg-slate-500/5 p-4 space-y-4">
                    <h3 className="flex items-center gap-2 text-sm font-semibold text-card-foreground"><ArrowRight size={14} /> Redirect</h3>

                    {/* Visual flow */}
                    <div className="flex items-center gap-2 rounded-md bg-background/50 border border-border/50 px-3 py-2 text-[10px]">
                      <span className="font-mono text-foreground">{form.host || 'domain.com'}</span>
                      <ArrowRight size={10} className="text-muted-foreground" />
                      <span className="rounded bg-blue-500/15 px-1.5 py-0.5 text-blue-400 font-medium">{form.redirectCode}</span>
                      <ArrowRight size={10} className="text-muted-foreground" />
                      <span className="font-mono text-foreground truncate max-w-[200px]">{form.redirectTarget || 'https://...'}</span>
                    </div>

                    <FormField label="Target URL" htmlFor="add-redirect-target">
                      <input id="add-redirect-target" type="url" value={form.redirectTarget} onChange={e => patchField('redirectTarget', e.target.value)}
                        placeholder="https://new-domain.com" className={inputCls} />
                    </FormField>

                    <div>
                      <label className="text-xs font-medium text-muted-foreground mb-2 block">Status Code</label>
                      <div className="grid grid-cols-4 gap-2">
                        {[
                          { code: '301', label: 'Permanent', desc: 'SEO-friendly move' },
                          { code: '302', label: 'Temporary', desc: 'May change back' },
                          { code: '307', label: 'Temp (strict)', desc: 'Keeps POST method' },
                          { code: '308', label: 'Perm (strict)', desc: 'Keeps POST method' },
                        ].map(r => (
                          <button key={r.code} type="button" onClick={() => patchField('redirectCode', r.code)}
                            className={`rounded-lg border p-2 text-center transition ${
                              form.redirectCode === r.code ? 'border-blue-500/50 bg-blue-500/10 ring-1 ring-blue-500/30' : 'border-border hover:border-foreground/20'
                            }`}>
                            <p className={`text-sm font-bold ${form.redirectCode === r.code ? 'text-blue-400' : 'text-foreground'}`}>{r.code}</p>
                            <p className={`text-[10px] ${form.redirectCode === r.code ? 'text-blue-400' : 'text-muted-foreground'}`}>{r.label}</p>
                            <p className="text-[9px] text-muted-foreground">{r.desc}</p>
                          </button>
                        ))}
                      </div>
                    </div>
                  </div>
                )}

                {/* Security section */}
                <div className="rounded-lg border border-border bg-card p-4 space-y-3">
                  <h3 className="flex items-center gap-2 text-sm font-semibold text-card-foreground"><Shield size={14} /> Security</h3>
                  <div className="grid grid-cols-2 gap-3">
                    <button type="button" onClick={() => patchField('wafEnabled', !form.wafEnabled)}
                      className={`flex items-center gap-3 rounded-lg border p-3 text-left transition ${
                        form.wafEnabled ? 'border-emerald-500/50 bg-emerald-500/10' : 'border-border hover:border-foreground/20'
                      }`}>
                      <Shield size={16} className={form.wafEnabled ? 'text-emerald-400' : 'text-muted-foreground'} />
                      <div>
                        <p className={`text-xs font-medium ${form.wafEnabled ? 'text-emerald-400' : 'text-foreground'}`}>WAF Protection</p>
                        <p className="text-[9px] text-muted-foreground">SQL injection, XSS, shell detection</p>
                      </div>
                    </button>
                    <button type="button" onClick={() => patchField('htaccessEnabled', !form.htaccessEnabled)}
                      className={`flex items-center gap-3 rounded-lg border p-3 text-left transition ${
                        form.htaccessEnabled ? 'border-blue-500/50 bg-blue-500/10' : 'border-border hover:border-foreground/20'
                      }`}>
                      <FileCode size={16} className={form.htaccessEnabled ? 'text-blue-400' : 'text-muted-foreground'} />
                      <div>
                        <p className={`text-xs font-medium ${form.htaccessEnabled ? 'text-blue-400' : 'text-foreground'}`}>.htaccess Import</p>
                        <p className="text-[9px] text-muted-foreground">Apache mod_rewrite rules</p>
                      </div>
                    </button>
                  </div>
                  {form.blockedPaths && (
                    <div className="text-[10px] text-muted-foreground">
                      Blocked: <code className="bg-accent px-1 rounded">{form.blockedPaths}</code>
                    </div>
                  )}
                </div>

                {/* Submit */}
                <div className="flex justify-end gap-3 pt-2">
                  <button type="button" onClick={() => { setShowAdd(false); setEditingHost(null); setPhpCustomInput(false); }}
                    className="rounded-md bg-accent px-4 py-2 text-sm font-medium text-card-foreground transition hover:bg-[#475569]">
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
      {/* Pin Code Modal */}
      <PinModal
        open={pinModalOpen}
        title="Delete Domain"
        message={`Enter pin code to delete "${pendingDeleteHost}".`}
        onCancel={() => { setPinModalOpen(false); clearPinCode(); }}
        onConfirm={(pin) => {
          setPinModalOpen(false);
          setPinCode(pin);
          handleDelete(pendingDeleteHost);
        }}
      />
    </div>
  );
}

/* ------------------------------------------------------------------ */
/*  Domain row + inline detail                                         */
/* ------------------------------------------------------------------ */

function DomainRow({
  domain: d,
  isExpanded,
  detail,
  detailLoading,
  certInfo,
  health,
  confirmDelete,
  purgingHost,
  cleanupOnDelete,
  onToggle,
  onEdit,
  onDelete,
  onConfirmDelete,
  onPurge,
  onCleanupChange,
}: any) {
  return (
    <>
      {/* Main row */}
      <tr
        onClick={onToggle}
        className={`cursor-pointer border-b border-border/50 text-card-foreground transition hover:bg-accent/30 ${isExpanded ? 'bg-accent/20' : ''}`}
      >
        <td className="px-3 py-3 text-muted-foreground">
          {isExpanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
        </td>
        <td className="px-5 py-3">
          <div className="flex items-center gap-2">
            {health ? (
              <span className={`inline-block h-2.5 w-2.5 rounded-full shrink-0 ${
                health.healthy ? 'bg-emerald-400' : 'bg-red-400 animate-pulse'
              }`} title={health.healthy ? `Up (${health.response_time_ms}ms)` : `Down: ${health.error}`} />
            ) : (
              <span className="inline-block h-2.5 w-2.5 rounded-full bg-slate-500 shrink-0" title="Health unknown" />
            )}
            <span className="font-mono text-xs">{d.host}</span>
            {health && !health.healthy && (
              <span className="rounded bg-red-500/15 px-1.5 py-0.5 text-[9px] font-medium text-red-400">DOWN</span>
            )}
            {health?.response_time_ms != null && health.healthy && (
              <span className="text-[9px] text-muted-foreground">{health.response_time_ms}ms</span>
            )}
            <RouterLink to={`/domains/${encodeURIComponent(d.host)}`} onClick={e => e.stopPropagation()}
              className="rounded px-1.5 py-0.5 text-[10px] font-medium text-blue-400 hover:bg-blue-500/10 flex items-center gap-0.5"
              title="Manage domain">
              Manage <ExternalLink size={9} />
            </RouterLink>
          </div>
        </td>
        <td className="px-5 py-3"><TypeBadge type={d.type} /></td>
        <td className="px-5 py-3"><SslBadge ssl={d.ssl} /></td>
        <td className="px-5 py-3 font-mono text-xs text-muted-foreground">{d.ip || 'shared'}</td>
        <td className="px-5 py-3">
          {health ? (
            <div className="flex items-center gap-1.5">
              <StatusDot active={health.status === 'up'} />
              {health.ms > 0 && <span className="text-[10px] text-muted-foreground">{health.ms}ms</span>}
            </div>
          ) : (
            <span className="inline-block h-2 w-2 rounded-full bg-slate-600" title="Checking..." />
          )}
        </td>
        <td className="px-5 py-3">
          {confirmDelete === d.host ? (
            <div className="flex flex-col items-end gap-1.5" onClick={e => e.stopPropagation()}>
              <label className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
                <input type="checkbox" checked={cleanupOnDelete} onChange={e => onCleanupChange(e.target.checked)}
                  className="rounded border-border bg-card text-red-600" />
                Delete files + PHP + SFTP user
              </label>
              <div className="flex items-center gap-2">
                <button onClick={() => onDelete(d.host)} className="rounded bg-red-600 px-2 py-1 text-xs font-medium text-white transition hover:bg-red-700">Delete</button>
                <button onClick={() => onConfirmDelete(null)} className="rounded bg-accent px-2 py-1 text-xs font-medium text-card-foreground transition hover:bg-[#475569]">Cancel</button>
              </div>
            </div>
          ) : (
            <div className="flex items-center gap-1">
              <button
                onClick={e => { e.stopPropagation(); onEdit(d.host); }}
                className="rounded p-1.5 text-muted-foreground transition hover:bg-blue-500/10 hover:text-blue-400"
                title="Edit domain"
              >
                <Pencil size={14} />
              </button>
              {!/^(localhost|127\.0\.0\.1)(:\d+)?$/.test(d.host) && (
                <button
                  onClick={e => { e.stopPropagation(); onConfirmDelete(d.host); }}
                  className="rounded p-1.5 text-muted-foreground transition hover:bg-red-500/10 hover:text-red-400"
                  title="Delete domain"
                >
                  <Trash2 size={14} />
                </button>
              )}
            </div>
          )}
        </td>
      </tr>

      {/* Expanded detail panel */}
      {isExpanded && (
        <tr>
          <td colSpan={7} className="border-b border-border bg-background/60 p-0">
            <div className="px-6 py-5">
              {detailLoading ? (
                <div className="flex items-center gap-2 py-6 text-sm text-muted-foreground">
                  <RefreshCw size={14} className="animate-spin" />
                  Loading domain details...
                </div>
              ) : detail ? (
                <DomainDetailPanel detail={detail} certInfo={certInfo} purgingHost={purgingHost} onPurge={onPurge} onDelete={onDelete} onConfirmDelete={onConfirmDelete} confirmDelete={confirmDelete} />
              ) : (
                <p className="py-4 text-sm text-muted-foreground">Could not load domain details.</p>
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
        <span className="text-xs text-muted-foreground">Host: <span className="font-mono text-card-foreground">{detail.host}</span></span>
        {detail.aliases && detail.aliases.length > 0 && (
          <span className="text-xs text-muted-foreground">Aliases: <span className="font-mono text-muted-foreground">{detail.aliases.join(', ')}</span></span>
        )}
      </div>

      {/* Info cards grid */}
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">

        {/* SSL card */}
        <InfoCard icon={<Lock size={16} />} title="SSL / TLS">
          <DetailRow label="Mode" value={<SslBadge ssl={detail.ssl?.mode ?? 'off'} />} />
          {certInfo ? (
            <>
              <DetailRow label="Status" value={
                <span className={`inline-flex items-center gap-1 text-xs ${certInfo.status === 'active' ? 'text-emerald-400' : 'text-amber-400'}`}>
                  {certInfo.status === 'active' ? <CheckCircle size={10} /> : <AlertTriangle size={10} />}
                  {certInfo.status}
                </span>
              } />
              <DetailRow label="Issuer" value={certInfo.issuer || '--'} />
            </>
          ) : (
            <DetailRow label="Certificate" value={<span className="text-muted-foreground">No cert info</span>} />
          )}
        </InfoCard>

        {/* Cache card */}
        <InfoCard icon={<Database size={16} />} title="Cache">
          {detail.cache ? (
            <>
              <DetailRow label="Enabled" value={
                <span className={`inline-flex items-center gap-1 text-xs ${detail.cache.enabled ? 'text-emerald-400' : 'text-muted-foreground'}`}>
                  {detail.cache.enabled ? <CheckCircle size={10} /> : <XCircle size={10} />}
                  {detail.cache.enabled ? 'Yes' : 'No'}
                </span>
              } />
              <DetailRow label="TTL" value={detail.cache.ttl > 0 ? `${detail.cache.ttl}s` : '--'} />
              {detail.cache.rules && detail.cache.rules.length > 0 && (
                <div className="mt-2 space-y-1">
                  <span className="text-xs font-medium uppercase text-muted-foreground">Rules</span>
                  {detail.cache.rules.map((r, i) => (
                    <div key={i} className="flex items-center gap-2 text-xs">
                      <span className="font-mono text-muted-foreground">{r.match}</span>
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
            <p className="text-xs text-muted-foreground">Not configured</p>
          )}
        </InfoCard>

        {/* Security card */}
        <InfoCard icon={<Shield size={16} />} title="Security">
          {detail.security ? (
            <>
              <DetailRow label="WAF" value={
                <span className={`inline-flex items-center gap-1 text-xs ${detail.security.waf?.enabled ? 'text-emerald-400' : 'text-muted-foreground'}`}>
                  {detail.security.waf?.enabled ? <CheckCircle size={10} /> : <XCircle size={10} />}
                  {detail.security.waf?.enabled ? 'Enabled' : 'Disabled'}
                </span>
              } />
              {detail.security.rate_limit && (
                <DetailRow label="Rate Limit" value={`${detail.security.rate_limit.requests}/${detail.security.rate_limit.window}`} />
              )}
              {detail.security.blocked_paths && detail.security.blocked_paths.length > 0 && (
                <div className="mt-2 space-y-1">
                  <span className="text-xs font-medium uppercase text-muted-foreground">Blocked Paths</span>
                  <div className="flex flex-wrap gap-1">
                    {detail.security.blocked_paths.map(p => (
                      <span key={p} className="rounded bg-red-500/15 px-1.5 py-0.5 font-mono text-xs text-red-400">{p}</span>
                    ))}
                  </div>
                </div>
              )}
            </>
          ) : (
            <p className="text-xs text-muted-foreground">Not configured</p>
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
                <span className={`text-xs ${detail.htaccess.mode ? 'text-emerald-400' : 'text-muted-foreground'}`}>
                  {detail.htaccess.mode || 'Disabled'}
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
                <span className="text-xs font-medium uppercase text-muted-foreground">Upstreams</span>
                {detail.proxy.upstreams.map((u, i) => (
                  <div key={i} className="flex items-center gap-2 text-xs">
                    <Server size={10} className="text-orange-400" />
                    <span className="font-mono text-card-foreground">{u}</span>
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
              <span className="rounded bg-slate-500/15 px-1.5 py-0.5 font-mono text-xs text-card-foreground">{detail.redirect.status}</span>
            } />
          </InfoCard>
        )}
      </div>
    </div>
  );
}
