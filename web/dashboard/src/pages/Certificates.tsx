import { useState, useEffect, useCallback } from 'react';
import {
  Lock, Shield, RefreshCw, CheckCircle, XCircle, Clock, AlertTriangle,
  Calendar, Eye, ChevronDown,
} from 'lucide-react';
import { fetchCerts, renewCert, type CertInfo } from '@/lib/api';

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

function CertCard({ cert, onViewDetails, onRenew, renewing }: { cert: CertInfo; onViewDetails: () => void; onRenew: () => void; renewing: boolean }) {
  const progressPercent = cert.days_left !== undefined
    ? Math.min(100, Math.max(0, (cert.days_left / 90) * 100))
    : 0;

  return (
    <div className="rounded-lg border border-border bg-card p-5 shadow-md">
      <div className="mb-3 flex items-start justify-between">
        <div className="flex items-center gap-2">
          <Lock size={16} className="text-muted-foreground" />
          <h3 className="font-mono text-sm font-medium text-foreground">{cert.host}</h3>
        </div>
        {sslModeBadge(cert.ssl_mode)}
      </div>

      <div className="mb-4 space-y-2.5">
        <div className="flex items-center justify-between text-xs">
          <span className="text-muted-foreground">Status</span>
          {statusBadge(cert.status)}
        </div>
        <div className="flex items-center justify-between text-xs">
          <span className="text-muted-foreground">Issuer</span>
          <span className="text-card-foreground">
            {cert.issuer || '--'}
          </span>
        </div>
        <div className="flex items-center justify-between text-xs">
          <span className="text-muted-foreground">Expires</span>
          <span className={expiryTextColor(cert.days_left)}>
            {formatExpiry(cert.expiry)}
          </span>
        </div>
        {cert.days_left !== undefined && (
          <div>
            <div className="mb-1 flex items-center justify-between text-xs">
              <span className="text-muted-foreground">Days Remaining</span>
              <span className={`font-medium ${expiryTextColor(cert.days_left)}`}>
                {cert.days_left}d
              </span>
            </div>
            <div className="h-1.5 w-full overflow-hidden rounded-full bg-accent">
              <div
                className={`h-full rounded-full transition-all ${expiryColor(cert.days_left)}`}
                style={{ width: `${progressPercent}%` }}
              />
            </div>
          </div>
        )}
      </div>

      <div className="flex gap-2 border-t border-border pt-3">
        <button
          onClick={onViewDetails}
          className="flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-xs text-card-foreground hover:bg-[#475569]"
        >
          <Eye size={12} /> View Details
        </button>
        {cert.ssl_mode === 'auto' && (
          <button
            onClick={onRenew}
            disabled={renewing}
            className="flex items-center gap-1.5 rounded-md bg-blue-600/15 px-3 py-1.5 text-xs text-blue-400 hover:bg-blue-600/25 disabled:opacity-50"
          >
            <RefreshCw size={12} className={renewing ? 'animate-spin' : ''} />
            {renewing ? 'Renewing...' : 'Force Renew'}
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

  const detailCert = detailHost ? certs.find((c) => c.host === detailHost) : null;

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
            SSL/TLS certificate management ({certs.length} domains)
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
            {certs.map((cert) => (
              <CertCard
                key={cert.host}
                cert={cert}
                onViewDetails={() =>
                  setDetailHost(detailHost === cert.host ? null : cert.host)
                }
                onRenew={() => handleRenew(cert.host)}
                renewing={renewingHost === cert.host}
              />
            ))}
            {certs.length === 0 && (
              <div className="col-span-full rounded-lg border border-border bg-card p-8 text-center text-sm text-muted-foreground">
                No certificates found
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
