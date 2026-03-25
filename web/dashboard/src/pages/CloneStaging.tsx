import { useState, useEffect, useCallback } from 'react';
import { Copy as CopyIcon, Check, ArrowRight, Loader2 } from 'lucide-react';
import { fetchDomains, cloneSite, type DomainData, type CloneResult } from '@/lib/api';

export default function CloneStaging() {
  const [domains, setDomains] = useState<DomainData[]>([]);
  const [loading, setLoading] = useState(true);
  const [sourceDomain, setSourceDomain] = useState('');
  const [targetDomain, setTargetDomain] = useState('');
  const [cloning, setCloning] = useState(false);
  const [result, setResult] = useState<CloneResult | null>(null);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    try {
      const d = await fetchDomains();
      setDomains(d ?? []);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  // Auto-suggest staging domain name
  useEffect(() => {
    if (sourceDomain && !targetDomain) {
      setTargetDomain(`staging.${sourceDomain}`);
    }
  }, [sourceDomain]);

  const handleClone = async () => {
    if (!sourceDomain || !targetDomain) return;
    setCloning(true);
    setError('');
    setResult(null);
    try {
      const res = await cloneSite({
        source_domain: sourceDomain,
        target_domain: targetDomain,
      });
      setResult(res);
      if (res.status === 'error' && res.error) {
        setError(res.error);
      }
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setCloning(false);
    }
  };

  const [copied, setCopied] = useState('');
  const copy = (text: string, label: string) => {
    navigator.clipboard.writeText(text);
    setCopied(label);
    setTimeout(() => setCopied(''), 2000);
  };

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl font-bold sm:text-2xl text-foreground">Clone / Staging</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Clone a site to create a staging environment. Files, database, and WordPress config are duplicated automatically.
        </p>
      </div>

      {error && (
        <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">{error}</div>
      )}

      {/* Clone form */}
      <div className="rounded-lg border border-border bg-card p-6">
        <h2 className="text-sm font-semibold text-foreground mb-4">Create Clone</h2>

        <div className="grid grid-cols-1 gap-4 md:grid-cols-[1fr,auto,1fr]">
          {/* Source */}
          <div>
            <label className="mb-1.5 block text-xs font-medium text-muted-foreground">Source Domain</label>
            <select
              value={sourceDomain}
              onChange={e => { setSourceDomain(e.target.value); setResult(null); }}
              disabled={cloning}
              className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none focus:border-blue-500 disabled:opacity-50"
            >
              <option value="">Select source...</option>
              {domains.map(d => (
                <option key={d.host} value={d.host}>{d.host}</option>
              ))}
            </select>
          </div>

          {/* Arrow */}
          <div className="hidden md:flex items-end pb-2.5">
            <ArrowRight size={20} className="text-muted-foreground" />
          </div>

          {/* Target */}
          <div>
            <label className="mb-1.5 block text-xs font-medium text-muted-foreground">Target Domain</label>
            <input
              type="text"
              value={targetDomain}
              onChange={e => { setTargetDomain(e.target.value); setResult(null); }}
              disabled={cloning}
              placeholder="staging.example.com"
              className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none placeholder:text-muted-foreground focus:border-blue-500 disabled:opacity-50"
            />
          </div>
        </div>

        {/* Info box */}
        <div className="mt-4 rounded-md bg-muted px-4 py-3 text-xs text-muted-foreground">
          <p className="font-medium text-foreground/70 mb-1">What happens:</p>
          <ul className="list-disc pl-4 space-y-0.5">
            <li>Files are copied via rsync to the target directory</li>
            <li>Database is cloned (dump + import to new DB)</li>
            <li>WordPress wp-config.php is updated with new domain and DB credentials</li>
            <li>File permissions are fixed (www-data ownership)</li>
          </ul>
        </div>

        <div className="mt-4 flex items-center gap-3">
          <button
            onClick={handleClone}
            disabled={cloning || !sourceDomain || !targetDomain || sourceDomain === targetDomain}
            className="flex items-center gap-2 rounded-md bg-blue-600 px-5 py-2.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          >
            {cloning ? (
              <>
                <Loader2 size={14} className="animate-spin" />
                Cloning...
              </>
            ) : (
              <>
                <CopyIcon size={14} />
                Clone Site
              </>
            )}
          </button>
          {sourceDomain === targetDomain && sourceDomain && (
            <span className="text-xs text-red-400">Source and target cannot be the same</span>
          )}
        </div>
      </div>

      {/* Result */}
      {result && result.status === 'done' && (
        <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-5 space-y-4">
          <h3 className="text-sm font-semibold text-emerald-400">Clone Complete</h3>

          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
            {([
              ['Source', result.source_domain],
              ['Target', result.target_domain],
              ['Target Root', result.target_root],
              ...(result.target_db ? [['Target Database', result.target_db] as const] : []),
              ...(result.duration ? [['Duration', result.duration] as const] : []),
            ] as const).map(([label, value]) => (
              <div key={label} className="flex items-center justify-between rounded bg-background px-3 py-2">
                <div>
                  <span className="text-xs text-muted-foreground">{label}</span>
                  <p className="font-mono text-sm text-foreground">{value}</p>
                </div>
                <button
                  onClick={() => copy(String(value), String(label))}
                  className="ml-2 rounded p-1 text-muted-foreground hover:text-foreground"
                >
                  {copied === label ? <Check size={14} className="text-emerald-400" /> : <CopyIcon size={14} />}
                </button>
              </div>
            ))}
          </div>

          <p className="text-xs text-muted-foreground">
            Next steps: Add <code className="font-mono text-foreground/70">{result.target_domain}</code> as a domain in UWAS, configure SSL, and update DNS.
          </p>
        </div>
      )}

      {/* Operation log */}
      {result && result.output && (
        <div className="rounded-lg border border-border bg-card p-5">
          <h3 className="text-sm font-semibold text-foreground mb-3">Operation Log</h3>
          <pre className="max-h-64 overflow-auto rounded bg-background p-4 font-mono text-xs text-muted-foreground whitespace-pre-wrap">
            {result.output}
          </pre>
        </div>
      )}

      {/* Loading state */}
      {loading && (
        <div className="text-center text-sm text-muted-foreground py-12">Loading domains...</div>
      )}
    </div>
  );
}
