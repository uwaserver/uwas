import { useState, useEffect, useCallback } from 'react';
import { UserPlus, Trash2, RefreshCw, Copy, Check, Key, Shield, Lock } from 'lucide-react';
import { fetchAdminUsers, createAdminUser, deleteAdminUser, changeAdminPassword, regenAdminApiKey, type AdminUser } from '@/lib/api';

export default function AdminUsers() {
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [status, setStatus] = useState('');
  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState({ username: '', password: '', role: 'user', email: '' });
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<{ username: string; password: string; api_key: string } | null>(null);
  const [copied, setCopied] = useState('');
  const [regenResult, setRegenResult] = useState<{ username: string; api_key: string } | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [pwUser, setPwUser] = useState<string | null>(null);
  const [newPw, setNewPw] = useState('');
  const [actionLoading, setActionLoading] = useState('');

  const load = useCallback(async () => {
    try {
      const data = await fetchAdminUsers();
      setUsers(data ?? []);
      setError('');
    } catch (e) { setError((e as Error).message); }
    finally { setLoading(false); }
  }, []);

  useEffect(() => { load(); }, [load]);

  const copy = (text: string, label: string) => {
    navigator.clipboard.writeText(text);
    setCopied(label);
    setTimeout(() => setCopied(''), 2000);
  };

  const handleCreate = async () => {
    if (!form.username || !form.password) return;
    setCreating(true); setError('');
    try {
      const res = await createAdminUser(form);
      setCreated({ username: res.username, password: form.password, api_key: res.api_key });
      setForm({ username: '', password: '', role: 'user', email: '' });
      setShowForm(false);
      await load();
    } catch (e) { setError((e as Error).message); }
    finally { setCreating(false); }
  };

  const handleDelete = async (username: string) => {
    try {
      await deleteAdminUser(username);
      setConfirmDelete(null);
      setStatus(`User ${username} deleted`);
      await load();
    } catch (e) { setError((e as Error).message); }
  };

  const handleChangePw = async (username: string) => {
    if (!newPw) return;
    setActionLoading('pw-' + username);
    try {
      await changeAdminPassword(username, newPw);
      setPwUser(null); setNewPw('');
      setStatus(`Password changed for ${username}`);
    } catch (e) { setError((e as Error).message); }
    finally { setActionLoading(''); }
  };

  const handleRegenKey = async (username: string) => {
    setActionLoading('key-' + username);
    try {
      const res = await regenAdminApiKey(username);
      setRegenResult({ username, api_key: res.api_key });
      setStatus(`API key regenerated for ${username}. Copy it now — it won't be shown again.`);
      await load();
    } catch (e) { setError((e as Error).message); }
    finally { setActionLoading(''); }
  };

  const roleBadge = (role: string) => {
    const colors: Record<string, string> = { admin: 'text-red-400 bg-red-500/10', reseller: 'text-blue-400 bg-blue-500/10', user: 'text-emerald-400 bg-emerald-500/10' };
    return <span className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${colors[role] || 'text-muted-foreground bg-accent'}`}>{role}</span>;
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">Admin Users</h1>
          <p className="mt-1 text-sm text-muted-foreground">Manage dashboard users and API access (admin, reseller, user roles)</p>
        </div>
        <button onClick={load} className="flex items-center gap-2 rounded-md border border-border bg-card px-3 py-2 text-sm text-card-foreground hover:bg-accent">
          <RefreshCw size={14} /> Refresh
        </button>
      </div>

      {error && <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">{error}</div>}
      {status && <div className="rounded-md bg-emerald-500/10 px-4 py-3 text-sm text-emerald-400">{status}</div>}

      {/* Regenerated API key — show once, copy-only */}
      {regenResult && (
        <div className="rounded-lg border border-amber-500/30 bg-amber-500/5 p-5">
          <h3 className="text-sm font-semibold text-amber-400 mb-3">New API Key for {regenResult.username}</h3>
          <div className="flex items-center justify-between rounded bg-background px-3 py-2">
            <div>
              <span className="text-xs text-muted-foreground">API Key</span>
              <p className="font-mono text-sm text-foreground">{'•'.repeat(8)}...{regenResult.api_key.slice(-8)}</p>
            </div>
            <button onClick={() => copy(regenResult.api_key, 'regen-key')} className="ml-2 rounded p-1.5 text-muted-foreground hover:text-foreground hover:bg-accent">
              {copied === 'regen-key' ? <Check size={14} className="text-emerald-400" /> : <Copy size={14} />}
            </button>
          </div>
          <p className="mt-3 text-xs text-amber-400">Copy this key now. It will not be shown again.</p>
          <button onClick={() => setRegenResult(null)} className="mt-2 text-xs text-muted-foreground hover:text-foreground">Dismiss</button>
        </div>
      )}

      {/* Created credentials */}
      {created && (
        <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-5">
          <h3 className="text-sm font-semibold text-emerald-400 mb-3">User Created</h3>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
            {[
              { label: 'Username', value: created.username, masked: false },
              { label: 'Password', value: created.password, masked: true },
              { label: 'API Key', value: created.api_key, masked: true },
            ].map(({ label, value, masked }) => (
              <div key={label} className="flex items-center justify-between rounded bg-background px-3 py-2">
                <div>
                  <span className="text-xs text-muted-foreground">{label}</span>
                  <p className="font-mono text-sm text-foreground">
                    {masked ? '•'.repeat(Math.min(value.length, 12)) : value}
                  </p>
                </div>
                <button onClick={() => copy(value, label)} className="ml-2 rounded p-1.5 text-muted-foreground hover:text-foreground hover:bg-accent" title={`Copy ${label}`}>
                  {copied === label ? <Check size={14} className="text-emerald-400" /> : <Copy size={14} />}
                </button>
              </div>
            ))}
          </div>
          <p className="mt-3 text-xs text-amber-400">Save these credentials now.</p>
          <button onClick={() => setCreated(null)} className="mt-2 text-xs text-muted-foreground hover:text-foreground">Dismiss</button>
        </div>
      )}

      {/* Create form */}
      <div className="rounded-lg border border-border bg-card p-5">
        {!showForm ? (
          <button onClick={() => setShowForm(true)} className="flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2.5 text-sm font-medium text-white hover:bg-blue-700">
            <UserPlus size={14} /> Add User
          </button>
        ) : (
          <div className="space-y-4">
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
              <div>
                <label className="mb-1.5 block text-xs font-medium text-muted-foreground">Username</label>
                <input type="text" value={form.username} onChange={e => setForm({ ...form, username: e.target.value })}
                  className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none focus:border-blue-500" />
              </div>
              <div>
                <label className="mb-1.5 block text-xs font-medium text-muted-foreground">Password</label>
                <input type="password" value={form.password} onChange={e => setForm({ ...form, password: e.target.value })}
                  className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none focus:border-blue-500" />
              </div>
              <div>
                <label className="mb-1.5 block text-xs font-medium text-muted-foreground">Role</label>
                <select value={form.role} onChange={e => setForm({ ...form, role: e.target.value })}
                  className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none focus:border-blue-500">
                  <option value="admin">Admin</option>
                  <option value="reseller">Reseller</option>
                  <option value="user">User</option>
                </select>
              </div>
              <div>
                <label className="mb-1.5 block text-xs font-medium text-muted-foreground">Email</label>
                <input type="email" value={form.email} onChange={e => setForm({ ...form, email: e.target.value })} placeholder="Optional"
                  className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none placeholder:text-muted-foreground focus:border-blue-500" />
              </div>
            </div>
            <div className="flex gap-2">
              <button onClick={handleCreate} disabled={creating || !form.username || !form.password}
                className="flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50">
                {creating ? <RefreshCw size={14} className="animate-spin" /> : <UserPlus size={14} />} Create
              </button>
              <button onClick={() => setShowForm(false)} className="px-4 py-2 text-sm text-muted-foreground hover:text-foreground">Cancel</button>
            </div>
          </div>
        )}
      </div>

      {/* User list */}
      {loading ? (
        <div className="text-center text-sm text-muted-foreground py-12">Loading...</div>
      ) : users.length === 0 ? (
        <div className="rounded-lg border border-border bg-card px-6 py-12 text-center">
          <Shield size={40} className="mx-auto mb-3 text-muted-foreground" />
          <p className="text-card-foreground font-medium">No admin users</p>
          <p className="text-sm text-muted-foreground mt-1">Multi-user auth may not be enabled. Check Settings.</p>
        </div>
      ) : (
        <div className="overflow-hidden rounded-lg border border-border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border bg-card/50 text-left text-xs uppercase tracking-wider text-muted-foreground">
                <th className="px-4 py-3">Username</th>
                <th className="px-4 py-3">Role</th>
                <th className="px-4 py-3">Email</th>
                <th className="px-4 py-3">Domains</th>
                <th className="px-4 py-3 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {users.map(u => (
                <tr key={u.username} className="bg-background hover:bg-card/50">
                  <td className="px-4 py-3 font-mono text-foreground">{u.username}</td>
                  <td className="px-4 py-3">{roleBadge(u.role)}</td>
                  <td className="px-4 py-3 text-muted-foreground">{u.email || '-'}</td>
                  <td className="px-4 py-3 text-muted-foreground text-xs">{u.domains?.length ? u.domains.join(', ') : 'all'}</td>
                  <td className="px-4 py-3 text-right">
                    <div className="flex items-center justify-end gap-2">
                      {pwUser === u.username ? (
                        <div className="flex items-center gap-1">
                          <input type="password" value={newPw} onChange={e => setNewPw(e.target.value)} placeholder="New password"
                            className="w-32 rounded border border-border bg-background px-2 py-1 text-xs text-foreground outline-none" />
                          <button disabled={!newPw || actionLoading === 'pw-' + u.username} onClick={() => handleChangePw(u.username)}
                            className="rounded bg-blue-600 px-2 py-1 text-xs text-white disabled:opacity-50">Save</button>
                          <button onClick={() => { setPwUser(null); setNewPw(''); }} className="text-xs text-muted-foreground">X</button>
                        </div>
                      ) : (
                        <>
                          <button onClick={() => setPwUser(u.username)} className="rounded bg-accent/50 p-1.5 text-muted-foreground hover:text-foreground" title="Change password"><Lock size={12} /></button>
                          <button disabled={!!actionLoading} onClick={() => handleRegenKey(u.username)} className="rounded bg-accent/50 p-1.5 text-muted-foreground hover:text-foreground" title="Regenerate API key"><Key size={12} /></button>
                          {confirmDelete === u.username ? (
                            <span className="flex items-center gap-1">
                              <button onClick={() => handleDelete(u.username)} className="rounded bg-red-600 px-2 py-1 text-xs text-white">Yes</button>
                              <button onClick={() => setConfirmDelete(null)} className="rounded bg-accent px-2 py-1 text-xs">No</button>
                            </span>
                          ) : (
                            <button onClick={() => setConfirmDelete(u.username)} className="rounded bg-red-500/15 p-1.5 text-red-400 hover:bg-red-500/25"><Trash2 size={12} /></button>
                          )}
                        </>
                      )}
                    </div>
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
