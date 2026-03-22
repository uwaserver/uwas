import { useState, useEffect, useCallback } from 'react';
import {
  Archive,
  Plus,
  RefreshCw,
  Trash2,
  RotateCcw,
  Download,
  CheckCircle,
  XCircle,
  Clock,
  HardDrive,
  Calendar,
  Shield,
  AlertTriangle,
  X,
} from 'lucide-react';
import {
  fetchBackups,
  createBackup,
  restoreBackup,
  deleteBackup,
  fetchBackupSchedule,
  updateBackupSchedule,
  type BackupInfo,
  type BackupSchedule,
} from '@/lib/api';
import Card from '@/components/Card';

type Provider = 'local' | 's3' | 'sftp';

const PROVIDERS: { value: Provider; label: string }[] = [
  { value: 'local', label: 'Local' },
  { value: 's3', label: 'S3' },
  { value: 'sftp', label: 'SFTP' },
];

const INTERVAL_OPTIONS = [
  { value: '6h', label: 'Every 6 hours' },
  { value: '12h', label: 'Every 12 hours' },
  { value: '24h', label: 'Every 24 hours' },
  { value: '7d', label: 'Every 7 days' },
];

function providerBadge(provider: string) {
  switch (provider) {
    case 'local':
      return (
        <span className="rounded-full bg-blue-500/15 px-2 py-0.5 text-xs font-medium text-blue-400">
          Local
        </span>
      );
    case 's3':
      return (
        <span className="rounded-full bg-orange-500/15 px-2 py-0.5 text-xs font-medium text-orange-400">
          S3
        </span>
      );
    case 'sftp':
      return (
        <span className="rounded-full bg-emerald-500/15 px-2 py-0.5 text-xs font-medium text-emerald-400">
          SFTP
        </span>
      );
    default:
      return (
        <span className="rounded-full bg-slate-500/15 px-2 py-0.5 text-xs font-medium text-slate-400">
          {provider}
        </span>
      );
  }
}

