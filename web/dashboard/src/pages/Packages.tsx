import { useState, useEffect, useCallback, useRef } from 'react';
import { Package, Download, Check, RefreshCw, Trash2, Shield } from 'lucide-react';
import { useConfirm } from '@/components/useConfirm';
import { fetchPackages, installPackage, removePackage, fetchTasks, type PackageInfo } from '@/lib/api';

const categoryOrder = ['Required', 'Infrastructure', 'Database', 'Runtime', 'Performance', 'Security', 'WordPress', 'Email'];

function installLabel(pkg: PackageInfo): string {
  return pkg.id === 'docker-compose' ? 'Fix Compose' : 'Install';
}

function installingLabel(pkg: PackageInfo): string {
  return pkg.id === 'docker-compose' ? 'Fixing...' : 'Installing...';
}

export default function Packages() {
  const { confirmAction } = useConfirm();
  const [packages, setPackages] = useState<PackageInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [acting, setActing] = useState('');
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');
  const pollRef = useRef<ReturnType<typeof setInterval>>(undefined);
  const timeoutRef = useRef<ReturnType<typeof setTimeout>>(undefined);

  const load = useCallback(async () => {
    try { setPackages((await fetchPackages()) ?? []); }
    catch (e) { setError((e as Error).message); }
    finally { setLoading(false); }
  }, []);

  useEffect(() => {
    load();
    // Resume monitoring if a package task is running
    fetchTasks().then(tasks => {
      const active = tasks?.find(t => t.type === 'package' && (t.status === 'running' || t.status === 'queued'));
      if (active) {
        setActing(active.name);
        setSuccess(`${active.action === 'remove' ? 'Removing' : 'Installing'} ${active.name}...`);
        pollRef.current = setInterval(async () => {
          const ts = await fetchTasks().catch(() => [] as Awaited<ReturnType<typeof fetchTasks>>);
          const t = ts?.find(x => x.id === active.id);
          if (!t || (t.status !== 'running' && t.status !== 'queued')) {
            clearInterval(pollRef.current);
            setActing('');
            if (t?.status === 'done') setSuccess(`${active.name} ${active.action === 'remove' ? 'removed' : 'installed'}!`);
            else if (t?.status === 'error') setError(t.error || 'Operation failed');
            load();
          }
        }, 3000);
      }
    }).catch(() => {});
    return () => { clearInterval(pollRef.current); clearTimeout(timeoutRef.current); };
  }, [load]);

  // Auto-dismiss success toasts after 5s. Long-finished install/remove
  // banners otherwise lingered through several user actions and were
  // confusing when the user later did something unrelated.
  useEffect(() => {
    if (!success) return;
    const id = window.setTimeout(() => setSuccess(s => s === success ? '' : s), 5000);
    return () => window.clearTimeout(id);
  }, [success]);

  const handleInstall = async (pkg: PackageInfo) => {
    setActing(pkg.id);
    setError(''); setSuccess('');
    try {
      await installPackage(pkg.id);
      setSuccess(`Installing ${pkg.name}...`);
      clearInterval(pollRef.current);
      clearTimeout(timeoutRef.current);
      pollRef.current = setInterval(async () => {
        try {
          const all = await fetchPackages();
          const updated = (all ?? []).find(p => p.id === pkg.id);
          if (updated?.installed) { clearInterval(pollRef.current); setActing(''); setSuccess(`${pkg.name} installed!`); load(); }
        } catch { clearInterval(pollRef.current); setActing(''); }
      }, 3000);
      // After 120s give up — but tell the user instead of going silent.
      timeoutRef.current = setTimeout(() => {
        clearInterval(pollRef.current);
        setActing('');
        setError(`Install of ${pkg.name} did not complete within 2 minutes. It may still be running on the server — refresh in a moment to see the latest state.`);
      }, 120000);
    } catch (e) { setError((e as Error).message); setActing(''); }
  };

  const handleRemove = async (pkg: PackageInfo) => {
    setActing(pkg.id + '-rm');
    setError(''); setSuccess('');
    try {
      await removePackage(pkg.id);
      setSuccess(`Removing ${pkg.name}...`);
      clearInterval(pollRef.current);
      clearTimeout(timeoutRef.current);
      pollRef.current = setInterval(async () => {
        try {
          const all = await fetchPackages();
          const updated = (all ?? []).find(p => p.id === pkg.id);
          if (!updated?.installed) { clearInterval(pollRef.current); setActing(''); setSuccess(`${pkg.name} removed.`); load(); }
        } catch { clearInterval(pollRef.current); setActing(''); }
      }, 3000);
      timeoutRef.current = setTimeout(() => {
        clearInterval(pollRef.current);
        setActing('');
        setError(`Remove of ${pkg.name} did not complete within 2 minutes. It may still be running on the server — refresh in a moment to see the latest state.`);
      }, 120000);
    } catch (e) { setError((e as Error).message); setActing(''); }
  };

  const requestRemove = async (pkg: PackageInfo) => {
    const ok = await confirmAction({
      title: `Remove ${pkg.name}?`,
      message: (
        <div className="space-y-2">
          {pkg.warning && (
            <div className="rounded bg-red-500/10 px-3 py-2 text-red-300">{pkg.warning}</div>
          )}
          <p>
            This will run <code className="rounded bg-background px-1 text-[11px]">apt remove --purge</code> and stop the service. Config files may be removed.
          </p>
        </div>
      ),
      confirmLabel: `Remove ${pkg.name}`,
      variant: 'danger',
    });
    if (ok) await handleRemove(pkg);
  };

  const grouped = packages.reduce((acc, p) => {
    if (!acc[p.category]) acc[p.category] = [];
    acc[p.category].push(p);
    return acc;
  }, {} as Record<string, PackageInfo[]>);

  const sorted = Object.keys(grouped).sort(
    (a, b) => (categoryOrder.indexOf(a) === -1 ? 99 : categoryOrder.indexOf(a)) - (categoryOrder.indexOf(b) === -1 ? 99 : categoryOrder.indexOf(b))
  );

  const installed = packages.filter(p => p.installed).length;
  const available = packages.filter(p => !p.installed).length;

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">Packages</h1>
          <p className="mt-1 text-sm text-muted-foreground">{installed} installed, {available} available</p>
        </div>
        <button onClick={load} disabled={loading} className="flex items-center gap-2 rounded-md border border-border bg-card px-3 py-2 text-sm text-card-foreground hover:bg-accent disabled:opacity-50">
          <RefreshCw size={14} className={loading ? 'animate-spin' : ''} />Refresh
        </button>
      </div>

      {error && <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">{error}</div>}
      {success && <div className="rounded-md bg-emerald-500/10 px-4 py-3 text-sm text-emerald-400">{success}</div>}

      {loading && <p className="text-center py-8 text-muted-foreground">Detecting packages...</p>}

      {sorted.map(cat => (
        <div key={cat}>
          <h2 className="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">{cat}</h2>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {grouped[cat].map(pkg => (
              <div key={pkg.id} className={`rounded-lg border bg-card p-4 shadow-md ${pkg.installed ? 'border-emerald-500/30' : 'border-border'}`}>
                <div className="flex items-start justify-between mb-1">
                  <div className="flex items-center gap-2">
                    <Package size={16} className={pkg.installed ? 'text-emerald-400' : 'text-muted-foreground'} />
                    <span className="text-sm font-medium text-foreground">{pkg.name}</span>
                    {pkg.required && <span title="Required by UWAS"><Shield size={12} className="text-blue-400" /></span>}
                  </div>
                  {pkg.installed ? (
                    <span className="flex items-center gap-1 rounded-full bg-emerald-500/15 px-2 py-0.5 text-[10px] font-medium text-emerald-400">
                      <Check size={10} /> Installed
                    </span>
                  ) : (
                    <span className="rounded-full bg-slate-500/15 px-2 py-0.5 text-[10px] text-muted-foreground">Not installed</span>
                  )}
                </div>
                <p className="text-xs text-muted-foreground">{pkg.description}</p>
                {pkg.used_by && <p className="mt-0.5 text-[10px] text-blue-400/60">Used by: {pkg.used_by}</p>}
                {pkg.version && <p className="mt-0.5 font-mono text-[10px] text-muted-foreground truncate">{pkg.version}</p>}

                <div className="mt-3 flex gap-2">
                  {!pkg.installed && (
                    <button onClick={() => handleInstall(pkg)} disabled={!!acting}
                      className="flex flex-1 items-center justify-center gap-1.5 rounded-md bg-blue-600 px-3 py-2 text-xs font-medium text-white hover:bg-blue-700 disabled:opacity-50">
                      {acting === pkg.id ? <><RefreshCw size={12} className="animate-spin" /> {installingLabel(pkg)}</> : <><Download size={12} /> {installLabel(pkg)}</>}
                    </button>
                  )}
                  {pkg.installed && pkg.can_remove && (
                    <button onClick={() => requestRemove(pkg)} disabled={!!acting}
                      className="flex flex-1 items-center justify-center gap-1.5 rounded-md bg-red-500/10 px-3 py-2 text-xs font-medium text-red-400 hover:bg-red-500/20 disabled:opacity-50">
                      {acting === pkg.id + '-rm' ? <><RefreshCw size={12} className="animate-spin" /> Removing...</> : <><Trash2 size={12} /> Remove</>}
                    </button>
                  )}
                  {pkg.installed && !pkg.can_remove && (
                    <p className="flex-1 text-center text-[10px] text-muted-foreground py-2">Required — cannot remove</p>
                  )}
                </div>
              </div>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}
