import { useState, useCallback } from 'react';
import {
  Download,
  RefreshCw,
  CheckCircle,
  XCircle,
  ArrowRight,
  ExternalLink,
  Package,
  Tag,
  Calendar,
} from 'lucide-react';
import {
  checkUpdate,
  performUpdate,
  type UpdateInfo,
} from '@/lib/api';

function formatDate(dateStr: string): string {
  if (!dateStr) return '--';
  try {
    return new Date(dateStr).toLocaleString('en-US', {
      month: 'short',
      day: 'numeric',
      year: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    });
  } catch {
    return dateStr;
  }
}

export default function Updates() {
  const [info, setInfo] = useState<UpdateInfo | null>(null);
  const [checking, setChecking] = useState(false);
  const [updating, setUpdating] = useState(false);
  const [error, setError] = useState('');
  const [updateResult, setUpdateResult] = useState<{ from: string; to: string; message: string } | null>(null);

  const handleCheck = useCallback(async () => {
    setChecking(true);
    setError('');
    setUpdateResult(null);
    try {
      const result = await checkUpdate();
      setInfo(result);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setChecking(false);
    }
  }, []);

  const handleUpdate = async () => {
    setUpdating(true);
    setError('');
    try {
      const result = await performUpdate();
      setUpdateResult(result);
      // Re-check after update
      try {
        const newInfo = await checkUpdate();
        setInfo(newInfo);
      } catch {
        // May fail if the server is restarting
      }
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setUpdating(false);
    }
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">Updates</h1>
          <p className="text-sm text-muted-foreground">Check for and install UWAS updates</p>
        </div>
      </div>

      {error && (
        <div className="flex items-center gap-2 rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">
          <XCircle size={14} /> {error}
        </div>
      )}

      {updateResult && (
        <div className="flex items-center gap-2 rounded-md bg-emerald-500/10 px-4 py-3 text-sm text-emerald-400">
          <CheckCircle size={14} />
          <span>
            Updated from <code className="font-mono">{updateResult.from}</code> to{' '}
            <code className="font-mono">{updateResult.to}</code>. {updateResult.message}
          </span>
        </div>
      )}

      {/* Check for updates */}
      {!info && (
        <div className="rounded-lg border border-border bg-card px-6 py-12 text-center shadow-md">
          <Download size={48} className="mx-auto mb-4 text-muted-foreground" />
          <h2 className="text-lg font-semibold text-foreground mb-2">Check for Updates</h2>
          <p className="text-sm text-muted-foreground mb-6">
            Verify if a newer version of UWAS is available.
          </p>
          <button
            onClick={handleCheck}
            disabled={checking}
            className="inline-flex items-center gap-2 rounded-md bg-blue-600 px-6 py-3 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          >
            {checking ? (
              <RefreshCw size={16} className="animate-spin" />
            ) : (
              <RefreshCw size={16} />
            )}
            {checking ? 'Checking...' : 'Check for Updates'}
          </button>
        </div>
      )}

      {/* Update info display */}
      {info && (
        <>
          {/* Version cards */}
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
            <div className="rounded-lg border border-border bg-card p-5 shadow-md">
              <div className="flex items-center gap-2 text-muted-foreground mb-3">
                <Package size={18} />
                <span className="text-xs font-medium uppercase">Current Version</span>
              </div>
              <p className="text-xl font-bold sm:text-2xl text-foreground font-mono">{info.current_version}</p>
            </div>

            <div className="rounded-lg border border-border bg-card p-5 shadow-md">
              <div className="flex items-center gap-2 text-muted-foreground mb-3">
                <Tag size={18} />
                <span className="text-xs font-medium uppercase">Latest Version</span>
              </div>
              <p className="text-xl font-bold sm:text-2xl text-foreground font-mono">{info.latest_version}</p>
            </div>

            <div className="rounded-lg border border-border bg-card p-5 shadow-md">
              <div className="flex items-center gap-2 text-muted-foreground mb-3">
                <Calendar size={18} />
                <span className="text-xs font-medium uppercase">Published</span>
              </div>
              <p className="text-lg font-semibold text-foreground">{formatDate(info.published_at)}</p>
            </div>
          </div>

          {/* Update status + action */}
          <div className={`rounded-lg border p-5 shadow-md ${
            info.update_available
              ? 'border-blue-500/30 bg-blue-500/5'
              : 'border-emerald-500/30 bg-emerald-500/5'
          }`}>
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-3">
                {info.update_available ? (
                  <>
                    <Download size={24} className="text-blue-400" />
                    <div>
                      <p className="text-sm font-semibold text-blue-400">Update Available</p>
                      <p className="text-xs text-muted-foreground">
                        {info.current_version} <ArrowRight size={12} className="inline" /> {info.latest_version}
                      </p>
                    </div>
                  </>
                ) : (
                  <>
                    <CheckCircle size={24} className="text-emerald-400" />
                    <div>
                      <p className="text-sm font-semibold text-emerald-400">Up to Date</p>
                      <p className="text-xs text-muted-foreground">
                        You are running the latest version.
                      </p>
                    </div>
                  </>
                )}
              </div>
              <div className="flex items-center gap-3">
                <button
                  onClick={handleCheck}
                  disabled={checking}
                  className="flex items-center gap-1.5 rounded-md border border-border bg-card px-3 py-2 text-xs text-card-foreground hover:bg-accent disabled:opacity-50"
                >
                  {checking ? <RefreshCw size={12} className="animate-spin" /> : <RefreshCw size={12} />}
                  Re-check
                </button>
                {info.update_available && (
                  <button
                    onClick={handleUpdate}
                    disabled={updating}
                    className="flex items-center gap-2 rounded-md bg-blue-600 px-5 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
                  >
                    {updating ? (
                      <RefreshCw size={14} className="animate-spin" />
                    ) : (
                      <Download size={14} />
                    )}
                    {updating ? 'Updating...' : 'Install Update'}
                  </button>
                )}
              </div>
            </div>
          </div>

          {/* Release notes */}
          {info.release_notes && (
            <div className="rounded-lg border border-border bg-card shadow-md">
              <div className="flex items-center justify-between border-b border-border px-5 py-4">
                <h2 className="text-sm font-semibold text-card-foreground">Release Notes</h2>
                {info.release_url && (
                  <a
                    href={info.release_url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="flex items-center gap-1 text-xs text-blue-400 hover:text-blue-300"
                  >
                    View on GitHub <ExternalLink size={12} />
                  </a>
                )}
              </div>
              <div className="px-5 py-4">
                <pre className="whitespace-pre-wrap text-sm text-card-foreground font-mono leading-relaxed">
                  {info.release_notes}
                </pre>
              </div>
            </div>
          )}
        </>
      )}
    </div>
  );
}
