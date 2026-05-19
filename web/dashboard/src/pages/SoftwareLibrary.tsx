import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  backupAllSoftware,
  backupSoftware,
  checkSoftwarePort,
  fetchSoftwareInstances,
  fetchSoftwareTemplates,
  fetchSoftwareLogs,
  fetchSoftwareMonitor,
  fetchSoftwareMonitorSummary,
  fetchSoftwareBackups,
  deleteSoftware,
  deleteSoftwareBackup,
  installSoftware,
  restoreSoftwareBackup,
  restartSoftware,
  startSoftware,
  stopSoftware,
  updateAllSoftware,
  updateSoftware,
  type SoftwarePortCheck,
  type SoftwareMonitor,
  type SoftwareMonitorSummary,
  type SoftwareBackupInfo,
  type SoftwareInstance,
  type SoftwareTemplate,
} from '@/lib/api';
import { Activity, Box, CheckCircle, DatabaseBackup, Download, FileText, Gauge, Globe, HardDrive, Play, RefreshCw, Square, Trash2, X, Zap } from 'lucide-react';

const secretKeys: Record<string, string[]> = {
  n8n: ['N8N_BASIC_AUTH_USER', 'N8N_BASIC_AUTH_PASSWORD'],
  'adminer-postgres': ['POSTGRES_DB', 'POSTGRES_USER', 'POSTGRES_PASSWORD'],
  postgres: ['POSTGRES_DB', 'POSTGRES_USER', 'POSTGRES_PASSWORD'],
  mysql: ['MYSQL_DATABASE', 'MYSQL_USER', 'MYSQL_PASSWORD', 'MYSQL_ROOT_PASSWORD'],
  mariadb: ['MARIADB_DATABASE', 'MARIADB_USER', 'MARIADB_PASSWORD', 'MARIADB_ROOT_PASSWORD'],
  minio: ['MINIO_ROOT_USER', 'MINIO_ROOT_PASSWORD'],
};

function formatBytes(n: number): string {
  if (!n) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let value = n;
  let i = 0;
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024;
    i++;
  }
  return `${value >= 10 || i === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[i]}`;
}

