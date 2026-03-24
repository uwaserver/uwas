import { useState, useEffect, useCallback } from 'react';
import {
  HardDrive,
  RefreshCw,
  Plus,
  Trash2,
  CheckCircle,
  XCircle,
  Copy,
  Download,
  X,
  AlertTriangle,
  Play,
  Square,
  RotateCw,
} from 'lucide-react';
import {
  fetchDBStatus,
  fetchDatabases,
  createDatabase,
  dropDatabase,
  installDatabase,
  startDB,
  stopDB,
  restartDB,
  type DBStatus,
  type DBInfo,
} from '@/lib/api';
import Card from '@/components/Card';

/* -- Confirmation Modal -------------------------------------------------- */

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

/* -- Credentials Panel --------------------------------------------------- */

function CredentialsPanel({
  name,
  user,
  password,
  onDismiss,
}: {
  name: string;
  user: string;
  password: string;
  onDismiss: () => void;
}) {
  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text);
  };

  return (
    <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 p-5">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="flex items-center gap-2 text-sm font-semibold text-emerald-400">
          <CheckCircle size={16} /> Database Created Successfully
        </h3>
        <button onClick={onDismiss} className="text-slate-400 hover:text-slate-200">
          <X size={16} />
        </button>
      </div>
      <p className="mb-4 text-xs text-slate-400">
        Save these credentials now. The password will not be shown again.
      </p>
      <div className="space-y-2">
        {[
          { label: 'Database', value: name },
          { label: 'User', value: user },
          { label: 'Password', value: password },
        ].map((item) => (
          <div
            key={item.label}
            className="flex items-center justify-between rounded-md bg-[#0f172a] px-4 py-2.5"
          >
            <div>
              <span className="text-xs text-slate-500">{item.label}</span>
              <p className="font-mono text-sm text-slate-200">{item.value}</p>
            </div>
            <button
              onClick={() => copyToClipboard(item.value)}
              className="rounded-md p-1.5 text-slate-400 transition hover:bg-[#334155] hover:text-slate-200"
              title={`Copy ${item.label.toLowerCase()}`}
            >
              <Copy size={14} />
            </button>
          </div>
        ))}
      </div>
    </div>
  );
}

/* -- Main Page ----------------------------------------------------------- */

