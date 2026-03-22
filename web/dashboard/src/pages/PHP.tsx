import { useState, useEffect, useCallback } from 'react';
import {
  Cpu,
  Play,
  Square,
  RefreshCw,
  CheckCircle,
  XCircle,
  Settings,
  Trash2,
  Zap,
  Plus,
  X,
  RotateCcw,
} from 'lucide-react';
import {
  fetchPHP,
  fetchDomains,
  fetchDomainPHPInstances,
  assignDomainPHP,
  unassignDomainPHP,
  startDomainPHP,
  stopDomainPHP,
  fetchDomainPHPConfig,
  updateDomainPHPConfig,
  type PHPInstall,
  type DomainPHP,
  type DomainData,
} from '@/lib/api';

/* ------------------------------------------------------------------ */
/*  Constants                                                          */
/* ------------------------------------------------------------------ */

const CONFIG_KEYS = [
  'memory_limit',
  'max_execution_time',
  'upload_max_filesize',
  'post_max_size',
  'display_errors',
  'opcache.enable',
] as const;

const CONFIG_LABELS: Record<string, string> = {
  memory_limit: 'Memory Limit',
  max_execution_time: 'Max Execution Time',
  upload_max_filesize: 'Upload Max Filesize',
  post_max_size: 'Post Max Size',
  display_errors: 'Display Errors',
  'opcache.enable': 'OPcache Enable',
};

const WP_OPTIMAL: Record<string, string> = {
  memory_limit: '256M',
  max_execution_time: '300',
  upload_max_filesize: '64M',
  post_max_size: '64M',
  display_errors: 'On',
  'opcache.enable': '1',
};

const DEFAULT_CONFIG: Record<string, string> = {
  memory_limit: '128M',
  max_execution_time: '30',
  upload_max_filesize: '2M',
  post_max_size: '8M',
  display_errors: 'Off',
  'opcache.enable': '0',
};

/* ------------------------------------------------------------------ */
/*  Per-row state                                                      */
/* ------------------------------------------------------------------ */

interface RowState {
  starting: boolean;
  stopping: boolean;
  removing: boolean;
  configExpanded: boolean;
  configLoading: boolean;
  configSaving: boolean;
  configData: Record<string, string>;
  configEdits: Record<string, string>;
  configDirty: boolean;
}

const defaultRow: RowState = {
  starting: false,
  stopping: false,
  removing: false,
  configExpanded: false,
  configLoading: false,
  configSaving: false,
  configData: {},
  configEdits: {},
  configDirty: false,
};

/* ------------------------------------------------------------------ */
/*  Component                                                          */
/* ------------------------------------------------------------------ */

