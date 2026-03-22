import { useState, useEffect, useCallback } from 'react';
import {
  RefreshCw,
  CheckCircle,
  XCircle,
  Server,
  Cpu,
  Activity,
  Database,
  Download,
} from 'lucide-react';
import { fetchConfig, fetchHealth, fetchSystem, fetchConfigExport, triggerReload, type ConfigData, type HealthData, type SystemInfo } from '@/lib/api';

function formatBytes(b: number): string {
  if (b >= 1 << 30) return `${(b / (1 << 30)).toFixed(1)} GB`;
  if (b >= 1 << 20) return `${(b / (1 << 20)).toFixed(1)} MB`;
  if (b >= 1 << 10) return `${(b / (1 << 10)).toFixed(1)} KB`;
  return `${b} B`;
}

export default function Settings() {
  const [config, setConfig] = useState<ConfigData | null>(null);
  const [health, setHealth] = useState<HealthData | null>(null);
  const [system, setSystem] = useState<SystemInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [reloading, setReloading] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [status, setStatus] = useState<{ ok: boolean; message: string } | null>(null);

  const load = useCallback(async () => {
    try {
      const [c, h, s] = await Promise.all([fetchConfig(), fetchHealth(), fetchSystem()]);
      setConfig(c);
      setHealth(h);
      setSystem(s);
    } catch {
      // ignore
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const handleReload = async () => {
    setReloading(true);
    setStatus(null);
    try {
      await triggerReload();
      setStatus({ ok: true, message: 'Configuration reloaded successfully' });
      await load();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setReloading(false);
    }
  };

  const handleExport = async () => {
    setExporting(true);
    setStatus(null);
    try {
      await fetchConfigExport();
      setStatus({ ok: true, message: 'Configuration exported successfully' });
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setExporting(false);
    }
  };

  if (loading) {
    return (
      <div className="flex h-96 items-center justify-center text-slate-400">
        Loading settings...
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">Settings</h1>
          <p className="text-sm text-slate-400">
            Server configuration and status
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={handleExport}
            disabled={exporting}
            className="flex items-center gap-2 rounded-md border border-[#334155] bg-[#1e293b] px-4 py-2 text-sm font-medium text-slate-200 transition hover:bg-[#334155] disabled:opacity-50"
          >
            <Download size={14} />
            {exporting ? 'Exporting...' : 'Export Config'}
          </button>
          <button
            onClick={handleReload}
            disabled={reloading}
            className="flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700 disabled:opacity-50"
          >
            <RefreshCw size={14} className={reloading ? 'animate-spin' : ''} />
            {reloading ? 'Reloading...' : 'Reload Config'}
          </button>
        </div>
      </div>

      {/* Status message */}
      {status && (
        <div
          className={`flex items-center gap-2 rounded-md px-4 py-3 text-sm ${
            status.ok
              ? 'bg-emerald-500/10 text-emerald-400'
              : 'bg-red-500/10 text-red-400'
          }`}
        >
          {status.ok ? <CheckCircle size={14} /> : <XCircle size={14} />}
          {status.message}
        </div>
      )}

      {/* Server Info */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5 shadow-md">
        <div className="mb-4 flex items-center gap-2">
          <Server size={18} className="text-blue-400" />
          <h2 className="text-sm font-semibold text-slate-300">
            Server Status
          </h2>
        </div>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
          <div>
            <dt className="text-xs font-medium uppercase text-slate-500">
              Status
            </dt>
            <dd className="mt-1">
              <span
                className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium ${
                  health?.status === 'ok'
                    ? 'bg-emerald-500/15 text-emerald-400'
                    : 'bg-amber-500/15 text-amber-400'
                }`}
              >
                {health?.status === 'ok' ? (
                  <CheckCircle size={12} />
                ) : (
                  <XCircle size={12} />
                )}
                {health?.status ?? 'unknown'}
              </span>
            </dd>
          </div>
          <div>
            <dt className="text-xs font-medium uppercase text-slate-500">
              Uptime
            </dt>
            <dd className="mt-1 text-sm text-slate-200">
              {health?.uptime ?? '--'}
            </dd>
          </div>
          <div>
            <dt className="text-xs font-medium uppercase text-slate-500">
              Domains
            </dt>
            <dd className="mt-1 text-sm text-slate-200">
              {config?.domain_count ?? '--'}
            </dd>
          </div>
        </div>
      </div>

      {/* Worker Settings */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5 shadow-md">
        <div className="mb-4 flex items-center gap-2">
          <Cpu size={18} className="text-purple-400" />
          <h2 className="text-sm font-semibold text-slate-300">
            Worker Configuration
          </h2>
        </div>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <ConfigItem
            label="Worker Count"
            value={config?.global.worker_count ?? '--'}
          />
          <ConfigItem
            label="Max Connections"
            value={config?.global.max_connections?.toLocaleString() ?? '--'}
          />
          <ConfigItem
            label="Log Level"
            value={config?.global.log_level ?? '--'}
          />
          <ConfigItem
            label="Log Format"
            value={config?.global.log_format ?? '--'}
          />
        </div>
      </div>

      {/* Cache Settings */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5 shadow-md">
        <div className="mb-4 flex items-center gap-2">
          <Database size={18} className="text-emerald-400" />
          <h2 className="text-sm font-semibold text-slate-300">
            Cache Settings
          </h2>
        </div>
        <p className="text-xs text-slate-500">
          Cache configuration is managed through the server config file. Use the
          Cache page for runtime statistics and purge controls.
        </p>
      </div>

      {/* System Info */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5 shadow-md">
        <div className="mb-4 flex items-center gap-2">
          <Activity size={18} className="text-amber-400" />
          <h2 className="text-sm font-semibold text-slate-300">
            System Information
          </h2>
        </div>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <ConfigItem label="Version" value={system?.version || '--'} />
          <ConfigItem label="Go Version" value={system?.go_version || '--'} />
          <ConfigItem label="OS / Arch" value={system ? `${system.os}/${system.arch}` : '--'} />
          <ConfigItem label="CPUs" value={system?.cpus ?? '--'} />
          <ConfigItem label="Goroutines" value={system?.goroutines ?? '--'} />
          <ConfigItem label="Memory (Alloc)" value={system ? formatBytes(system.memory_alloc) : '--'} />
          <ConfigItem label="Memory (Sys)" value={system ? formatBytes(system.memory_sys) : '--'} />
          <ConfigItem label="GC Cycles" value={system?.gc_cycles ?? '--'} />
        </div>
      </div>
    </div>
  );
}

function ConfigItem({ label, value }: { label: string; value: string | number }) {
  return (
    <div>
      <dt className="text-xs font-medium uppercase text-slate-500">{label}</dt>
      <dd className="mt-1 text-sm text-slate-200">{value}</dd>
    </div>
  );
}