export default function Database() {
  const [dbStatus, setDbStatus] = useState<DBStatus | null>(null);
  const [databases, setDatabases] = useState<DBInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [status, setStatus] = useState<{ ok: boolean; message: string } | null>(null);

  // Create form
  const [newDbName, setNewDbName] = useState('');
  const [creating, setCreating] = useState(false);

  // Created credentials
  const [credentials, setCredentials] = useState<{
    name: string;
    user: string;
    password: string;
  } | null>(null);

  // Drop modal
  const [dropTarget, setDropTarget] = useState<DBInfo | null>(null);
  const [dropping, setDropping] = useState(false);

  // Install
  const [installing, setInstalling] = useState(false);

  // DB service action
  const [dbAction, setDbAction] = useState('');

  const load = useCallback(async () => {
    try {
      const [s, dbs] = await Promise.all([fetchDBStatus(), fetchDatabases()]);
      setDbStatus(s);
      setDatabases(dbs ?? []);
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

  /* -- Actions ----------------------------------------------------------- */

  const handleInstall = async () => {
    setInstalling(true);
    setStatus(null);
    try {
      await installDatabase();
      setStatus({ ok: true, message: 'MariaDB installation initiated. This may take a few minutes.' });
      await load();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setInstalling(false);
    }
  };

  const handleDBAction = async (action: 'start' | 'stop' | 'restart') => {
    setDbAction(action);
    setStatus(null);
    try {
      if (action === 'start') await startDB();
      else if (action === 'stop') await stopDB();
      else await restartDB();
      setStatus({ ok: true, message: `MariaDB ${action} succeeded` });
      await load();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setDbAction('');
    }
  };

  const handleCreate = async () => {
    if (!newDbName.trim()) return;
    setCreating(true);
    setStatus(null);
    setCredentials(null);
    try {
      const result = await createDatabase(newDbName.trim());
      setCredentials({
        name: result.name,
        user: result.user,
        password: result.password,
      });
      setNewDbName('');
      setStatus({ ok: true, message: `Database "${result.name}" created successfully` });
      await load();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setCreating(false);
    }
  };

  const handleDrop = async () => {
    if (!dropTarget) return;
    setDropping(true);
    setStatus(null);
    try {
      await dropDatabase(dropTarget.name);
      setDropTarget(null);
      setStatus({ ok: true, message: `Database "${dropTarget.name}" dropped` });
      await load();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setDropping(false);
    }
  };

  /* -- Render ------------------------------------------------------------ */

  if (loading) {
    return (
      <div className="flex h-96 items-center justify-center text-slate-400">
        Loading database status...
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">Database</h1>
          <p className="text-sm text-slate-400">MySQL / MariaDB management</p>
        </div>
        <button
          onClick={load}
          className="flex items-center gap-1.5 rounded-md bg-[#334155] px-3 py-1.5 text-xs text-slate-300 hover:bg-[#475569]"
        >
          <RefreshCw size={12} /> Refresh
        </button>
      </div>

      {/* Status messages */}
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

      {error && (
        <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">{error}</div>
      )}

      {/* Overview Cards */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <Card
          icon={<HardDrive size={20} />}
          label="Backend"
          value={dbStatus?.backend || 'Unknown'}
        />
        <Card
          icon={
            dbStatus?.installed ? (
              <CheckCircle size={20} className="text-emerald-400" />
            ) : (
              <XCircle size={20} className="text-red-400" />
            )
          }
          label="Installed"
          value={dbStatus?.installed ? 'Yes' : 'No'}
        />
        <Card
          icon={
            dbStatus?.running ? (
              <CheckCircle size={20} className="text-emerald-400" />
            ) : (
              <XCircle size={20} className="text-red-400" />
            )
          }
          label="Running"
          value={dbStatus?.running ? 'Yes' : 'No'}
        />
        <Card
          icon={<HardDrive size={20} />}
          label="Version"
          value={dbStatus?.version || '--'}
        />
      </div>

      {/* DB Service Controls — shown when installed */}
      {dbStatus?.installed && (
        <div className="flex items-center gap-3">
          {!dbStatus.running ? (
            <button
              onClick={() => handleDBAction('start')}
              disabled={!!dbAction}
              className="flex items-center gap-1.5 rounded-md bg-emerald-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-emerald-700 disabled:opacity-50"
            >
              {dbAction === 'start' ? (
                <RefreshCw size={14} className="animate-spin" />
              ) : (
                <Play size={14} />
              )}
              Start MariaDB
            </button>
          ) : (
            <>
              <button
                onClick={() => handleDBAction('stop')}
                disabled={!!dbAction}
                className="flex items-center gap-1.5 rounded-md bg-red-600/15 px-4 py-2 text-sm font-medium text-red-400 transition hover:bg-red-600/25 disabled:opacity-50"
              >
                {dbAction === 'stop' ? (
                  <RefreshCw size={14} className="animate-spin" />
                ) : (
                  <Square size={14} />
                )}
                Stop
              </button>
              <button
                onClick={() => handleDBAction('restart')}
                disabled={!!dbAction}
                className="flex items-center gap-1.5 rounded-md bg-blue-600/15 px-4 py-2 text-sm font-medium text-blue-400 transition hover:bg-blue-600/25 disabled:opacity-50"
              >
                {dbAction === 'restart' ? (
                  <RefreshCw size={14} className="animate-spin" />
                ) : (
                  <RotateCw size={14} />
                )}
                Restart
              </button>
            </>
          )}
        </div>
      )}

      {/* Install MariaDB (if not installed) */}
      {dbStatus && !dbStatus.installed && (
        <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 p-5">
          <div className="flex items-start gap-3">
            <AlertTriangle size={20} className="mt-0.5 shrink-0 text-amber-400" />
            <div className="flex-1">
              <h3 className="text-sm font-semibold text-amber-400">
                MariaDB Not Installed
              </h3>
              <p className="mt-1 text-sm text-slate-400">
                MariaDB is required for database management. Click below to install it automatically.
              </p>
              <button
                onClick={handleInstall}
                disabled={installing}
                className="mt-3 flex items-center gap-2 rounded-md bg-amber-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-amber-700 disabled:opacity-50"
              >
                {installing ? (
                  <RefreshCw size={14} className="animate-spin" />
                ) : (
                  <Download size={14} />
                )}
                {installing ? 'Installing...' : 'Install MariaDB'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Created credentials */}
      {credentials && (
        <CredentialsPanel
          name={credentials.name}
          user={credentials.user}
          password={credentials.password}
          onDismiss={() => setCredentials(null)}
        />
      )}

      {/* Create Database Form */}
      {dbStatus?.installed && (
        <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5 shadow-md">
          <div className="mb-4 flex items-center gap-2">
            <Plus size={18} className="text-blue-400" />
            <h2 className="text-sm font-semibold text-slate-300">Create Database</h2>
          </div>
          <div className="flex flex-col gap-4 sm:flex-row sm:items-end">
            <div className="flex-1">
              <label className="mb-1.5 block text-xs font-medium uppercase text-slate-500">
                Database Name
              </label>
              <input
                type="text"
                value={newDbName}
                onChange={(e) => setNewDbName(e.target.value)}
                placeholder="my_database"
                className="w-full rounded-md border border-[#334155] bg-[#0f172a] px-3 py-2 text-sm text-slate-200 outline-none focus:border-blue-500"
                onKeyDown={(e) => {
                  if (e.key === 'Enter') handleCreate();
                }}
              />
              <p className="mt-1 text-xs text-slate-500">
                A user with the same name will be created automatically.
              </p>
            </div>
            <button
              onClick={handleCreate}
              disabled={creating || !newDbName.trim()}
              className="flex items-center gap-2 rounded-md bg-blue-600 px-6 py-2.5 text-sm font-medium text-white transition hover:bg-blue-700 disabled:opacity-50"
            >
              {creating ? (
                <RefreshCw size={16} className="animate-spin" />
              ) : (
                <Plus size={16} />
              )}
              {creating ? 'Creating...' : 'Create Database'}
            </button>
          </div>
        </div>
      )}

      {/* Database List */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] shadow-md">
        <div className="border-b border-[#334155] px-5 py-4">
          <h2 className="text-sm font-semibold text-slate-300">
            Databases ({databases.length})
          </h2>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-[#334155] text-slate-400">
                <th className="px-5 py-3 font-medium">Name</th>
                <th className="px-5 py-3 font-medium">User</th>
                <th className="px-5 py-3 font-medium">Host</th>
                <th className="px-5 py-3 font-medium">Size</th>
                <th className="px-5 py-3 font-medium">Tables</th>
                <th className="px-5 py-3 font-medium text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {databases.map((db) => (
                <tr
                  key={db.name}
                  className="border-b border-[#334155]/50 text-slate-300 transition hover:bg-[#334155]/30"
                >
                  <td className="px-5 py-3 font-mono text-xs">{db.name}</td>
                  <td className="px-5 py-3 font-mono text-xs text-slate-400">{db.user}</td>
                  <td className="px-5 py-3 text-slate-400">{db.host}</td>
                  <td className="px-5 py-3 text-slate-400">{db.size || '--'}</td>
                  <td className="px-5 py-3 text-slate-400">
                    {db.tables !== undefined ? db.tables : '--'}
                  </td>
                  <td className="px-5 py-3">
                    <div className="flex items-center justify-end gap-2">
                      <button
                        onClick={() => setDropTarget(db)}
                        className="flex items-center gap-1 rounded-md bg-red-600/15 px-2.5 py-1.5 text-xs font-medium text-red-400 transition hover:bg-red-600/25"
                        title="Drop this database"
                      >
                        <Trash2 size={12} /> Drop
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
              {databases.length === 0 && (
                <tr>
                  <td colSpan={6} className="px-5 py-12 text-center text-slate-500">
                    <HardDrive size={32} className="mx-auto mb-3 opacity-40" />
                    No databases found.{' '}
                    {dbStatus?.installed
                      ? 'Create your first database above.'
                      : 'Install MariaDB to get started.'}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Drop Confirmation Modal */}
      <ConfirmModal
        open={dropTarget !== null}
        title="Drop Database"
        confirmLabel="Drop Database"
        confirmClass="bg-red-600 hover:bg-red-700"
        onConfirm={handleDrop}
        onCancel={() => setDropTarget(null)}
        loading={dropping}
      >
        <div className="space-y-3">
          <div className="flex items-start gap-2 rounded-md bg-red-500/10 p-3 text-red-400">
            <AlertTriangle size={18} className="mt-0.5 shrink-0" />
            <p>
              This will permanently delete the database and all its data. This action cannot be
              undone.
            </p>
          </div>
          {dropTarget && (
            <div className="space-y-1 text-slate-400">
              <div className="flex items-center gap-2">
                <HardDrive size={14} />
                <span className="font-mono text-xs text-slate-200">{dropTarget.name}</span>
              </div>
              <p className="text-xs">
                User: {dropTarget.user} &middot; Host: {dropTarget.host}
                {dropTarget.size ? ` \u00b7 Size: ${dropTarget.size}` : ''}
              </p>
            </div>
          )}
        </div>
      </ConfirmModal>
    </div>
  );
}
