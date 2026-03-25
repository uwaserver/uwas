import { useState, useEffect, useCallback } from 'react';
import { Package, Download, Check, RefreshCw, AlertTriangle, Trash2, Shield } from 'lucide-react';
import { fetchPackages, installPackage, removePackage, type PackageInfo } from '@/lib/api';

const categoryOrder = ['Required', 'Database', 'Performance', 'Security', 'WordPress', 'Email'];

export default function Packages() {
  const [packages, setPackages] = useState<PackageInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [acting, setActing] = useState('');
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');
  const [confirmRemove, setConfirmRemove] = useState<PackageInfo | null>(null);

  const load = useCallback(async () => {
    try { setPackages((await fetchPackages()) ?? []); }
    catch (e) { setError((e as Error).message); }
    finally { setLoading(false); }
  }, []);

  useEffect(() => { load(); }, [load]);

  const handleInstall = async (pkg: PackageInfo) => {
    setActing(pkg.id);
    setError(''); setSuccess('');
    try {
      await installPackage(pkg.id);
      setSuccess(`Installing ${pkg.name}...`);
      const poll = setInterval(async () => {
        const updated = (await fetchPackages()).find(p => p.id === pkg.id);
        if (updated?.installed) { clearInterval(poll); setActing(''); setSuccess(`${pkg.name} installed!`); load(); }
      }, 3000);
      setTimeout(() => { clearInterval(poll); setActing(''); }, 120000);
    } catch (e) { setError((e as Error).message); setActing(''); }
  };

  const handleRemove = async (pkg: PackageInfo) => {
    setConfirmRemove(null);
    setActing(pkg.id + '-rm');
    setError(''); setSuccess('');
    try {
      await removePackage(pkg.id);
      setSuccess(`Removing ${pkg.name}...`);
      const poll = setInterval(async () => {
        const updated = (await fetchPackages()).find(p => p.id === pkg.id);
        if (!updated?.installed) { clearInterval(poll); setActing(''); setSuccess(`${pkg.name} removed.`); load(); }
      }, 3000);
      setTimeout(() => { clearInterval(poll); setActing(''); }, 120000);
    } catch (e) { setError((e as Error).message); setActing(''); }
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
          <h1 className="text-2xl font-bold text-slate-100">Packages</h1>
          <p className="mt-1 text-sm text-slate-400">{installed} installed, {available} available</p>
        </div>
        <button onClick={load} disabled={loading} className="flex items-center gap-2 rounded-md border border-[#334155] bg-[#1e293b] px-3 py-2 text-sm text-slate-300 hover:bg-[#334155] disabled:opacity-50">
          <RefreshCw size={14} className={loading ? 'animate-spin' : ''} />Refresh
        </button>
      </div>

      {error && <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">{error}</div>}
      {success && <div className="rounded-md bg-emerald-500/10 px-4 py-3 text-sm text-emerald-400">{success}</div>}

      {/* Remove confirmation modal */}
      {confirmRemove && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
          <div className="w-full max-w-md rounded-lg border border-red-500/50 bg-[#1e293b] p-6 shadow-xl">
            <div className="flex items-center gap-2 text-red-400 font-medium mb-3">
              <AlertTriangle size={18} /> Remove {confirmRemove.name}?
            </div>
            {confirmRemove.warning && (
              <div className="rounded bg-red-500/10 px-3 py-2 mb-3 text-sm text-red-300">{confirmRemove.warning}</div>
            )}
            <p className="text-sm text-slate-400 mb-4">
              This will run <code className="bg-[#0f172a] px-1 rounded text-xs">apt remove --purge</code> and stop the service. Config files may be removed.
            </p>
            <div className="flex gap-2 justify-end">
              <button onClick={() => setConfirmRemove(null)} className="rounded-md border border-[#334155] px-4 py-2 text-sm text-slate-300 hover:bg-[#334155]">Cancel</button>
              <button onClick={() => handleRemove(confirmRemove)} className="rounded-md bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700">
                Yes, Remove {confirmRemove.name}
              </button>
            </div>
          </div>
        </div>
      )}

      {loading && <p className="text-center py-8 text-slate-500">Detecting packages...</p>}

      {sorted.map(cat => (
        <div key={cat}>
          <h2 className="mb-3 text-xs font-semibold uppercase tracking-wider text-slate-500">{cat}</h2>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {grouped[cat].map(pkg => (
              <div key={pkg.id} className={`rounded-lg border bg-[#1e293b] p-4 shadow-md ${pkg.installed ? 'border-emerald-500/30' : 'border-[#334155]'}`}>
                <div className="flex items-start justify-between mb-1">
                  <div className="flex items-center gap-2">
                    <Package size={16} className={pkg.installed ? 'text-emerald-400' : 'text-slate-500'} />
                    <span className="text-sm font-medium text-slate-200">{pkg.name}</span>
                    {pkg.required && <span title="Required by UWAS"><Shield size={12} className="text-blue-400" /></span>}
                  </div>
                  {pkg.installed ? (
                    <span className="flex items-center gap-1 rounded-full bg-emerald-500/15 px-2 py-0.5 text-[10px] font-medium text-emerald-400">
                      <Check size={10} /> Installed
                    </span>
                  ) : (
                    <span className="rounded-full bg-slate-500/15 px-2 py-0.5 text-[10px] text-slate-500">Not installed</span>
                  )}
                </div>
                <p className="text-xs text-slate-500">{pkg.description}</p>
                {pkg.used_by && <p className="mt-0.5 text-[10px] text-blue-400/60">Used by: {pkg.used_by}</p>}
                {pkg.version && <p className="mt-0.5 font-mono text-[10px] text-slate-600 truncate">{pkg.version}</p>}

                <div className="mt-3 flex gap-2">
                  {!pkg.installed && (
                    <button onClick={() => handleInstall(pkg)} disabled={!!acting}
                      className="flex flex-1 items-center justify-center gap-1.5 rounded-md bg-blue-600 px-3 py-2 text-xs font-medium text-white hover:bg-blue-700 disabled:opacity-50">
                      {acting === pkg.id ? <><RefreshCw size={12} className="animate-spin" /> Installing...</> : <><Download size={12} /> Install</>}
                    </button>
                  )}
                  {pkg.installed && pkg.can_remove && (
                    <button onClick={() => setConfirmRemove(pkg)} disabled={!!acting}
                      className="flex flex-1 items-center justify-center gap-1.5 rounded-md bg-red-500/10 px-3 py-2 text-xs font-medium text-red-400 hover:bg-red-500/20 disabled:opacity-50">
                      {acting === pkg.id + '-rm' ? <><RefreshCw size={12} className="animate-spin" /> Removing...</> : <><Trash2 size={12} /> Remove</>}
                    </button>
                  )}
                  {pkg.installed && !pkg.can_remove && (
                    <p className="flex-1 text-center text-[10px] text-slate-600 py-2">Required — cannot remove</p>
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
