import { useState, useEffect, useCallback } from 'react';
import { Package, Download, Check, RefreshCw, AlertTriangle } from 'lucide-react';
import { fetchPackages, installPackage, type PackageInfo } from '@/lib/api';

const categoryOrder = ['Database', 'Image', 'Security', 'WordPress', 'Email', 'Containers', 'Dev Tools', 'Utilities', 'SSL'];

export default function Packages() {
  const [packages, setPackages] = useState<PackageInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [installing, setInstalling] = useState('');
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');

  const load = useCallback(async () => {
    try {
      const p = await fetchPackages();
      setPackages(p ?? []);
    } catch (e) { setError((e as Error).message); }
    finally { setLoading(false); }
  }, []);

  useEffect(() => { load(); }, [load]);

  const handleInstall = async (pkg: PackageInfo) => {
    setInstalling(pkg.id);
    setError('');
    setSuccess('');
    try {
      await installPackage(pkg.id);
      setSuccess(`${pkg.name} installation started...`);
      // Poll for completion
      const poll = setInterval(async () => {
        await load();
        const updated = (await fetchPackages()).find(p => p.id === pkg.id);
        if (updated?.installed) {
          clearInterval(poll);
          setInstalling('');
          setSuccess(`${pkg.name} installed successfully!`);
        }
      }, 3000);
      // Timeout after 2 minutes
      setTimeout(() => { clearInterval(poll); setInstalling(''); }, 120000);
    } catch (e) {
      setError((e as Error).message);
      setInstalling('');
    }
  };

  // Group by category
  const grouped = packages.reduce((acc, pkg) => {
    if (!acc[pkg.category]) acc[pkg.category] = [];
    acc[pkg.category].push(pkg);
    return acc;
  }, {} as Record<string, PackageInfo[]>);

  const sortedCategories = Object.keys(grouped).sort(
    (a, b) => (categoryOrder.indexOf(a) === -1 ? 99 : categoryOrder.indexOf(a)) - (categoryOrder.indexOf(b) === -1 ? 99 : categoryOrder.indexOf(b))
  );

  const installed = packages.filter(p => p.installed).length;
  const available = packages.filter(p => !p.installed).length;

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">Packages</h1>
          <p className="mt-1 text-sm text-slate-400">
            {installed} installed, {available} available to install
          </p>
        </div>
        <button onClick={load} disabled={loading}
          className="flex items-center gap-2 rounded-md border border-[#334155] bg-[#1e293b] px-3 py-2 text-sm text-slate-300 hover:bg-[#334155] disabled:opacity-50">
          <RefreshCw size={14} className={loading ? 'animate-spin' : ''} />Refresh
        </button>
      </div>

      {error && <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">{error}</div>}
      {success && <div className="rounded-md bg-emerald-500/10 px-4 py-3 text-sm text-emerald-400">{success}</div>}

      {loading && <p className="text-center py-8 text-slate-500">Detecting installed packages...</p>}

      {sortedCategories.map(cat => (
        <div key={cat}>
          <h2 className="mb-3 text-xs font-semibold uppercase tracking-wider text-slate-500">{cat}</h2>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {grouped[cat].map(pkg => (
              <div key={pkg.id}
                className={`rounded-lg border bg-[#1e293b] p-4 shadow-md transition ${pkg.installed ? 'border-emerald-500/30' : 'border-[#334155]'}`}>
                <div className="flex items-start justify-between">
                  <div className="flex items-center gap-2">
                    <Package size={16} className={pkg.installed ? 'text-emerald-400' : 'text-slate-500'} />
                    <span className="text-sm font-medium text-slate-200">{pkg.name}</span>
                  </div>
                  {pkg.installed ? (
                    <span className="flex items-center gap-1 rounded-full bg-emerald-500/15 px-2 py-0.5 text-[10px] font-medium text-emerald-400">
                      <Check size={10} /> Installed
                    </span>
                  ) : (
                    <span className="rounded-full bg-slate-500/15 px-2 py-0.5 text-[10px] font-medium text-slate-500">
                      Not installed
                    </span>
                  )}
                </div>
                <p className="mt-1.5 text-xs text-slate-500">{pkg.description}</p>
                {pkg.version && (
                  <p className="mt-1 font-mono text-[10px] text-slate-600 truncate" title={pkg.version}>{pkg.version}</p>
                )}
                {!pkg.installed && (
                  <button
                    onClick={() => handleInstall(pkg)}
                    disabled={!!installing}
                    className="mt-3 flex w-full items-center justify-center gap-1.5 rounded-md bg-blue-600 px-3 py-2 text-xs font-medium text-white hover:bg-blue-700 disabled:opacity-50"
                  >
                    {installing === pkg.id ? (
                      <><RefreshCw size={12} className="animate-spin" /> Installing...</>
                    ) : (
                      <><Download size={12} /> Install {pkg.name}</>
                    )}
                  </button>
                )}
                {!pkg.installed && pkg.install_cmd && (
                  <p className="mt-1.5 text-[9px] text-slate-600 font-mono truncate" title={pkg.install_cmd}>
                    {pkg.install_cmd}
                  </p>
                )}
              </div>
            ))}
          </div>
        </div>
      ))}

      {!loading && packages.length === 0 && (
        <div className="text-center py-12 text-slate-500">
          <AlertTriangle size={32} className="mx-auto mb-3 opacity-30" />
          <p className="text-sm">Could not detect packages (not running on Linux?)</p>
        </div>
      )}
    </div>
  );
}
