import { useState, useEffect, useCallback } from 'react';
import { Play, Square, RefreshCw, Box, Clock, Hash, Terminal, Cpu } from 'lucide-react';
import { fetchApps, startApp, stopApp, restartApp, type AppInstance } from '@/lib/api';

const runtimeColors: Record<string, string> = {
  node: 'bg-green-500/15 text-green-400',
  python: 'bg-yellow-500/15 text-yellow-400',
  ruby: 'bg-red-500/15 text-red-400',
  go: 'bg-cyan-500/15 text-cyan-400',
  custom: 'bg-slate-500/15 text-muted-foreground',
};

export default function Apps() {
  const [apps, setApps] = useState<AppInstance[]>([]);
  const [loading, setLoading] = useState(true);
  const [acting, setActing] = useState<string | null>(null);
  const [status, setStatus] = useState<{ ok: boolean; msg: string } | null>(null);

  const load = useCallback(async () => {
    try {
      const data = await fetchApps();
      setApps(data ?? []);
    } catch {
      // ignore
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  const showStatus = (ok: boolean, msg: string) => {
    setStatus({ ok, msg });
    setTimeout(() => setStatus(null), 4000);
  };

  const handleAction = async (domain: string, action: 'start' | 'stop' | 'restart') => {
    setActing(domain);
    try {
      const fn = action === 'start' ? startApp : action === 'stop' ? stopApp : restartApp;
      await fn(domain);
      showStatus(true, `${domain}: ${action}ed`);
      await load();
    } catch (e) {
      showStatus(false, (e as Error).message);
    } finally {
      setActing(null);
    }
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">Applications</h1>
          <p className="text-sm text-muted-foreground">Manage Node.js, Python, Ruby, and Go app processes</p>
        </div>
        <button onClick={() => { setLoading(true); load(); }}
          className="flex items-center gap-1.5 rounded-md border border-border bg-card px-3 py-2 text-sm text-card-foreground hover:bg-accent">
          <RefreshCw size={14} className={loading ? 'animate-spin' : ''} /> Refresh
        </button>
      </div>

      {status && (
        <div className={`flex items-center gap-2 rounded-md px-4 py-2.5 text-sm ${status.ok ? 'bg-emerald-500/10 text-emerald-400' : 'bg-red-500/10 text-red-400'}`}>
          {status.msg}
        </div>
      )}

      {apps.length === 0 && !loading && (
        <div className="rounded-lg border border-border bg-card p-8 text-center">
          <Box size={32} className="mx-auto mb-3 text-muted-foreground" />
          <p className="text-sm text-muted-foreground">No application processes configured.</p>
          <p className="mt-1 text-xs text-muted-foreground">
            Add a domain with <code className="rounded bg-accent px-1.5 py-0.5">type: app</code> in your config.
          </p>
        </div>
      )}

      <div className="grid gap-4">
        {apps.map(app => (
          <div key={app.domain} className="rounded-lg border border-border bg-card p-5">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-3">
                <div className={`flex h-10 w-10 items-center justify-center rounded-lg ${app.running ? 'bg-emerald-500/15' : 'bg-slate-500/15'}`}>
                  <Cpu size={18} className={app.running ? 'text-emerald-400' : 'text-muted-foreground'} />
                </div>
                <div>
                  <div className="flex items-center gap-2">
                    <p className="text-sm font-semibold text-foreground">{app.domain}</p>
                    <span className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${runtimeColors[app.runtime] || runtimeColors.custom}`}>
                      {app.runtime}
                    </span>
                    <span className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${app.running ? 'bg-emerald-500/15 text-emerald-400' : 'bg-red-500/15 text-red-400'}`}>
                      {app.running ? 'Running' : 'Stopped'}
                    </span>
                  </div>
                  <p className="mt-0.5 text-xs text-muted-foreground font-mono">{app.command}</p>
                </div>
              </div>

              <div className="flex items-center gap-2">
                {!app.running && (
                  <button onClick={() => handleAction(app.domain, 'start')} disabled={acting === app.domain}
                    className="flex items-center gap-1 rounded-md bg-emerald-600/15 px-3 py-1.5 text-xs font-medium text-emerald-400 hover:bg-emerald-600/25 disabled:opacity-50">
                    {acting === app.domain ? <RefreshCw size={11} className="animate-spin" /> : <Play size={11} />} Start
                  </button>
                )}
                {app.running && (
                  <>
                    <button onClick={() => handleAction(app.domain, 'restart')} disabled={acting === app.domain}
                      className="flex items-center gap-1 rounded-md bg-blue-600/15 px-3 py-1.5 text-xs font-medium text-blue-400 hover:bg-blue-600/25 disabled:opacity-50">
                      {acting === app.domain ? <RefreshCw size={11} className="animate-spin" /> : <RefreshCw size={11} />} Restart
                    </button>
                    <button onClick={() => handleAction(app.domain, 'stop')} disabled={acting === app.domain}
                      className="flex items-center gap-1 rounded-md bg-red-600/15 px-3 py-1.5 text-xs font-medium text-red-400 hover:bg-red-600/25 disabled:opacity-50">
                      <Square size={11} /> Stop
                    </button>
                  </>
                )}
              </div>
            </div>

            {/* Details row */}
            <div className="mt-3 flex gap-6 text-xs text-muted-foreground">
              <span className="flex items-center gap-1"><Hash size={11} /> Port {app.port}</span>
              {app.pid > 0 && <span className="flex items-center gap-1"><Terminal size={11} /> PID {app.pid}</span>}
              {app.uptime && <span className="flex items-center gap-1"><Clock size={11} /> {app.uptime}</span>}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
