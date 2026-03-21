import { useState, useEffect, type FormEvent } from 'react';
import { Globe, X, Plus, Trash2, CheckCircle, XCircle } from 'lucide-react';
import { fetchDomains, addDomain, deleteDomain, type DomainData } from '@/lib/api';

const typeBadge = (type: string) => {
  const styles: Record<string, string> = {
    static: 'bg-blue-500/15 text-blue-400',
    php: 'bg-purple-500/15 text-purple-400',
    proxy: 'bg-orange-500/15 text-orange-400',
    redirect: 'bg-slate-500/15 text-slate-400',
  };
  return (
    <span
      className={`rounded-full px-2.5 py-0.5 text-xs font-medium ${styles[type] ?? 'bg-slate-500/15 text-slate-400'}`}
    >
      {type}
    </span>
  );
};

const sslBadge = (ssl: string) => {
  const styles: Record<string, string> = {
    auto: 'bg-emerald-500/15 text-emerald-400',
    manual: 'bg-amber-500/15 text-amber-400',
    off: 'bg-red-500/15 text-red-400',
  };
  return (
    <span
      className={`rounded-full px-2.5 py-0.5 text-xs font-medium ${styles[ssl] ?? 'bg-slate-500/15 text-slate-400'}`}
    >
      {ssl}
    </span>
  );
};

const domainTypes = ['static', 'php', 'proxy', 'redirect'];
const sslModes = ['auto', 'manual', 'off'];

