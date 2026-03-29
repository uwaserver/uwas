import React, { useState, useEffect, useCallback } from 'react';
import { Link } from 'react-router-dom';
import {
  HardDrive,
  RefreshCw,
  Plus,
  Trash2,
  CheckCircle,
  XCircle,
  Copy,
  Table2,
  Play,
  Database as DatabaseIcon,
  Download,
  Upload,
  X,
  AlertTriangle,
  Square,
  RotateCw,
  Stethoscope,
  Container,
  Key,
} from 'lucide-react';
import {
  fetchDBStatus,
  fetchDatabases,
  createDatabase,
  dropDatabase,
  installDatabase,
  uninstallDatabase,
  diagnoseDatabase,
  fetchDBUsers,
  changeDBPassword,
  exportDatabase,
  importDatabase,
  startDB,
  stopDB,
  restartDB,
  fetchDockerDBs,
  createDockerDB,
  startDockerDB,
  stopDockerDB,
  removeDockerDB,
  fetchDockerDBDatabases,
  createDockerDBDatabase,
  dropDockerDBDatabase,
  type DBStatus,
  type DBInfo,
  type DBUser,
  type DockerDBContainer,
  type DBCreateResult,
  fetchDBTables,
  fetchDBColumns,
  runDBQuery,
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
      <div className="w-full max-w-md rounded-lg border border-border bg-card p-6 shadow-xl">
        <div className="mb-4 flex items-center justify-between">
          <h3 className="text-lg font-semibold text-foreground">{title}</h3>
          <button onClick={onCancel} className="text-muted-foreground hover:text-foreground">
            <X size={18} />
          </button>
        </div>
        <div className="mb-6 text-sm text-card-foreground">{children}</div>
        <div className="flex justify-end gap-3">
          <button
            onClick={onCancel}
            disabled={loading}
            className="rounded-md border border-border px-4 py-2 text-sm font-medium text-card-foreground transition hover:bg-accent disabled:opacity-50"
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
        <button onClick={onDismiss} className="text-muted-foreground hover:text-foreground">
          <X size={16} />
        </button>
      </div>
      <p className="mb-4 text-xs text-muted-foreground">
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
            className="flex items-center justify-between rounded-md bg-background px-4 py-2.5"
          >
            <div>
              <span className="text-xs text-muted-foreground">{item.label}</span>
              <p className="font-mono text-sm text-foreground">{item.value}</p>
            </div>
            <button
              onClick={() => copyToClipboard(item.value)}
              className="rounded-md p-1.5 text-muted-foreground transition hover:bg-accent hover:text-foreground"
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
  const [pageTab, setPageTab] = useState<'manage' | 'explorer'>('manage');
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

  // Users
  const [dbUsers, setDbUsers] = useState<DBUser[]>([]);
  const [pwUser, setPwUser] = useState<DBUser | null>(null);
  const [newPassword, setNewPassword] = useState('');
  const [changingPw, setChangingPw] = useState(false);

  // Docker
  const [dockerAvailable, setDockerAvailable] = useState(false);
  const [dockerContainers, setDockerContainers] = useState<DockerDBContainer[]>([]);
  const [dockerVersion, setDockerVersion] = useState('');
  const [showDockerForm, setShowDockerForm] = useState(false);
  const [dockerForm, setDockerForm] = useState({ engine: 'mariadb', name: '', port: '3307', root_pass: '' });
  const [dockerAction, setDockerAction] = useState('');
  const [expandedContainer, setExpandedContainer] = useState('');
  const [containerDBs, setContainerDBs] = useState<DBInfo[]>([]);
  const [newDockerDBName, setNewDockerDBName] = useState('');
  const [dockerDBResult, setDockerDBResult] = useState<DBCreateResult | null>(null);

  // Generic confirm modal (for docker container remove, docker db drop, uninstall)
  const [confirmAction, setConfirmAction] = useState<{
    title: string;
    message: string;
    label: string;
    action: () => Promise<void>;
  } | null>(null);
  const [confirmLoading, setConfirmLoading] = useState(false);

  // Diagnose
  const [diagData, setDiagData] = useState<Record<string, any> | null>(null);

  // Import
  const [importing, setImporting] = useState(false);

  const load = useCallback(async () => {
    try {
      const [s, dbs] = await Promise.all([fetchDBStatus(), fetchDatabases()]);
      setDbStatus(s);
      setDatabases(dbs ?? []);
      setError('');
      // Load users (may fail if DB not running)
      fetchDBUsers().then(u => setDbUsers(u ?? [])).catch(() => {});
      // Load Docker containers
      fetchDockerDBs().then(d => {
        setDockerAvailable(d.docker);
        setDockerVersion(d.version || '');
        setDockerContainers(d.containers ?? []);
      }).catch(() => {});
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
      const res = await installDatabase();
      setStatus({ ok: true, message: 'MariaDB installation started. This may take a few minutes...' });
      if (res.task_id) {
        // Poll task status
        const poll = setInterval(async () => {
          try {
            const { fetchTask } = await import('../lib/api');
            const task = await fetchTask(res.task_id!);
            if (task.status === 'done') {
              clearInterval(poll);
              setStatus({ ok: true, message: 'MariaDB installed successfully.' });
              setInstalling(false);
              await load();
            } else if (task.status === 'error') {
              clearInterval(poll);
              setStatus({ ok: false, message: 'Installation failed: ' + (task.error || 'unknown error') });
              setInstalling(false);
            }
          } catch { clearInterval(poll); setInstalling(false); }
        }, 3000);
      } else {
        await load();
        setInstalling(false);
      }
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
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
      <div className="flex h-96 items-center justify-center text-muted-foreground">
        Loading database status...
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">Database</h1>
          <p className="text-sm text-muted-foreground">MySQL / MariaDB management</p>
        </div>
        <button
          onClick={load}
          className="flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-xs text-card-foreground hover:bg-[#475569]"
        >
          <RefreshCw size={12} /> Refresh
        </button>
      </div>

      {/* Tab selector */}
      <div className="flex border-b border-border">
        <button onClick={() => setPageTab('manage')}
          className={`flex items-center gap-1.5 px-4 py-3 text-sm font-medium border-b-2 transition-colors ${
            pageTab === 'manage' ? 'border-blue-500 text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'
          }`}>
          <HardDrive size={14} /> Management
        </button>
        <button onClick={() => setPageTab('explorer')}
          className={`flex items-center gap-1.5 px-4 py-3 text-sm font-medium border-b-2 transition-colors ${
            pageTab === 'explorer' ? 'border-blue-500 text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'
          }`}>
          <Table2 size={14} /> SQL Explorer
        </button>
      </div>

      {/* SQL Explorer tab */}
      {pageTab === 'explorer' && <SQLExplorer databases={databases} />}

      {/* Management tab */}
      {pageTab === 'manage' && <>

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
              <p className="mt-1 text-sm text-muted-foreground">
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
        <div className="rounded-lg border border-border bg-card p-5 shadow-md">
          <div className="mb-4 flex items-center gap-2">
            <Plus size={18} className="text-blue-400" />
            <h2 className="text-sm font-semibold text-card-foreground">Create Database</h2>
          </div>
          <div className="flex flex-col gap-4 sm:flex-row sm:items-end">
            <div className="flex-1">
              <label className="mb-1.5 block text-xs font-medium uppercase text-muted-foreground">
                Database Name
              </label>
              <input
                type="text"
                value={newDbName}
                onChange={(e) => setNewDbName(e.target.value)}
                placeholder="my_database"
                className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground outline-none focus:border-blue-500"
                onKeyDown={(e) => {
                  if (e.key === 'Enter') handleCreate();
                }}
              />
              <p className="mt-1 text-xs text-muted-foreground">
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
      <div className="rounded-lg border border-border bg-card shadow-md">
        <div className="border-b border-border px-5 py-4">
          <h2 className="text-sm font-semibold text-card-foreground">
            Databases ({databases.length})
          </h2>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-border text-muted-foreground">
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
                  className="border-b border-border/50 text-card-foreground transition hover:bg-accent/30"
                >
                  <td className="px-5 py-3 font-mono text-xs">{db.name}</td>
                  <td className="px-5 py-3 font-mono text-xs text-muted-foreground">{db.user}</td>
                  <td className="px-5 py-3 text-muted-foreground">{db.host}</td>
                  <td className="px-5 py-3 text-muted-foreground">{db.size || '--'}</td>
                  <td className="px-5 py-3 text-muted-foreground">
                    {db.tables !== undefined ? db.tables : '--'}
                  </td>
                  <td className="px-5 py-3">
                    <div className="flex items-center justify-end gap-2">
                      <a
                        href={exportDatabase(db.name)}
                        className="flex items-center gap-1 rounded-md bg-accent/50 px-2.5 py-1.5 text-xs font-medium text-card-foreground transition hover:bg-accent"
                        title="Export SQL dump"
                      >
                        <Download size={12} /> Export
                      </a>
                      <label
                        className={`flex cursor-pointer items-center gap-1 rounded-md bg-accent/50 px-2.5 py-1.5 text-xs font-medium text-card-foreground transition hover:bg-accent ${importing ? 'opacity-50 pointer-events-none' : ''}`}
                        title="Import SQL file"
                      >
                        {importing ? <RefreshCw size={12} className="animate-spin" /> : <Upload size={12} />} Import
                        <input type="file" accept=".sql" className="hidden" onChange={async (e) => {
                          const file = e.target.files?.[0];
                          if (!file) return;
                          setImporting(true);
                          try {
                            await importDatabase(db.name, file);
                            setStatus({ ok: true, message: `Imported ${file.name} into ${db.name}` });
                            await load();
                          } catch (err) {
                            setStatus({ ok: false, message: (err as Error).message });
                          } finally {
                            setImporting(false);
                            e.target.value = '';
                          }
                        }} />
                      </label>
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
                  <td colSpan={6} className="px-5 py-12 text-center text-muted-foreground">
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
            <div className="space-y-1 text-muted-foreground">
              <div className="flex items-center gap-2">
                <HardDrive size={14} />
                <span className="font-mono text-xs text-foreground">{dropTarget.name}</span>
              </div>
              <p className="text-xs">
                User: {dropTarget.user} &middot; Host: {dropTarget.host}
                {dropTarget.size ? ` \u00b7 Size: ${dropTarget.size}` : ''}
              </p>
            </div>
          )}
        </div>
      </ConfirmModal>

      {/* Generic Confirm Modal (docker container remove, docker db drop, uninstall) */}
      <ConfirmModal
        open={confirmAction !== null}
        title={confirmAction?.title ?? ''}
        confirmLabel={confirmAction?.label ?? 'Confirm'}
        confirmClass="bg-red-600 hover:bg-red-700"
        onConfirm={async () => {
          if (!confirmAction) return;
          setConfirmLoading(true);
          try {
            await confirmAction.action();
          } catch (e) { setStatus({ ok: false, message: (e as Error).message }); }
          finally {
            setConfirmLoading(false);
            setConfirmAction(null);
          }
        }}
        onCancel={() => setConfirmAction(null)}
        loading={confirmLoading}
      >
        <div className="flex items-start gap-2 rounded-md bg-red-500/10 p-3 text-red-400">
          <AlertTriangle size={18} className="mt-0.5 shrink-0" />
          <p>{confirmAction?.message}</p>
        </div>
      </ConfirmModal>

      {/* Database Users */}
      {dbUsers.length > 0 && (
        <div className="rounded-lg border border-border bg-card shadow-md">
          <div className="border-b border-border px-5 py-4">
            <h2 className="flex items-center gap-2 text-sm font-semibold text-card-foreground">
              <Key size={14} /> Database Users ({dbUsers.length})
            </h2>
          </div>
          <div className="overflow-x-auto">
            <table className="w-full text-left text-sm">
              <thead>
                <tr className="border-b border-border text-muted-foreground">
                  <th className="px-5 py-3 font-medium">User</th>
                  <th className="px-5 py-3 font-medium">Host</th>
                  <th className="px-5 py-3 font-medium text-right">Actions</th>
                </tr>
              </thead>
              <tbody>
                {dbUsers.map(u => (
                  <tr key={u.user + u.host} className="border-b border-border/50 text-card-foreground hover:bg-accent/30">
                    <td className="px-5 py-3 font-mono text-xs">{u.user}</td>
                    <td className="px-5 py-3 text-muted-foreground">{u.host}</td>
                    <td className="px-5 py-3 text-right">
                      {pwUser?.user === u.user && pwUser?.host === u.host ? (
                        <div className="flex items-center justify-end gap-2">
                          <input type="password" value={newPassword} onChange={e => setNewPassword(e.target.value)}
                            placeholder="New password" className="w-40 rounded border border-border bg-background px-2 py-1 text-xs text-foreground outline-none" />
                          <button disabled={changingPw || !newPassword} onClick={async () => {
                            setChangingPw(true);
                            try {
                              await changeDBPassword(u.user, u.host, newPassword);
                              setStatus({ ok: true, message: `Password changed for ${u.user}` });
                              setPwUser(null); setNewPassword('');
                            } catch (e) { setStatus({ ok: false, message: (e as Error).message }); }
                            finally { setChangingPw(false); }
                          }} className="rounded bg-blue-600 px-2 py-1 text-xs text-white disabled:opacity-50">
                            {changingPw ? '...' : 'Save'}
                          </button>
                          <button onClick={() => { setPwUser(null); setNewPassword(''); }} className="text-xs text-muted-foreground">Cancel</button>
                        </div>
                      ) : (
                        <button onClick={() => setPwUser(u)} className="flex items-center gap-1 rounded-md bg-accent/50 px-2.5 py-1.5 text-xs text-card-foreground hover:bg-accent">
                          <Key size={12} /> Change Password
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Docker Database Containers */}
      {!dockerAvailable && (
        <div className="rounded-lg border border-border bg-card p-5">
          <h2 className="flex items-center gap-2 text-sm font-semibold text-card-foreground mb-2">
            Docker Containers
          </h2>
          <p className="text-xs text-muted-foreground mb-3">
            Docker is not installed. Install it from the <Link to="/packages" className="text-blue-400 hover:underline">Packages</Link> page to create containerized MariaDB, MySQL, or PostgreSQL databases.
          </p>
        </div>
      )}
      {dockerAvailable && (
        <div className="rounded-lg border border-border bg-card shadow-md">
          <div className="border-b border-border px-5 py-4 flex items-center justify-between">
            <h2 className="flex items-center gap-2 text-sm font-semibold text-card-foreground">
              <Container size={14} /> Docker Containers
              {dockerVersion && <span className="text-xs font-normal text-muted-foreground">v{dockerVersion}</span>}
            </h2>
            <button onClick={() => setShowDockerForm(!showDockerForm)}
              className="flex items-center gap-1 rounded-md bg-blue-600 px-3 py-1.5 text-xs text-white hover:bg-blue-700">
              <Plus size={12} /> New Container
            </button>
          </div>

          {showDockerForm && (
            <div className="border-b border-border p-5">
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
                <div>
                  <label className="mb-1 block text-xs text-muted-foreground">Engine</label>
                  <select value={dockerForm.engine} onChange={e => setDockerForm({ ...dockerForm, engine: e.target.value })}
                    className="w-full rounded border border-border bg-background px-2 py-1.5 text-sm text-foreground outline-none">
                    <option value="mariadb">MariaDB</option>
                    <option value="mysql">MySQL</option>
                    <option value="postgresql">PostgreSQL</option>
                  </select>
                </div>
                <div>
                  <label className="mb-1 block text-xs text-muted-foreground">Name</label>
                  <input type="text" value={dockerForm.name} onChange={e => setDockerForm({ ...dockerForm, name: e.target.value })}
                    placeholder="mydb" className="w-full rounded border border-border bg-background px-2 py-1.5 text-sm text-foreground outline-none" />
                </div>
                <div>
                  <label className="mb-1 block text-xs text-muted-foreground">Port</label>
                  <input type="text" value={dockerForm.port} onChange={e => setDockerForm({ ...dockerForm, port: e.target.value })}
                    placeholder="3307" className="w-full rounded border border-border bg-background px-2 py-1.5 text-sm text-foreground outline-none" />
                </div>
                <div>
                  <label className="mb-1 block text-xs text-muted-foreground">Root Password</label>
                  <input type="password" value={dockerForm.root_pass} onChange={e => setDockerForm({ ...dockerForm, root_pass: e.target.value })}
                    placeholder="password" className="w-full rounded border border-border bg-background px-2 py-1.5 text-sm text-foreground outline-none" />
                </div>
              </div>
              <div className="mt-3 flex gap-2">
                <button disabled={dockerAction === 'create' || !dockerForm.name || !dockerForm.root_pass}
                  onClick={async () => {
                    setDockerAction('create');
                    try {
                      await createDockerDB(dockerForm.engine, dockerForm.name, Number(dockerForm.port) || 3307, dockerForm.root_pass);
                      setStatus({ ok: true, message: `Docker ${dockerForm.engine} container created` });
                      setShowDockerForm(false);
                      setDockerForm({ engine: 'mariadb', name: '', port: '3307', root_pass: '' });
                      await load();
                    } catch (e) { setStatus({ ok: false, message: (e as Error).message }); }
                    finally { setDockerAction(''); }
                  }}
                  className="flex items-center gap-1 rounded bg-emerald-600 px-3 py-1.5 text-xs text-white hover:bg-emerald-700 disabled:opacity-50">
                  {dockerAction === 'create' ? <RefreshCw size={12} className="animate-spin" /> : <Plus size={12} />}
                  Create
                </button>
                <button onClick={() => setShowDockerForm(false)} className="text-xs text-muted-foreground hover:text-foreground">Cancel</button>
              </div>
            </div>
          )}

          {dockerContainers.length > 0 ? (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead>
                  <tr className="border-b border-border text-muted-foreground">
                    <th className="px-5 py-3 font-medium">Name</th>
                    <th className="px-5 py-3 font-medium">Engine</th>
                    <th className="px-5 py-3 font-medium">Image</th>
                    <th className="px-5 py-3 font-medium">Status</th>
                    <th className="px-5 py-3 font-medium text-right">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {dockerContainers.map(c => {
                    const shortName = c.name.replace('uwas-db-', '');
                    const isExpanded = expandedContainer === shortName;
                    return (
                      <React.Fragment key={c.id}>
                      <tr className="border-b border-border/50 text-card-foreground hover:bg-accent/30">
                        <td className="px-5 py-3 font-mono text-xs">{c.name}</td>
                        <td className="px-5 py-3 text-muted-foreground">{c.engine}</td>
                        <td className="px-5 py-3 font-mono text-xs text-muted-foreground">{c.image}</td>
                        <td className="px-5 py-3">
                          <span className={`inline-flex items-center gap-1 text-xs ${c.running ? 'text-emerald-400' : 'text-muted-foreground'}`}>
                            <span className={`h-1.5 w-1.5 rounded-full ${c.running ? 'bg-emerald-400' : 'bg-muted-foreground'}`} />
                            {c.running ? 'Running' : 'Stopped'}
                          </span>
                        </td>
                        <td className="px-5 py-3 text-right">
                          <div className="flex items-center justify-end gap-2">
                            {c.running && (
                              <button onClick={async () => {
                                if (isExpanded) { setExpandedContainer(''); return; }
                                setExpandedContainer(shortName);
                                try { setContainerDBs(await fetchDockerDBDatabases(shortName) ?? []); } catch { setContainerDBs([]); }
                              }} className="rounded bg-blue-600/15 px-2.5 py-1.5 text-xs text-blue-400 hover:bg-blue-600/25">
                                <HardDrive size={11} />
                              </button>
                            )}
                            {c.running ? (
                              <button disabled={!!dockerAction} onClick={async () => {
                                setDockerAction('stop-' + shortName);
                                try { await stopDockerDB(shortName); await load(); } catch (e) { setStatus({ ok: false, message: (e as Error).message }); }
                                finally { setDockerAction(''); }
                              }} className="rounded bg-red-600/15 px-2.5 py-1.5 text-xs text-red-400 hover:bg-red-600/25 disabled:opacity-50">
                                <Square size={11} />
                              </button>
                            ) : (
                              <button disabled={!!dockerAction} onClick={async () => {
                                setDockerAction('start-' + shortName);
                                try { await startDockerDB(shortName); await load(); } catch (e) { setStatus({ ok: false, message: (e as Error).message }); }
                                finally { setDockerAction(''); }
                              }} className="rounded bg-emerald-600/15 px-2.5 py-1.5 text-xs text-emerald-400 hover:bg-emerald-600/25 disabled:opacity-50">
                                <Play size={11} />
                              </button>
                            )}
                            <button disabled={!!dockerAction} onClick={() => {
                              setConfirmAction({
                                title: 'Remove Container',
                                message: `Remove container "${c.name}"? This will stop and delete the container and its data.`,
                                label: 'Remove',
                                action: async () => {
                                  setDockerAction('rm-' + shortName);
                                  try { await removeDockerDB(shortName); await load(); } catch (e) { setStatus({ ok: false, message: (e as Error).message }); }
                                  finally { setDockerAction(''); }
                                },
                              });
                            }} className="rounded bg-red-600/15 px-2.5 py-1.5 text-xs text-red-400 hover:bg-red-600/25 disabled:opacity-50">
                              <Trash2 size={11} />
                            </button>
                          </div>
                        </td>
                      </tr>
                      {/* Expanded: DB management */}
                      {isExpanded && (
                        <tr>
                          <td colSpan={5} className="bg-background px-5 py-4">
                            <div className="space-y-3">
                              <div className="flex items-center justify-between">
                                <h3 className="text-xs font-semibold text-muted-foreground">Databases in {c.name}</h3>
                                <div className="flex items-center gap-2">
                                  <input value={newDockerDBName} onChange={e => setNewDockerDBName(e.target.value)} placeholder="new_database"
                                    className="rounded border border-border bg-card px-2 py-1 text-xs text-foreground outline-none font-mono w-40" />
                                  <button disabled={!newDockerDBName.trim()} onClick={async () => {
                                    try {
                                      const r = await createDockerDBDatabase(shortName, newDockerDBName.trim());
                                      setDockerDBResult(r);
                                      setNewDockerDBName('');
                                      setContainerDBs(await fetchDockerDBDatabases(shortName) ?? []);
                                    } catch (e) { setStatus({ ok: false, message: (e as Error).message }); }
                                  }} className="rounded bg-blue-600 px-2.5 py-1 text-xs text-white hover:bg-blue-700 disabled:opacity-50">
                                    <Plus size={11} />
                                  </button>
                                </div>
                              </div>
                              {dockerDBResult && (
                                <div className="rounded bg-emerald-500/10 px-3 py-2 text-xs text-emerald-400">
                                  Created: {dockerDBResult.name} — User: {dockerDBResult.user} — Pass: <span className="font-mono">{dockerDBResult.password}</span>
                                  <button onClick={() => setDockerDBResult(null)} className="ml-2 text-muted-foreground hover:text-foreground">x</button>
                                </div>
                              )}
                              {containerDBs.length > 0 ? (
                                <div className="space-y-1">
                                  {containerDBs.map(db => (
                                    <div key={db.name} className="flex items-center justify-between rounded bg-card px-3 py-2">
                                      <span className="font-mono text-xs text-card-foreground">{db.name}</span>
                                      <button onClick={() => {
                                        setConfirmAction({
                                          title: 'Drop Database',
                                          message: `Drop database "${db.name}" from container? This cannot be undone.`,
                                          label: 'Drop',
                                          action: async () => {
                                            await dropDockerDBDatabase(shortName, db.name);
                                            setContainerDBs(await fetchDockerDBDatabases(shortName) ?? []);
                                          },
                                        });
                                      }} className="text-red-400 hover:text-red-300">
                                        <Trash2 size={11} />
                                      </button>
                                    </div>
                                  ))}
                                </div>
                              ) : (
                                <p className="text-xs text-muted-foreground">No databases yet.</p>
                              )}
                            </div>
                          </td>
                        </tr>
                      )}
                      </React.Fragment>
                    );
                  })}
                </tbody>
              </table>
            </div>
          ) : !showDockerForm && (
            <div className="px-5 py-8 text-center text-sm text-muted-foreground">
              No Docker database containers. Click "New Container" to create one.
            </div>
          )}
        </div>
      )}

      {/* Diagnostics */}
      {dbStatus?.installed && (
        <div className="rounded-lg border border-border bg-card p-5 shadow-md">
          <div className="flex items-center justify-between mb-3">
            <h2 className="flex items-center gap-2 text-sm font-semibold text-card-foreground">
              <Stethoscope size={14} /> Diagnostics
            </h2>
            <div className="flex gap-2">
              <button onClick={async () => {
                try { const d = await diagnoseDatabase(); setDiagData(d); } catch (e) { setStatus({ ok: false, message: (e as Error).message }); }
              }} className="flex items-center gap-1 rounded-md bg-accent/50 px-3 py-1.5 text-xs text-card-foreground hover:bg-accent">
                <Stethoscope size={12} /> Run Diagnostics
              </button>
              <button onClick={() => {
                setConfirmAction({
                  title: 'Uninstall Database',
                  message: 'This will completely remove MariaDB/MySQL including all databases and data. This action cannot be undone.',
                  label: 'Uninstall',
                  action: async () => {
                    const res = await uninstallDatabase();
                    setStatus({ ok: true, message: 'Database uninstalled: ' + res.output?.slice(0, 100) });
                    await load();
                  },
                });
              }} className="flex items-center gap-1 rounded-md bg-red-600/15 px-3 py-1.5 text-xs text-red-400 hover:bg-red-600/25">
                <Trash2 size={12} /> Uninstall
              </button>
            </div>
          </div>
          {diagData && (
            <pre className="max-h-48 overflow-auto rounded bg-background p-4 font-mono text-xs text-muted-foreground whitespace-pre-wrap">
              {JSON.stringify(diagData, null, 2)}
            </pre>
          )}
        </div>
      )}
      </>}
    </div>
  );
}

/* ── SQL Explorer Component ──────────────────────────────────────── */

function SQLExplorer({ databases }: { databases: { name: string }[] }) {
  const [selectedDB, setSelectedDB] = useState('');
  const [tables, setTables] = useState<{ name: string; rows: string; data_size: string; engine: string }[]>([]);
  const [selectedTable, setSelectedTable] = useState('');
  const [columns, setColumns] = useState<{ name: string; type: string; nullable: string; key: string; default: string; extra: string }[]>([]);
  const [sql, setSQL] = useState('');
  const [result, setResult] = useState<{ columns: string[]; rows: string[][]; count: number } | null>(null);
  const [queryError, setQueryError] = useState('');
  const [running, setRunning] = useState(false);

  const loadTables = async (db: string) => {
    setSelectedDB(db);
    setSelectedTable('');
    setColumns([]);
    setResult(null);
    try {
      const t = await fetchDBTables(db);
      setTables(t ?? []);
    } catch { setTables([]); }
  };

  const loadColumns = async (table: string) => {
    setSelectedTable(table);
    try {
      const c = await fetchDBColumns(selectedDB, table);
      setColumns(c ?? []);
    } catch { setColumns([]); }
    // Auto-fill SQL
    setSQL(`SELECT * FROM ${table}`);
  };

  const executeQuery = async () => {
    if (!selectedDB || !sql.trim()) return;
    setRunning(true);
    setQueryError('');
    setResult(null);
    try {
      const r = await runDBQuery(selectedDB, sql.trim());
      setResult(r);
    } catch (e) { setQueryError((e as Error).message); }
    finally { setRunning(false); }
  };

  return (
    <div className="space-y-4">
      {/* Database selector */}
      <div className="flex items-center gap-3">
        <DatabaseIcon size={14} className="text-muted-foreground" />
        <select value={selectedDB} onChange={e => loadTables(e.target.value)}
          className="rounded-md border border-border bg-background px-3 py-1.5 text-sm text-foreground outline-none">
          <option value="">Select database...</option>
          {databases.map(db => <option key={db.name} value={db.name}>{db.name}</option>)}
        </select>
        {selectedDB && <span className="text-xs text-muted-foreground">{tables.length} tables</span>}
      </div>

      {selectedDB && (
        <div className="grid grid-cols-[220px_1fr] gap-4">
          {/* Left sidebar — tables */}
          <div className="rounded-lg border border-border bg-card">
            <div className="border-b border-border px-3 py-2 text-[10px] font-medium text-muted-foreground uppercase tracking-wider">Tables</div>
            <div className="max-h-[50vh] overflow-auto">
              {tables.map(t => (
                <button key={t.name} onClick={() => loadColumns(t.name)}
                  className={`w-full text-left px-3 py-2 text-xs hover:bg-accent/50 transition flex items-center justify-between ${
                    selectedTable === t.name ? 'bg-blue-500/10 text-blue-400' : 'text-card-foreground'
                  }`}>
                  <span className="font-mono truncate">{t.name}</span>
                  <span className="text-[9px] text-muted-foreground shrink-0 ml-2">{t.rows}r</span>
                </button>
              ))}
              {tables.length === 0 && <p className="px-3 py-4 text-xs text-muted-foreground text-center">No tables</p>}
            </div>
          </div>

          {/* Right — SQL editor + results */}
          <div className="space-y-3">
            {/* Column info */}
            {columns.length > 0 && (
              <div className="flex flex-wrap gap-1.5">
                {columns.map(c => (
                  <span key={c.name} className={`rounded px-2 py-0.5 text-[10px] font-mono ${
                    c.key === 'PRI' ? 'bg-amber-500/15 text-amber-400' :
                    c.key === 'UNI' ? 'bg-blue-500/15 text-blue-400' :
                    'bg-accent text-muted-foreground'
                  }`} title={`${c.type} ${c.nullable === 'YES' ? 'NULL' : 'NOT NULL'} ${c.extra}`}>
                    {c.name} <span className="opacity-60">{c.type.split('(')[0]}</span>
                  </span>
                ))}
              </div>
            )}

            {/* SQL editor */}
            <div className="rounded-lg border border-border bg-[#0d1117] overflow-hidden">
              <textarea value={sql} onChange={e => setSQL(e.target.value)}
                onKeyDown={e => { if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') { e.preventDefault(); executeQuery(); } }}
                rows={4} placeholder="SELECT * FROM users WHERE id > 10"
                className="w-full bg-transparent px-4 py-3 text-xs font-mono text-green-400 outline-none resize-none placeholder:text-green-800" />
              <div className="flex items-center justify-between border-t border-border/30 px-3 py-1.5 bg-[#161b22]">
                <span className="text-[9px] text-muted-foreground">Ctrl+Enter to run</span>
                <button onClick={executeQuery} disabled={running || !sql.trim()}
                  className="flex items-center gap-1 rounded bg-emerald-600 px-3 py-1 text-xs font-medium text-white hover:bg-emerald-700 disabled:opacity-50">
                  {running ? <RefreshCw size={10} className="animate-spin" /> : <Play size={10} />} Run
                </button>
              </div>
            </div>

            {/* Error */}
            {queryError && (
              <div className="rounded-md bg-red-500/10 px-3 py-2 text-xs text-red-400">{queryError}</div>
            )}

            {/* Results table */}
            {result && (
              <div className="rounded-lg border border-border bg-card overflow-hidden">
                <div className="border-b border-border px-3 py-2 flex items-center justify-between">
                  <span className="text-[10px] font-medium text-muted-foreground">{result.count} rows</span>
                </div>
                <div className="overflow-auto max-h-[40vh]">
                  <table className="w-full text-xs">
                    <thead>
                      <tr className="bg-accent/30 text-left">
                        {result.columns.map(col => (
                          <th key={col} className="px-3 py-2 font-medium text-muted-foreground whitespace-nowrap">{col}</th>
                        ))}
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-border/50">
                      {result.rows.map((row, i) => (
                        <tr key={i} className="hover:bg-accent/20">
                          {row.map((cell, j) => (
                            <td key={j} className="px-3 py-1.5 font-mono text-card-foreground whitespace-nowrap max-w-[300px] truncate" title={cell}>
                              {cell === 'NULL' ? <span className="text-muted-foreground italic">NULL</span> : cell}
                            </td>
                          ))}
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
            )}
          </div>
        </div>
      )}

      {!selectedDB && (
        <div className="rounded-lg border border-dashed border-border bg-card p-8 text-center">
          <Table2 size={32} className="mx-auto mb-3 text-muted-foreground opacity-30" />
          <p className="text-sm text-muted-foreground">Select a database above to browse tables and run queries</p>
        </div>
      )}
    </div>
  );
}
