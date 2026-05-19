import { useState, useEffect, useCallback } from 'react';
import { Link as RouterLink } from 'react-router-dom';
import {
  Lock, Shield, RefreshCw, CheckCircle, XCircle, Clock, AlertTriangle,
  Calendar, Eye, ChevronDown, Upload,
} from 'lucide-react';
import { fetchCerts, renewCert, uploadCert, type CertInfo } from '@/lib/api';
import { usePolling } from '@/hooks/usePolling';

function sslModeBadge(mode: string) {
  switch (mode) {
    case 'auto':
      return (
        <span className="rounded-full bg-emerald-500/15 px-2 py-0.5 text-xs font-medium text-emerald-400">
          Auto
        </span>
      );
    case 'manual':
      return (
        <span className="rounded-full bg-amber-500/15 px-2 py-0.5 text-xs font-medium text-amber-400">
          Manual
        </span>
      );
    default:
      return (
        <span className="rounded-full bg-slate-500/15 px-2 py-0.5 text-xs font-medium text-muted-foreground">
          Off
        </span>
      );
  }
}

function statusBadge(status: string) {
  switch (status) {
    case 'active':
      return (
        <span className="flex items-center gap-1 rounded-full bg-emerald-500/15 px-2 py-0.5 text-xs font-medium text-emerald-400">
          <CheckCircle size={10} /> Active
        </span>
      );
    case 'pending':
      return (
        <span className="flex items-center gap-1 rounded-full bg-blue-500/15 px-2 py-0.5 text-xs font-medium text-blue-400">
          <Clock size={10} /> Pending
        </span>
      );
    case 'expired':
      return (
        <span className="flex items-center gap-1 rounded-full bg-red-500/15 px-2 py-0.5 text-xs font-medium text-red-400">
          <XCircle size={10} /> Expired
        </span>
      );
    default:
      return (
        <span className="flex items-center gap-1 rounded-full bg-slate-500/15 px-2 py-0.5 text-xs font-medium text-muted-foreground">
          <AlertTriangle size={10} /> None
        </span>
      );
  }
}

function expiryColor(days: number | undefined): string {
  if (days === undefined) return 'bg-slate-600';
  if (days > 30) return 'bg-emerald-500';
  if (days >= 7) return 'bg-amber-500';
  return 'bg-red-500';
}

function expiryTextColor(days: number | undefined): string {
  if (days === undefined) return 'text-muted-foreground';
  if (days > 30) return 'text-emerald-400';
  if (days >= 7) return 'text-amber-400';
  return 'text-red-400';
}

function formatExpiry(expiry?: string): string {
  if (!expiry) return '--';
  try {
    return new Date(expiry).toLocaleDateString('en-US', {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    });
  } catch {
    return expiry;
  }
}

function canonicalDomain(host: string) {
  return host.trim().toLowerCase().replace(/\.$/, '').replace(/^www\./, '');
}

function certNeedsAttention(cert: CertInfo) {
  if (cert.ssl_mode === 'off') return false;
  if (cert.status !== 'active') return true;
  return cert.days_left !== undefined && cert.days_left <= 7;
}

type CertGroup = {
  domain: string;
  mainHost: string;
  canonicalHost: 'apex' | 'www';
  certs: CertInfo[];
};

function groupCerts(certs: CertInfo[]): CertGroup[] {
  const groups = new Map<string, CertGroup>();
  for (const cert of certs) {
    const domain = cert.domain || canonicalDomain(cert.host);
    const canonicalHost = cert.canonical_host === 'www' ? 'www' : 'apex';
    const mainHost = cert.main_host || (canonicalHost === 'www' ? `www.${domain}` : domain);
    const group = groups.get(domain) || { domain, mainHost, canonicalHost, certs: [] };
    group.certs.push(cert);
    group.mainHost = mainHost;
    group.canonicalHost = canonicalHost;
    groups.set(domain, group);
  }
  return Array.from(groups.values()).sort((a, b) => a.domain.localeCompare(b.domain));
}

