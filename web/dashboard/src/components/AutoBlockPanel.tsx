import { useState, useEffect, useCallback } from 'react';
import { Ban, RefreshCw, Plus, XCircle, CheckCircle, ShieldOff } from 'lucide-react';
import {
  fetchAutoBlock,
  autoBlockAdd,
  autoBlockRemove,
  type AutoBlockStatus,
  type AutoBlockEntry,
} from '@/lib/api';
import { useConfirm } from '@/components/useConfirm';

// Live view of the connection-level auto-blocker. Config (enable, thresholds)
// lives in Settings; this panel is the operational side — what is blocked right
// now, and manual block/unblock. It shows a clear disabled state rather than an
// error when the feature is off, since off is the default.
export default function AutoBlockPanel() {
  const { confirmAction } = useConfirm();
  const [data, setData] = useState<AutoBlockStatus | null>(null);
  const [error, setError] = useState('');
  const [status, setStatus] = useState('');
  const [loading, setLoading] = useState(true);

  const [ip, setIp] = useState('');
  const [duration, setDuration] = useState('');
  const [adding, setAdding] = useState(false);
  // "now" is held in state and advanced from an effect so the relative-time
  // column stays a pure function of props during render (calling Date.now()
  // in render is impure and flagged by react-hooks).
  const [now, setNow] = useState(0);

  const load = useCallback(async () => {
    try {
      setData(await fetchAutoBlock());
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

  // A block flips to "expired" on its own; a periodic refresh keeps the table
  // honest without the operator hitting Refresh. Only polls while enabled.
  // The same tick advances "now" so the "expires in" column counts down.
  useEffect(() => {
    setNow(Date.now());
    if (!data?.enabled) return;
    const id = window.setInterval(() => {
      setNow(Date.now());
      load();
    }, 15000);
    return () => window.clearInterval(id);
  }, [data?.enabled, load]);

  const flash = (msg: string) => {
    setStatus(msg);
    window.setTimeout(() => setStatus(''), 4000);
  };

  const handleAdd = async () => {
    const v = ip.trim();
    if (!v) return;
    setAdding(true);
    try {
      await autoBlockAdd(v, 'manual', duration.trim() || undefined);
      flash(`Blocked ${v}`);
      setIp('');
      setDuration('');
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setAdding(false);
    }
  };

  const handleRemove = async (entry: AutoBlockEntry) => {
    const ok = await confirmAction({
      title: `Unblock ${entry.ip}?`,
      message:
        'The source can reach the server again immediately, and its escalation history is cleared so a repeat ' +
        'offence starts from the base block duration.',
      confirmLabel: 'Unblock',
    });
    if (!ok) return;
    try {
      await autoBlockRemove(entry.ip);
      flash(`Unblocked ${entry.ip}`);
      await load();
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const fmtWhen = (iso: string, ref: number) => {
    if (!iso || ref === 0) return '—';
    const t = new Date(iso).getTime();
    if (Number.isNaN(t)) return '—';
    const diff = t - ref;
    if (diff <= 0) return 'expired';
    const mins = Math.round(diff / 60000);
    if (mins < 60) return `${mins}m`;
    const hrs = Math.round(mins / 6) / 10;
    return `${hrs}h`;
  };

  const blocks = data?.blocks ?? [];

  return (
    <div className="rounded-xl border border-border bg-card">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-5 py-4">
        <div className="flex items-center gap-2">
          <Ban size={18} className="text-red-400" />
          <div>
            <h2 className="text-sm font-semibold text-foreground">Auto-Block</h2>
            <p className="text-xs text-muted-foreground">
              Source IPs blocked at the connection accept path
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          {data && (
            <span
              className={`rounded-full px-2.5 py-0.5 text-xs font-medium ${
                !data.enabled
                  ? 'bg-muted text-muted-foreground'
                  : data.dry_run
                    ? 'bg-amber-500/15 text-amber-400'
                    : 'bg-emerald-500/15 text-emerald-400'
              }`}
            >
              {!data.enabled ? 'Disabled' : data.dry_run ? 'Dry run' : 'Enforcing'}
            </span>
          )}
          <button
            onClick={load}
            className="flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-xs text-card-foreground hover:bg-[#475569]"
          >
            <RefreshCw size={12} /> Refresh
          </button>
        </div>
      </div>

      {error && (
        <div className="mx-5 mt-4 flex items-center gap-2 rounded-md bg-red-500/10 px-4 py-2.5 text-sm text-red-400">
          <XCircle size={14} /> {error}
        </div>
      )}
      {status && (
        <div className="mx-5 mt-4 flex items-center gap-2 rounded-md bg-emerald-500/10 px-4 py-2.5 text-sm text-emerald-400">
          <CheckCircle size={14} /> {status}
        </div>
      )}

      {loading ? (
        <div className="px-5 py-10 text-center text-sm text-muted-foreground">Loading…</div>
      ) : !data?.enabled ? (
        <div className="px-5 py-10 text-center text-sm text-muted-foreground">
          <ShieldOff size={28} className="mx-auto mb-3 opacity-40" />
          Auto-block is disabled. Enable it under{' '}
          <span className="font-medium text-foreground">Settings → Security → Auto-Block</span>, then restart the
          service.
        </div>
      ) : (
        <>
          {/* Stat row */}
          <div className="grid grid-cols-2 gap-px bg-border sm:grid-cols-4">
            {[
              { label: 'Active blocks', value: data.active_blocks ?? 0 },
              { label: 'Total detected', value: data.total_detected ?? 0 },
              { label: 'Window', value: data.window ?? '—' },
              { label: 'Firewall sync', value: data.firewall_sync ? 'on' : 'off' },
            ].map((s) => (
              <div key={s.label} className="bg-card px-5 py-3">
                <div className="text-lg font-semibold text-foreground">{s.value}</div>
                <div className="text-xs text-muted-foreground">{s.label}</div>
              </div>
            ))}
          </div>

          {/* Manual block form */}
          <div className="flex flex-wrap items-end gap-2 border-t border-border px-5 py-4">
            <div className="flex-1 min-w-[10rem]">
              <label className="mb-1 block text-xs text-muted-foreground">IP address</label>
              <input
                value={ip}
                onChange={(e) => setIp(e.target.value)}
                onKeyDown={(e) => e.key === 'Enter' && handleAdd()}
                placeholder="203.0.113.10"
                className="w-full rounded-md border border-border bg-background px-3 py-1.5 text-sm text-foreground"
              />
            </div>
            <div className="w-32">
              <label className="mb-1 block text-xs text-muted-foreground">Duration</label>
              <input
                value={duration}
                onChange={(e) => setDuration(e.target.value)}
                onKeyDown={(e) => e.key === 'Enter' && handleAdd()}
                placeholder="permanent"
                className="w-full rounded-md border border-border bg-background px-3 py-1.5 text-sm text-foreground"
              />
            </div>
            <button
              onClick={handleAdd}
              disabled={adding || !ip.trim()}
              className="flex items-center gap-1.5 rounded-md bg-red-500/15 px-3 py-1.5 text-xs font-medium text-red-400 hover:bg-red-500/25 disabled:opacity-50"
            >
              <Plus size={12} /> {adding ? 'Blocking…' : 'Block IP'}
            </button>
          </div>

          {/* Blocks table */}
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-t border-border text-left text-xs text-muted-foreground">
                  <th className="px-5 py-2 font-medium">IP</th>
                  <th className="px-5 py-2 font-medium">Reason</th>
                  <th className="px-5 py-2 font-medium">Hits</th>
                  <th className="px-5 py-2 font-medium">Level</th>
                  <th className="px-5 py-2 font-medium">Expires in</th>
                  <th className="px-5 py-2 font-medium">Firewall</th>
                  <th className="px-5 py-2 font-medium"></th>
                </tr>
              </thead>
              <tbody>
                {blocks.map((b) => (
                  <tr key={b.ip} className="border-t border-border">
                    <td className="px-5 py-2.5 font-mono text-foreground">{b.ip}</td>
                    <td className="px-5 py-2.5 text-muted-foreground">{b.reason}</td>
                    <td className="px-5 py-2.5 text-muted-foreground">{b.hits}</td>
                    <td className="px-5 py-2.5 text-muted-foreground">{b.level}</td>
                    <td className="px-5 py-2.5 text-muted-foreground">
                      {b.expires_at ? fmtWhen(b.expires_at, now) : 'permanent'}
                    </td>
                    <td className="px-5 py-2.5">
                      {b.firewall ? (
                        <span className="text-emerald-400">kernel</span>
                      ) : (
                        <span className="text-muted-foreground">memory</span>
                      )}
                    </td>
                    <td className="px-5 py-2.5 text-right">
                      <button
                        onClick={() => handleRemove(b)}
                        className="rounded-md bg-accent px-2.5 py-1 text-xs text-card-foreground hover:bg-[#475569]"
                      >
                        Unblock
                      </button>
                    </td>
                  </tr>
                ))}
                {blocks.length === 0 && (
                  <tr>
                    <td colSpan={7} className="px-5 py-10 text-center text-muted-foreground">
                      <CheckCircle size={28} className="mx-auto mb-3 opacity-40" />
                      No active blocks.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </>
      )}
    </div>
  );
}
