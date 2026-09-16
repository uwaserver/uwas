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
  ArrowUp,
  ArrowDown,
} from 'lucide-react';
import {
  fetchFirewall,
  firewallAllow,
  firewallDeny,
  firewallDeleteRule,
  firewallMoveRule,
  firewallEnable,
  firewallDisable,
  firewallConfirm,
  type FirewallStatus,
  type FirewallRule,
} from '@/lib/api';
import { useConfirm } from '@/components/useConfirm';
import AutoBlockPanel from '@/components/AutoBlockPanel';

export default function Firewall() {
  const { confirmAction } = useConfirm();
  const [fw, setFw] = useState<FirewallStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [status, setStatus] = useState('');
  const [toggling, setToggling] = useState(false);

  // Add rule form
  const [port, setPort] = useState('');
  const [proto, setProto] = useState('tcp');
  const [from, setFrom] = useState('');
  const [action, setAction] = useState<'allow' | 'deny'>('allow');
  const [adding, setAdding] = useState(false);
  const [moving, setMoving] = useState<number | null>(null);

  // Delete confirmation
  const [confirmDelete, setConfirmDelete] = useState<number | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [showV6, setShowV6] = useState(false);

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

  useEffect(() => {
    if (!fw?.rollback_pending) return;
    const id = window.setInterval(load, 1000);
    return () => window.clearInterval(id);
  }, [fw?.rollback_pending, load]);

  useEffect(() => {
    if (!status) return;
    const id = window.setTimeout(() => setStatus(s => s === status ? '' : s), 4000);
    return () => window.clearTimeout(id);
  }, [status]);

  const handleToggle = async () => {
    if (!fw) return;

    if (fw.active) {
      const ok = await confirmAction({
        title: 'Disable the firewall?',
        message: 'Every listening service on this server will be reachable from the internet, including SSH, databases, and any internal admin ports. This is rarely what you want on a production server.',
        confirmLabel: 'Disable firewall',
        variant: 'danger',
      });
      if (!ok) {
        return;
      }
    } else {
      const rules = fw.rules ?? [];
      const hasSSH = rules.some(r =>
        r.action.toLowerCase() === 'allow' &&
        (r.port === '22' || r.port === '22/tcp' || /\b22\b/.test(r.port || '')),
      );
      if (!hasSSH) {
        const ok = await confirmAction({
          title: 'Enable the firewall?',
          message: 'UWAS will first allow its own ports (SSH 22, HTTP 80, HTTPS 443, and the admin port), set default incoming deny (policy — not a numbered rule), then turn the firewall on with a 60-second safety timer. Duplicate allow rules are skipped.',
          confirmLabel: 'Enable firewall',
        });
        if (!ok) {
          return;
        }
      }
    }

    setToggling(true);
    setError('');
    setStatus('');
    try {
      if (fw.active) {
        await firewallDisable();
        setStatus('Firewall disabled.');
      } else {
        const res = await firewallEnable();
        setStatus(
          res.rollback_seconds
            ? `Firewall enabled. Auto-disables in ${res.rollback_seconds}s unless you confirm access below.`
            : 'Firewall enabled.',
        );
      }
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setToggling(false);
    }
  };

  const handleConfirmRollback = async () => {
    try {
      await firewallConfirm();
      setStatus('Firewall confirmed — it will stay enabled.');
      await load();
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const handleAddRule = async () => {
    const p = port.trim();
    const src = from.trim();
    if (!p && !src && action === 'allow') {
      // Allow any/any opens the host completely — require an explicit source or port.
      setError('Specify a port and/or source IP for allow rules (empty both = open everything)');
      return;
    }
    if (p) {
      const portRangeRe = /^(\d{1,5})(:\d{1,5})?$/;
      const match = p.match(portRangeRe);
      if (match) {
        const num = parseInt(match[1]);
        if (num < 1 || num > 65535) { setError('Port must be between 1 and 65535'); return; }
        if (match[2]) {
          const end = parseInt(match[2].slice(1));
          if (end < 1 || end > 65535 || end <= num) { setError('Invalid port range'); return; }
        }
      } else if (!/^(any|all|\*)$/i.test(p)) {
        setError('Port must be a number, range, or empty/any');
        return;
      }
    }
    if (src && !/^[\d.:a-fA-F/]+$/.test(src)) {
      setError('Source must be an IP or CIDR (e.g. 203.0.113.10 or 10.0.0.0/8)');
      return;
    }
    setAdding(true);
    setError('');
    setStatus('');
    try {
      const protoParam = proto === 'both' ? undefined : proto;
      const portParam = !p || /^(any|all|\*)$/i.test(p) ? '' : p;
      if (action === 'allow') {
        await firewallAllow(portParam, protoParam, src || undefined);
      } else {
        await firewallDeny(portParam, protoParam, src || undefined);
      }
      setPort('');
      setFrom('');
      setStatus(`Rule added: ${action} ${portParam || 'any'}/${proto}${src ? ` from ${src}` : ''}`);
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

  const handleMove = async (num: number, direction: 'up' | 'down') => {
    setMoving(num);
    setError('');
    try {
      await firewallMoveRule(num, direction);
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setMoving(null);
    }
  };

  const rules: FirewallRule[] = fw?.rules ?? [];
  const filteredRules = showV6 ? rules : rules.filter(r => !r.v6);
  const v6Count = rules.filter(r => r.v6).length;

  if (loading) {
    return (
      <div className="flex h-96 items-center justify-center text-muted-foreground">Loading firewall status...</div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">Firewall</h1>
          <p className="text-sm text-muted-foreground">
            Manage firewall rules{fw?.backend ? ` (backend: ${fw.backend})` : ''} — allows above port denies; use arrows to reorder
          </p>
        </div>
        <button
          onClick={load}
          className="flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-xs text-card-foreground hover:bg-[#475569]"
        >
          <RefreshCw size={12} /> Refresh
        </button>
      </div>

      <AutoBlockPanel />

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

      {fw?.rollback_pending && (
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-amber-500/40 bg-amber-500/10 px-4 py-3">
          <div className="flex items-center gap-2 text-sm text-amber-300">
            <Power size={16} />
            <span>
              Firewall just enabled. It will <strong>auto-disable in {fw.rollback_seconds ?? 0}s</strong> unless you
              confirm you still have access — so a lockout fixes itself.
            </span>
          </div>
          <button
            onClick={handleConfirmRollback}
            className="flex items-center gap-1.5 rounded-md bg-emerald-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-emerald-700"
          >
            <CheckCircle size={12} /> I still have access — keep it on
          </button>
        </div>
      )}

      <div className="flex items-center justify-between rounded-lg border border-border bg-card p-5 shadow-md">
        <div className="flex items-center gap-4">
          {fw?.active ? (
            <div className="flex items-center gap-2">
              <ShieldCheck size={24} className="text-emerald-400" />
              <div>
                <p className="text-sm font-semibold text-emerald-400">Firewall Active</p>
                <p className="text-xs text-muted-foreground">
                  {filteredRules.length} rules shown
                  {v6Count > 0 && !showV6 ? ` (${v6Count} IPv6 hidden)` : rules.length !== filteredRules.length ? ` / ${rules.length} total` : ''}
                </p>
              </div>
            </div>
          ) : (
            <div className="flex items-center gap-2">
              <ShieldOff size={24} className="text-red-400" />
              <div>
                <p className="text-sm font-semibold text-red-400">Firewall Inactive</p>
                <p className="text-xs text-muted-foreground">
                  {fw?.staged
                    ? `${filteredRules.length} rule(s) staged — applied when you enable`
                    : 'No rules are being enforced'}
                  {v6Count > 0 && !showV6 ? ` (${v6Count} IPv6 hidden)` : ''}
                </p>
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

      <div className="rounded-lg border border-border bg-card p-5 shadow-md">
        <div className="mb-4 flex items-center gap-2">
          <Plus size={18} className="text-blue-400" />
          <h2 className="text-sm font-semibold text-card-foreground">Add Rule</h2>
        </div>

        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-4 sm:flex-row sm:items-end">
            <div className="flex-1">
              <label className="mb-1.5 block text-xs font-medium uppercase text-muted-foreground">Port</label>
              <input
                type="text"
                value={port}
                onChange={e => setPort(e.target.value)}
                placeholder="empty = any port"
                className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none focus:border-blue-500"
              />
            </div>

            <div className="flex-1">
              <label className="mb-1.5 block text-xs font-medium uppercase text-muted-foreground">Source IP / CIDR</label>
              <input
                type="text"
                value={from}
                onChange={e => setFrom(e.target.value)}
                placeholder="Anywhere (leave empty) or 203.0.113.10"
                className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm font-mono text-foreground outline-none focus:border-blue-500"
              />
            </div>

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

            <button
              onClick={handleAddRule}
              disabled={adding}
              className="flex items-center gap-1.5 rounded-md bg-blue-600 px-5 py-2.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
            >
              {adding ? <RefreshCw size={14} className="animate-spin" /> : <Plus size={14} />}
              {adding ? 'Adding...' : 'Add Rule'}
            </button>
          </div>
          <p className="text-[11px] text-muted-foreground">
            Empty port = any port. Empty source = anywhere. New allows insert above port denies / default deny. Enable adds a single DENY any→any at the bottom (plus UFW default policy).
          </p>
        </div>
      </div>

      <div className="rounded-lg border border-border bg-card shadow-md">
        <div className="flex items-center justify-between border-b border-border px-5 py-4">
          <h2 className="text-sm font-semibold text-card-foreground">Firewall Rules ({filteredRules.length})</h2>
          {v6Count > 0 && (
            <button
              onClick={() => setShowV6(!showV6)}
              className="text-xs text-muted-foreground hover:text-foreground"
            >
              {showV6 ? 'Hide' : 'Show'} {v6Count} IPv6 rule{v6Count > 1 ? 's' : ''}
            </button>
          )}
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-border text-muted-foreground">
                <th className="px-5 py-3 font-medium">#</th>
                <th className="px-5 py-3 font-medium">Action</th>
                <th className="px-5 py-3 font-medium">Port</th>
                <th className="px-5 py-3 font-medium">Protocol</th>
                <th className="px-5 py-3 font-medium">Source</th>
                <th className="px-5 py-3 font-medium text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {filteredRules.map((rule, i) => (
                <tr
                  key={`${rule.number}-${rule.v6 ? 'v6' : 'v4'}`}
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
                      <span className="flex items-center justify-end gap-1">
                        <button
                          onClick={() => handleMove(rule.number, 'up')}
                          disabled={moving === rule.number || i === 0}
                          className="rounded-md bg-accent/50 p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground disabled:opacity-30"
                          title="Move up"
                        >
                          <ArrowUp size={12} />
                        </button>
                        <button
                          onClick={() => handleMove(rule.number, 'down')}
                          disabled={moving === rule.number || i === filteredRules.length - 1}
                          className="rounded-md bg-accent/50 p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground disabled:opacity-30"
                          title="Move down"
                        >
                          <ArrowDown size={12} />
                        </button>
                        <button
                          onClick={() => setConfirmDelete(rule.number)}
                          className="flex items-center gap-1 rounded-md bg-red-500/15 px-2.5 py-1.5 text-xs font-medium text-red-400 hover:bg-red-500/25"
                        >
                          <Trash2 size={12} /> Delete
                        </button>
                      </span>
                    )}
                  </td>
                </tr>
              ))}
              {filteredRules.length === 0 && (
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
