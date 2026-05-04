import { useState, useEffect, useCallback } from 'react';
import { Link as RouterLink } from 'react-router-dom';
import {
  Waypoints,
  RefreshCw,
  Search,
  CheckCircle,
  XCircle,
  Globe,
  Server,
  AlertTriangle,
  Plus,
  Trash2,
  Cloud,
  Settings,
  Shield,
  Pencil,
  Check,
  X,
} from 'lucide-react';
import {
  fetchDomains,
  checkDNS,
  fetchDNSRecords,
  createDNSRecord,
  updateDNSRecord,
  deleteDNSRecord,
  syncDNS,
  type DomainData,
  type DNSResult,
  type DNSRecord,
} from '@/lib/api';

/* -- Record Row ---------------------------------------------------------- */

function RecordSection({
  label,
  records,
}: {
  label: string;
  records: string[] | undefined;
}) {
  if (!records || records.length === 0) return null;
  return (
    <div>
      <h4 className="mb-1.5 text-xs font-medium uppercase text-muted-foreground">{label}</h4>
      <div className="space-y-1">
        {records.map((r, i) => (
          <div
            key={`${label}-${i}`}
            className="rounded-md bg-background px-3 py-2 font-mono text-sm text-card-foreground"
          >
            {r}
          </div>
        ))}
      </div>
    </div>
  );
}

/* -- Main Page ----------------------------------------------------------- */

