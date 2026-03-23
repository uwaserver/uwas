import { useState, useEffect, useCallback } from 'react';
import {
  Waypoints,
  RefreshCw,
  Search,
  CheckCircle,
  XCircle,
  Globe,
  Server,
  AlertTriangle,
} from 'lucide-react';
import { fetchDomains, checkDNS, type DomainData, type DNSResult } from '@/lib/api';

/* -- Record Row ---------------------------------------------------------- */

function RecordSection({
  label,
  records,
}: {
  label: string;
  records: string[] | undefined;
}) {
  if (!records || records.length === 0) return null;
  return (
    <div>
      <h4 className="mb-1.5 text-xs font-medium uppercase text-slate-500">{label}</h4>
      <div className="space-y-1">
        {records.map((r, i) => (
          <div
            key={`${label}-${i}`}
            className="rounded-md bg-[#0f172a] px-3 py-2 font-mono text-sm text-slate-300"
          >
            {r}
          </div>
        ))}
      </div>
    </div>
  );
}

/* -- Main Page ----------------------------------------------------------- */

export default function DNS() {
  const [domains, setDomains] = useState<DomainData[]>([]);
  const [selectedDomain, setSelectedDomain] = useState('');
  const [result, setResult] = useState<DNSResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [domainsLoading, setDomainsLoading] = useState(true);
  const [error, setError] = useState('');

  const loadDomains = useCallback(async () => {
    try {
      const data = await fetchDomains();
      setDomains(data ?? []);
      if (data && data.length > 0 && !selectedDomain) {
        setSelectedDomain(data[0].host);
      }
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setDomainsLoading(false);
    }
  }, [selectedDomain]);

  useEffect(() => {
    loadDomains();
  }, [loadDomains]);

  const handleCheck = async () => {
    if (!selectedDomain) return;
    setLoading(true);
    setError('');
    setResult(null);
    try {
      const data = await checkDNS(selectedDomain);
      setResult(data);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  };

  /* -- Render ------------------------------------------------------------ */

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <h1 className="text-2xl font-bold text-slate-100">DNS Checker</h1>
        <p className="text-sm text-slate-400">
          Verify DNS records and check if domains point to this server
        </p>
      </div>

      {/* Domain Selector */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5 shadow-md">
        <div className="mb-4 flex items-center gap-2">
          <Search size={18} className="text-blue-400" />
          <h2 className="text-sm font-semibold text-slate-300">Check Domain DNS</h2>
        </div>
        <div className="flex flex-col gap-4 sm:flex-row sm:items-end">
          <div className="flex-1">
            <label className="mb-1.5 block text-xs font-medium uppercase text-slate-500">
              Domain
            </label>
            {domainsLoading ? (
              <div className="flex h-10 items-center text-sm text-slate-500">
                Loading domains...
              </div>
            ) : (
              <select
                value={selectedDomain}
                onChange={(e) => setSelectedDomain(e.target.value)}
                className="w-full rounded-md border border-[#334155] bg-[#0f172a] px-3 py-2 text-sm text-slate-200 outline-none focus:border-blue-500"
              >
                {domains.length === 0 && (
                  <option value="">No domains configured</option>
                )}
                {domains.map((d) => (
                  <option key={d.host} value={d.host}>
                    {d.host}
                  </option>
                ))}
              </select>
            )}
          </div>
          <button
            onClick={handleCheck}
            disabled={loading || !selectedDomain}
            className="flex items-center gap-2 rounded-md bg-blue-600 px-6 py-2.5 text-sm font-medium text-white transition hover:bg-blue-700 disabled:opacity-50"
          >
            {loading ? (
              <RefreshCw size={16} className="animate-spin" />
            ) : (
              <Waypoints size={16} />
            )}
            {loading ? 'Checking...' : 'Check DNS'}
          </button>
        </div>
      </div>

      {/* Error */}
      {error && (
        <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">{error}</div>
      )}

      {/* Results */}
      {result && (
        <>
          {/* Points Here Badge */}
          <div
            className={`flex items-center gap-3 rounded-lg border p-5 shadow-md ${
              result.points_here
                ? 'border-emerald-500/30 bg-emerald-500/10'
                : 'border-red-500/30 bg-red-500/10'
            }`}
          >
            {result.points_here ? (
              <CheckCircle size={32} className="shrink-0 text-emerald-400" />
            ) : (
              <XCircle size={32} className="shrink-0 text-red-400" />
            )}
            <div>
              <h3
                className={`text-lg font-bold ${
                  result.points_here ? 'text-emerald-400' : 'text-red-400'
                }`}
              >
                {result.points_here
                  ? 'Points to this server'
                  : 'Does NOT point to this server'}
              </h3>
              <p className="text-sm text-slate-400">
                {result.points_here
                  ? `DNS records for ${result.domain} are correctly configured.`
                  : `DNS records for ${result.domain} do not resolve to this server's IP addresses.`}
              </p>
            </div>
          </div>

          {/* DNS error from lookup */}
          {result.error && (
            <div className="flex items-start gap-2 rounded-md bg-amber-500/10 px-4 py-3 text-sm text-amber-400">
              <AlertTriangle size={16} className="mt-0.5 shrink-0" />
              {result.error}
            </div>
          )}

          {/* Server IPs */}
          <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5 shadow-md">
            <div className="mb-3 flex items-center gap-2">
              <Server size={16} className="text-slate-400" />
              <h3 className="text-sm font-semibold text-slate-300">Server IP Addresses</h3>
            </div>
            <div className="flex flex-wrap gap-2">
              {result.server_ips.length > 0 ? (
                result.server_ips.map((ip) => (
                  <span
                    key={ip}
                    className="rounded-md bg-[#0f172a] px-3 py-1.5 font-mono text-sm text-slate-300"
                  >
                    {ip}
                  </span>
                ))
              ) : (
                <span className="text-sm text-slate-500">No server IPs detected</span>
              )}
            </div>
          </div>

          {/* DNS Records */}
          <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5 shadow-md">
            <div className="mb-4 flex items-center gap-2">
              <Globe size={16} className="text-blue-400" />
              <h3 className="text-sm font-semibold text-slate-300">
                DNS Records for {result.domain}
              </h3>
            </div>
            <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
              <RecordSection label="A Records" records={result.a} />
              <RecordSection label="AAAA Records" records={result.aaaa} />
              {result.cname && (
                <div>
                  <h4 className="mb-1.5 text-xs font-medium uppercase text-slate-500">CNAME</h4>
                  <div className="rounded-md bg-[#0f172a] px-3 py-2 font-mono text-sm text-slate-300">
                    {result.cname}
                  </div>
                </div>
              )}
              <RecordSection label="MX Records" records={result.mx} />
              <RecordSection label="NS Records" records={result.ns} />
              <RecordSection label="TXT Records" records={result.txt} />
            </div>

            {/* No records at all */}
            {!result.a?.length &&
              !result.aaaa?.length &&
              !result.cname &&
              !result.mx?.length &&
              !result.ns?.length &&
              !result.txt?.length && (
                <div className="py-8 text-center text-sm text-slate-500">
                  <Waypoints size={32} className="mx-auto mb-3 opacity-40" />
                  No DNS records found for this domain
                </div>
              )}
          </div>

          {/* Guidance if not pointing here */}
          {!result.points_here && (
            <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5 shadow-md">
              <div className="mb-3 flex items-center gap-2">
                <AlertTriangle size={16} className="text-amber-400" />
                <h3 className="text-sm font-semibold text-slate-300">How to Fix</h3>
              </div>
              <div className="space-y-2 text-sm text-slate-400">
                <p>
                  To point <span className="font-mono text-slate-200">{result.domain}</span> to
                  this server, update the DNS records at your domain registrar:
                </p>
                <ol className="ml-4 list-decimal space-y-1">
                  <li>
                    Log in to your domain registrar or DNS provider.
                  </li>
                  <li>
                    Create or update an <strong className="text-slate-300">A record</strong>{' '}
                    pointing to{' '}
                    {result.server_ips.length > 0 ? (
                      <span className="font-mono text-slate-200">
                        {result.server_ips[0]}
                      </span>
                    ) : (
                      "this server's IP"
                    )}
                    .
                  </li>
                  {result.server_ips.some((ip) => ip.includes(':')) && (
                    <li>
                      Optionally add an <strong className="text-slate-300">AAAA record</strong>{' '}
                      for IPv6 support.
                    </li>
                  )}
                  <li>
                    Wait for DNS propagation (usually 5 minutes to 48 hours).
                  </li>
                  <li>
                    Come back here and run the check again.
                  </li>
                </ol>
              </div>
            </div>
          )}
        </>
      )}
    </div>
  );
}
