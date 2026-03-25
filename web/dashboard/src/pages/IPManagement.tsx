import { useState, useEffect, useCallback } from 'react';
import {
  Server,
  RefreshCw,
  Globe,
  CheckCircle,
  XCircle,
} from 'lucide-react';
import {
  fetchServerIPs,
  fetchDomains,
  updateDomain,
  type ServerIPInfo,
  type DomainData,
} from '@/lib/api';

export default function IPManagement() {
  const [ips, setIPs] = useState<ServerIPInfo[]>([]);
  const [publicIP, setPublicIP] = useState('');
  const [domains, setDomains] = useState<DomainData[]>([]);
  const [loading, setLoading] = useState(true);
  const [status, setStatus] = useState<{ ok: boolean; message: string } | null>(null);
  const [saving, setSaving] = useState<Record<string, boolean>>({});

  const loadAll = useCallback(async () => {
    try {
      const [ipData, domainData] = await Promise.all([
        fetchServerIPs(),
        fetchDomains(),
      ]);
      setIPs(ipData?.ips ?? []);
      setPublicIP(ipData?.public_ip ?? '');
      setDomains(domainData ?? []);
    } catch {
      // ignore
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadAll();
  }, [loadAll]);

  const handleIPChange = async (host: string, newIP: string) => {
    setSaving(prev => ({ ...prev, [host]: true }));
    setStatus(null);
    try {
      await updateDomain(host, { ip: newIP });
      setStatus({ ok: true, message: `IP updated for ${host} to ${newIP}` });
      await loadAll();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setSaving(prev => ({ ...prev, [host]: false }));
    }
  };

  /* Which domains use a given IP */
  const domainsUsingIP = (ip: string): string[] =>
    domains.filter(d => d.ip === ip).map(d => d.host);

  if (loading) {
    return (
      <div className="flex h-96 items-center justify-center text-muted-foreground">
        Loading IP information...
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">IP Management</h1>
          <p className="text-sm text-muted-foreground">
            Manage server IP addresses and domain IP assignments
          </p>
        </div>
        <button
          onClick={() => { setLoading(true); void loadAll(); }}
          className="flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-xs text-card-foreground hover:bg-[#475569]"
        >
          <RefreshCw size={12} /> Refresh
        </button>
      </div>

      {/* Status toast */}
      {status && (
        <div
          className={`flex items-center gap-2 rounded-md px-4 py-3 text-sm ${
            status.ok ? 'bg-emerald-500/10 text-emerald-400' : 'bg-red-500/10 text-red-400'
          }`}
        >
          {status.ok ? <CheckCircle size={14} /> : <XCircle size={14} />}
          {status.message}
        </div>
      )}

      {/* Public IP */}
      {publicIP && (
        <div className="rounded-lg border border-blue-500/30 bg-blue-500/10 p-5 shadow-md">
          <div className="flex items-center gap-3">
            <Globe size={24} className="text-blue-400" />
            <div>
              <p className="text-xs font-medium uppercase text-blue-400/70">Public IP Address</p>
              <p className="text-xl font-bold sm:text-2xl font-mono text-blue-300">{publicIP}</p>
            </div>
          </div>
        </div>
      )}

      {/* Server IPs Table */}
      <div>
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground">
          Server IP Addresses
        </h2>
        <div className="rounded-lg border border-border bg-card shadow-md">
          <div className="overflow-x-auto">
            <table className="w-full text-left text-sm">
              <thead>
                <tr className="border-b border-border text-muted-foreground">
                  <th className="px-5 py-3 font-medium">IP Address</th>
                  <th className="px-5 py-3 font-medium">Version</th>
                  <th className="px-5 py-3 font-medium">Interface</th>
                  <th className="px-5 py-3 font-medium">Primary</th>
                  <th className="px-5 py-3 font-medium">Used by Domains</th>
                </tr>
              </thead>
              <tbody>
                {ips.length === 0 && (
                  <tr>
                    <td colSpan={5} className="px-5 py-8 text-center text-muted-foreground">
                      No server IPs detected.
                    </td>
                  </tr>
                )}
                {ips.map(ip => {
                  const usedBy = domainsUsingIP(ip.ip);
                  return (
                    <tr key={`${ip.ip}-${ip.interface}`} className="border-b border-border/50 hover:bg-background/30">
                      <td className="px-5 py-3">
                        <span className="font-mono font-semibold text-foreground">{ip.ip}</span>
                      </td>
                      <td className="px-5 py-3">
                        <span className={`rounded px-2 py-0.5 text-xs font-medium ${
                          ip.version === 4
                            ? 'bg-blue-500/15 text-blue-400'
                            : 'bg-purple-500/15 text-purple-400'
                        }`}>
                          IPv{ip.version}
                        </span>
                      </td>
                      <td className="px-5 py-3">
                        <span className="font-mono text-xs text-muted-foreground">{ip.interface}</span>
                      </td>
                      <td className="px-5 py-3">
                        {ip.primary ? (
                          <span className="rounded bg-emerald-500/15 px-2 py-0.5 text-xs font-medium text-emerald-400">
                            Primary
                          </span>
                        ) : (
                          <span className="text-xs text-muted-foreground">--</span>
                        )}
                      </td>
                      <td className="px-5 py-3">
                        {usedBy.length > 0 ? (
                          <div className="flex flex-wrap gap-1">
                            {usedBy.map(host => (
                              <span
                                key={host}
                                className="rounded bg-accent px-2 py-0.5 text-xs text-card-foreground"
                              >
                                {host}
                              </span>
                            ))}
                          </div>
                        ) : (
                          <span className="text-xs text-muted-foreground">None</span>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      </div>

      {/* Domain IP Assignments */}
      <div>
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground">
          Domain IP Assignments
        </h2>
        <div className="rounded-lg border border-border bg-card shadow-md">
          <div className="overflow-x-auto">
            <table className="w-full text-left text-sm">
              <thead>
                <tr className="border-b border-border text-muted-foreground">
                  <th className="px-5 py-3 font-medium">Domain</th>
                  <th className="px-5 py-3 font-medium">Type</th>
                  <th className="px-5 py-3 font-medium">Current IP</th>
                  <th className="px-5 py-3 font-medium">Assign IP</th>
                </tr>
              </thead>
              <tbody>
                {domains.length === 0 && (
                  <tr>
                    <td colSpan={4} className="px-5 py-8 text-center text-muted-foreground">
                      No domains configured.
                    </td>
                  </tr>
                )}
                {domains.map(d => (
                  <tr key={d.host} className="border-b border-border/50 hover:bg-background/30">
                    <td className="px-5 py-3">
                      <span className="font-semibold text-foreground">{d.host}</span>
                    </td>
                    <td className="px-5 py-3">
                      <span className="rounded bg-accent px-2 py-0.5 text-xs text-card-foreground">
                        {d.type}
                      </span>
                    </td>
                    <td className="px-5 py-3">
                      <span className="font-mono text-xs text-muted-foreground">
                        {d.ip || 'Default'}
                      </span>
                    </td>
                    <td className="px-5 py-3">
                      <div className="flex items-center gap-2">
                        <select
                          value={d.ip || ''}
                          onChange={e => void handleIPChange(d.host, e.target.value)}
                          disabled={saving[d.host] || ips.length === 0}
                          className="rounded-md border border-border bg-background px-2.5 py-1.5 text-xs text-foreground outline-none focus:border-blue-500 focus:ring-1 focus:ring-blue-500 disabled:opacity-50"
                        >
                          <option value="">Default</option>
                          {ips.map(ip => (
                            <option key={`${ip.ip}-${ip.interface}`} value={ip.ip}>
                              {ip.ip} (IPv{ip.version} - {ip.interface})
                            </option>
                          ))}
                        </select>
                        {saving[d.host] && (
                          <RefreshCw size={12} className="animate-spin text-muted-foreground" />
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      </div>

      {/* Info */}
      <div className="rounded-lg border border-border bg-card p-5 shadow-md">
        <div className="mb-3 flex items-center gap-2">
          <Server size={16} className="text-muted-foreground" />
          <h3 className="text-sm font-semibold text-card-foreground">About IP Assignment</h3>
        </div>
        <p className="text-sm text-muted-foreground">
          Assign a specific server IP to a domain to control which network interface serves its traffic.
          Domains set to "Default" will use the server's primary IP address.
          This is useful for servers with multiple IPs or when you need separate IPs for different SSL certificates.
        </p>
      </div>
    </div>
  );
}
