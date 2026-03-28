import { useState, useEffect, useCallback } from 'react';
import { RefreshCw, Play, Square, RotateCw, CheckCircle, XCircle, AlertTriangle, X } from 'lucide-react';
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
  const [confirmAction, setConfirmAction] = useState<{ name: string; action: 'stop' | 'restart' } | null>(null);

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
      <div className="flex h-96 items-center justify-center text-muted-foreground">
        Loading services...
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">Services</h1>
          <p className="text-sm text-muted-foreground">System services management</p>
        </div>
        <button
          onClick={load}
          className="flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-xs text-card-foreground hover:bg-[#475569]"
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
      <div className="rounded-lg border border-border bg-card shadow-md">
        <div className="border-b border-border px-5 py-4">
          <h2 className="text-sm font-semibold text-card-foreground">
            Services ({services.length})
          </h2>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-border text-muted-foreground">
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
                  className="border-b border-border/50 text-card-foreground transition hover:bg-accent/30"
                >
                  <td className="px-5 py-3">
                    <span className="font-medium text-foreground">{svc.display || svc.name}</span>
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
                  <td className="px-5 py-3 text-muted-foreground">{svc.enabled ? 'Yes' : 'No'}</td>
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
                        onClick={() => setConfirmAction({ name: svc.name, action: 'stop' })}
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
                        onClick={() => setConfirmAction({ name: svc.name, action: 'restart' })}
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
                  <td colSpan={4} className="px-5 py-12 text-center text-muted-foreground">
                    No services found.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
      {/* Confirm stop/restart modal */}
      {confirmAction && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60" onClick={() => setConfirmAction(null)}>
          <div className="w-full max-w-sm rounded-lg border border-border bg-card p-6 shadow-xl" onClick={e => e.stopPropagation()}>
            <div className="mb-4 flex items-center justify-between">
              <h3 className="text-sm font-semibold text-foreground capitalize">{confirmAction.action} Service</h3>
              <button onClick={() => setConfirmAction(null)} className="text-muted-foreground hover:text-foreground"><X size={16} /></button>
            </div>
            <div className="mb-5 flex items-start gap-2 rounded-md bg-amber-500/10 p-3 text-amber-400 text-sm">
              <AlertTriangle size={16} className="mt-0.5 shrink-0" />
              <p>{confirmAction.action === 'stop' ? 'Stopping' : 'Restarting'} <strong>{confirmAction.name}</strong> may cause downtime for sites depending on it.</p>
            </div>
            <div className="flex justify-end gap-2">
              <button onClick={() => setConfirmAction(null)}
                className="rounded-md border border-border px-4 py-2 text-sm text-card-foreground hover:bg-accent">Cancel</button>
              <button onClick={() => { handleAction(confirmAction.name, confirmAction.action); setConfirmAction(null); }}
                className={`flex items-center gap-1.5 rounded-md px-4 py-2 text-sm font-medium text-white ${
                  confirmAction.action === 'stop' ? 'bg-red-600 hover:bg-red-700' : 'bg-blue-600 hover:bg-blue-700'
                }`}>
                {confirmAction.action === 'stop' ? <Square size={12} /> : <RotateCw size={12} />}
                {confirmAction.action === 'stop' ? 'Stop' : 'Restart'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