export default function DNS() {
  const [domains, setDomains] = useState<DomainData[]>([]);
  const [selectedDomain, setSelectedDomain] = useState('');
  const [result, setResult] = useState<DNSResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [domainsLoading, setDomainsLoading] = useState(true);
  const [error, setError] = useState('');

  /* -- DNS Record Management State ---------------------------------------- */
  const [cfRecords, setCfRecords] = useState<DNSRecord[]>([]);
  const [cfLoading, setCfLoading] = useState(false);
  const [cfError, setCfError] = useState('');
  const [cfNotConfigured, setCfNotConfigured] = useState(false);
  const [syncLoading, setSyncLoading] = useState(false);
  const [syncMsg, setSyncMsg] = useState('');
  const [deleteLoading, setDeleteLoading] = useState<string | null>(null);
  const [showAddForm, setShowAddForm] = useState(false);
  const [newRec, setNewRec] = useState({ type: 'A', name: '', content: '', ttl: '1', proxied: false, priority: 0 });
  const [addLoading, setAddLoading] = useState(false);
  const [editId, setEditId] = useState<string | null>(null);
  const [editRec, setEditRec] = useState({ content: '', ttl: '1', proxied: false });
  const [editLoading, setEditLoading] = useState(false);

  const loadDomains = useCallback(async () => {
    try {
      const data = await fetchDomains();
      setDomains(data ?? []);
      const urlDomain = new URLSearchParams(window.location.search).get('domain');
      if (urlDomain) {
        setSelectedDomain(urlDomain);
      } else if (data && data.length > 0) {
        setSelectedDomain(prev => prev || data[0].host);
      }
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setDomainsLoading(false);
    }
  }, []);

  useEffect(() => {
    loadDomains();
  }, [loadDomains]);

  // Auto-load records when domain comes from ?domain= URL param.
  const [autoLoadDone, setAutoLoadDone] = useState(false);
  useEffect(() => {
    if (autoLoadDone || !selectedDomain) return;
    const urlDomain = new URLSearchParams(window.location.search).get('domain');
    if (urlDomain && urlDomain === selectedDomain) {
      setAutoLoadDone(true);
      handleLoadRecords();
    }
  }, [selectedDomain, autoLoadDone]);

  const handleCheck = async () => {
    if (!selectedDomain) return;
    setLoading(true);
    setError('');
    setResult(null);
    try {
      const data = await checkDNS(selectedDomain);
      setResult(data);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  };

  /* -- DNS Record Management Handlers ------------------------------------- */

  const handleLoadRecords = async () => {
    if (!selectedDomain) return;
    setCfLoading(true);
    setCfError('');
    setCfNotConfigured(false);
    setCfRecords([]);
    setSyncMsg('');
    try {
      const data = await fetchDNSRecords(selectedDomain);
      setCfRecords(data.records ?? []);
    } catch (e) {
      const msg = (e as Error).message;
      if (msg.includes('501') || msg.toLowerCase().includes('not configured') || msg.toLowerCase().includes('no dns provider')) {
        setCfNotConfigured(true);
      } else {
        setCfError(msg);
      }
    } finally {
      setCfLoading(false);
    }
  };

  const handleSyncDNS = async () => {
    if (!selectedDomain) return;
    setSyncLoading(true);
    setSyncMsg('');
    setCfError('');
    try {
      const data = await syncDNS(selectedDomain);
      setSyncMsg(`A record synced to ${data.ip}`);
      await handleLoadRecords();
    } catch (e) {
      const msg = (e as Error).message;
      if (msg.includes('501') || msg.toLowerCase().includes('not configured') || msg.toLowerCase().includes('no dns provider')) {
        setCfNotConfigured(true);
      } else {
        setCfError(msg);
      }
    } finally {
      setSyncLoading(false);
    }
  };

  const handleDeleteRecord = async (id: string) => {
    if (!selectedDomain) return;
    setDeleteLoading(id);
    setCfError('');
    try {
      await deleteDNSRecord(selectedDomain, id);
      setCfRecords((prev) => prev.filter((r) => r.id !== id));
    } catch (e) {
      setCfError((e as Error).message);
    } finally {
      setDeleteLoading(null);
    }
  };

  const startEdit = (rec: DNSRecord) => {
    setEditId(rec.id);
    setEditRec({ content: rec.content, ttl: rec.ttl === 1 ? '1' : String(rec.ttl), proxied: rec.proxied });
  };

  const handleUpdateRecord = async (id: string, type: string, name: string) => {
    if (!selectedDomain) return;
    setEditLoading(true);
    setCfError('');
    try {
      const updated = await updateDNSRecord(selectedDomain, id, {
        type, name,
        content: editRec.content,
        ttl: Number(editRec.ttl) || 1,
        proxied: editRec.proxied,
      });
      setCfRecords(prev => prev.map(r => r.id === id ? updated : r));
      setEditId(null);
    } catch (e) {
      setCfError((e as Error).message);
    } finally {
      setEditLoading(false);
    }
  };

  const handleCreateRecord = async () => {
    if (!selectedDomain || !newRec.name || !newRec.content) return;
    setAddLoading(true);
    setCfError('');
    try {
      const rec = await createDNSRecord(selectedDomain, {
        type: newRec.type,
        name: newRec.name,
        content: newRec.content,
        ttl: Number(newRec.ttl) || 1,
        proxied: newRec.proxied,
        priority: newRec.type === 'MX' || newRec.type === 'SRV' ? newRec.priority : 0,
      });
      setCfRecords((prev) => [...prev, rec]);
      setNewRec({ type: 'A', name: '', content: '', ttl: '1', proxied: false, priority: 0 });
      setShowAddForm(false);
    } catch (e) {
      setCfError((e as Error).message);
    } finally {
      setAddLoading(false);
    }
  };

  /* -- Render ------------------------------------------------------------ */

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <h1 className="text-xl font-bold sm:text-2xl text-foreground">DNS</h1>
        <p className="text-sm text-muted-foreground">
          Check whether a domain points to this server, and edit zone records when a DNS provider is configured.
        </p>
      </div>

      {/* Domain Selector */}
      <div className="rounded-lg border border-border bg-card p-5 shadow-md">
        <div className="mb-4 flex items-center gap-2">
          <Search size={18} className="text-blue-400" />
          <h2 className="text-sm font-semibold text-card-foreground">Check Domain DNS</h2>
        </div>
        <div className="flex flex-col gap-4 sm:flex-row sm:items-end">
          <div className="flex-1">
            <label className="mb-1.5 block text-xs font-medium uppercase text-muted-foreground">
              Domain
            </label>
            {domainsLoading ? (
              <div className="flex h-10 items-center text-sm text-muted-foreground">
                Loading domains...
              </div>
            ) : (
              <select
                value={selectedDomain}
                onChange={(e) => setSelectedDomain(e.target.value)}
                className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground outline-none focus:border-blue-500"
              >
                {domains.length === 0 && (
                  <option value="">No domains configured</option>
                )}
                {domains.map((d) => (
                  <option key={d.host} value={d.host}>
                    {d.host}
                  </option>
                ))}
              </select>
            )}
          </div>
          <button
            onClick={handleCheck}
            disabled={loading || !selectedDomain}
            className="flex items-center gap-2 rounded-md bg-blue-600 px-6 py-2.5 text-sm font-medium text-white transition hover:bg-blue-700 disabled:opacity-50"
          >
            {loading ? (
              <RefreshCw size={16} className="animate-spin" />
            ) : (
              <Waypoints size={16} />
            )}
            {loading ? 'Checking...' : 'Check DNS'}
          </button>
        </div>
      </div>

      {/* Error */}
      {error && (
        <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">{error}</div>
      )}

      {/* Results */}
      {result && (
        <>
          {/* Points Here Badge */}
          <div
            className={`flex items-center gap-3 rounded-lg border p-5 shadow-md ${
              result.points_here
                ? 'border-emerald-500/30 bg-emerald-500/10'
                : 'border-red-500/30 bg-red-500/10'
            }`}
          >
            {result.points_here ? (
              <CheckCircle size={32} className="shrink-0 text-emerald-400" />
            ) : (
              <XCircle size={32} className="shrink-0 text-red-400" />
            )}
            <div>
              <h3
                className={`text-lg font-bold ${
                  result.points_here ? 'text-emerald-400' : 'text-red-400'
                }`}
              >
                {result.points_here
                  ? 'Points to this server'
                  : 'Does NOT point to this server'}
              </h3>
              <p className="text-sm text-muted-foreground">
                {result.points_here
                  ? `DNS records for ${result.domain} are correctly configured.`
                  : `DNS records for ${result.domain} do not resolve to this server's IP addresses.`}
              </p>
            </div>
          </div>

          {/* DNS error from lookup */}
          {result.error && (
            <div className="flex items-start gap-2 rounded-md bg-amber-500/10 px-4 py-3 text-sm text-amber-400">
              <AlertTriangle size={16} className="mt-0.5 shrink-0" />
              {result.error}
            </div>
          )}

          {/* Server IPs */}
          <div className="rounded-lg border border-border bg-card p-5 shadow-md">
            <div className="mb-3 flex items-center gap-2">
              <Server size={16} className="text-muted-foreground" />
              <h3 className="text-sm font-semibold text-card-foreground">Server IP Addresses</h3>
            </div>
            <div className="flex flex-wrap gap-2">
              {(result.server_ips ?? []).length > 0 ? (
                (result.server_ips ?? []).map((ip) => (
                  <span
                    key={ip}
                    className="rounded-md bg-background px-3 py-1.5 font-mono text-sm text-card-foreground"
                  >
                    {ip}
                  </span>
                ))
              ) : (
                <span className="text-sm text-muted-foreground">No server IPs detected</span>
              )}
            </div>
          </div>

          {/* DNS Records */}
          <div className="rounded-lg border border-border bg-card p-5 shadow-md">
            <div className="mb-4 flex items-center gap-2">
              <Globe size={16} className="text-blue-400" />
              <h3 className="text-sm font-semibold text-card-foreground">
                DNS Records for {result.domain}
              </h3>
            </div>
            <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
              <RecordSection label="A Records" records={result.a} />
              <RecordSection label="AAAA Records" records={result.aaaa} />
              {result.cname && (
                <div>
                  <h4 className="mb-1.5 text-xs font-medium uppercase text-muted-foreground">CNAME</h4>
                  <div className="rounded-md bg-background px-3 py-2 font-mono text-sm text-card-foreground">
                    {result.cname}
                  </div>
                </div>
              )}
              <RecordSection label="MX Records" records={result.mx} />
              <RecordSection label="NS Records" records={result.ns} />
              <RecordSection label="TXT Records" records={result.txt} />
            </div>

            {/* No records at all */}
            {!result.a?.length &&
              !result.aaaa?.length &&
              !result.cname &&
              !result.mx?.length &&
              !result.ns?.length &&
              !result.txt?.length && (
                <div className="py-8 text-center text-sm text-muted-foreground">
                  <Waypoints size={32} className="mx-auto mb-3 opacity-40" />
                  No DNS records found for this domain
                </div>
              )}
          </div>

          {/* Guidance if not pointing here */}
          {!result.points_here && (
            <div className="rounded-lg border border-border bg-card p-5 shadow-md">
              <div className="mb-3 flex items-center gap-2">
                <AlertTriangle size={16} className="text-amber-400" />
                <h3 className="text-sm font-semibold text-card-foreground">How to Fix</h3>
              </div>
              <div className="space-y-2 text-sm text-muted-foreground">
                <p>
                  To point <span className="font-mono text-foreground">{result.domain}</span> to
                  this server, update the DNS records at your domain registrar:
                </p>
                <ol className="ml-4 list-decimal space-y-1">
                  <li>
                    Log in to your domain registrar or DNS provider.
                  </li>
                  <li>
                    Create or update an <strong className="text-card-foreground">A record</strong>{' '}
                    pointing to{' '}
                    {(result.server_ips ?? []).length > 0 ? (
                      <span className="font-mono text-foreground">
                        {result.server_ips[0]}
                      </span>
                    ) : (
                      "this server's IP"
                    )}
                    .
                  </li>
                  {(result.server_ips ?? []).some((ip) => ip.includes(':')) && (
                    <li>
                      Optionally add an <strong className="text-card-foreground">AAAA record</strong>{' '}
                      for IPv6 support.
                    </li>
                  )}
                  <li>
                    Wait for DNS propagation (usually 5 minutes to 48 hours).
                  </li>
                  <li>
                    Come back here and run the check again.
                  </li>
                </ol>
              </div>
            </div>
          )}
        </>
      )}

      {/* ================================================================== */}
      {/* DNS Records (Cloudflare)                                           */}
      {/* ================================================================== */}
      {selectedDomain && (
        <div className="rounded-lg border border-border bg-card p-5 shadow-md">
          <div className="mb-4 flex items-center justify-between">
            <div className="flex items-center gap-2">
              <Cloud size={18} className="text-orange-400" />
              <h2 className="text-sm font-semibold text-card-foreground">
                DNS Zone Editor
              </h2>
            </div>
            <div className="flex items-center gap-2">
              <button
                onClick={handleSyncDNS}
                disabled={syncLoading || cfNotConfigured}
                className="flex items-center gap-1.5 rounded-md bg-orange-600 px-3 py-1.5 text-xs font-medium text-white transition hover:bg-orange-700 disabled:opacity-50"
              >
                {syncLoading ? (
                  <RefreshCw size={14} className="animate-spin" />
                ) : (
                  <Shield size={14} />
                )}
                Sync A Record
              </button>
              <button
                onClick={handleLoadRecords}
                disabled={cfLoading}
                className="flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-1.5 text-xs font-medium text-white transition hover:bg-blue-700 disabled:opacity-50"
              >
                {cfLoading ? (
                  <RefreshCw size={14} className="animate-spin" />
                ) : (
                  <RefreshCw size={14} />
                )}
                Load Records
              </button>
            </div>
          </div>

          {/* Sync success message */}
          {syncMsg && (
            <div className="mb-4 flex items-center gap-2 rounded-md bg-emerald-500/10 px-4 py-2.5 text-sm text-emerald-400">
              <CheckCircle size={16} className="shrink-0" />
              {syncMsg}
            </div>
          )}

          {/* Not configured message */}
          {cfNotConfigured && (
            <div className="flex items-start gap-3 rounded-md bg-amber-500/10 px-4 py-3 text-sm text-amber-400">
              <Settings size={16} className="mt-0.5 shrink-0" />
              <div>
                <p className="font-medium">DNS provider not configured</p>
                <p className="mt-1 text-muted-foreground">
                  Configure your DNS provider (Cloudflare, Hetzner, DigitalOcean, or Route53) in{' '}
                  <RouterLink
                    to="/settings"
                    className="font-medium text-blue-400 underline hover:text-blue-300"
                  >
                    Settings &rarr; ACME
                  </RouterLink>{' '}
                  to manage DNS records from here.
                </p>
              </div>
            </div>
          )}

          {/* CF error */}
          {cfError && (
            <div className="mb-4 rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">
              {cfError}
            </div>
          )}

          {/* Records table */}
          {cfRecords.length > 0 && (
            <div className="mb-4 overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead>
                  <tr className="border-b border-border text-xs uppercase text-muted-foreground">
                    <th className="pb-2 pr-4">Type</th>
                    <th className="pb-2 pr-4">Name</th>
                    <th className="pb-2 pr-4">Content</th>
                    <th className="pb-2 pr-4">TTL</th>
                    <th className="pb-2 pr-4">Proxied</th>
                    <th className="pb-2">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {cfRecords.map((rec) => (
                    <tr key={rec.id} className="border-b border-border/50">
                      <td className="py-2.5 pr-4">
                        <span className="rounded bg-accent px-1.5 py-0.5 font-mono text-xs text-card-foreground">
                          {rec.type}
                        </span>
                      </td>
                      <td className="py-2.5 pr-4 font-mono text-card-foreground">
                        {rec.name}
                      </td>
                      <td className="max-w-[200px] py-2.5 pr-4">
                        {editId === rec.id ? (
                          <input
                            type="text" value={editRec.content}
                            onChange={e => setEditRec({ ...editRec, content: e.target.value })}
                            className="w-full rounded border border-blue-500 bg-background px-2 py-1 font-mono text-sm text-foreground outline-none"
                            autoFocus
                          />
                        ) : (
                          <span className="truncate block font-mono text-card-foreground cursor-pointer hover:text-blue-400" title={rec.content} onClick={() => startEdit(rec)}>
                            {rec.content}
                          </span>
                        )}
                      </td>
                      <td className="py-2.5 pr-4">
                        {editId === rec.id ? (
                          <input
                            type="text" value={editRec.ttl}
                            onChange={e => setEditRec({ ...editRec, ttl: e.target.value })}
                            className="w-16 rounded border border-border bg-background px-2 py-1 text-sm text-foreground outline-none"
                          />
                        ) : (
                          <span className="text-muted-foreground">{rec.ttl === 1 ? 'Auto' : `${rec.ttl}s`}</span>
                        )}
                      </td>
                      <td className="py-2.5 pr-4">
                        {editId === rec.id ? (
                          <input type="checkbox" checked={editRec.proxied} onChange={e => setEditRec({ ...editRec, proxied: e.target.checked })} />
                        ) : rec.proxied ? (
                          <span className="text-orange-400">Yes</span>
                        ) : (
                          <span className="text-muted-foreground">No</span>
                        )}
                      </td>
                      <td className="py-2.5">
                        {editId === rec.id ? (
                          <div className="flex items-center gap-1">
                            <button
                              onClick={() => handleUpdateRecord(rec.id, rec.type, rec.name)}
                              disabled={editLoading}
                              className="flex items-center gap-1 rounded px-2 py-1 text-xs text-emerald-400 transition hover:bg-emerald-500/10 disabled:opacity-50"
                            >
                              {editLoading ? <RefreshCw size={12} className="animate-spin" /> : <Check size={12} />}
                              Save
                            </button>
                            <button onClick={() => setEditId(null)} className="rounded px-2 py-1 text-xs text-muted-foreground hover:text-foreground">
                              <X size={12} />
                            </button>
                          </div>
                        ) : (
                          <div className="flex items-center gap-1">
                            <button
                              onClick={() => startEdit(rec)}
                              className="flex items-center gap-1 rounded px-2 py-1 text-xs text-blue-400 transition hover:bg-blue-500/10"
                            >
                              <Pencil size={12} /> Edit
                            </button>
                            <button
                              onClick={() => handleDeleteRecord(rec.id)}
                              disabled={deleteLoading === rec.id}
                              className="flex items-center gap-1 rounded px-2 py-1 text-xs text-red-400 transition hover:bg-red-500/10 disabled:opacity-50"
                            >
                              {deleteLoading === rec.id ? <RefreshCw size={12} className="animate-spin" /> : <Trash2 size={12} />}
                              Delete
                            </button>
                          </div>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          {/* Empty state after loading */}
          {!cfLoading && !cfNotConfigured && !cfError && cfRecords.length === 0 && (
            <div className="py-6 text-center text-sm text-muted-foreground">
              Click &ldquo;Load Records&rdquo; to fetch DNS records from your DNS provider
            </div>
          )}

          {/* Add record form */}
          {!cfNotConfigured && (
            <div>
              {!showAddForm ? (
                <button
                  onClick={() => setShowAddForm(true)}
                  className="flex items-center gap-1.5 rounded-md border border-dashed border-border px-3 py-2 text-xs text-muted-foreground transition hover:border-slate-400 hover:text-card-foreground"
                >
                  <Plus size={14} />
                  Add Record
                </button>
              ) : (
                <div className="rounded-md border border-border bg-background p-4">
                  <h4 className="mb-3 text-xs font-medium uppercase text-muted-foreground">
                    New DNS Record
                  </h4>
                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-6">
                    {/* Type */}
                    <div>
                      <label className="mb-1 block text-xs text-muted-foreground">Type</label>
                      <select
                        value={newRec.type}
                        onChange={(e) => setNewRec({ ...newRec, type: e.target.value })}
                        className="w-full rounded-md border border-border bg-card px-2 py-1.5 text-sm text-foreground outline-none focus:border-blue-500"
                      >
                        <option value="A">A</option>
                        <option value="AAAA">AAAA</option>
                        <option value="CNAME">CNAME</option>
                        <option value="MX">MX</option>
                        <option value="TXT">TXT</option>
                        <option value="NS">NS</option>
                        <option value="SRV">SRV</option>
                        <option value="CAA">CAA</option>
                      </select>
                    </div>

                    {/* Name */}
                    <div className="lg:col-span-2">
                      <label className="mb-1 block text-xs text-muted-foreground">Name</label>
                      <input
                        type="text"
                        value={newRec.name}
                        onChange={(e) => setNewRec({ ...newRec, name: e.target.value })}
                        placeholder="@ or subdomain"
                        className="w-full rounded-md border border-border bg-card px-2 py-1.5 text-sm text-foreground outline-none focus:border-blue-500"
                      />
                    </div>

                    {/* Content */}
                    <div className="lg:col-span-2">
                      <label className="mb-1 block text-xs text-muted-foreground">Content</label>
                      <input
                        type="text"
                        value={newRec.content}
                        onChange={(e) => setNewRec({ ...newRec, content: e.target.value })}
                        placeholder="IP address or value"
                        className="w-full rounded-md border border-border bg-card px-2 py-1.5 text-sm text-foreground outline-none focus:border-blue-500"
                      />
                    </div>

                    {/* TTL */}
                    <div>
                      <label className="mb-1 block text-xs text-muted-foreground">TTL</label>
                      <input
                        type="text"
                        value={newRec.ttl}
                        onChange={(e) => setNewRec({ ...newRec, ttl: e.target.value })}
                        placeholder="1 = Auto"
                        className="w-full rounded-md border border-border bg-card px-2 py-1.5 text-sm text-foreground outline-none focus:border-blue-500"
                      />
                    </div>
                  </div>

                  {/* Proxied toggle + action buttons */}
                  <div className="mt-3 flex items-center justify-between">
                    <label className="flex items-center gap-2 text-sm text-muted-foreground">
                      <input
                        type="checkbox"
                        checked={newRec.proxied}
                        onChange={(e) => setNewRec({ ...newRec, proxied: e.target.checked })}
                        className="rounded border-border bg-card"
                      />
                      Proxied (orange cloud)
                    </label>
                    <div className="flex gap-2">
                      <button
                        onClick={() => {
                          setShowAddForm(false);
                          setNewRec({ type: 'A', name: '', content: '', ttl: '1', proxied: false, priority: 0 });
                        }}
                        className="rounded-md px-3 py-1.5 text-xs text-muted-foreground transition hover:text-foreground"
                      >
                        Cancel
                      </button>
                      <button
                        onClick={handleCreateRecord}
                        disabled={addLoading || !newRec.name || !newRec.content}
                        className="flex items-center gap-1.5 rounded-md bg-emerald-600 px-4 py-1.5 text-xs font-medium text-white transition hover:bg-emerald-700 disabled:opacity-50"
                      >
                        {addLoading ? (
                          <RefreshCw size={12} className="animate-spin" />
                        ) : (
                          <Plus size={12} />
                        )}
                        Create Record
                      </button>
                    </div>
                  </div>
                </div>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
