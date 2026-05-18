import { useState, useEffect, useCallback } from 'react';
import { Link2, ShieldAlert, ShieldOff, ShieldCheck, Trash2, RefreshCw } from 'lucide-react';
import {
  fetchUnknownDomains, blockUnknownDomain, unblockUnknownDomain, dismissUnknownDomain,
  fetchFeatures, fetchDomains, aliasUnknownDomain,
  type UnknownDomainEntry, type FeatureStatus, type DomainData,
} from '@/lib/api';
import FeatureBanner from '@/components/FeatureBanner';
import { usePolling } from '@/hooks/usePolling';

function timeAgo(iso: string): string {
  const t = new Date(iso).getTime();
  // Date constructed from "" / "invalid" / undefined yields NaN, which
  // would otherwise cascade into "NaNm ago" in the table.
  if (!Number.isFinite(t)) return '—';
  const diff = Date.now() - t;
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return 'just now';
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

export default function UnknownDomains() {
  const [entries, setEntries] = useState<UnknownDomainEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [acting, setActing] = useState<string | null>(null);
  const [error, setError] = useState('');
  const [featureStatus, setFeatureStatus] = useState<FeatureStatus | null>(null);
  const [domains, setDomains] = useState<DomainData[]>([]);
  const [aliasHost, setAliasHost] = useState('');
  const [aliasTarget, setAliasTarget] = useState('');

  const load = useCallback(() => {
    fetchUnknownDomains()
      .then(d => setEntries(d ?? []))
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    fetchFeatures().then(f => setFeatureStatus(f.unknown_domains ?? null)).catch(() => {});
    fetchDomains().then(d => {
      setDomains(d ?? []);
      if ((d ?? []).length > 0) setAliasTarget((d ?? [])[0].host);
    }).catch(() => {});
  }, []);
  usePolling(load, 10_000);

  const act = async (host: string, fn: (h: string) => Promise<unknown>) => {
    setActing(host);
    setError('');
    try {
      await fn(host);
      load();
    } catch (e) {
      // Previously this catch was empty — every Block/Unblock/Dismiss
      // failure (server error, permission revoked, network) silently
      // disappeared and the button just stopped spinning, leaving the
      // user thinking the action worked when it didn't.
      setError(`${host}: ${(e as Error).message}`);
    } finally {
      setActing(null);
    }
  };

  const blocked = entries.filter(e => e.blocked);
  const unblocked = entries.filter(e => !e.blocked);
  const attachAlias = async () => {
    if (!aliasHost || !aliasTarget) return;
    setActing(aliasHost);
    setError('');
    try {
      await aliasUnknownDomain(aliasHost, aliasTarget);
      setAliasHost('');
      load();
      fetchDomains().then(d => setDomains(d ?? [])).catch(() => {});
    } catch (e) {
      setError(`${aliasHost}: ${(e as Error).message}`);
    } finally {
      setActing(null);
    }
  };

  return (
    <div className="space-y-6">
      <FeatureBanner feature="unknown_domains" status={featureStatus} label="Unknown-host tracker" />

      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">Unknown Domains</h1>
          <p className="mt-1 text-sm text-muted-foreground flex items-center gap-2">
            Hostnames hitting your server that aren't configured.
            <span className="flex items-center gap-1 text-[10px] text-emerald-400"><span className="inline-block h-1.5 w-1.5 rounded-full bg-emerald-400 animate-pulse" /> Auto-refresh 10s</span>
          </p>
        </div>
        <button
          onClick={load}
          className="flex items-center gap-2 rounded-md border border-border bg-card px-3 py-2 text-sm text-card-foreground hover:bg-accent"
        >
          <RefreshCw size={14} />
          Refresh
        </button>
      </div>

      {error && (
        <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">
          {error}
        </div>
      )}

      {aliasHost && (
        <div className="rounded-lg border border-blue-500/30 bg-blue-500/10 px-4 py-3">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
            <div className="min-w-0 flex-1">
              <p className="text-sm font-medium text-blue-200">
                Attach <span className="font-mono">{aliasHost}</span> as an alias
              </p>
              <p className="mt-0.5 text-xs text-muted-foreground">
                The alias will share the selected site and auto SSL will request a separate certificate for it.
              </p>
            </div>
            <select
              value={aliasTarget}
              onChange={e => setAliasTarget(e.target.value)}
              className="rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground outline-none"
            >
              {domains.map(d => <option key={d.host} value={d.host}>{d.host}</option>)}
            </select>
            <div className="flex gap-2">
              <button
                type="button"
                onClick={attachAlias}
                disabled={!aliasTarget || acting === aliasHost}
                className="rounded-md bg-blue-500 px-3 py-2 text-sm font-medium text-white hover:bg-blue-600 disabled:opacity-50"
              >
                Attach
              </button>
              <button type="button" onClick={() => setAliasHost('')} className="rounded-md border border-border px-3 py-2 text-sm text-foreground hover:bg-card">
                Cancel
              </button>
            </div>
          </div>
        </div>
      )}

      {loading && (
        <div className="text-center text-muted-foreground py-12">Loading...</div>
      )}

      {!loading && entries.length === 0 && (
        <div className="rounded-lg border border-border bg-card px-6 py-12 text-center">
          <ShieldCheck size={40} className="mx-auto mb-3 text-green-400" />
          <p className="text-card-foreground font-medium">No unknown domains detected</p>
          <p className="text-sm text-muted-foreground mt-1">All incoming requests match a configured domain.</p>
        </div>
      )}

      {/* Unblocked — candidate domains */}
      {unblocked.length > 0 && (
        <div>
          <h2 className="text-sm font-semibold uppercase tracking-wider text-muted-foreground mb-3">
            Unconfigured Hosts ({unblocked.length})
          </h2>
          <div className="overflow-hidden rounded-lg border border-border">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border bg-card/50 text-left text-xs uppercase tracking-wider text-muted-foreground">
                  <th className="px-4 py-3">Hostname</th>
                  <th className="px-4 py-3 text-right">Hits</th>
                  <th className="px-4 py-3">First Seen</th>
                  <th className="px-4 py-3">Last Seen</th>
                  <th className="px-4 py-3 text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {unblocked.map(e => (
                  <tr key={e.host} className="bg-background hover:bg-card/50 transition-colors">
                    <td className="px-4 py-3 font-mono text-foreground">{e.host}</td>
                    <td className="px-4 py-3 text-right">
                      <span className="rounded-full bg-amber-500/15 px-2.5 py-0.5 text-xs font-medium text-amber-400">
                        {e.hits.toLocaleString()}
                      </span>
                    </td>
                    <td className="px-4 py-3 text-muted-foreground">{timeAgo(e.first_seen)}</td>
                    <td className="px-4 py-3 text-muted-foreground">{timeAgo(e.last_seen)}</td>
                    <td className="px-4 py-3 text-right">
                      <div className="flex items-center justify-end gap-2">
                        <button
                          onClick={() => {
                            setAliasHost(e.host);
                            if (!aliasTarget && domains.length > 0) setAliasTarget(domains[0].host);
                          }}
                          disabled={acting === e.host || domains.length === 0}
                          className="flex items-center gap-1 rounded-md bg-blue-500/15 px-2.5 py-1.5 text-xs font-medium text-blue-400 hover:bg-blue-500/25 disabled:opacity-50"
                          title="Attach as alias to an existing domain"
                        >
                          <Link2 size={13} />
                          Alias
                        </button>
                        <button
                          onClick={() => act(e.host, blockUnknownDomain)}
                          disabled={acting === e.host}
                          className="flex items-center gap-1 rounded-md bg-red-500/15 px-2.5 py-1.5 text-xs font-medium text-red-400 hover:bg-red-500/25 disabled:opacity-50"
                          title="Block this domain (403)"
                        >
                          <ShieldAlert size={13} />
                          Block
                        </button>
                        <button
                          onClick={() => act(e.host, dismissUnknownDomain)}
                          disabled={acting === e.host}
                          className="flex items-center gap-1 rounded-md bg-slate-500/15 px-2.5 py-1.5 text-xs font-medium text-muted-foreground hover:bg-slate-500/25 disabled:opacity-50"
                          title="Dismiss from list"
                        >
                          <Trash2 size={13} />
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Blocked domains */}
      {blocked.length > 0 && (
        <div>
          <h2 className="text-sm font-semibold uppercase tracking-wider text-red-400 mb-3">
            Blocked ({blocked.length})
          </h2>
          <div className="overflow-hidden rounded-lg border border-red-500/30">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-red-500/20 bg-red-500/5 text-left text-xs uppercase tracking-wider text-muted-foreground">
                  <th className="px-4 py-3">Hostname</th>
                  <th className="px-4 py-3 text-right">Hits</th>
                  <th className="px-4 py-3">Last Seen</th>
                  <th className="px-4 py-3 text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-red-500/10">
                {blocked.map(e => (
                  <tr key={e.host} className="bg-background hover:bg-red-500/5 transition-colors">
                    <td className="px-4 py-3 font-mono text-card-foreground line-through decoration-red-500/40">{e.host}</td>
                    <td className="px-4 py-3 text-right">
                      <span className="rounded-full bg-red-500/15 px-2.5 py-0.5 text-xs font-medium text-red-400">
                        {e.hits.toLocaleString()}
                      </span>
                    </td>
                    <td className="px-4 py-3 text-muted-foreground">{timeAgo(e.last_seen)}</td>
                    <td className="px-4 py-3 text-right">
                      <div className="flex items-center justify-end gap-2">
                        <button
                          onClick={() => {
                            setAliasHost(e.host);
                            if (!aliasTarget && domains.length > 0) setAliasTarget(domains[0].host);
                          }}
                          disabled={acting === e.host || domains.length === 0}
                          className="flex items-center gap-1 rounded-md bg-blue-500/15 px-2.5 py-1.5 text-xs font-medium text-blue-400 hover:bg-blue-500/25 disabled:opacity-50"
                          title="Attach as alias to an existing domain"
                        >
                          <Link2 size={13} />
                          Alias
                        </button>
                        <button
                          onClick={() => act(e.host, unblockUnknownDomain)}
                          disabled={acting === e.host}
                          className="flex items-center gap-1 rounded-md bg-green-500/15 px-2.5 py-1.5 text-xs font-medium text-green-400 hover:bg-green-500/25 disabled:opacity-50"
                          title="Unblock"
                        >
                          <ShieldOff size={13} />
                          Unblock
                        </button>
                        <button
                          onClick={() => act(e.host, dismissUnknownDomain)}
                          disabled={acting === e.host}
                          className="flex items-center gap-1 rounded-md bg-slate-500/15 px-2.5 py-1.5 text-xs font-medium text-muted-foreground hover:bg-slate-500/25 disabled:opacity-50"
                          title="Remove from list"
                        >
                          <Trash2 size={13} />
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}