export default function Domains() {
  const [domains, setDomains] = useState<DomainData[]>([]);
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<DomainData | null>(null);
  const [showAdd, setShowAdd] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [status, setStatus] = useState<{ ok: boolean; message: string } | null>(null);

  // Add form state
  const [formHost, setFormHost] = useState('');
  const [formType, setFormType] = useState('static');
  const [formRoot, setFormRoot] = useState('');
  const [formSsl, setFormSsl] = useState('auto');
  const [submitting, setSubmitting] = useState(false);

  const loadDomains = () => {
    fetchDomains()
      .then(setDomains)
      .catch(() => {})
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    loadDomains();
  }, []);

  const resetForm = () => {
    setFormHost('');
    setFormType('static');
    setFormRoot('');
    setFormSsl('auto');
  };

  const handleAdd = async (e: FormEvent) => {
    e.preventDefault();
    if (!formHost.trim()) return;
    setSubmitting(true);
    setStatus(null);
    try {
      await addDomain({
        host: formHost.trim(),
        type: formType,
        root: formRoot.trim(),
        ssl: formSsl,
      });
      setStatus({ ok: true, message: `Domain "${formHost.trim()}" added successfully` });
      resetForm();
      setShowAdd(false);
      loadDomains();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = async (host: string) => {
    setStatus(null);
    try {
      await deleteDomain(host);
      setStatus({ ok: true, message: `Domain "${host}" deleted successfully` });
      setConfirmDelete(null);
      setSelected(null);
      loadDomains();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
      setConfirmDelete(null);
    }
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">Domains</h1>
          <p className="text-sm text-slate-400">
            Manage your server domains and routing
          </p>
        </div>
        <button
          onClick={() => { setShowAdd(true); setStatus(null); }}
          className="flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700"
        >
          <Plus size={14} />
          Add Domain
        </button>
      </div>

      {/* Status message */}
      {status && (
        <div
          className={`flex items-center gap-2 rounded-md px-4 py-3 text-sm ${
            status.ok
              ? 'bg-emerald-500/10 text-emerald-400'
              : 'bg-red-500/10 text-red-400'
          }`}
        >
          {status.ok ? <CheckCircle size={14} /> : <XCircle size={14} />}
          {status.message}
        </div>
      )}

      {/* Table */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] shadow-md">
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-[#334155] text-slate-400">
                <th className="px-5 py-3 font-medium">Host</th>
                <th className="px-5 py-3 font-medium">Type</th>
                <th className="px-5 py-3 font-medium">SSL</th>
                <th className="px-5 py-3 font-medium">Root / Target</th>
                <th className="px-5 py-3 font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {loading && (
                <tr>
                  <td
                    colSpan={5}
                    className="px-5 py-8 text-center text-slate-500"
                  >
                    Loading...
                  </td>
                </tr>
              )}
              {!loading && domains.length === 0 && (
                <tr>
                  <td
                    colSpan={5}
                    className="px-5 py-8 text-center text-slate-500"
                  >
                    No domains configured
                  </td>
                </tr>
              )}
              {domains.map((d) => (
                <tr
                  key={d.host}
                  onClick={() => setSelected(d)}
                  className="cursor-pointer border-b border-[#334155]/50 text-slate-300 transition hover:bg-[#334155]/30"
                >
                  <td className="px-5 py-3">
                    <div className="flex items-center gap-2">
                      <Globe size={14} className="text-slate-500" />
                      <span className="font-mono text-xs">{d.host}</span>
                    </div>
                  </td>
                  <td className="px-5 py-3">{typeBadge(d.type)}</td>
                  <td className="px-5 py-3">{sslBadge(d.ssl)}</td>
                  <td className="px-5 py-3 font-mono text-xs text-slate-400">
                    {d.root || '--'}
                  </td>
                  <td className="px-5 py-3">
                    {confirmDelete === d.host ? (
                      <div className="flex items-center gap-2" onClick={(e) => e.stopPropagation()}>
                        <button
                          onClick={() => handleDelete(d.host)}
                          className="rounded bg-red-600 px-2 py-1 text-xs font-medium text-white transition hover:bg-red-700"
                        >
                          Confirm
                        </button>
                        <button
                          onClick={() => setConfirmDelete(null)}
                          className="rounded bg-[#334155] px-2 py-1 text-xs font-medium text-slate-300 transition hover:bg-[#475569]"
                        >
                          Cancel
                        </button>
                      </div>
                    ) : (
                      <button
                        onClick={(e) => {
                          e.stopPropagation();
                          setConfirmDelete(d.host);
                        }}
                        className="rounded p-1.5 text-slate-500 transition hover:bg-red-500/10 hover:text-red-400"
                        title="Delete domain"
                      >
                        <Trash2 size={14} />
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      {/* Add domain modal */}
      {showAdd && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          <div
            className="absolute inset-0 bg-black/50"
            onClick={() => setShowAdd(false)}
          />
          <div className="relative z-10 w-full max-w-md rounded-xl border border-[#334155] bg-[#0f172a] p-6 shadow-2xl">
            <div className="mb-5 flex items-center justify-between">
              <h2 className="text-lg font-bold text-slate-100">
                Add Domain
              </h2>
              <button
                onClick={() => setShowAdd(false)}
                className="rounded-md p-1 text-slate-400 hover:text-slate-200"
              >
                <X size={18} />
              </button>
            </div>
            <form onSubmit={handleAdd} className="space-y-4">
              {/* Host */}
              <div>
                <label
                  htmlFor="add-host"
                  className="mb-1.5 block text-sm font-medium text-slate-300"
                >
                  Host
                </label>
                <input
                  id="add-host"
                  type="text"
                  value={formHost}
                  onChange={(e) => setFormHost(e.target.value)}
                  placeholder="example.com"
                  required
                  autoFocus
                  className="w-full rounded-md border border-[#334155] bg-[#1e293b] px-3 py-2.5 text-sm text-slate-200 placeholder-slate-500 outline-none transition focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
                />
              </div>

              {/* Type */}
              <div>
                <label
                  htmlFor="add-type"
                  className="mb-1.5 block text-sm font-medium text-slate-300"
                >
                  Type
                </label>
                <select
                  id="add-type"
                  value={formType}
                  onChange={(e) => setFormType(e.target.value)}
                  className="w-full rounded-md border border-[#334155] bg-[#1e293b] px-3 py-2.5 text-sm text-slate-200 outline-none transition focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
                >
                  {domainTypes.map((t) => (
                    <option key={t} value={t}>
                      {t}
                    </option>
                  ))}
                </select>
              </div>

              {/* Root */}
              <div>
                <label
                  htmlFor="add-root"
                  className="mb-1.5 block text-sm font-medium text-slate-300"
                >
                  Root / Target
                </label>
                <input
                  id="add-root"
                  type="text"
                  value={formRoot}
                  onChange={(e) => setFormRoot(e.target.value)}
                  placeholder="/var/www/html or http://localhost:3000"
                  className="w-full rounded-md border border-[#334155] bg-[#1e293b] px-3 py-2.5 text-sm text-slate-200 placeholder-slate-500 outline-none transition focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
                />
              </div>

              {/* SSL Mode */}
              <div>
                <label
                  htmlFor="add-ssl"
                  className="mb-1.5 block text-sm font-medium text-slate-300"
                >
                  SSL Mode
                </label>
                <select
                  id="add-ssl"
                  value={formSsl}
                  onChange={(e) => setFormSsl(e.target.value)}
                  className="w-full rounded-md border border-[#334155] bg-[#1e293b] px-3 py-2.5 text-sm text-slate-200 outline-none transition focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
                >
                  {sslModes.map((m) => (
                    <option key={m} value={m}>
                      {m}
                    </option>
                  ))}
                </select>
              </div>

              {/* Actions */}
              <div className="flex justify-end gap-3 pt-2">
                <button
                  type="button"
                  onClick={() => setShowAdd(false)}
                  className="rounded-md bg-[#334155] px-4 py-2 text-sm font-medium text-slate-300 transition hover:bg-[#475569]"
                >
                  Cancel
                </button>
                <button
                  type="submit"
                  disabled={submitting || !formHost.trim()}
                  className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
                >
                  {submitting ? 'Adding...' : 'Add Domain'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* Detail slide-over */}
      {selected && (
        <div className="fixed inset-0 z-50 flex justify-end">
          <div
            className="absolute inset-0 bg-black/50"
            onClick={() => setSelected(null)}
          />
          <div className="relative z-10 w-full max-w-md overflow-y-auto border-l border-[#334155] bg-[#0f172a] p-6 shadow-2xl">
            <div className="mb-6 flex items-center justify-between">
              <h2 className="text-lg font-bold text-slate-100">
                Domain Details
              </h2>
              <button
                onClick={() => setSelected(null)}
                className="rounded-md p-1 text-slate-400 hover:text-slate-200"
              >
                <X size={18} />
              </button>
            </div>
            <dl className="space-y-4">
              {([
                ['Host', selected.host],
                ['Type', selected.type],
                ['SSL', selected.ssl],
                ['Root / Target', selected.root || '--'],
                [
                  'Aliases',
                  selected.aliases?.length
                    ? selected.aliases.join(', ')
                    : 'None',
                ],
              ] as const).map(([label, value]) => (
                <div key={label}>
                  <dt className="text-xs font-medium text-slate-500 uppercase">
                    {label}
                  </dt>
                  <dd className="mt-1 text-sm text-slate-200">{value}</dd>
                </div>
              ))}
            </dl>
          </div>
        </div>
      )}
    </div>
  );
}
