import { useState, useEffect, useCallback } from 'react';
import {
  Lock, Shield, RefreshCw, CheckCircle, XCircle, Clock, AlertTriangle,
  Calendar, Eye, ChevronDown,
} from 'lucide-react';
import { fetchCerts, type CertInfo } from '@/lib/api';

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
        <span className="rounded-full bg-slate-500/15 px-2 py-0.5 text-xs font-medium text-slate-400">
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
        <span className="flex items-center gap-1 rounded-full bg-slate-500/15 px-2 py-0.5 text-xs font-medium text-slate-400">
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
  if (days === undefined) return 'text-slate-400';
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

function CertCard({ cert, onViewDetails }: { cert: CertInfo; onViewDetails: () => void }) {
  const progressPercent = cert.days_remaining !== undefined
    ? Math.min(100, Math.max(0, (cert.days_remaining / 90) * 100))
    : 0;

  return (
    <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5 shadow-md">
      <div className="mb-3 flex items-start justify-between">
        <div className="flex items-center gap-2">
          <Lock size={16} className="text-slate-400" />
          <h3 className="font-mono text-sm font-medium text-slate-200">{cert.host}</h3>
        </div>
        {sslModeBadge(cert.ssl_mode)}
      </div>

      <div className="mb-4 space-y-2.5">
        <div className="flex items-center justify-between text-xs">
          <span className="text-slate-400">Status</span>
          {statusBadge(cert.status)}
        </div>
        <div className="flex items-center justify-between text-xs">
          <span className="text-slate-400">Issuer</span>
          <span className="text-slate-300">
            {cert.issuer || '--'}
          </span>
        </div>
        <div className="flex items-center justify-between text-xs">
          <span className="text-slate-400">Expires</span>
          <span className={expiryTextColor(cert.days_remaining)}>
            {formatExpiry(cert.expiry)}
          </span>
        </div>
        {cert.days_remaining !== undefined && (
          <div>
            <div className="mb-1 flex items-center justify-between text-xs">
              <span className="text-slate-400">Days Remaining</span>
              <span className={`font-medium ${expiryTextColor(cert.days_remaining)}`}>
                {cert.days_remaining}d
              </span>
            </div>
            <div className="h-1.5 w-full overflow-hidden rounded-full bg-[#334155]">
              <div
                className={`h-full rounded-full transition-all ${expiryColor(cert.days_remaining)}`}
                style={{ width: `${progressPercent}%` }}
              />
            </div>
          </div>
        )}
      </div>

      <div className="flex gap-2 border-t border-[#334155] pt-3">
        <button
          onClick={onViewDetails}
          className="flex items-center gap-1.5 rounded-md bg-[#334155] px-3 py-1.5 text-xs text-slate-300 hover:bg-[#475569]"
        >
          <Eye size={12} /> View Details
        </button>
        <button
          disabled
          title="Coming soon"
          className="flex items-center gap-1.5 rounded-md bg-blue-600/15 px-3 py-1.5 text-xs text-blue-400 opacity-50 cursor-not-allowed"
        >
          <RefreshCw size={12} /> Force Renew
        </button>
      </div>
    </div>
  );
}

export default function Certificates() {
  const [certs, setCerts] = useState<CertInfo[]>([]);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const [detailHost, setDetailHost] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const result = await fetchCerts();
      setCerts(result);
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

  // Upcoming renewals: certs with days_remaining, sorted ascending
  const upcomingRenewals = certs
    .filter((c) => c.days_remaining !== undefined && c.status === 'active')
    .sort((a, b) => (a.days_remaining ?? 0) - (b.days_remaining ?? 0));

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">Certificates</h1>
          <p className="text-sm text-slate-400">
            SSL/TLS certificate management ({certs.length} domains)
          </p>
        </div>
        <button
          onClick={load}
          className="flex items-center gap-1.5 rounded-md bg-[#334155] px-3 py-1.5 text-xs text-slate-300 hover:bg-[#475569]"
        >
          <RefreshCw size={12} /> Refresh
        </button>
      </div>

      {error && (
        <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">{error}</div>
      )}

      {loading ? (
        <div className="flex items-center justify-center py-16 text-sm text-slate-500">
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
              />
            ))}
            {certs.length === 0 && (
              <div className="col-span-full rounded-lg border border-[#334155] bg-[#1e293b] p-8 text-center text-sm text-slate-500">
                No certificates found
              </div>
            )}
          </div>

          {/* Detail panel */}
          {detailCert && (
            <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5 shadow-md">
              <div className="mb-4 flex items-center justify-between">
                <h2 className="flex items-center gap-2 text-sm font-semibold text-slate-300">
                  <Shield size={14} /> Certificate Details: {detailCert.host}
                </h2>
                <button
                  onClick={() => setDetailHost(null)}
                  className="text-slate-400 hover:text-slate-200"
                >
                  <ChevronDown size={16} />
                </button>
              </div>
              <div className="grid grid-cols-2 gap-4 text-sm md:grid-cols-4">
                <div>
                  <span className="text-xs text-slate-400">Host</span>
                  <p className="font-mono text-slate-200">{detailCert.host}</p>
                </div>
                <div>
                  <span className="text-xs text-slate-400">SSL Mode</span>
                  <p className="mt-0.5">{sslModeBadge(detailCert.ssl_mode)}</p>
                </div>
                <div>
                  <span className="text-xs text-slate-400">Status</span>
                  <p className="mt-0.5">{statusBadge(detailCert.status)}</p>
                </div>
                <div>
                  <span className="text-xs text-slate-400">Issuer</span>
                  <p className="text-slate-200">{detailCert.issuer || '--'}</p>
                </div>
                <div>
                  <span className="text-xs text-slate-400">Expiry Date</span>
                  <p className="text-slate-200">{formatExpiry(detailCert.expiry)}</p>
                </div>
                <div>
                  <span className="text-xs text-slate-400">Days Remaining</span>
                  <p className={`font-medium ${expiryTextColor(detailCert.days_remaining)}`}>
                    {detailCert.days_remaining !== undefined
                      ? `${detailCert.days_remaining} days`
                      : '--'}
                  </p>
                </div>
              </div>
            </div>
          )}

          {/* Upcoming renewals timeline */}
          {upcomingRenewals.length > 0 && (
            <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5 shadow-md">
              <h2 className="mb-4 flex items-center gap-2 text-sm font-semibold text-slate-300">
                <Calendar size={14} /> Upcoming Renewals
              </h2>
              <div className="space-y-3">
                {upcomingRenewals.map((cert) => (
                  <div
                    key={cert.host}
                    className="flex items-center gap-4 rounded-md bg-[#0f172a]/50 px-4 py-3"
                  >
                    <div
                      className={`h-3 w-3 shrink-0 rounded-full ${expiryColor(cert.days_remaining)}`}
                    />
                    <div className="flex-1">
                      <span className="font-mono text-sm text-slate-200">{cert.host}</span>
                    </div>
                    <div className="text-right">
                      <span className={`text-sm font-medium ${expiryTextColor(cert.days_remaining)}`}>
                        {cert.days_remaining}d remaining
                      </span>
                      <p className="text-xs text-slate-500">{formatExpiry(cert.expiry)}</p>
                    </div>
                    <div className="w-24">
                      <div className="h-1.5 w-full overflow-hidden rounded-full bg-[#334155]">
                        <div
                          className={`h-full rounded-full ${expiryColor(cert.days_remaining)}`}
                          style={{
                            width: `${Math.min(100, Math.max(0, ((cert.days_remaining ?? 0) / 90) * 100))}%`,
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