export default function SoftwareLibrary() {
  const [templates, setTemplates] = useState<SoftwareTemplate[]>([]);
  const [instances, setInstances] = useState<SoftwareInstance[]>([]);
  const [selected, setSelected] = useState<SoftwareTemplate | null>(null);
  const [form, setForm] = useState({ name: '', host_port: '', domain: '', env: {} as Record<string, string> });
  const [busy, setBusy] = useState('');
  const [status, setStatus] = useState<{ ok: boolean; message: string } | null>(null);
  const [portCheck, setPortCheck] = useState<SoftwarePortCheck | null>(null);
  const [summary, setSummary] = useState<SoftwareMonitorSummary | null>(null);
  const [logsFor, setLogsFor] = useState('');
  const [logs, setLogs] = useState('');
  const [monitorFor, setMonitorFor] = useState('');
  const [monitor, setMonitor] = useState<SoftwareMonitor | null>(null);
  const [backups, setBackups] = useState<SoftwareBackupInfo[]>([]);

  const load = useCallback(async () => {
    const [tpls, inst, mon] = await Promise.all([fetchSoftwareTemplates(), fetchSoftwareInstances(), fetchSoftwareMonitorSummary()]);
    setTemplates(tpls ?? []);
    setInstances(inst ?? []);
    setSummary(mon ?? null);
  }, []);

  useEffect(() => { load().catch(e => setStatus({ ok: false, message: (e as Error).message })); }, [load]);

  const grouped = useMemo(() => {
    const out: Record<string, SoftwareTemplate[]> = {};
    for (const tpl of templates) {
      if (!out[tpl.category]) out[tpl.category] = [];
      out[tpl.category].push(tpl);
    }
    return out;
  }, [templates]);

  const openInstall = (tpl: SoftwareTemplate) => {
    const env: Record<string, string> = {};
    for (const [k, v] of Object.entries(tpl.env ?? {})) env[k] = v;
    setSelected(tpl);
    setForm({
      name: tpl.id,
      host_port: '',
      domain: '',
      env,
    });
    setStatus(null);
    setPortCheck(null);
  };

  useEffect(() => {
    if (!selected?.has_web) {
      setPortCheck(null);
      return;
    }
    const raw = form.host_port.trim();
    const port = raw ? parseInt(raw, 10) : undefined;
    if (raw && (!port || port < 1 || port > 65535)) {
      setPortCheck({ port: 0, available: false, reason: 'Port must be 1-65535' });
      return;
    }
    let cancelled = false;
    const timer = window.setTimeout(() => {
      checkSoftwarePort(port, selected.default_port)
        .then(result => {
          if (!cancelled) setPortCheck(result);
        })
        .catch(e => {
          if (!cancelled) setPortCheck({ port: port ?? 0, available: false, reason: (e as Error).message });
        });
    }, 250);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [form.host_port, selected]);

  const submitInstall = async () => {
    if (!selected) return;
    if (!form.name.trim()) {
      setStatus({ ok: false, message: 'Name is required' });
      return;
    }
    const port = form.host_port ? parseInt(form.host_port, 10) : 0;
    if (selected.has_web && form.host_port && (!port || port < 1 || port > 65535)) {
      setStatus({ ok: false, message: 'Web templates need a host port between 1 and 65535' });
      return;
    }
    setBusy('install');
    try {
      const inst = await installSoftware({
        template_id: selected.id,
        name: form.name.trim(),
        host_port: selected.has_web && port ? port : undefined,
        domain: selected.has_web ? form.domain.trim() || undefined : undefined,
        env: form.env,
      });
      setStatus({ ok: true, message: `${inst.template} installed` });
      setSelected(null);
      await load();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setBusy('');
    }
  };

  const action = async (inst: SoftwareInstance, kind: 'start' | 'stop' | 'restart') => {
    setBusy(`${inst.name}:${kind}`);
    try {
      if (kind === 'start') await startSoftware(inst.name);
      else if (kind === 'stop') await stopSoftware(inst.name);
      else await restartSoftware(inst.name);
      setStatus({ ok: true, message: `${inst.name}: ${kind} ok` });
      await load();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setBusy('');
    }
  };

  const remove = async (inst: SoftwareInstance) => {
    if (!window.confirm(`Remove ${inst.name}? Docker volumes are preserved.`)) return;
    setBusy(`${inst.name}:delete`);
    try {
      await deleteSoftware(inst.name);
      setStatus({ ok: true, message: `${inst.name} removed` });
      await load();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setBusy('');
    }
  };

  const openLogs = async (inst: SoftwareInstance) => {
    setLogsFor(inst.name);
    setLogs('Loading...');
    try {
      setLogs((await fetchSoftwareLogs(inst.name)).logs || '(no logs)');
    } catch (e) {
      setLogs((e as Error).message);
    }
  };

  const openMonitor = async (inst: SoftwareInstance) => {
    setMonitorFor(inst.name);
    setMonitor(null);
    setBackups([]);
    try {
      const [mon, backupList] = await Promise.all([fetchSoftwareMonitor(inst.name), fetchSoftwareBackups(inst.name)]);
      setMonitor(mon);
      setBackups(backupList);
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
      setMonitorFor('');
    }
  };

  const backup = async (inst: SoftwareInstance) => {
    setBusy(`${inst.name}:backup`);
    try {
      const result = await backupSoftware(inst.name);
      setStatus({ ok: true, message: result.files.length ? `${inst.name} backup created (${result.files.length} volume)` : `${inst.name} has no persistent volumes` });
      if (monitorFor === inst.name) {
        const [mon, backupList] = await Promise.all([fetchSoftwareMonitor(inst.name), fetchSoftwareBackups(inst.name)]);
        setMonitor(mon);
        setBackups(backupList);
      }
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setBusy('');
    }
  };

  const update = async (inst: SoftwareInstance) => {
    setBusy(`${inst.name}:update`);
    try {
      const result = await updateSoftware(inst.name);
      const backupText = result.backup_files.length ? `backup created (${result.backup_files.length} volume)` : `backup ${result.backup_status}`;
      setStatus({ ok: true, message: `${inst.name} updated, ${backupText}` });
      await load();
      if (monitorFor === inst.name) {
        const [mon, backupList] = await Promise.all([fetchSoftwareMonitor(inst.name), fetchSoftwareBackups(inst.name)]);
        setMonitor(mon);
        setBackups(backupList);
      }
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setBusy('');
    }
  };

  const backupAll = async () => {
    if (instances.length === 0) return;
    setBusy('backup-all');
    try {
      const result = await backupAllSoftware();
      setStatus({
        ok: result.failed === 0,
        message: `Backup all: ${result.created} created, ${result.skipped} skipped, ${result.failed} failed`,
      });
      await load();
      if (monitorFor) {
        const [mon, backupList] = await Promise.all([fetchSoftwareMonitor(monitorFor), fetchSoftwareBackups(monitorFor)]);
        setMonitor(mon);
        setBackups(backupList);
      }
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setBusy('');
    }
  };

  const updateAll = async () => {
    if (instances.length === 0) return;
    setBusy('update-all');
    try {
      const result = await updateAllSoftware();
      setStatus({
        ok: result.failed === 0,
        message: `Update all: ${result.updated} updated, ${result.failed} failed`,
      });
      await load();
      if (monitorFor) {
        const [mon, backupList] = await Promise.all([fetchSoftwareMonitor(monitorFor), fetchSoftwareBackups(monitorFor)]);
        setMonitor(mon);
        setBackups(backupList);
      }
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setBusy('');
    }
  };

  const restore = async (backup: SoftwareBackupInfo) => {
    if (!monitorFor || !window.confirm(`Restore ${backup.name}? The target volume will be overwritten.`)) return;
    setBusy(`${monitorFor}:restore`);
    try {
      await restoreSoftwareBackup(monitorFor, backup.name);
      const [mon, backupList] = await Promise.all([fetchSoftwareMonitor(monitorFor), fetchSoftwareBackups(monitorFor)]);
      setMonitor(mon);
      setBackups(backupList);
      setStatus({ ok: true, message: `${monitorFor} restored from ${backup.name}` });
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setBusy('');
    }
  };

  const removeBackup = async (backup: SoftwareBackupInfo) => {
    if (!monitorFor || !window.confirm(`Delete backup ${backup.name}?`)) return;
    setBusy(`${monitorFor}:backup-delete`);
    try {
      await deleteSoftwareBackup(monitorFor, backup.name);
      setBackups(await fetchSoftwareBackups(monitorFor));
      setStatus({ ok: true, message: `${backup.name} deleted` });
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setBusy('');
    }
  };

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">Software Library</h1>
          <p className="mt-1 text-sm text-muted-foreground">One-click Docker Compose software with optional web domain binding.</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <button
            onClick={updateAll}
            disabled={!!busy || instances.length === 0}
            className="inline-flex items-center gap-2 rounded-md border border-border bg-card px-3 py-2 text-sm hover:bg-accent disabled:opacity-50"
          >
            <Download size={14} /> Update All
          </button>
          <button
            onClick={backupAll}
            disabled={!!busy || instances.length === 0}
            className="inline-flex items-center gap-2 rounded-md border border-border bg-card px-3 py-2 text-sm hover:bg-accent disabled:opacity-50"
          >
            <DatabaseBackup size={14} /> Backup All
          </button>
          <button onClick={() => load()} className="inline-flex items-center gap-2 rounded-md border border-border bg-card px-3 py-2 text-sm hover:bg-accent">
            <RefreshCw size={14} /> Refresh
          </button>
        </div>
      </div>

      {status && (
        <div className={`rounded-md px-4 py-3 text-sm ${status.ok ? 'bg-emerald-500/10 text-emerald-400' : 'bg-red-500/10 text-red-400'}`}>
          {status.message}
        </div>
      )}

      <section className="grid gap-3 sm:grid-cols-4">
        <div className="rounded-md border border-border bg-card p-4">
          <div className="text-[10px] uppercase text-muted-foreground">Containers</div>
          <div className="mt-1 text-xl font-semibold">{summary?.container_count ?? 0}</div>
        </div>
        <div className="rounded-md border border-border bg-card p-4">
          <div className="text-[10px] uppercase text-muted-foreground">Total CPU</div>
          <div className="mt-1 text-xl font-semibold">{(summary?.total_cpu_percent ?? 0).toFixed(2)}%</div>
        </div>
        <div className="rounded-md border border-border bg-card p-4">
          <div className="text-[10px] uppercase text-muted-foreground">Total Memory</div>
          <div className="mt-1 text-xl font-semibold">{formatBytes(summary?.total_memory ?? 0)}</div>
        </div>
        <div className="rounded-md border border-border bg-card p-4">
          <div className="text-[10px] uppercase text-muted-foreground">Volumes</div>
          <div className="mt-1 text-xl font-semibold">{summary?.volume_count ?? 0}</div>
        </div>
      </section>

      <section className="space-y-3">
        <h2 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Installed</h2>
        {instances.length === 0 ? (
          <div className="rounded-md border border-dashed border-border p-6 text-sm text-muted-foreground">No compose software installed yet.</div>
        ) : (
          <div className="grid gap-3 lg:grid-cols-2">
            {instances.map(inst => (
              <div key={inst.name} className="rounded-md border border-border bg-card p-4">
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <div className="flex items-center gap-2">
                      <Box size={16} className="text-blue-400" />
                      <span className="font-medium text-foreground">{inst.name}</span>
                      <span className="rounded-full bg-muted px-2 py-0.5 text-[10px] text-muted-foreground">{inst.template}</span>
                    </div>
                    <p className="mt-1 text-xs text-muted-foreground">{inst.project}</p>
                    {inst.has_web && (
                      <p className="mt-1 text-xs text-blue-300">
                        Web: 127.0.0.1:{inst.host_port}{inst.domain ? ` -> ${inst.domain}` : ''}
                      </p>
                    )}
                  </div>
                  <span className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] ${
                    inst.status === 'running' ? 'bg-emerald-500/15 text-emerald-400' : 'bg-slate-500/15 text-muted-foreground'
                  }`}>
                    <Activity size={10} /> {inst.status || 'unknown'}
                  </span>
                </div>
                <div className="mt-3 flex flex-wrap gap-2">
                  <button onClick={() => action(inst, 'start')} disabled={!!busy} className="inline-flex items-center gap-1 rounded-md border border-green-500/30 bg-green-500/10 px-2 py-1 text-xs text-green-400 disabled:opacity-50">
                    <Play size={12} /> Start
                  </button>
                  <button onClick={() => action(inst, 'stop')} disabled={!!busy} className="inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 text-xs hover:bg-muted disabled:opacity-50">
                    <Square size={12} /> Stop
                  </button>
                  <button onClick={() => action(inst, 'restart')} disabled={!!busy} className="inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 text-xs hover:bg-muted disabled:opacity-50">
                    <RefreshCw size={12} /> Restart
                  </button>
                  <button onClick={() => update(inst)} disabled={!!busy} className="inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 text-xs hover:bg-muted disabled:opacity-50">
                    <Download size={12} /> Update
                  </button>
                  <button onClick={() => openLogs(inst)} className="inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 text-xs hover:bg-muted">
                    <FileText size={12} /> Logs
                  </button>
                  <button onClick={() => openMonitor(inst)} className="inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 text-xs hover:bg-muted">
                    <Gauge size={12} /> Monitor
                  </button>
                  <button onClick={() => backup(inst)} disabled={!!busy} className="inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 text-xs hover:bg-muted disabled:opacity-50">
                    <DatabaseBackup size={12} /> Backup
                  </button>
                  <button onClick={() => remove(inst)} disabled={!!busy} className="inline-flex items-center gap-1 rounded-md border border-red-500/30 bg-red-500/10 px-2 py-1 text-xs text-red-300 hover:bg-red-500/15 disabled:opacity-50">
                    <Trash2 size={12} /> Remove
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}
      </section>

      <section className="space-y-4">
        <h2 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Library</h2>
        {Object.entries(grouped).map(([category, items]) => (
          <div key={category}>
            <h3 className="mb-2 text-sm font-medium text-foreground">{category}</h3>
            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
              {items.map(tpl => (
                <button key={tpl.id} onClick={() => openInstall(tpl)} className="rounded-md border border-border bg-card p-4 text-left hover:border-blue-500/40 hover:bg-accent/50">
                  <div className="flex items-center gap-2">
                    {tpl.has_web ? <Globe size={16} className="text-blue-400" /> : <Zap size={16} className="text-amber-400" />}
                    <span className="font-medium text-foreground">{tpl.name}</span>
                    {tpl.internal && <span className="rounded-full bg-amber-500/10 px-2 py-0.5 text-[10px] text-amber-300">internal</span>}
                  </div>
                  <p className="mt-2 text-xs text-muted-foreground">{tpl.description}</p>
                  {tpl.has_web && <p className="mt-2 text-[10px] text-blue-300">Web service: {tpl.web_service}:{tpl.web_port}</p>}
                </button>
              ))}
            </div>
          </div>
        ))}
      </section>

      {selected && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
          <div className="w-full max-w-lg rounded-lg border border-border bg-card p-5">
            <div className="flex items-start justify-between">
              <div>
                <h2 className="text-lg font-medium">Install {selected.name}</h2>
                <p className="mt-1 text-xs text-muted-foreground">{selected.description}</p>
              </div>
              <button onClick={() => setSelected(null)} className="rounded-md p-1 text-muted-foreground hover:bg-muted hover:text-foreground" aria-label="Close install dialog">
                <X size={16} />
              </button>
            </div>
            <div className="mt-4 grid gap-3">
              <label className="space-y-1">
                <span className="text-xs text-muted-foreground">Instance name</span>
                <input value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm font-mono" />
              </label>
              {selected.has_web && (
                <>
                  <label className="space-y-1">
                    <span className="text-xs text-muted-foreground">Host port</span>
                    <input value={form.host_port} onChange={e => setForm(f => ({ ...f, host_port: e.target.value }))} placeholder={selected.default_port ? `auto from ${selected.default_port}` : 'auto'} className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm font-mono" />
                  </label>
                  {portCheck && (
                    <div className={`rounded-md border px-3 py-2 text-xs ${
                      portCheck.available ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-300' : 'border-red-500/30 bg-red-500/10 text-red-300'
                    }`}>
                      {portCheck.available
                        ? `Port ${portCheck.suggested_port || portCheck.port} is available`
                        : `Port unavailable${portCheck.reason ? `: ${portCheck.reason}` : ''}`}
                      {!portCheck.available && portCheck.suggested_port ? (
                        <button
                          type="button"
                          onClick={() => setForm(f => ({ ...f, host_port: String(portCheck.suggested_port) }))}
                          className="ml-2 rounded border border-current px-2 py-0.5 text-[11px] hover:bg-red-500/10"
                        >
                          Use {portCheck.suggested_port}
                        </button>
                      ) : null}
                    </div>
                  )}
                  <label className="space-y-1">
                    <span className="text-xs text-muted-foreground">Domain (optional)</span>
                    <input value={form.domain} onChange={e => setForm(f => ({ ...f, domain: e.target.value }))} placeholder="app.example.com" className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm font-mono" />
                  </label>
                </>
              )}
              {(secretKeys[selected.id] ?? []).map(key => (
                <label key={key} className="space-y-1">
                  <span className="text-xs text-muted-foreground">{key}</span>
                  <input
                    value={form.env[key] ?? ''}
                    onChange={e => setForm(f => ({ ...f, env: { ...f.env, [key]: e.target.value } }))}
                    placeholder={key.includes('PASSWORD') ? 'auto-generated if empty' : ''}
                    className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm font-mono"
                  />
                </label>
              ))}
              {!selected.has_web && (
                <div className="rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-200">
                  This template is internal-only. No domain or public port will be attached.
                </div>
              )}
            </div>
            <div className="mt-5 flex justify-end gap-2">
              <button onClick={() => setSelected(null)} disabled={busy === 'install'} className="rounded-md border border-border px-3 py-2 text-sm hover:bg-muted disabled:opacity-50">Cancel</button>
              <button onClick={submitInstall} disabled={busy === 'install'} className="inline-flex items-center gap-2 rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground hover:bg-primary/90 disabled:opacity-50">
                {busy === 'install' ? <RefreshCw size={14} className="animate-spin" /> : <CheckCircle size={14} />}
                Install
              </button>
            </div>
          </div>
        </div>
      )}

      {logsFor && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
          <div className="flex max-h-[80vh] w-full max-w-4xl flex-col rounded-lg border border-border bg-card">
            <div className="flex items-center justify-between border-b border-border p-4">
              <h2 className="font-medium">{logsFor} logs</h2>
              <button onClick={() => setLogsFor('')} className="rounded-md p-1 text-muted-foreground hover:bg-muted hover:text-foreground" aria-label="Close logs dialog">
                <X size={16} />
              </button>
            </div>
            <pre className="flex-1 overflow-auto bg-background p-4 text-[11px] font-mono whitespace-pre-wrap">{logs}</pre>
          </div>
        </div>
      )}

      {monitorFor && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
          <div className="flex max-h-[86vh] w-full max-w-5xl flex-col rounded-lg border border-border bg-card">
            <div className="flex items-center justify-between border-b border-border p-4">
              <h2 className="font-medium">{monitorFor} monitor</h2>
              <button onClick={() => setMonitorFor('')} className="rounded-md p-1 text-muted-foreground hover:bg-muted hover:text-foreground" aria-label="Close monitor dialog">
                <X size={16} />
              </button>
            </div>
            <div className="space-y-4 overflow-auto p-4">
              {!monitor ? (
                <div className="text-sm text-muted-foreground">Loading...</div>
              ) : (
                <>
                  <div className="grid gap-3 sm:grid-cols-4">
                    <div className="rounded-md border border-border p-3">
                      <div className="text-[10px] uppercase text-muted-foreground">CPU</div>
                      <div className="mt-1 text-lg font-semibold">{monitor.total_cpu_percent.toFixed(2)}%</div>
                    </div>
                    <div className="rounded-md border border-border p-3">
                      <div className="text-[10px] uppercase text-muted-foreground">Memory</div>
                      <div className="mt-1 text-lg font-semibold">{formatBytes(monitor.total_memory)}</div>
                    </div>
                    <div className="rounded-md border border-border p-3">
                      <div className="text-[10px] uppercase text-muted-foreground">Network In</div>
                      <div className="mt-1 text-lg font-semibold">{formatBytes(monitor.total_network_input)}</div>
                    </div>
                    <div className="rounded-md border border-border p-3">
                      <div className="text-[10px] uppercase text-muted-foreground">Network Out</div>
                      <div className="mt-1 text-lg font-semibold">{formatBytes(monitor.total_network_output)}</div>
                    </div>
                  </div>
                  <div className="space-y-2">
                    <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Containers</h3>
                    {monitor.containers.length === 0 ? (
                      <div className="rounded-md border border-dashed border-border p-4 text-sm text-muted-foreground">No running containers reported.</div>
                    ) : monitor.containers.map(c => (
                      <div key={`${c.id}-${c.name}`} className="rounded-md border border-border p-3 text-sm">
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <span className="font-medium">{c.service || c.name}</span>
                          <span className="text-xs text-muted-foreground">{c.state || c.name}</span>
                        </div>
                        <div className="mt-2 grid gap-2 text-xs text-muted-foreground sm:grid-cols-4">
                          <span>CPU {c.cpu_percent.toFixed(2)}%</span>
                          <span>MEM {formatBytes(c.memory_usage)} / {formatBytes(c.memory_limit)}</span>
                          <span>NET {formatBytes(c.network_input)} / {formatBytes(c.network_output)}</span>
                          <span>IO {formatBytes(c.block_input)} / {formatBytes(c.block_output)}</span>
                        </div>
                      </div>
                    ))}
                  </div>
                  <div className="space-y-2">
                    <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Persistent Volumes</h3>
                    {monitor.volumes.length === 0 ? (
                      <div className="rounded-md border border-dashed border-border p-4 text-sm text-muted-foreground">No named volumes in this compose template.</div>
                    ) : monitor.volumes.map(v => (
                      <div key={v.name} className="rounded-md border border-border p-3 text-sm">
                        <div className="flex items-center gap-2">
                          <HardDrive size={14} className="text-blue-400" />
                          <span className="font-medium">{v.name}</span>
                          {v.driver && <span className="rounded-full bg-muted px-2 py-0.5 text-[10px] text-muted-foreground">{v.driver}</span>}
                        </div>
                        {v.mountpoint && <div className="mt-1 break-all text-xs text-muted-foreground">{v.mountpoint}</div>}
                      </div>
                    ))}
                  </div>
                  <div className="space-y-2">
                    <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Backups</h3>
                    {backups.length === 0 ? (
                      <div className="rounded-md border border-dashed border-border p-4 text-sm text-muted-foreground">No volume backups yet.</div>
                    ) : backups.map(b => (
                      <div key={b.path} className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-border p-3 text-sm">
                        <div>
                          <div className="font-medium">{b.name}</div>
                          <div className="mt-1 text-xs text-muted-foreground">{b.volume_key || 'volume'} · {formatBytes(b.size)} · {new Date(b.created_at).toLocaleString()}</div>
                        </div>
                        <button onClick={() => restore(b)} disabled={!!busy} className="inline-flex items-center gap-1 rounded-md border border-amber-500/30 bg-amber-500/10 px-2 py-1 text-xs text-amber-200 hover:bg-amber-500/15 disabled:opacity-50">
                          <DatabaseBackup size={12} /> Restore
                        </button>
                        <button onClick={() => removeBackup(b)} disabled={!!busy} className="inline-flex items-center gap-1 rounded-md border border-red-500/30 bg-red-500/10 px-2 py-1 text-xs text-red-300 hover:bg-red-500/15 disabled:opacity-50">
                          <Trash2 size={12} /> Delete
                        </button>
                      </div>
                    ))}
                  </div>
                </>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
