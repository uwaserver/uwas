import { useState, useEffect, useCallback } from 'react';
import { UserPlus, Trash2, RefreshCw, Copy, Check, FolderOpen } from 'lucide-react';
import {
  fetchUsers, createUser, deleteUser, fetchDomains,
  type SiteUser, type SiteUserCreated, type DomainData,
} from '@/lib/api';

export default function Users() {
  const [users, setUsers] = useState<SiteUser[]>([]);
  const [domains, setDomains] = useState<DomainData[]>([]);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [newDomain, setNewDomain] = useState('');
  const [created, setCreated] = useState<SiteUserCreated | null>(null);
  const [copied, setCopied] = useState('');
  const [error, setError] = useState('');
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);

  const load = useCallback(() => {
    Promise.all([fetchUsers(), fetchDomains()])
      .then(([u, d]) => {
        setUsers(u ?? []);
        setDomains(d ?? []);
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => { load(); }, [load]);

  const handleCreate = async () => {
    if (!newDomain) return;
    setCreating(true);
    setError('');
    setCreated(null);
    try {
      const result = await createUser(newDomain);
      setCreated(result);
      setNewDomain('');
      load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setCreating(false);
    }
  };

  const handleDelete = async (domain: string) => {
    try {
      await deleteUser(domain);
      setConfirmDelete(null);
      load();
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const copyToClipboard = (text: string, label: string) => {
    navigator.clipboard.writeText(text);
    setCopied(label);
    setTimeout(() => setCopied(''), 2000);
  };

  // Domains that don't have a user yet
  const availableDomains = domains.filter(
    d => !users.some(u => u.domain === d.host)
  );

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">SFTP Users</h1>
          <p className="mt-1 text-sm text-slate-400">
            Manage chroot-jailed SFTP users for domain file uploads.
          </p>
        </div>
        <button onClick={load} className="flex items-center gap-2 rounded-md border border-[#334155] bg-[#1e293b] px-3 py-2 text-sm text-slate-300 hover:bg-[#334155]">
          <RefreshCw size={14} /> Refresh
        </button>
      </div>

      {error && (
        <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">{error}</div>
      )}

      {/* Created user credentials */}
      {created && (
        <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-5">
          <h3 className="text-sm font-semibold text-emerald-400 mb-3">SFTP User Created</h3>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
            {([
              ['Domain', created.domain],
              ['Username', created.username],
              ['Password', created.password],
              ['Web Root', created.web_dir],
            ] as const).map(([label, value]) => (
              <div key={label} className="flex items-center justify-between rounded bg-[#0f172a] px-3 py-2">
                <div>
                  <span className="text-xs text-slate-500">{label}</span>
                  <p className="font-mono text-sm text-slate-200">{value}</p>
                </div>
                <button
                  onClick={() => copyToClipboard(value, label)}
                  className="ml-2 rounded p-1 text-slate-500 hover:text-slate-300"
                  title={`Copy ${label}`}
                >
                  {copied === label ? <Check size={14} className="text-emerald-400" /> : <Copy size={14} />}
                </button>
              </div>
            ))}
          </div>
          <p className="mt-3 text-xs text-amber-400">Save these credentials — the password cannot be recovered.</p>
          <button onClick={() => setCreated(null)} className="mt-2 text-xs text-slate-500 hover:text-slate-300">Dismiss</button>
        </div>
      )}

      {/* Create new user */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5">
        <h2 className="text-sm font-semibold text-slate-300 mb-3">Add SFTP User</h2>
        <div className="flex items-end gap-3">
          <div className="flex-1">
            <label className="mb-1.5 block text-xs text-slate-500">Domain</label>
            <select
              value={newDomain}
              onChange={e => setNewDomain(e.target.value)}
              className="w-full rounded-md border border-[#334155] bg-[#0f172a] px-3 py-2.5 text-sm text-slate-200 outline-none focus:border-blue-500"
            >
              <option value="">Select a domain...</option>
              {availableDomains.map(d => (
                <option key={d.host} value={d.host}>{d.host}</option>
              ))}
            </select>
          </div>
          <button
            onClick={handleCreate}
            disabled={creating || !newDomain}
            className="flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          >
            <UserPlus size={14} />
            {creating ? 'Creating...' : 'Create User'}
          </button>
        </div>
        {availableDomains.length === 0 && domains.length > 0 && (
          <p className="mt-2 text-xs text-slate-500">All domains already have SFTP users.</p>
        )}
      </div>

      {/* User list */}
      {loading ? (
        <div className="text-center text-sm text-slate-500 py-12">Loading...</div>
      ) : users.length === 0 ? (
        <div className="rounded-lg border border-[#334155] bg-[#1e293b] px-6 py-12 text-center">
          <UserPlus size={40} className="mx-auto mb-3 text-slate-500" />
          <p className="text-slate-300 font-medium">No SFTP users configured</p>
          <p className="text-sm text-slate-500 mt-1">Create a user above to enable SFTP file uploads for a domain.</p>
        </div>
      ) : (
        <div className="overflow-hidden rounded-lg border border-[#334155]">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[#334155] bg-[#1e293b]/50 text-left text-xs uppercase tracking-wider text-slate-500">
                <th className="px-4 py-3">Username</th>
                <th className="px-4 py-3">Domain</th>
                <th className="px-4 py-3">Web Root</th>
                <th className="px-4 py-3 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[#334155]">
              {users.map(u => (
                <tr key={u.username} className="bg-[#0f172a] hover:bg-[#1e293b]/50">
                  <td className="px-4 py-3 font-mono text-slate-200">{u.username}</td>
                  <td className="px-4 py-3 text-slate-300">{u.domain}</td>
                  <td className="px-4 py-3">
                    <span className="flex items-center gap-1.5 text-slate-400">
                      <FolderOpen size={13} />
                      <span className="font-mono text-xs">{u.web_dir}</span>
                    </span>
                  </td>
                  <td className="px-4 py-3 text-right">
                    {confirmDelete === u.domain ? (
                      <span className="flex items-center justify-end gap-2">
                        <span className="text-xs text-red-400">Delete user?</span>
                        <button onClick={() => handleDelete(u.domain)} className="rounded bg-red-600 px-2 py-1 text-xs text-white hover:bg-red-700">Yes</button>
                        <button onClick={() => setConfirmDelete(null)} className="rounded bg-[#334155] px-2 py-1 text-xs text-slate-300">No</button>
                      </span>
                    ) : (
                      <button
                        onClick={() => setConfirmDelete(u.domain)}
                        className="flex items-center gap-1 rounded-md bg-red-500/15 px-2.5 py-1.5 text-xs font-medium text-red-400 hover:bg-red-500/25"
                      >
                        <Trash2 size={13} /> Remove
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