function groupSummary(group: CertGroup) {
  const actionable = group.certs.filter(certNeedsAttention);
  if (group.certs.every(c => c.ssl_mode === 'off')) return { status: 'none', label: 'SSL off' };
  if (actionable.some(c => c.status === 'expired')) return { status: 'expired', label: 'Expired hostname' };
  if (actionable.length > 0) return { status: 'pending', label: `${actionable.length} hostname needs attention` };
  return { status: 'active', label: 'Apex + www healthy' };
}

function CertGroupCard({
  group,
  onViewDetails,
  onRenew,
  renewingHost,
}: {
  group: CertGroup;
  onViewDetails: (host: string) => void;
  onRenew: (host: string) => void;
  renewingHost: string | null;
}) {
  const summary = groupSummary(group);
  const primary = group.certs.find(c => c.host === group.mainHost) || group.certs[0];
  const progressPercent = primary?.days_left !== undefined
    ? Math.min(100, Math.max(0, (primary.days_left / 90) * 100))
    : 0;

  return (
    <div className="rounded-lg border border-border bg-card p-5 shadow-md">
      <div className="mb-3 flex items-start justify-between">
        <div className="flex items-center gap-2">
          <Lock size={16} className="text-muted-foreground" />
          <div>
            <h3 className="font-mono text-sm font-medium text-foreground">{group.domain}</h3>
            <p className="mt-0.5 text-[10px] text-muted-foreground">
              Main: <span className="font-mono">{group.mainHost}</span>
            </p>
          </div>
        </div>
        {sslModeBadge(primary?.ssl_mode || 'off')}
      </div>

      <div className="mb-4 space-y-2.5">
        <div className="flex items-center justify-between text-xs">
          <span className="text-muted-foreground">Status</span>
          {statusBadge(summary.status)}
        </div>
        <div className="flex items-center justify-between text-xs">
          <span className="text-muted-foreground">Summary</span>
          <span className="text-card-foreground">
            {summary.label}
          </span>
        </div>
        <div className="flex items-center justify-between text-xs">
          <span className="text-muted-foreground">Primary Expires</span>
          <span className={expiryTextColor(primary?.days_left)}>
            {formatExpiry(primary?.expiry)}
          </span>
        </div>
        {primary?.days_left !== undefined && (
          <div>
            <div className="mb-1 flex items-center justify-between text-xs">
              <span className="text-muted-foreground">Days Remaining</span>
              <span className={`font-medium ${expiryTextColor(primary.days_left)}`}>
                {primary.days_left}d
              </span>
            </div>
            <div className="h-1.5 w-full overflow-hidden rounded-full bg-accent">
              <div
                className={`h-full rounded-full transition-all ${expiryColor(primary.days_left)}`}
                style={{ width: `${progressPercent}%` }}
              />
            </div>
          </div>
        )}
        <div className="space-y-1.5 pt-1">
          {group.certs.map(cert => {
            const attention = certNeedsAttention(cert);
            const renewing = renewingHost === cert.host;
            return (
              <div key={cert.host} className={`flex items-center gap-2 rounded-md border px-2 py-1.5 text-xs ${attention ? 'border-amber-500/30 bg-amber-500/5' : 'border-border/60 bg-background/50'}`}>
                <span className="min-w-0 flex-1 truncate font-mono text-card-foreground">{cert.host}</span>
                {statusBadge(cert.status)}
                {cert.ssl_mode === 'auto' && attention && (
                  <button
                    onClick={() => onRenew(cert.host)}
                    disabled={renewing}
                    className="flex shrink-0 items-center gap-1 rounded bg-blue-600/15 px-2 py-1 text-[10px] text-blue-400 hover:bg-blue-600/25 disabled:opacity-50"
                  >
                    <RefreshCw size={10} className={renewing ? 'animate-spin' : ''} />
                    Force Renew
                  </button>
                )}
              </div>
            );
          })}
        </div>
      </div>

      <div className="flex gap-2 border-t border-border pt-3">
        <button
          onClick={() => onViewDetails(primary?.host || group.domain)}
          className="flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-xs text-card-foreground hover:bg-[#475569]"
        >
          <Eye size={12} /> View Details
        </button>
        {primary?.ssl_mode === 'auto' && (
          <button
            onClick={() => onRenew(primary.host)}
            disabled={renewingHost === primary.host}
            className="flex items-center gap-1.5 rounded-md bg-blue-600/15 px-3 py-1.5 text-xs text-blue-400 hover:bg-blue-600/25 disabled:opacity-50"
          >
            <RefreshCw size={12} className={renewingHost === primary.host ? 'animate-spin' : ''} />
            {renewingHost === primary.host ? 'Renewing...' : 'Renew Primary'}
          </button>
        )}
      </div>
    </div>
  );
}