export default function PHP() {
  /* Data */
  const [installs, setInstalls] = useState<PHPInstall[]>([]);
  const [instances, setInstances] = useState<DomainPHP[]>([]);
  const [domains, setDomains] = useState<DomainData[]>([]);
  const [loading, setLoading] = useState(true);
  const [status, setStatus] = useState<{ ok: boolean; message: string } | null>(null);

  /* Per-row state keyed by domain */
  const [rowState, setRowState] = useState<Record<string, RowState>>({});

  /* Modal */
  const [showAssignModal, setShowAssignModal] = useState(false);
  const [assignDomain, setAssignDomain] = useState('');
  const [assignVersion, setAssignVersion] = useState('');
  const [assigning, setAssigning] = useState(false);

  /* Bulk actions */
  const [startingAll, setStartingAll] = useState(false);
  const [stoppingAll, setStoppingAll] = useState(false);
  const [wpSetup, setWpSetup] = useState(false);

  /* -------- helpers -------- */

  const getRow = (domain: string): RowState => rowState[domain] ?? { ...defaultRow };

  const patchRow = (domain: string, patch: Partial<RowState>) => {
    setRowState(prev => ({ ...prev, [domain]: { ...getRow(domain), ...patch } }));
  };

  /* -------- data loading -------- */

  const loadAll = useCallback(async () => {
    try {
      const [phpData, instanceData, domainData] = await Promise.all([
        fetchPHP(),
        fetchDomainPHPInstances(),
        fetchDomains(),
      ]);
      setInstalls(phpData);
      setInstances(instanceData);
      setDomains(domainData);
    } catch {
      // ignore
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadAll();
  }, [loadAll]);

  /* -------- actions -------- */

  const handleStartDomain = async (domain: string) => {
    patchRow(domain, { starting: true });
    setStatus(null);
    try {
      await startDomainPHP(domain);
      setStatus({ ok: true, message: `PHP started for ${domain}` });
      await loadAll();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      patchRow(domain, { starting: false });
    }
  };

  const handleStopDomain = async (domain: string) => {
    patchRow(domain, { stopping: true });
    setStatus(null);
    try {
      await stopDomainPHP(domain);
      setStatus({ ok: true, message: `PHP stopped for ${domain}` });
      await loadAll();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      patchRow(domain, { stopping: false });
    }
  };

  const handleRemoveDomain = async (domain: string) => {
    patchRow(domain, { removing: true });
    setStatus(null);
    try {
      await unassignDomainPHP(domain);
      setStatus({ ok: true, message: `PHP unassigned from ${domain}` });
      await loadAll();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      patchRow(domain, { removing: false });
    }
  };

  const handleToggleConfig = async (domain: string) => {
    const row = getRow(domain);
    if (row.configExpanded) {
      patchRow(domain, { configExpanded: false });
      return;
    }
    patchRow(domain, { configExpanded: true, configLoading: true });
    try {
      const data = await fetchDomainPHPConfig(domain);
      patchRow(domain, { configData: data, configEdits: { ...data }, configDirty: false, configLoading: false });
    } catch {
      patchRow(domain, { configData: {}, configEdits: {}, configLoading: false });
    }
  };

  const handleConfigEdit = (domain: string, key: string, value: string) => {
    const row = getRow(domain);
    const edits = { ...row.configEdits, [key]: value };
    patchRow(domain, { configEdits: edits, configDirty: true });
  };

  const handleSaveConfig = async (domain: string) => {
    const row = getRow(domain);
    patchRow(domain, { configSaving: true });
    setStatus(null);
    try {
      for (const [key, value] of Object.entries(row.configEdits)) {
        if (row.configData[key] !== value) {
          await updateDomainPHPConfig(domain, key, value);
        }
      }
      setStatus({ ok: true, message: `Config saved for ${domain}` });
      const fresh = await fetchDomainPHPConfig(domain);
      patchRow(domain, { configData: fresh, configEdits: { ...fresh }, configDirty: false, configSaving: false });
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
      patchRow(domain, { configSaving: false });
    }
  };

  const handleResetConfig = (domain: string) => {
    patchRow(domain, { configEdits: { ...DEFAULT_CONFIG }, configDirty: true });
  };

  const handleWPConfig = (domain: string) => {
    patchRow(domain, { configEdits: { ...WP_OPTIMAL }, configDirty: true });
  };

  /* -------- assign modal -------- */

  const phpDomains = domains.filter(d => d.type === 'php');
  const assignedDomainSet = new Set(instances.map(i => i.domain));
  const availableDomains = phpDomains.filter(d => !assignedDomainSet.has(d.host));

  const openAssignModal = () => {
    setAssignDomain(availableDomains[0]?.host ?? '');
    setAssignVersion(installs[0]?.version ?? '');
    setShowAssignModal(true);
  };

  const handleAssign = async () => {
    if (!assignDomain || !assignVersion) return;
    setAssigning(true);
    setStatus(null);
    try {
      await assignDomainPHP(assignDomain, assignVersion);
      setStatus({ ok: true, message: `PHP ${assignVersion} assigned to ${assignDomain}` });
      setShowAssignModal(false);
      await loadAll();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setAssigning(false);
    }
  };

  /* -------- bulk actions -------- */

  const handleStartAll = async () => {
    setStartingAll(true);
    setStatus(null);
    try {
      const stopped = instances.filter(i => !i.running);
      for (const inst of stopped) {
        await startDomainPHP(inst.domain);
      }
      setStatus({ ok: true, message: `Started ${stopped.length} PHP instance(s)` });
      await loadAll();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setStartingAll(false);
    }
  };

  const handleStopAll = async () => {
    setStoppingAll(true);
    setStatus(null);
    try {
      const running = instances.filter(i => i.running);
      for (const inst of running) {
        await stopDomainPHP(inst.domain);
      }
      setStatus({ ok: true, message: `Stopped ${running.length} PHP instance(s)` });
      await loadAll();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setStoppingAll(false);
    }
  };

  const handleWPSetup = async () => {
    if (availableDomains.length === 0 || installs.length === 0) return;
    setWpSetup(true);
    setStatus(null);
    try {
      const targetDomain = availableDomains[0].host;
      const bestVersion = installs.find(i => i.version.startsWith('8.4'))?.version
        ?? installs.find(i => i.version.startsWith('8.'))?.version
        ?? installs[0].version;

      await assignDomainPHP(targetDomain, bestVersion);
      for (const [key, value] of Object.entries(WP_OPTIMAL)) {
        await updateDomainPHPConfig(targetDomain, key, value);
      }
      await startDomainPHP(targetDomain);
      setStatus({ ok: true, message: `WordPress PHP setup complete for ${targetDomain} (PHP ${bestVersion})` });
      await loadAll();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setWpSetup(false);
    }
  };

  /* -------- computed -------- */

  const runningCount = instances.filter(i => i.running).length;

  /* ---------------------------------------------------------------- */
  /*  Render                                                           */
  /* ---------------------------------------------------------------- */

  if (loading) {
    return (
      <div className="flex h-96 items-center justify-center text-slate-400">
        Loading PHP installations...
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">PHP Manager</h1>
          <p className="text-sm text-slate-400">
            {installs.length} version{installs.length !== 1 ? 's' : ''} detected
            {instances.length > 0 && <> &middot; {instances.length} assigned &middot; {runningCount} running</>}
          </p>
        </div>
        <button
          onClick={() => { setLoading(true); void loadAll(); }}
          className="flex items-center gap-1.5 rounded-md bg-[#334155] px-3 py-1.5 text-xs text-slate-300 hover:bg-[#475569]"
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

      {/* ============ Section 1: Detected PHP Versions ============ */}
      <div>
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wider text-slate-500">Detected PHP Versions</h2>
        {installs.length === 0 ? (
          <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-8 text-center">
            <Cpu size={40} className="mx-auto mb-3 text-slate-500" />
            <p className="text-sm text-slate-400">No PHP installations detected.</p>
            <p className="mt-1 text-xs text-slate-500">
              Install PHP and ensure it is in the system PATH, then refresh.
            </p>
          </div>
        ) : (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
            {installs.map(inst => (
              <div
                key={inst.version}
                className="rounded-lg border border-[#334155] bg-[#1e293b] p-4 shadow-md"
              >
                <div className="flex items-start justify-between">
                  <div>
                    <span className="text-2xl font-bold text-slate-100">{inst.version}</span>
                    <span className="ml-2 rounded bg-purple-500/15 px-2 py-0.5 text-xs font-medium text-purple-400">
                      {inst.sapi}
                    </span>
                  </div>
                  <span className="flex items-center gap-1.5">
                    <span className={`inline-block h-2.5 w-2.5 rounded-full ${inst.running ? 'bg-emerald-400' : 'bg-slate-500'}`} />
                    <span className={`text-xs ${inst.running ? 'text-emerald-400' : 'text-slate-500'}`}>
                      {inst.running ? 'Available' : 'Installed'}
                    </span>
                  </span>
                </div>
                <p className="mt-2 truncate text-xs text-slate-500" title={inst.binary}>
                  {inst.binary}
                </p>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* ============ Section 2: Per-Domain PHP Assignments ============ */}
      {installs.length > 0 && (
        <div>
          <div className="mb-3 flex items-center justify-between">
            <h2 className="text-sm font-semibold uppercase tracking-wider text-slate-500">Per-Domain PHP Assignments</h2>
            <button
              onClick={openAssignModal}
              disabled={availableDomains.length === 0 || installs.length === 0}
              className="flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-blue-700 disabled:opacity-40 disabled:cursor-not-allowed"
            >
              <Plus size={12} /> Assign PHP to Domain
            </button>
          </div>

          <div className="rounded-lg border border-[#334155] bg-[#1e293b] shadow-md">
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead>
                  <tr className="border-b border-[#334155] text-slate-400">
                    <th className="px-5 py-3 font-medium">Domain</th>
                    <th className="px-5 py-3 font-medium">PHP Version</th>
                    <th className="px-5 py-3 font-medium">Port</th>
                    <th className="px-5 py-3 font-medium">Status</th>
                    <th className="px-5 py-3 font-medium">Config</th>
                    <th className="px-5 py-3 font-medium">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {instances.length === 0 && (
                    <tr>
                      <td colSpan={6} className="px-5 py-8 text-center text-slate-500">
                        No PHP assignments yet. Click "Assign PHP to Domain" to get started.
                      </td>
                    </tr>
                  )}
                  {instances.map(inst => {
                    const row = getRow(inst.domain);
                    return (
                      <InstanceRow
                        key={inst.domain}
                        inst={inst}
                        row={row}
                        installs={installs}
                        onStart={() => void handleStartDomain(inst.domain)}
                        onStop={() => void handleStopDomain(inst.domain)}
                        onRemove={() => void handleRemoveDomain(inst.domain)}
                        onToggleConfig={() => void handleToggleConfig(inst.domain)}
                        onConfigEdit={(key, val) => handleConfigEdit(inst.domain, key, val)}
                        onSaveConfig={() => void handleSaveConfig(inst.domain)}
                        onResetConfig={() => handleResetConfig(inst.domain)}
                        onWPConfig={() => handleWPConfig(inst.domain)}
                      />
                    );
                  })}
                </tbody>
              </table>
            </div>
          </div>
        </div>
      )}

      {/* ============ Section 3: Quick Actions ============ */}
      {installs.length > 0 && (
        <div>
          <h2 className="mb-3 text-sm font-semibold uppercase tracking-wider text-slate-500">Quick Actions</h2>
          <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
            {/* Start All */}
            <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5 shadow-md">
              <div className="mb-3 flex items-center gap-2">
                <Play size={18} className="text-emerald-400" />
                <h3 className="text-sm font-semibold text-slate-300">Start All PHP</h3>
              </div>
              <p className="mb-4 text-xs text-slate-500">
                Start all stopped PHP instances at once.
              </p>
              <button
                onClick={() => void handleStartAll()}
                disabled={startingAll || instances.filter(i => !i.running).length === 0}
                className="flex w-full items-center justify-center gap-1.5 rounded-md bg-emerald-600 px-4 py-2 text-sm font-medium text-white hover:bg-emerald-700 disabled:opacity-40 disabled:cursor-not-allowed"
              >
                <Play size={14} />
                {startingAll ? 'Starting...' : 'Start All'}
              </button>
            </div>

            {/* Stop All */}
            <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5 shadow-md">
              <div className="mb-3 flex items-center gap-2">
                <Square size={18} className="text-red-400" />
                <h3 className="text-sm font-semibold text-slate-300">Stop All PHP</h3>
              </div>
              <p className="mb-4 text-xs text-slate-500">
                Stop all running PHP instances at once.
              </p>
              <button
                onClick={() => void handleStopAll()}
                disabled={stoppingAll || instances.filter(i => i.running).length === 0}
                className="flex w-full items-center justify-center gap-1.5 rounded-md bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700 disabled:opacity-40 disabled:cursor-not-allowed"
              >
                <Square size={14} />
                {stoppingAll ? 'Stopping...' : 'Stop All'}
              </button>
            </div>

            {/* WordPress Setup */}
            <div className="rounded-lg border border-amber-500/20 bg-[#1e293b] p-5 shadow-md">
              <div className="mb-3 flex items-center gap-2">
                <Zap size={18} className="text-amber-400" />
                <h3 className="text-sm font-semibold text-slate-300">WordPress Setup</h3>
              </div>
              <p className="mb-4 text-xs text-slate-500">
                One-click: picks a PHP domain, assigns PHP 8.4, sets optimal config (256M, 300s, 64M), and starts.
              </p>
              <button
                onClick={() => void handleWPSetup()}
                disabled={wpSetup || availableDomains.length === 0 || installs.length === 0}
                className="flex w-full items-center justify-center gap-1.5 rounded-md bg-amber-600 px-4 py-2 text-sm font-medium text-white hover:bg-amber-700 disabled:opacity-40 disabled:cursor-not-allowed"
              >
                <Zap size={14} />
                {wpSetup ? 'Setting up...' : 'Quick WordPress Setup'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* ============ Assign Modal ============ */}
      {showAssignModal && (
        <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto py-10">
          <div className="absolute inset-0 bg-black/60" onClick={() => setShowAssignModal(false)} />
          <div className="relative z-10 w-full max-w-md rounded-xl border border-[#334155] bg-[#0f172a] p-6 shadow-2xl">
            {/* Modal header */}
            <div className="mb-5 flex items-center justify-between">
              <h2 className="text-lg font-semibold text-slate-100">Assign PHP to Domain</h2>
              <button
                onClick={() => setShowAssignModal(false)}
                className="rounded-md p-1 text-slate-500 hover:bg-[#1e293b] hover:text-slate-300"
              >
                <X size={18} />
              </button>
            </div>

            <div className="space-y-4">
              {/* Domain select */}
              <div>
                <label htmlFor="assign-domain" className="mb-1.5 block text-sm font-medium text-slate-300">
                  Domain
                </label>
                <select
                  id="assign-domain"
                  value={assignDomain}
                  onChange={e => setAssignDomain(e.target.value)}
                  className="w-full rounded-md border border-[#334155] bg-[#1e293b] px-3 py-2.5 text-sm text-slate-200 outline-none focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
                >
                  {availableDomains.length === 0 && (
                    <option value="">No PHP domains available</option>
                  )}
                  {availableDomains.map(d => (
                    <option key={d.host} value={d.host}>{d.host}</option>
                  ))}
                </select>
                <p className="mt-1 text-xs text-slate-500">Only domains with type "php" are shown.</p>
              </div>

              {/* Version select */}
              <div>
                <label htmlFor="assign-version" className="mb-1.5 block text-sm font-medium text-slate-300">
                  PHP Version
                </label>
                <select
                  id="assign-version"
                  value={assignVersion}
                  onChange={e => setAssignVersion(e.target.value)}
                  className="w-full rounded-md border border-[#334155] bg-[#1e293b] px-3 py-2.5 text-sm text-slate-200 outline-none focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
                >
                  {installs.map(i => (
                    <option key={i.version} value={i.version}>PHP {i.version}</option>
                  ))}
                </select>
              </div>

              {/* Actions */}
              <div className="flex items-center justify-end gap-3 pt-2">
                <button
                  onClick={() => setShowAssignModal(false)}
                  className="rounded-md px-4 py-2 text-sm text-slate-400 hover:text-slate-200"
                >
                  Cancel
                </button>
                <button
                  onClick={() => void handleAssign()}
                  disabled={assigning || !assignDomain || !assignVersion}
                  className="flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
                >
                  {assigning ? 'Assigning...' : 'Assign'}
                </button>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

/* ------------------------------------------------------------------ */
/*  Instance Row Sub-Component                                         */
/* ------------------------------------------------------------------ */

interface InstanceRowProps {
  inst: DomainPHP;
  row: RowState;
  installs: PHPInstall[];
  onStart: () => void;
  onStop: () => void;
  onRemove: () => void;
  onToggleConfig: () => void;
  onConfigEdit: (key: string, value: string) => void;
  onSaveConfig: () => void;
  onResetConfig: () => void;
  onWPConfig: () => void;
}

function InstanceRow({
  inst,
  row,
  installs,
  onStart,
  onStop,
  onRemove,
  onToggleConfig,
  onConfigEdit,
  onSaveConfig,
  onResetConfig,
  onWPConfig,
}: InstanceRowProps) {
  const port = inst.listen_addr
    ? inst.listen_addr.includes(':')
      ? inst.listen_addr.split(':').pop()
      : inst.listen_addr
    : '--';

  return (
    <>
      <tr className="border-b border-[#334155]/50 hover:bg-[#0f172a]/30">
        {/* Domain */}
        <td className="px-5 py-3">
          <span className="font-semibold text-slate-200">{inst.domain}</span>
        </td>

        {/* PHP Version */}
        <td className="px-5 py-3">
          <span className="rounded bg-purple-500/15 px-2 py-0.5 text-xs font-medium text-purple-400">
            PHP {inst.version}
          </span>
          {installs.length > 1 && (
            <span className="ml-1 text-xs text-slate-600">
              ({installs.length} available)
            </span>
          )}
        </td>

        {/* Port */}
        <td className="px-5 py-3">
          <span className="font-mono text-xs text-slate-400">
            :{port}
          </span>
        </td>

        {/* Status */}
        <td className="px-5 py-3">
          <span className="flex items-center gap-1.5">
            <span className={`inline-block h-2 w-2 rounded-full ${inst.running ? 'bg-emerald-400' : 'bg-slate-500'}`} />
            <span className={`text-xs ${inst.running ? 'text-emerald-400' : 'text-slate-500'}`}>
              {inst.running ? 'Running' : 'Stopped'}
            </span>
          </span>
        </td>

        {/* Config toggle */}
        <td className="px-5 py-3">
          <button
            onClick={onToggleConfig}
            className={`flex items-center gap-1 rounded-md px-2 py-1 text-xs transition ${
              row.configExpanded
                ? 'bg-blue-500/15 text-blue-400'
                : 'bg-[#334155] text-slate-400 hover:bg-[#475569]'
            }`}
          >
            <Settings size={12} />
            Config
          </button>
        </td>

        {/* Actions */}
        <td className="px-5 py-3">
          <div className="flex items-center gap-2">
            {inst.running ? (
              <button
                onClick={onStop}
                disabled={row.stopping}
                className="flex items-center gap-1 rounded-md bg-red-500/10 px-2.5 py-1 text-xs text-red-400 hover:bg-red-500/20 disabled:opacity-50"
                title="Stop"
              >
                <Square size={12} />
                {row.stopping ? 'Stopping...' : 'Stop'}
              </button>
            ) : (
              <button
                onClick={onStart}
                disabled={row.starting}
                className="flex items-center gap-1 rounded-md bg-emerald-500/10 px-2.5 py-1 text-xs text-emerald-400 hover:bg-emerald-500/20 disabled:opacity-50"
                title="Start"
              >
                <Play size={12} />
                {row.starting ? 'Starting...' : 'Start'}
              </button>
            )}
            <button
              onClick={onRemove}
              disabled={row.removing}
              className="flex items-center gap-1 rounded-md bg-slate-500/10 px-2.5 py-1 text-xs text-slate-400 hover:bg-red-500/10 hover:text-red-400 disabled:opacity-50"
              title="Remove"
            >
              <Trash2 size={12} />
            </button>
          </div>
        </td>
      </tr>

      {/* Expanded config editor */}
      {row.configExpanded && (
        <tr>
          <td colSpan={6} className="border-b border-[#334155]/50 bg-[#0f172a]/50 px-5 py-4">
            {row.configLoading ? (
              <p className="text-xs text-slate-500">Loading configuration...</p>
            ) : (
              <div className="space-y-4">
                {/* Config grid */}
                <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
                  {CONFIG_KEYS.map(key => (
                    <div key={key}>
                      <label className="mb-1 block text-xs text-slate-500">{CONFIG_LABELS[key]}</label>
                      <input
                        type="text"
                        value={row.configEdits[key] ?? ''}
                        onChange={e => onConfigEdit(key, e.target.value)}
                        className="w-full rounded-md border border-[#334155] bg-[#0f172a] px-2.5 py-1.5 text-sm text-slate-200 outline-none focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
                      />
                    </div>
                  ))}
                </div>

                {/* Config actions */}
                <div className="flex flex-wrap items-center gap-2">
                  <button
                    onClick={onSaveConfig}
                    disabled={!row.configDirty || row.configSaving}
                    className="flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-blue-700 disabled:opacity-40 disabled:cursor-not-allowed"
                  >
                    {row.configSaving ? 'Saving...' : 'Save'}
                  </button>
                  <button
                    onClick={onResetConfig}
                    className="flex items-center gap-1.5 rounded-md bg-[#334155] px-3 py-1.5 text-xs text-slate-300 hover:bg-[#475569]"
                  >
                    <RotateCcw size={12} />
                    Reset to defaults
                  </button>
                  <button
                    onClick={onWPConfig}
                    className="flex items-center gap-1.5 rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-1.5 text-xs font-medium text-amber-400 hover:bg-amber-500/20"
                  >
                    <Zap size={12} />
                    WordPress Optimized
                  </button>
                </div>
              </div>
            )}
          </td>
        </tr>
      )}
    </>
  );
}
