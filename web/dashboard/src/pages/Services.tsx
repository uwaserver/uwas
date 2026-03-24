import { useState, useEffect, useCallback } from 'react';
import { RefreshCw, Play, Square, RotateCw, CheckCircle, XCircle } from 'lucide-react';
import {
  fetchServices,
  startService,
  stopService,
  restartService,
  type SystemService,
} from '@/lib/api';

export default function Services() {
  const [services, setServices] = useState<SystemService[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [actionLoading, setActionLoading] = useState<Record<string, string>>({});
  const [status, setStatus] = useState<{ ok: boolean; message: string } | null>(null);

  const load = useCallback(async () => {
    try {
      const data = await fetchServices();
      setServices(data ?? []);
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

  // Auto-refresh every 10 seconds
  useEffect(() => {
    const interval = setInterval(() => {
      load();
    }, 10_000);
    return () => clearInterval(interval);
  }, [load]);

  const handleAction = async (name: string, action: 'start' | 'stop' | 'restart') => {
    setActionLoading((prev) => ({ ...prev, [name]: action }));
    setStatus(null);
    try {
      if (action === 'start') await startService(name);
      else if (action === 'stop') await stopService(name);
      else await restartService(name);
      setStatus({ ok: true, message: `Service "${name}" ${action} succeeded` });
      await load();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setActionLoading((prev) => {
        const next = { ...prev };
        delete next[name];
        return next;
      });
    }
  };

  if (loading) {
    return (
      <div className="flex h-96 items-center justify-center text-slate-400">
        Loading services...
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">Services</h1>
          <p className="text-sm text-slate-400">System services management</p>
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

      {/* Services Table */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] shadow-md">
        <div className="border-b border-[#334155] px-5 py-4">
          <h2 className="text-sm font-semibold text-slate-300">
            Services ({services.length})
          </h2>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-[#334155] text-slate-400">
                <th className="px-5 py-3 font-medium">Service Name</th>
                <th className="px-5 py-3 font-medium">Status</th>
                <th className="px-5 py-3 font-medium">Enabled</th>
                <th className="px-5 py-3 font-medium text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {services.map((svc) => (
                <tr
                  key={svc.name}
                  className="border-b border-[#334155]/50 text-slate-300 transition hover:bg-[#334155]/30"
                >
                  <td className="px-5 py-3">
                    <span className="font-medium text-slate-200">{svc.display || svc.name}</span>
                  </td>
                  <td className="px-5 py-3">
                    <span className="flex items-center gap-2">
                      <span
                        className={`inline-block h-2.5 w-2.5 rounded-full ${
                          svc.running ? 'bg-emerald-400' : 'bg-red-400'
                        }`}
                      />
                      <span className={svc.running ? 'text-emerald-400' : 'text-red-400'}>
                        {svc.running ? 'Running' : 'Stopped'}
                      </span>
                    </span>
                  </td>
                  <td className="px-5 py-3 text-slate-400">{svc.enabled ? 'Yes' : 'No'}</td>
                  <td className="px-5 py-3">
                    <div className="flex items-center justify-end gap-2">
                      <button
                        onClick={() => handleAction(svc.name, 'start')}
                        disabled={!!actionLoading[svc.name]}
                        className="flex items-center gap-1 rounded-md bg-emerald-600/15 px-2.5 py-1.5 text-xs font-medium text-emerald-400 transition hover:bg-emerald-600/25 disabled:opacity-50"
                        title="Start service"
                      >
                        {actionLoading[svc.name] === 'start' ? (
                          <RefreshCw size={12} className="animate-spin" />
                        ) : (
                          <Play size={12} />
                        )}
                        Start
                      </button>
                      <button
                        onClick={() => handleAction(svc.name, 'stop')}
                        disabled={!!actionLoading[svc.name]}
                        className="flex items-center gap-1 rounded-md bg-red-600/15 px-2.5 py-1.5 text-xs font-medium text-red-400 transition hover:bg-red-600/25 disabled:opacity-50"
                        title="Stop service"
                      >
                        {actionLoading[svc.name] === 'stop' ? (
                          <RefreshCw size={12} className="animate-spin" />
                        ) : (
                          <Square size={12} />
                        )}
                        Stop
                      </button>
                      <button
                        onClick={() => handleAction(svc.name, 'restart')}
                        disabled={!!actionLoading[svc.name]}
                        className="flex items-center gap-1 rounded-md bg-blue-600/15 px-2.5 py-1.5 text-xs font-medium text-blue-400 transition hover:bg-blue-600/25 disabled:opacity-50"
                        title="Restart service"
                      >
                        {actionLoading[svc.name] === 'restart' ? (
                          <RefreshCw size={12} className="animate-spin" />
                        ) : (
                          <RotateCw size={12} />
                        )}
                        Restart
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
              {services.length === 0 && (
                <tr>
                  <td colSpan={4} className="px-5 py-12 text-center text-slate-500">
                    No services found.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
