import { useState, useEffect, useCallback } from 'react';
import {
  ShieldCheck,
  ShieldOff,
  Plus,
  Trash2,
  RefreshCw,
  CheckCircle,
  XCircle,
  Power,
} from 'lucide-react';
import {
  fetchFirewall,
  firewallAllow,
  firewallDeny,
  firewallDeleteRule,
  firewallEnable,
  firewallDisable,
  type FirewallStatus,
  type FirewallRule,
} from '@/lib/api';

export default function Firewall() {
  const [fw, setFw] = useState<FirewallStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [status, setStatus] = useState('');
  const [toggling, setToggling] = useState(false);

  // Add rule form
  const [port, setPort] = useState('');
  const [proto, setProto] = useState('tcp');
  const [action, setAction] = useState<'allow' | 'deny'>('allow');
  const [adding, setAdding] = useState(false);

  // Delete confirmation
  const [confirmDelete, setConfirmDelete] = useState<number | null>(null);
  const [deleting, setDeleting] = useState(false);

  const load = useCallback(async () => {
    try {
      const result = await fetchFirewall();
      setFw(result);
      setError('');
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const handleToggle = async () => {
    if (!fw) return;
    setToggling(true);
    setError('');
    setStatus('');
    try {
      if (fw.active) {
        await firewallDisable();
        setStatus('Firewall disabled.');
      } else {
        await firewallEnable();
        setStatus('Firewall enabled.');
      }
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setToggling(false);
    }
  };

  const handleAddRule = async () => {
    const p = port.trim();
    if (!p) return;
    // Validate port: single port (1-65535), range (8080:8090), or service name
    const portRangeRe = /^(\d{1,5})(:\d{1,5})?$/;
    const match = p.match(portRangeRe);
    if (match) {
      const num = parseInt(match[1]);
      if (num < 1 || num > 65535) { setError('Port must be between 1 and 65535'); return; }
      if (match[2]) {
        const end = parseInt(match[2].slice(1));
        if (end < 1 || end > 65535 || end <= num) { setError('Invalid port range'); return; }
      }
    }
    setAdding(true);
    setError('');
    setStatus('');
    try {
      const protoParam = proto === 'both' ? undefined : proto;
      if (action === 'allow') {
        await firewallAllow(port.trim(), protoParam);
      } else {
        await firewallDeny(port.trim(), protoParam);
      }
      setPort('');
      setStatus(`Rule added: ${action} port ${port.trim()}/${proto}`);
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setAdding(false);
    }
  };

  const handleDeleteRule = async (num: number) => {
    setDeleting(true);
    setError('');
    setStatus('');
    try {
      await firewallDeleteRule(num);
      setConfirmDelete(null);
      setStatus(`Rule #${num} deleted.`);
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setDeleting(false);
    }
  };

  const rules: FirewallRule[] = fw?.rules ?? [];

  if (loading) {
    return (
      <div className="flex h-96 items-center justify-center text-muted-foreground">Loading firewall status...</div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">Firewall</h1>
          <p className="text-sm text-muted-foreground">
            Manage firewall rules{fw?.backend ? ` (backend: ${fw.backend})` : ''}
          </p>
        </div>
        <button
          onClick={load}
          className="flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-xs text-card-foreground hover:bg-[#475569]"
        >
          <RefreshCw size={12} /> Refresh
        </button>
      </div>

      {error && (
        <div className="flex items-center gap-2 rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">
          <XCircle size={14} /> {error}
        </div>
      )}
      {status && (
        <div className="flex items-center gap-2 rounded-md bg-emerald-500/10 px-4 py-3 text-sm text-emerald-400">
          <CheckCircle size={14} /> {status}
        </div>
      )}

      {/* Status + toggle */}
      <div className="flex items-center justify-between rounded-lg border border-border bg-card p-5 shadow-md">
        <div className="flex items-center gap-4">
          {fw?.active ? (
            <div className="flex items-center gap-2">
              <ShieldCheck size={24} className="text-emerald-400" />
              <div>
                <p className="text-sm font-semibold text-emerald-400">Firewall Active</p>
                <p className="text-xs text-muted-foreground">{rules.length} rules configured</p>
              </div>
            </div>
          ) : (
            <div className="flex items-center gap-2">
              <ShieldOff size={24} className="text-red-400" />
              <div>
                <p className="text-sm font-semibold text-red-400">Firewall Inactive</p>
                <p className="text-xs text-muted-foreground">No rules are being enforced</p>
              </div>
            </div>
          )}
        </div>
        <button
          onClick={handleToggle}
          disabled={toggling}
          className={`flex items-center gap-2 rounded-md px-5 py-2.5 text-sm font-medium text-white transition disabled:opacity-50 ${
            fw?.active
              ? 'bg-red-600 hover:bg-red-700'
              : 'bg-emerald-600 hover:bg-emerald-700'
          }`}
        >
          {toggling ? (
            <RefreshCw size={14} className="animate-spin" />
          ) : (
            <Power size={14} />
          )}
          {fw?.active ? 'Disable Firewall' : 'Enable Firewall'}
        </button>
      </div>

      {/* Add rule form */}
      <div className="rounded-lg border border-border bg-card p-5 shadow-md">
        <div className="mb-4 flex items-center gap-2">
          <Plus size={18} className="text-blue-400" />
          <h2 className="text-sm font-semibold text-card-foreground">Add Rule</h2>
        </div>

        <div className="flex flex-col gap-4 sm:flex-row sm:items-end">
          {/* Port */}
          <div className="flex-1">
            <label className="mb-1.5 block text-xs font-medium uppercase text-muted-foreground">Port</label>
            <input
              type="text"
              value={port}
              onChange={e => setPort(e.target.value)}
              placeholder="e.g. 80, 443, 8080:8090"
              className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none focus:border-blue-500"
            />
          </div>

          {/* Protocol */}
          <div>
            <label className="mb-1.5 block text-xs font-medium uppercase text-muted-foreground">Protocol</label>
            <div className="flex gap-1 rounded-lg bg-background p-1">
              {(['tcp', 'udp', 'both'] as const).map(p => (
                <button
                  key={p}
                  onClick={() => setProto(p)}
                  className={`rounded-md px-4 py-2 text-sm font-medium transition ${
                    proto === p
                      ? 'bg-blue-600 text-white shadow'
                      : 'text-muted-foreground hover:text-foreground'
                  }`}
                >
                  {p.toUpperCase()}
                </button>
              ))}
            </div>
          </div>

          {/* Action */}
          <div>
            <label className="mb-1.5 block text-xs font-medium uppercase text-muted-foreground">Action</label>
            <div className="flex gap-1 rounded-lg bg-background p-1">
              <button
                onClick={() => setAction('allow')}
                className={`rounded-md px-4 py-2 text-sm font-medium transition ${
                  action === 'allow'
                    ? 'bg-emerald-600 text-white shadow'
                    : 'text-muted-foreground hover:text-foreground'
                }`}
              >
                Allow
              </button>
              <button
                onClick={() => setAction('deny')}
                className={`rounded-md px-4 py-2 text-sm font-medium transition ${
                  action === 'deny'
                    ? 'bg-red-600 text-white shadow'
                    : 'text-muted-foreground hover:text-foreground'
                }`}
              >
                Deny
              </button>
            </div>
          </div>

          {/* Submit */}
          <button
            onClick={handleAddRule}
            disabled={adding || !port.trim()}
            className="flex items-center gap-1.5 rounded-md bg-blue-600 px-5 py-2.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          >
            {adding ? <RefreshCw size={14} className="animate-spin" /> : <Plus size={14} />}
            {adding ? 'Adding...' : 'Add Rule'}
          </button>
        </div>
      </div>

      {/* Rules table */}
      <div className="rounded-lg border border-border bg-card shadow-md">
        <div className="border-b border-border px-5 py-4">
          <h2 className="text-sm font-semibold text-card-foreground">Firewall Rules ({rules.length})</h2>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-border text-muted-foreground">
                <th className="px-5 py-3 font-medium">#</th>
                <th className="px-5 py-3 font-medium">Action</th>
                <th className="px-5 py-3 font-medium">Port</th>
                <th className="px-5 py-3 font-medium">Protocol</th>
                <th className="px-5 py-3 font-medium">From</th>
                <th className="px-5 py-3 font-medium text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {rules.map(rule => (
                <tr
                  key={rule.number}
                  className="border-b border-border/50 text-card-foreground transition hover:bg-accent/30"
                >
                  <td className="px-5 py-3 font-mono text-xs text-muted-foreground">{rule.number}</td>
                  <td className="px-5 py-3">
                    <span
                      className={`inline-block rounded-md px-2.5 py-0.5 text-xs font-medium ${
                        rule.action.toLowerCase() === 'allow'
                          ? 'bg-emerald-500/20 text-emerald-400'
                          : 'bg-red-500/20 text-red-400'
                      }`}
                    >
                      {rule.action}
                    </span>
                  </td>
                  <td className="px-5 py-3 font-mono text-sm text-foreground">{rule.port || 'Any'}</td>
                  <td className="px-5 py-3 text-xs text-muted-foreground uppercase">{rule.proto || '--'}</td>
                  <td className="px-5 py-3 font-mono text-xs text-muted-foreground">{rule.from || 'Anywhere'}</td>
                  <td className="px-5 py-3 text-right">
                    {confirmDelete === rule.number ? (
                      <span className="flex items-center justify-end gap-2">
                        <span className="text-xs text-red-400">Delete?</span>
                        <button
                          onClick={() => handleDeleteRule(rule.number)}
                          disabled={deleting}
                          className="rounded bg-red-600 px-2 py-1 text-xs text-white hover:bg-red-700 disabled:opacity-50"
                        >
                          {deleting ? '...' : 'Yes'}
                        </button>
                        <button
                          onClick={() => setConfirmDelete(null)}
                          className="rounded bg-accent px-2 py-1 text-xs text-card-foreground"
                        >
                          No
                        </button>
                      </span>
                    ) : (
                      <button
                        onClick={() => setConfirmDelete(rule.number)}
                        className="flex items-center gap-1 rounded-md bg-red-500/15 px-2.5 py-1.5 text-xs font-medium text-red-400 hover:bg-red-500/25"
                      >
                        <Trash2 size={12} /> Delete
                      </button>
                    )}
                  </td>
                </tr>
              ))}
              {rules.length === 0 && (
                <tr>
                  <td colSpan={6} className="px-5 py-12 text-center text-muted-foreground">
                    <ShieldCheck size={32} className="mx-auto mb-3 opacity-40" />
                    No firewall rules configured.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