function formatSize(bytes: number): string {
  if (bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`;
}

function formatRelativeTime(dateStr: string): string {
  if (!dateStr) return '--';
  try {
    const date = new Date(dateStr);
    const now = new Date();
    const diffMs = now.getTime() - date.getTime();
    const diffSec = Math.floor(diffMs / 1000);
    const diffMin = Math.floor(diffSec / 60);
    const diffHour = Math.floor(diffMin / 60);
    const diffDay = Math.floor(diffHour / 24);

    if (diffSec < 60) return 'just now';
    if (diffMin < 60) return `${diffMin}m ago`;
    if (diffHour < 24) return `${diffHour}h ago`;
    if (diffDay < 30) return `${diffDay}d ago`;
    return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' });
  } catch {
    return dateStr;
  }
}

function formatDateTime(dateStr: string): string {
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

/* ── Confirmation Modal ────────────────────────────────────────────── */

interface ConfirmModalProps {
  open: boolean;
  title: string;
  children: React.ReactNode;
  confirmLabel: string;
  confirmClass?: string;
  onConfirm: () => void;
  onCancel: () => void;
  loading?: boolean;
}

function ConfirmModal({
  open,
  title,
  children,
  confirmLabel,
  confirmClass = 'bg-red-600 hover:bg-red-700',
  onConfirm,
  onCancel,
  loading,
}: ConfirmModalProps) {
  if (!open) return null;
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
      <div className="w-full max-w-md rounded-lg border border-[#334155] bg-[#1e293b] p-6 shadow-xl">
        <div className="mb-4 flex items-center justify-between">
          <h3 className="text-lg font-semibold text-slate-100">{title}</h3>
          <button onClick={onCancel} className="text-slate-400 hover:text-slate-200">
            <X size={18} />
          </button>
        </div>
        <div className="mb-6 text-sm text-slate-300">{children}</div>
        <div className="flex justify-end gap-3">
          <button
            onClick={onCancel}
            disabled={loading}
            className="rounded-md border border-[#334155] px-4 py-2 text-sm font-medium text-slate-300 transition hover:bg-[#334155] disabled:opacity-50"
          >
            Cancel
          </button>
          <button
            onClick={onConfirm}
            disabled={loading}
            className={`flex items-center gap-2 rounded-md px-4 py-2 text-sm font-medium text-white transition disabled:opacity-50 ${confirmClass}`}
          >
            {loading && <RefreshCw size={14} className="animate-spin" />}
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}

/* ── Main Page ─────────────────────────────────────────────────────── */

export default function Backups() {
  const [backups, setBackups] = useState<BackupInfo[]>([]);
  const [schedule, setSchedule] = useState<BackupSchedule | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [status, setStatus] = useState<{ ok: boolean; message: string } | null>(null);

  // Create backup state
  const [createProvider, setCreateProvider] = useState<Provider>('local');
  const [creating, setCreating] = useState(false);

  // Restore modal state
  const [restoreTarget, setRestoreTarget] = useState<BackupInfo | null>(null);
  const [restoring, setRestoring] = useState(false);

  // Delete modal state
  const [deleteTarget, setDeleteTarget] = useState<BackupInfo | null>(null);
  const [deleting, setDeleting] = useState(false);

  // Schedule edit state
  const [scheduleForm, setScheduleForm] = useState<{
    enabled: boolean;
    interval: string;
    keep: number;
  }>({ enabled: false, interval: '24h', keep: 7 });
  const [savingSchedule, setSavingSchedule] = useState(false);

  const load = useCallback(async () => {
    try {
      const [b, s] = await Promise.all([fetchBackups(), fetchBackupSchedule()]);
      setBackups(b);
      setSchedule(s);
      setScheduleForm({ enabled: s.enabled, interval: s.interval, keep: s.keep });
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

  // Derived data
  const sortedBackups = [...backups].sort(
    (a, b) => new Date(b.created).getTime() - new Date(a.created).getTime(),
  );
  const latestBackup = sortedBackups[0] ?? null;
  const totalSize = backups.reduce((sum, b) => sum + b.size, 0);

  /* ── Actions ──────────────────────────────────────────────────────── */

  const handleCreate = async () => {
    setCreating(true);
    setStatus(null);
    try {
      const backup = await createBackup(createProvider);
      setStatus({ ok: true, message: `Backup created: ${backup.name} (${formatSize(backup.size)})` });
      await load();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setCreating(false);
    }
  };

  const handleRestore = async () => {
    if (!restoreTarget) return;
    setRestoring(true);
    setStatus(null);
    try {
      await restoreBackup(restoreTarget.name, restoreTarget.provider);
      setRestoreTarget(null);
      setStatus({ ok: true, message: `Backup restored: ${restoreTarget.name}. A reload may be required for changes to take effect.` });
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setRestoring(false);
    }
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    setDeleting(true);
    setStatus(null);
    try {
      await deleteBackup(deleteTarget.name, deleteTarget.provider);
      setDeleteTarget(null);
      setStatus({ ok: true, message: `Backup deleted: ${deleteTarget.name}` });
      await load();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setDeleting(false);
    }
  };

  const handleSaveSchedule = async () => {
    setSavingSchedule(true);
    setStatus(null);
    try {
      await updateBackupSchedule(scheduleForm);
      setStatus({ ok: true, message: 'Backup schedule updated' });
      await load();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setSavingSchedule(false);
    }
  };

  /* ── Render ───────────────────────────────────────────────────────── */

  if (loading) {
    return (
      <div className="flex h-96 items-center justify-center text-slate-400">
        Loading backups...
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">Backups</h1>
          <p className="text-sm text-slate-400">Backup and restore management</p>
        </div>
        <button
          onClick={load}
          className="flex items-center gap-1.5 rounded-md bg-[#334155] px-3 py-1.5 text-xs text-slate-300 hover:bg-[#475569]"
        >
          <RefreshCw size={12} /> Refresh
        </button>
      </div>

      {/* Status message */}
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

      {/* Error */}
      {error && (
        <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">{error}</div>
      )}

      {/* Section 1: Overview Cards */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <Card
          icon={<Archive size={20} />}
          label="Total Backups"
          value={backups.length}
        />
        <Card
          icon={<Clock size={20} />}
          label="Latest Backup"
          value={latestBackup ? formatRelativeTime(latestBackup.created) : 'None'}
        />
        <Card
          icon={<HardDrive size={20} />}
          label="Storage Used"
          value={formatSize(totalSize)}
        />
        <Card
          icon={<Calendar size={20} />}
          label="Auto-Backup"
          value={schedule?.enabled ? 'On' : 'Off'}
        />
      </div>

      {/* Section 2: Create Backup */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5 shadow-md">
        <div className="mb-4 flex items-center gap-2">
          <Plus size={18} className="text-blue-400" />
          <h2 className="text-sm font-semibold text-slate-300">Create Backup</h2>
        </div>
        <div className="flex flex-col gap-4 sm:flex-row sm:items-end">
          {/* Provider selector */}
          <div>
            <label className="mb-2 block text-xs font-medium uppercase text-slate-500">
              Provider
            </label>
            <div className="flex gap-1 rounded-lg bg-[#0f172a] p-1">
              {PROVIDERS.map((p) => (
                <button
                  key={p.value}
                  onClick={() => setCreateProvider(p.value)}
                  className={`rounded-md px-4 py-2 text-sm font-medium transition ${
                    createProvider === p.value
                      ? 'bg-blue-600 text-white shadow'
                      : 'text-slate-400 hover:text-slate-200'
                  }`}
                >
                  {p.label}
                </button>
              ))}
            </div>
          </div>
          {/* Create button */}
          <button
            onClick={handleCreate}
            disabled={creating}
            className="flex items-center gap-2 rounded-md bg-blue-600 px-6 py-2.5 text-sm font-medium text-white transition hover:bg-blue-700 disabled:opacity-50"
          >
            {creating ? (
              <RefreshCw size={16} className="animate-spin" />
            ) : (
              <Plus size={16} />
            )}
            {creating ? 'Creating Backup...' : 'Create Backup Now'}
          </button>
        </div>
      </div>

      {/* Section 3: Backup List */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] shadow-md">
        <div className="border-b border-[#334155] px-5 py-4">
          <h2 className="text-sm font-semibold text-slate-300">
            Backup List ({backups.length})
          </h2>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-[#334155] text-slate-400">
                <th className="px-5 py-3 font-medium">Name</th>
                <th className="px-5 py-3 font-medium">Provider</th>
                <th className="px-5 py-3 font-medium">Size</th>
                <th className="px-5 py-3 font-medium">Created</th>
                <th className="px-5 py-3 font-medium text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {sortedBackups.map((b) => (
                <tr
                  key={`${b.provider}-${b.name}`}
                  className="border-b border-[#334155]/50 text-slate-300 transition hover:bg-[#334155]/30"
                >
                  <td className="px-5 py-3 font-mono text-xs">{b.name}</td>
                  <td className="px-5 py-3">{providerBadge(b.provider)}</td>
                  <td className="px-5 py-3 text-slate-400">{formatSize(b.size)}</td>
                  <td className="px-5 py-3 text-slate-400" title={formatDateTime(b.created)}>
                    {formatRelativeTime(b.created)}
                  </td>
                  <td className="px-5 py-3">
                    <div className="flex items-center justify-end gap-2">
                      <button
                        onClick={() => setRestoreTarget(b)}
                        className="flex items-center gap-1 rounded-md bg-amber-600/15 px-2.5 py-1.5 text-xs font-medium text-amber-400 transition hover:bg-amber-600/25"
                        title="Restore this backup"
                      >
                        <RotateCcw size={12} /> Restore
                      </button>
                      {b.provider === 'local' && (
                        <button
                          className="flex items-center gap-1 rounded-md bg-blue-600/15 px-2.5 py-1.5 text-xs font-medium text-blue-400 transition hover:bg-blue-600/25"
                          title="Download this backup"
                        >
                          <Download size={12} /> Download
                        </button>
                      )}
                      <button
                        onClick={() => setDeleteTarget(b)}
                        className="flex items-center gap-1 rounded-md bg-red-600/15 px-2.5 py-1.5 text-xs font-medium text-red-400 transition hover:bg-red-600/25"
                        title="Delete this backup"
                      >
                        <Trash2 size={12} /> Delete
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
              {sortedBackups.length === 0 && (
                <tr>
                  <td colSpan={5} className="px-5 py-12 text-center text-slate-500">
                    <Archive size={32} className="mx-auto mb-3 opacity-40" />
                    No backups yet. Create your first backup.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Section 4: Auto-Backup Schedule */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5 shadow-md">
        <div className="mb-4 flex items-center gap-2">
          <Calendar size={18} className="text-purple-400" />
          <h2 className="text-sm font-semibold text-slate-300">Auto-Backup Schedule</h2>
        </div>

        <div className="space-y-5">
          {/* Enabled toggle */}
          <div className="flex items-center gap-3">
            <button
              onClick={() =>
                setScheduleForm((prev) => ({ ...prev, enabled: !prev.enabled }))
              }
              className={`relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full transition-colors ${
                scheduleForm.enabled ? 'bg-blue-600' : 'bg-[#334155]'
              }`}
            >
              <span
                className={`inline-block h-5 w-5 translate-y-0.5 rounded-full bg-white shadow transition-transform ${
                  scheduleForm.enabled ? 'translate-x-[22px]' : 'translate-x-0.5'
                }`}
              />
            </button>
            <span className="text-sm text-slate-300">
              {scheduleForm.enabled ? 'Auto-backup enabled' : 'Auto-backup disabled'}
            </span>
          </div>

          {scheduleForm.enabled && (
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
              {/* Interval */}
              <div>
                <label className="mb-1.5 block text-xs font-medium uppercase text-slate-500">
                  Interval
                </label>
                <select
                  value={scheduleForm.interval}
                  onChange={(e) =>
                    setScheduleForm((prev) => ({ ...prev, interval: e.target.value }))
                  }
                  className="w-full rounded-md border border-[#334155] bg-[#0f172a] px-3 py-2 text-sm text-slate-200 outline-none focus:border-blue-500"
                >
                  {INTERVAL_OPTIONS.map((opt) => (
                    <option key={opt.value} value={opt.value}>
                      {opt.label}
                    </option>
                  ))}
                </select>
              </div>

              {/* Keep last N */}
              <div>
                <label className="mb-1.5 block text-xs font-medium uppercase text-slate-500">
                  Keep Last N Backups
                </label>
                <input
                  type="number"
                  min={1}
                  max={100}
                  value={scheduleForm.keep}
                  onChange={(e) =>
                    setScheduleForm((prev) => ({
                      ...prev,
                      keep: parseInt(e.target.value, 10) || 7,
                    }))
                  }
                  className="w-full rounded-md border border-[#334155] bg-[#0f172a] px-3 py-2 text-sm text-slate-200 outline-none focus:border-blue-500"
                />
              </div>

              {/* Last backup */}
              <div>
                <label className="mb-1.5 block text-xs font-medium uppercase text-slate-500">
                  Last Backup
                </label>
                <p className="py-2 text-sm text-slate-300">
                  {schedule?.last_backup ? formatDateTime(schedule.last_backup) : '--'}
                </p>
              </div>

              {/* Next backup */}
              <div>
                <label className="mb-1.5 block text-xs font-medium uppercase text-slate-500">
                  Next Backup
                </label>
                <p className="py-2 text-sm text-slate-300">
                  {schedule?.next_backup ? formatDateTime(schedule.next_backup) : '--'}
                </p>
              </div>
            </div>
          )}

          {/* Save button */}
          <div className="flex justify-end">
            <button
              onClick={handleSaveSchedule}
              disabled={savingSchedule}
              className="flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700 disabled:opacity-50"
            >
              {savingSchedule && <RefreshCw size={14} className="animate-spin" />}
              {savingSchedule ? 'Saving...' : 'Save Schedule'}
            </button>
          </div>
        </div>
      </div>

      {/* ── Restore Confirmation Modal ──────────────────────────────── */}
      <ConfirmModal
        open={restoreTarget !== null}
        title="Restore Backup"
        confirmLabel="Restore Now"
        confirmClass="bg-amber-600 hover:bg-amber-700"
        onConfirm={handleRestore}
        onCancel={() => setRestoreTarget(null)}
        loading={restoring}
      >
        <div className="space-y-3">
          <div className="flex items-start gap-2 rounded-md bg-amber-500/10 p-3 text-amber-400">
            <AlertTriangle size={18} className="mt-0.5 shrink-0" />
            <p>
              Warning: This will replace the current configuration and certificates
              with the contents of this backup.
            </p>
          </div>
          {restoreTarget && (
            <div className="space-y-1 text-slate-400">
              <div className="flex items-center gap-2">
                <Shield size={14} />
                <span className="font-mono text-xs text-slate-200">{restoreTarget.name}</span>
              </div>
              <p className="text-xs">
                Provider: {restoreTarget.provider} &middot; Size: {formatSize(restoreTarget.size)} &middot; Created: {formatDateTime(restoreTarget.created)}
              </p>
            </div>
          )}
        </div>
      </ConfirmModal>

      {/* ── Delete Confirmation Modal ───────────────────────────────── */}
      <ConfirmModal
        open={deleteTarget !== null}
        title="Delete Backup"
        confirmLabel="Delete"
        confirmClass="bg-red-600 hover:bg-red-700"
        onConfirm={handleDelete}
        onCancel={() => setDeleteTarget(null)}
        loading={deleting}
      >
        <div className="space-y-3">
          <p>Are you sure you want to permanently delete this backup?</p>
          {deleteTarget && (
            <div className="space-y-1 text-slate-400">
              <div className="flex items-center gap-2">
                <Archive size={14} />
                <span className="font-mono text-xs text-slate-200">{deleteTarget.name}</span>
              </div>
              <p className="text-xs">
                Provider: {deleteTarget.provider} &middot; Size: {formatSize(deleteTarget.size)}
              </p>
            </div>
          )}
        </div>
      </ConfirmModal>
    </div>
  );
}