export default function Certificates() {
  const [certs, setCerts] = useState<CertInfo[]>([]);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const [detailHost, setDetailHost] = useState<string | null>(null);
  const [renewingHost, setRenewingHost] = useState<string | null>(null);
  const [renewStatus, setRenewStatus] = useState<{ ok: boolean; message: string } | null>(null);

  const handleRenew = async (host: string) => {
    setRenewingHost(host);
    setRenewStatus(null);
    try {
      await renewCert(host);
      setRenewStatus({ ok: true, message: `Certificate for ${host} renewed` });
      load();
    } catch (e) {
      setRenewStatus({ ok: false, message: `Renewal failed: ${(e as Error).message}` });
    } finally {
      setRenewingHost(null);
    }
  };

  // Upload cert state
  const [uploadHost, setUploadHost] = useState('');
  const [uploadCertPEM, setUploadCertPEM] = useState('');
  const [uploadKeyPEM, setUploadKeyPEM] = useState('');
  const [uploadChain, setUploadChain] = useState('');
  const [uploading, setUploading] = useState(false);

  const handleUploadCert = async () => {
    if (!uploadHost || !uploadCertPEM || !uploadKeyPEM) return;
    setUploading(true);
    try {
      await uploadCert(uploadHost, uploadCertPEM, uploadKeyPEM, uploadChain || undefined);
      setRenewStatus({ ok: true, message: `Certificate uploaded for ${uploadHost}` });
      setUploadHost('');
      setUploadCertPEM('');
      setUploadKeyPEM('');
      setUploadChain('');
      await load();
    } catch (e) { setRenewStatus({ ok: false, message: (e as Error).message }); }
    finally { setUploading(false); }
  };

  const load = useCallback(async () => {
    try {
      const result = await fetchCerts();
      setCerts(result ?? []);
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

  // Auto-refresh while any cert is pending (ACME issuance in progress).
  // Visibility-aware so the page doesn't keep poking the cert manager when
  // the tab is in the background.
  const hasPending = certs.some(c => c.status === 'pending');
  usePolling(load, hasPending ? 5000 : null);

  const detailCert = detailHost ? certs.find((c) => c.host === detailHost) : null;
  const certGroups = groupCerts(certs);

  // Upcoming renewals: certs with days_left, sorted ascending
  const upcomingRenewals = certs
    .filter((c) => c.days_left !== undefined && c.status === 'active')
    .sort((a, b) => (a.days_left ?? 0) - (b.days_left ?? 0));

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">Certificates</h1>
          <p className="text-sm text-muted-foreground">
            SSL/TLS certificate management ({certGroups.length} domains, {certs.length} hostnames)
          </p>
        </div>
        <div className="flex gap-2">
          <button onClick={() => setUploadHost(uploadHost ? '' : '_new')}
            className="flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-blue-700">
            <Upload size={12} /> Upload Certificate
          </button>
          <button onClick={load}
            className="flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-xs text-card-foreground hover:bg-[#475569]">
            <RefreshCw size={12} /> Refresh
          </button>
        </div>
      </div>

      {/* Upload certificate form */}
      {uploadHost && (
        <div className="rounded-lg border border-blue-500/30 bg-blue-500/5 p-5 space-y-3">
          <h3 className="text-sm font-semibold text-blue-400 flex items-center gap-2"><Upload size={14} /> Upload SSL Certificate</h3>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-xs font-medium text-muted-foreground mb-1 block">Domain</label>
              <select value={uploadHost === '_new' ? '' : uploadHost} onChange={e => setUploadHost(e.target.value)}
                className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground outline-none">
                <option value="">Select domain...</option>
                {certs.map(c => <option key={c.host} value={c.host}>{c.host}</option>)}
              </select>
            </div>
          </div>
          <div>
            <label className="text-xs font-medium text-muted-foreground mb-1 block">Certificate (PEM)</label>
            <textarea value={uploadCertPEM} onChange={e => setUploadCertPEM(e.target.value)} rows={4}
              placeholder="-----BEGIN CERTIFICATE-----&#10;...&#10;-----END CERTIFICATE-----"
              className="w-full rounded-md border border-border bg-background px-3 py-2 text-xs font-mono text-foreground outline-none" />
          </div>
          <div>
            <label className="text-xs font-medium text-muted-foreground mb-1 block">Private Key (PEM)</label>
            <textarea value={uploadKeyPEM} onChange={e => setUploadKeyPEM(e.target.value)} rows={4}
              placeholder="-----BEGIN PRIVATE KEY-----&#10;...&#10;-----END PRIVATE KEY-----"
              className="w-full rounded-md border border-border bg-background px-3 py-2 text-xs font-mono text-foreground outline-none" />
          </div>
          <div>
            <label className="text-xs font-medium text-muted-foreground mb-1 block">Chain (optional)</label>
            <textarea value={uploadChain} onChange={e => setUploadChain(e.target.value)} rows={2}
              placeholder="Intermediate CA certificate (optional)"
              className="w-full rounded-md border border-border bg-background px-3 py-2 text-xs font-mono text-foreground outline-none" />
          </div>
          <div className="flex gap-2">
            <button onClick={handleUploadCert} disabled={uploading || !uploadHost || uploadHost === '_new' || !uploadCertPEM || !uploadKeyPEM}
              className="flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50">
              {uploading ? <RefreshCw size={12} className="animate-spin" /> : <Upload size={12} />} Upload
            </button>
            <button onClick={() => setUploadHost('')} className="rounded-md bg-accent px-4 py-2 text-sm text-card-foreground hover:bg-accent/80">Cancel</button>
          </div>
        </div>
      )}

      {error && (
        <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">{error}</div>
      )}

      {renewStatus && (
        <div className={`rounded-md px-4 py-3 text-sm ${renewStatus.ok ? 'bg-emerald-500/10 text-emerald-400' : 'bg-red-500/10 text-red-400'}`}>
          {renewStatus.message}
        </div>
      )}

      {loading ? (
        <div className="flex items-center justify-center py-16 text-sm text-muted-foreground">
          Loading certificates...
        </div>
      ) : (
        <>
          {/* Certificate cards grid */}
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
            {certGroups.map((group) => (
              <CertGroupCard
                key={group.domain}
                group={group}
                onViewDetails={(host) => setDetailHost(detailHost === host ? null : host)}
                onRenew={handleRenew}
                renewingHost={renewingHost}
              />
            ))}
            {certGroups.length === 0 && (
              <div className="col-span-full rounded-lg border border-dashed border-border bg-card p-10 text-center">
                <Lock size={32} className="mx-auto mb-3 text-muted-foreground opacity-40" />
                <p className="text-sm font-medium text-card-foreground">No certificates configured</p>
                <p className="mt-1 text-xs text-muted-foreground">
                  Add a domain with <code className="rounded bg-accent px-1.5 py-0.5 font-mono text-[11px] text-foreground">ssl: auto</code> on the{' '}
                  <RouterLink to="/domains" className="text-blue-400 underline hover:text-blue-300">Domains page</RouterLink>{' '}
                  to issue a free Let's Encrypt certificate, or click <strong>Upload Certificate</strong> above to install one manually.
                </p>
              </div>
            )}
          </div>

          {/* Detail panel */}
          {detailCert && (
            <div className="rounded-lg border border-border bg-card p-5 shadow-md">
              <div className="mb-4 flex items-center justify-between">
                <h2 className="flex items-center gap-2 text-sm font-semibold text-card-foreground">
                  <Shield size={14} /> Certificate Details: {detailCert.host}
                </h2>
                <button
                  onClick={() => setDetailHost(null)}
                  className="text-muted-foreground hover:text-foreground"
                >
                  <ChevronDown size={16} />
                </button>
              </div>
              <div className="grid grid-cols-2 gap-4 text-sm md:grid-cols-4">
                <div>
                  <span className="text-xs text-muted-foreground">Host</span>
                  <p className="font-mono text-foreground">{detailCert.host}</p>
                </div>
                <div>
                  <span className="text-xs text-muted-foreground">SSL Mode</span>
                  <p className="mt-0.5">{sslModeBadge(detailCert.ssl_mode)}</p>
                </div>
                <div>
                  <span className="text-xs text-muted-foreground">Status</span>
                  <p className="mt-0.5">{statusBadge(detailCert.status)}</p>
                </div>
                <div>
                  <span className="text-xs text-muted-foreground">Issuer</span>
                  <p className="text-foreground">{detailCert.issuer || '--'}</p>
                </div>
                <div>
                  <span className="text-xs text-muted-foreground">Expiry Date</span>
                  <p className="text-foreground">{formatExpiry(detailCert.expiry)}</p>
                </div>
                <div>
                  <span className="text-xs text-muted-foreground">Days Remaining</span>
                  <p className={`font-medium ${expiryTextColor(detailCert.days_left)}`}>
                    {detailCert.days_left !== undefined
                      ? `${detailCert.days_left} days`
                      : '--'}
                  </p>
                </div>
              </div>
            </div>
          )}

          {/* Upcoming renewals timeline */}
          {upcomingRenewals.length > 0 && (
            <div className="rounded-lg border border-border bg-card p-5 shadow-md">
              <h2 className="mb-4 flex items-center gap-2 text-sm font-semibold text-card-foreground">
                <Calendar size={14} /> Upcoming Renewals
              </h2>
              <div className="space-y-3">
                {upcomingRenewals.map((cert) => (
                  <div
                    key={cert.host}
                    className="flex items-center gap-4 rounded-md bg-background/50 px-4 py-3"
                  >
                    <div
                      className={`h-3 w-3 shrink-0 rounded-full ${expiryColor(cert.days_left)}`}
                    />
                    <div className="flex-1">
                      <span className="font-mono text-sm text-foreground">{cert.host}</span>
                    </div>
                    <div className="text-right">
                      <span className={`text-sm font-medium ${expiryTextColor(cert.days_left)}`}>
                        {cert.days_left}d remaining
                      </span>
                      <p className="text-xs text-muted-foreground">{formatExpiry(cert.expiry)}</p>
                    </div>
                    <div className="w-24">
                      <div className="h-1.5 w-full overflow-hidden rounded-full bg-accent">
                        <div
                          className={`h-full rounded-full ${expiryColor(cert.days_left)}`}
                          style={{
                            width: `${Math.min(100, Math.max(0, ((cert.days_left ?? 0) / 90) * 100))}%`,
                          }}
                        />
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </>
      )}
    </div>
  );
}
