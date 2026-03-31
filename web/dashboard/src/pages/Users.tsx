import { useState, useEffect, useCallback, Fragment } from 'react';
import { UserPlus, Trash2, RefreshCw, Copy, Check, FolderOpen, Key, ChevronDown, ChevronRight, Plus, X } from 'lucide-react';
import {
  fetchUsers, createUser, deleteUser, fetchDomains,
  fetchSSHKeys, addSSHKey, deleteSSHKey,
  type SiteUser, type SiteUserCreated, type DomainData,
} from '@/lib/api';

function SSHKeyPanel({ domain }: { domain: string }) {
  const [keys, setKeys] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [newKey, setNewKey] = useState('');
  const [adding, setAdding] = useState(false);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    try {
      const data = await fetchSSHKeys(domain);
      setKeys(data ?? []);
      setError('');
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, [domain]);

  useEffect(() => { load(); }, [load]);

  const handleAdd = async () => {
    if (!newKey.trim()) return;
    setAdding(true);
    setError('');
    try {
      await addSSHKey(domain, newKey.trim());
      setNewKey('');
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setAdding(false);
    }
  };

  const handleDelete = async (key: string) => {
    try {
      // Use the key content as fingerprint identifier
      await deleteSSHKey(domain, key);
      await load();
    } catch (e) {
      setError((e as Error).message);
    }
  };

  // Extract short label from SSH key (type + last part of comment or fingerprint)
  const keyLabel = (key: string) => {
    const parts = key.trim().split(/\s+/);
    const type = parts[0] || '';
    const comment = parts.length >= 3 ? parts[parts.length - 1] : '';
    const shortType = type.replace('ssh-', '').replace('ecdsa-sha2-', '');
    return comment ? `${shortType}: ${comment}` : shortType;
  };

  return (
    <div className="border-t border-border bg-background/60 px-4 py-3">
      {error && (
        <div className="mb-2 rounded bg-red-500/10 px-3 py-2 text-xs text-red-400">{error}</div>
      )}

      {loading ? (
        <p className="text-xs text-muted-foreground">Loading keys...</p>
      ) : (
        <>
          {keys.length > 0 && (
            <div className="mb-3 space-y-1.5">
              {keys.map((key, i) => (
                <div key={i} className="flex items-center justify-between rounded bg-card px-3 py-2">
                  <div className="flex items-center gap-2 min-w-0">
                    <Key size={12} className="shrink-0 text-muted-foreground" />
                    <span className="truncate font-mono text-xs text-card-foreground" title={key}>{keyLabel(key)}</span>
                  </div>
                  <button
                    onClick={() => handleDelete(key)}
                    className="ml-2 shrink-0 rounded p-1 text-red-400/60 hover:text-red-400 hover:bg-red-500/10"
                    title="Remove key"
                  >
                    <X size={13} />
                  </button>
                </div>
              ))}
            </div>
          )}

          <div className="flex gap-2">
            <input
              type="text"
              value={newKey}
              onChange={e => setNewKey(e.target.value)}
              placeholder="ssh-ed25519 AAAA... user@host"
              className="flex-1 rounded border border-border bg-background px-3 py-1.5 font-mono text-xs text-foreground outline-none placeholder:text-muted-foreground focus:border-blue-500"
              onKeyDown={e => e.key === 'Enter' && handleAdd()}
            />
            <button
              onClick={handleAdd}
              disabled={adding || !newKey.trim()}
              className="flex shrink-0 items-center gap-1 rounded bg-blue-600 px-2.5 py-1.5 text-xs font-medium text-white hover:bg-blue-700 disabled:opacity-50"
            >
              <Plus size={12} />
              {adding ? '...' : 'Add Key'}
            </button>
          </div>

          {keys.length === 0 && !newKey && (
            <p className="mt-2 text-[11px] text-muted-foreground">No SSH keys. Password authentication will be used.</p>
          )}
        </>
      )}
    </div>
  );
}

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
  const [expandedUser, setExpandedUser] = useState<string | null>(null);

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

  const [deleting, setDeleting] = useState(false);
  const handleDelete = async (domain: string) => {
    setDeleting(true);
    try {
      await deleteUser(domain);
      setConfirmDelete(null);
      if (expandedUser === domain) setExpandedUser(null);
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setDeleting(false);
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
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">SFTP Users</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Manage chroot-jailed SFTP users and SSH keys for domain file uploads.
          </p>
        </div>
        <button onClick={load} className="flex items-center gap-2 rounded-md border border-border bg-card px-3 py-2 text-sm text-card-foreground hover:bg-accent">
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
              { label: 'Host', value: created.server_ip || 'your-server-ip', secret: false },
              { label: 'Port', value: created.port || '22', secret: false },
              { label: 'Username', value: created.username, secret: false },
              { label: 'Password', value: created.password, secret: true },
              { label: 'Web Root', value: created.web_dir, secret: false },
            ] as const).map(({ label, value, secret }) => (
              <div key={label} className="flex items-center justify-between rounded bg-background px-3 py-2">
                <div>
                  <span className="text-xs text-muted-foreground">{label}</span>
                  <p className="font-mono text-sm text-foreground">{secret ? '•'.repeat(Math.min(value.length, 12)) : value}</p>
                </div>
                <button
                  onClick={() => copyToClipboard(value, label)}
                  className="ml-2 rounded p-1.5 text-muted-foreground hover:text-card-foreground hover:bg-accent"
                  title={`Copy ${label}`}
                >
                  {copied === label ? <Check size={14} className="text-emerald-400" /> : <Copy size={14} />}
                </button>
              </div>
            ))}
          </div>

          {/* Quick connect commands */}
          <div className="mt-3 rounded bg-background p-3">
            <p className="text-xs text-muted-foreground mb-2">Quick connect:</p>
            <div className="space-y-1.5">
              <div className="flex items-center gap-2">
                <code className="flex-1 text-xs font-mono text-card-foreground select-all">
                  sftp {created.username}@{created.server_ip || 'your-server-ip'}
                </code>
                <button onClick={() => copyToClipboard(`sftp ${created.username}@${created.server_ip}`, 'sftp-cmd')}
                  className="rounded p-1 text-muted-foreground hover:text-card-foreground">
                  {copied === 'sftp-cmd' ? <Check size={12} className="text-emerald-400" /> : <Copy size={12} />}
                </button>
              </div>
              <p className="text-[10px] text-muted-foreground">
                Or use FileZilla / WinSCP / Cyberduck — Protocol: SFTP, Host: {created.server_ip}, Port: 22
              </p>
            </div>
          </div>

          <p className="mt-3 text-xs text-amber-400">Save these credentials — the password cannot be recovered.</p>
          <button onClick={() => setCreated(null)} className="mt-2 text-xs text-muted-foreground hover:text-card-foreground">Dismiss</button>
        </div>
      )}

      {/* Create new user */}
      <div className="rounded-lg border border-border bg-card p-5">
        <h2 className="text-sm font-semibold text-card-foreground mb-3">Add SFTP User</h2>
        <div className="flex items-end gap-3">
          <div className="flex-1">
            <label className="mb-1.5 block text-xs text-muted-foreground">Domain</label>
            <select
              value={newDomain}
              onChange={e => setNewDomain(e.target.value)}
              className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none focus:border-blue-500"
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
          <p className="mt-2 text-xs text-muted-foreground">All domains already have SFTP users.</p>
        )}
      </div>

      {/* User list */}
      {loading ? (
        <div className="text-center text-sm text-muted-foreground py-12">Loading...</div>
      ) : users.length === 0 ? (
        <div className="rounded-lg border border-border bg-card px-6 py-12 text-center">
          <UserPlus size={40} className="mx-auto mb-3 text-muted-foreground" />
          <p className="text-card-foreground font-medium">No SFTP users configured</p>
          <p className="text-sm text-muted-foreground mt-1">Create a user above to enable SFTP file uploads for a domain.</p>
        </div>
      ) : (
        <div className="overflow-hidden rounded-lg border border-border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border bg-card/50 text-left text-xs uppercase tracking-wider text-muted-foreground">
                <th className="w-8 px-2 py-3"></th>
                <th className="px-4 py-3">Username</th>
                <th className="px-4 py-3">Domain</th>
                <th className="px-4 py-3">Web Root</th>
                <th className="px-4 py-3 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {users.map(u => (
                <Fragment key={u.username}>
                  <tr className="bg-background hover:bg-card/50">
                    <td className="px-2 py-3">
                      <button
                        onClick={() => setExpandedUser(expandedUser === u.domain ? null : u.domain)}
                        className="rounded p-1 text-muted-foreground hover:text-card-foreground"
                        title="SSH Keys"
                      >
                        {expandedUser === u.domain ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                      </button>
                    </td>
                    <td className="px-4 py-3 font-mono text-foreground">{u.username}</td>
                    <td className="px-4 py-3 text-card-foreground">{u.domain}</td>
                    <td className="px-4 py-3">
                      <span className="flex items-center gap-1.5 text-muted-foreground">
                        <FolderOpen size={13} />
                        <span className="font-mono text-xs">{u.web_dir}</span>
                      </span>
                    </td>
                    <td className="px-4 py-3 text-right">
                      <div className="flex items-center justify-end gap-2">
                        <button
                          onClick={() => setExpandedUser(expandedUser === u.domain ? null : u.domain)}
                          className="flex items-center gap-1 rounded-md bg-accent/50 px-2.5 py-1.5 text-xs font-medium text-muted-foreground hover:bg-accent hover:text-card-foreground"
                        >
                          <Key size={13} /> SSH Keys
                        </button>
                        {confirmDelete === u.domain ? (
                          <span className="flex items-center gap-2">
                            <span className="text-xs text-red-400">Delete?</span>
                            <button onClick={() => handleDelete(u.domain)} disabled={deleting} className="rounded bg-red-600 px-2 py-1 text-xs text-white hover:bg-red-700 disabled:opacity-50">{deleting ? '...' : 'Yes'}</button>
                            <button onClick={() => setConfirmDelete(null)} className="rounded bg-accent px-2 py-1 text-xs text-card-foreground">No</button>
                          </span>
                        ) : (
                          <button
                            onClick={() => setConfirmDelete(u.domain)}
                            className="flex items-center gap-1 rounded-md bg-red-500/15 px-2.5 py-1.5 text-xs font-medium text-red-400 hover:bg-red-500/25"
                          >
                            <Trash2 size={13} /> Remove
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                  {expandedUser === u.domain && (
                    <tr>
                      <td colSpan={5} className="p-0">
                        <SSHKeyPanel domain={u.domain} />
                      </td>
                    </tr>
                  )}
                </Fragment>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
