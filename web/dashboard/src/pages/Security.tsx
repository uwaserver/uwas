import { useState, useEffect, useCallback } from 'react';
import { Shield, ShieldAlert, Bot, Link2Off, Gauge, RefreshCw } from 'lucide-react';
import {
  fetchSecurityStats, fetchSecurityBlocked,
  type SecurityStats, type BlockedRequest,
} from '@/lib/api';

function timeAgo(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  const secs = Math.floor(diff / 1000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

const reasonColors: Record<string, string> = {
  waf: 'bg-red-500/15 text-red-400',
  bot: 'bg-orange-500/15 text-orange-400',
  rate: 'bg-amber-500/15 text-amber-400',
  hotlink: 'bg-purple-500/15 text-purple-400',
};

const reasonLabels: Record<string, string> = {
  waf: 'WAF',
  bot: 'Bot',
  rate: 'Rate Limit',
  hotlink: 'Hotlink',
};

export default function Security() {
  const [stats, setStats] = useState<SecurityStats | null>(null);
  const [blocked, setBlocked] = useState<BlockedRequest[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(() => {
    Promise.all([fetchSecurityStats(), fetchSecurityBlocked()])
      .then(([s, b]) => {
        setStats(s);
        setBlocked(b ?? []);
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    load();
    const iv = setInterval(load, 5000);
    return () => clearInterval(iv);
  }, [load]);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">Security</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            WAF, bot protection, rate limiting, and threat monitoring.
          </p>
        </div>
        <button onClick={load} className="flex items-center gap-2 rounded-md border border-border bg-card px-3 py-2 text-sm text-card-foreground hover:bg-accent">
          <RefreshCw size={14} /> Refresh
        </button>
      </div>

      {/* Stats cards */}
      {stats && (
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
          {([
            { label: 'Total Blocked', value: stats.total_blocked, icon: Shield, color: 'text-red-400' },
            { label: 'WAF Blocked', value: stats.waf_blocked, icon: ShieldAlert, color: 'text-red-400' },
            { label: 'Bots Blocked', value: stats.bot_blocked, icon: Bot, color: 'text-orange-400' },
            { label: 'Rate Limited', value: stats.rate_blocked, icon: Gauge, color: 'text-amber-400' },
            { label: 'Hotlinks Blocked', value: stats.hotlink_blocked, icon: Link2Off, color: 'text-purple-400' },
          ] as const).map(card => (
            <div key={card.label} className="rounded-lg border border-border bg-card p-4">
              <div className="flex items-center gap-2 text-xs text-muted-foreground mb-2">
                <card.icon size={14} className={card.color} />
                {card.label}
              </div>
              <p className={`text-xl font-bold sm:text-2xl ${card.color}`}>
                {card.value.toLocaleString()}
              </p>
            </div>
          ))}
        </div>
      )}

      {/* Active protections */}
      <div className="rounded-lg border border-border bg-card p-5">
        <h2 className="text-sm font-semibold text-card-foreground mb-3">Active Protections</h2>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">
          {[
            { name: 'WAF (SQL/XSS/Shell)', active: true, detail: 'URL + body inspection' },
            { name: 'Bot Guard', active: true, detail: 'Blocks scanners & scrapers' },
            { name: 'Path Protection', active: true, detail: '.git, .env, wp-config.php' },
            { name: 'Security Headers', active: true, detail: 'X-Frame, HSTS, nosniff' },
            { name: 'Rate Limiting', active: true, detail: 'Per-IP token bucket' },
            { name: 'IP ACL', active: true, detail: 'Per-domain whitelist/blacklist' },
            { name: 'Basic Auth', active: true, detail: 'SHA256 + constant-time' },
            { name: 'Hotlink Protection', active: true, detail: 'Referer-based blocking' },
          ].map(p => (
            <div key={p.name} className="flex items-start gap-2 rounded-md bg-background px-3 py-2.5">
              <span className="mt-0.5 h-2 w-2 shrink-0 rounded-full bg-emerald-400" />
              <div>
                <p className="text-xs font-medium text-card-foreground">{p.name}</p>
                <p className="text-[10px] text-muted-foreground">{p.detail}</p>
              </div>
            </div>
          ))}
        </div>
      </div>

      {/* Recent blocked requests */}
      <div>
        <h2 className="text-sm font-semibold uppercase tracking-wider text-muted-foreground mb-3">
          Recent Blocked Requests ({blocked.length})
        </h2>
        {loading ? (
          <div className="text-center text-sm text-muted-foreground py-8">Loading...</div>
        ) : blocked.length === 0 ? (
          <div className="rounded-lg border border-border bg-card px-6 py-12 text-center">
            <Shield size={40} className="mx-auto mb-3 text-emerald-400" />
            <p className="text-card-foreground font-medium">No blocked requests yet</p>
            <p className="text-sm text-muted-foreground mt-1">All traffic is clean.</p>
          </div>
        ) : (
          <div className="overflow-hidden rounded-lg border border-border">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border bg-card/50 text-left text-xs uppercase tracking-wider text-muted-foreground">
                  <th className="px-4 py-3">Time</th>
                  <th className="px-4 py-3">IP</th>
                  <th className="px-4 py-3">Path</th>
                  <th className="px-4 py-3">Reason</th>
                  <th className="px-4 py-3">User Agent</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {blocked.slice(0, 100).map((b, i) => (
                  <tr key={i} className="bg-background hover:bg-card/50">
                    <td className="px-4 py-2.5 text-xs text-muted-foreground whitespace-nowrap">{timeAgo(b.time)}</td>
                    <td className="px-4 py-2.5 font-mono text-xs text-card-foreground">{b.ip}</td>
                    <td className="px-4 py-2.5 font-mono text-xs text-muted-foreground max-w-[200px] truncate" title={b.path}>{b.path}</td>
                    <td className="px-4 py-2.5">
                      <span className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${reasonColors[b.reason] || 'bg-slate-500/15 text-muted-foreground'}`}>
                        {reasonLabels[b.reason] || b.reason}
                      </span>
                    </td>
                    <td className="px-4 py-2.5 text-[10px] text-muted-foreground max-w-[250px] truncate" title={b.ua}>{b.ua || '-'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
