import { useState, useEffect, useCallback, useRef, useMemo } from 'react';
import {
  Wand2, Check, Download, RefreshCw, ChevronRight, ChevronLeft, Loader2,
  CircleDashed, CircleCheck, CircleX, Package as PackageIcon, Shield,
} from 'lucide-react';
import {
  fetchPackages, fetchPHP, fetchTasks, startSetupInstall,
  type PackageInfo, type PHPInstall, type InstallTask, type SetupInstallResult,
} from '@/lib/api';

const PHP_VERSIONS = ['8.2', '8.3', '8.4', '8.5'];
const RECOMMENDED_PHP = '8.3';
// Packages recommended on top of the "required" ones for a typical web host.
const RECOMMENDED_PKGS = new Set(['mariadb']);

const categoryOrder = ['PHP', 'Required', 'Database', 'Infrastructure', 'Runtime', 'Performance', 'Security', 'WordPress', 'Email'];

type ItemType = 'php' | 'package';
interface CatalogItem {
  key: string;          // "php:8.3" | "pkg:redis"
  type: ItemType;
  id: string;           // "8.3" | "redis"
  name: string;
  description: string;
  category: string;
  installed: boolean;
  version?: string;
  required?: boolean;
  recommended: boolean;
}

function phpInstalled(installed: PHPInstall[], v: string): boolean {
  return installed.some(i => i.version === v || i.version.startsWith(v + '.'));
}

function buildCatalog(pkgs: PackageInfo[], php: PHPInstall[]): CatalogItem[] {
  const items: CatalogItem[] = PHP_VERSIONS.map(v => ({
    key: 'php:' + v,
    type: 'php' as const,
    id: v,
    name: 'PHP ' + v,
    description: `PHP ${v} runtime (FPM) for WordPress and PHP apps`,
    category: 'PHP',
    installed: phpInstalled(php, v),
    recommended: v === RECOMMENDED_PHP,
  }));
  for (const p of pkgs) {
    items.push({
      key: 'pkg:' + p.id,
      type: 'package',
      id: p.id,
      name: p.name,
      description: p.description,
      category: p.category,
      installed: p.installed,
      version: p.version,
      required: p.required,
      recommended: p.required || RECOMMENDED_PKGS.has(p.id),
    });
  }
  return items;
}

const StatusIcon = ({ status }: { status?: InstallTask['status'] | 'skipped' }) => {
  if (status === 'done') return <CircleCheck size={16} className="text-emerald-400" />;
  if (status === 'error') return <CircleX size={16} className="text-red-400" />;
  if (status === 'running') return <Loader2 size={16} className="animate-spin text-blue-400" />;
  if (status === 'skipped') return <Check size={16} className="text-muted-foreground" />;
  return <CircleDashed size={16} className="text-muted-foreground" />;
};

export default function Setup() {
  const [step, setStep] = useState<1 | 2 | 3>(1);
  const [catalog, setCatalog] = useState<CatalogItem[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  // step 3 state
  const [results, setResults] = useState<SetupInstallResult[]>([]);
  const [tasks, setTasks] = useState<Record<string, InstallTask>>({});
  const pollRef = useRef<ReturnType<typeof setInterval>>(undefined);

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const [pkgs, php] = await Promise.all([fetchPackages(), fetchPHP()]);
      const cat = buildCatalog(pkgs ?? [], php ?? []);
      setCatalog(cat);
      // Pre-select recommended items that aren't installed yet.
      setSelected(new Set(cat.filter(c => c.recommended && !c.installed).map(c => c.key)));
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);
  useEffect(() => () => { if (pollRef.current) clearInterval(pollRef.current); }, []);

  const grouped = useMemo(() => {
    const g: Record<string, CatalogItem[]> = {};
    for (const c of catalog) (g[c.category] ??= []).push(c);
    return g;
  }, [catalog]);
  const cats = useMemo(
    () => Object.keys(grouped).sort((a, b) => {
      const ia = categoryOrder.indexOf(a), ib = categoryOrder.indexOf(b);
      return (ia === -1 ? 99 : ia) - (ib === -1 ? 99 : ib);
    }),
    [grouped]
  );

  const selectable = catalog.filter(c => !c.installed);
  const toggle = (key: string) => setSelected(s => {
    const n = new Set(s);
    if (n.has(key)) n.delete(key); else n.add(key);
    return n;
  });
  const selectAll = () => setSelected(new Set(selectable.map(c => c.key)));
  const selectRecommended = () => setSelected(new Set(selectable.filter(c => c.recommended).map(c => c.key)));
  const clearAll = () => setSelected(new Set());

  const chosen = catalog.filter(c => selected.has(c.key) && !c.installed);

  const beginInstall = async () => {
    setStep(3);
    setError('');
    try {
      const resp = await startSetupInstall(chosen.map(c => ({ type: c.type, id: c.id })));
      setResults(resp.items);
      const taskIds = resp.items.filter(i => i.task_id).map(i => i.task_id!);
      if (taskIds.length === 0) return; // everything skipped
      const poll = async () => {
        try {
          const all = await fetchTasks();
          const byId: Record<string, InstallTask> = {};
          for (const t of all ?? []) byId[t.id] = t;
          setTasks(byId);
          const allDone = taskIds.every(id => {
            const st = byId[id]?.status;
            return st === 'done' || st === 'error';
          });
          if (allDone && pollRef.current) { clearInterval(pollRef.current); pollRef.current = undefined; }
        } catch { /* keep polling */ }
      };
      poll();
      pollRef.current = setInterval(poll, 1500);
    } catch (e) {
      setError((e as Error).message);
    }
  };

  // ── Step 3 derived progress ──
  const rows = results.map(r => {
    const status: InstallTask['status'] | 'skipped' =
      r.skipped ? 'skipped' : (r.task_id ? tasks[r.task_id]?.status ?? 'queued' : 'queued');
    return { ...r, status, task: r.task_id ? tasks[r.task_id] : undefined };
  });
  const activeTasks = rows.filter(r => !r.skipped);
  const doneCount = activeTasks.filter(r => r.status === 'done' || r.status === 'error').length;
  const allFinished = activeTasks.length > 0 && doneCount === activeTasks.length;
  const noopFinished = activeTasks.length === 0 && results.length > 0;
  const currentOutput = rows.find(r => r.status === 'running')?.task?.output
    ?? rows.filter(r => r.status === 'error').slice(-1)[0]?.task?.output
    ?? rows.filter(r => r.status === 'done').slice(-1)[0]?.task?.output ?? '';

  const stepBadge = (n: number, label: string) => (
    <div className="flex items-center gap-2">
      <div className={`flex h-6 w-6 items-center justify-center rounded-full text-xs font-semibold ${
        step === n ? 'bg-blue-600 text-white' : step > n ? 'bg-emerald-500/20 text-emerald-400' : 'bg-accent text-muted-foreground'
      }`}>{step > n ? <Check size={13} /> : n}</div>
      <span className={`text-sm ${step >= n ? 'text-foreground' : 'text-muted-foreground'}`}>{label}</span>
    </div>
  );

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-bold sm:text-2xl text-foreground"><Wand2 size={22} className="text-blue-400" /> Setup Wizard</h1>
          <p className="mt-1 text-sm text-muted-foreground">Install PHP, MariaDB and other components in one go. Already-installed items are skipped.</p>
        </div>
        {step === 1 && (
          <button onClick={load} disabled={loading} className="flex items-center gap-2 rounded-md border border-border bg-card px-3 py-2 text-sm text-card-foreground hover:bg-accent disabled:opacity-50">
            <RefreshCw size={14} className={loading ? 'animate-spin' : ''} />Refresh
          </button>
        )}
      </div>

      <div className="flex items-center gap-4 rounded-lg border border-border bg-card px-4 py-3">
        {stepBadge(1, 'Select')}<ChevronRight size={14} className="text-muted-foreground" />
        {stepBadge(2, 'Review')}<ChevronRight size={14} className="text-muted-foreground" />
        {stepBadge(3, 'Install')}
      </div>

      {error && <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">{error}</div>}

      {/* ── Step 1: Select ── */}
      {step === 1 && (
        <>
          {loading && <p className="py-8 text-center text-muted-foreground">Detecting installed components...</p>}
          {!loading && (
            <>
              <div className="flex flex-wrap items-center gap-2">
                <button onClick={selectRecommended} className="rounded-md border border-border bg-card px-3 py-1.5 text-sm text-card-foreground hover:bg-accent">Select recommended</button>
                <button onClick={selectAll} className="rounded-md border border-border bg-card px-3 py-1.5 text-sm text-card-foreground hover:bg-accent">Select all</button>
                <button onClick={clearAll} className="rounded-md border border-border bg-card px-3 py-1.5 text-sm text-card-foreground hover:bg-accent">Clear</button>
                <span className="ml-auto text-sm text-muted-foreground">{chosen.length} selected</span>
              </div>

              {cats.map(cat => (
                <div key={cat}>
                  <h2 className="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">{cat}</h2>
                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
                    {grouped[cat].map(c => {
                      const isSel = selected.has(c.key);
                      const disabled = c.installed;
                      return (
                        <button
                          key={c.key}
                          type="button"
                          disabled={disabled}
                          onClick={() => toggle(c.key)}
                          className={`rounded-lg border p-4 text-left shadow-md transition ${
                            c.installed ? 'border-emerald-500/30 bg-card opacity-70 cursor-default'
                            : isSel ? 'border-blue-500 bg-blue-500/5' : 'border-border bg-card hover:border-blue-500/40'
                          }`}
                        >
                          <div className="mb-1 flex items-start justify-between">
                            <div className="flex items-center gap-2">
                              {c.type === 'php'
                                ? <span className="font-mono text-xs text-blue-400">PHP</span>
                                : <PackageIcon size={15} className="text-muted-foreground" />}
                              <span className="text-sm font-medium text-foreground">{c.name}</span>
                              {c.required && <span title="Required by UWAS"><Shield size={12} className="text-blue-400" /></span>}
                            </div>
                            {c.installed ? (
                              <span className="flex items-center gap-1 rounded-full bg-emerald-500/15 px-2 py-0.5 text-[10px] font-medium text-emerald-400"><Check size={10} /> Installed</span>
                            ) : (
                              <span className={`flex h-4 w-4 items-center justify-center rounded border ${isSel ? 'border-blue-500 bg-blue-600 text-white' : 'border-border'}`}>
                                {isSel && <Check size={11} />}
                              </span>
                            )}
                          </div>
                          <p className="text-xs text-muted-foreground">{c.description}</p>
                          {c.recommended && !c.installed && <p className="mt-1 text-[10px] font-medium text-blue-400/70">Recommended</p>}
                        </button>
                      );
                    })}
                  </div>
                </div>
              ))}

              <div className="flex justify-end">
                <button disabled={chosen.length === 0} onClick={() => setStep(2)}
                  className="flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50">
                  Next <ChevronRight size={15} />
                </button>
              </div>
            </>
          )}
        </>
      )}

      {/* ── Step 2: Review ── */}
      {step === 2 && (
        <>
          <div className="rounded-lg border border-border bg-card p-4">
            <h2 className="mb-3 text-sm font-semibold text-foreground">About to install {chosen.length} component{chosen.length === 1 ? '' : 's'}</h2>
            <ul className="space-y-1.5">
              {chosen.map(c => (
                <li key={c.key} className="flex items-center gap-2 text-sm text-card-foreground">
                  <Download size={14} className="text-blue-400" />
                  <span>{c.name}</span>
                  <span className="text-xs text-muted-foreground">({c.category})</span>
                </li>
              ))}
            </ul>
            <p className="mt-3 text-xs text-muted-foreground">Components install one at a time on the server. You can watch progress on the next step; it keeps running even if you navigate away.</p>
          </div>
          <div className="flex justify-between">
            <button onClick={() => setStep(1)} className="flex items-center gap-1.5 rounded-md border border-border bg-card px-4 py-2 text-sm text-card-foreground hover:bg-accent">
              <ChevronLeft size={15} /> Back
            </button>
            <button onClick={beginInstall} className="flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700">
              <Download size={15} /> Install {chosen.length}
            </button>
          </div>
        </>
      )}

      {/* ── Step 3: Install & watch ── */}
      {step === 3 && (
        <>
          <div className="rounded-lg border border-border bg-card p-4">
            <div className="mb-3 flex items-center justify-between">
              <h2 className="text-sm font-semibold text-foreground">
                {allFinished || noopFinished ? 'Installation complete' : 'Installing...'}
              </h2>
              {activeTasks.length > 0 && <span className="text-xs text-muted-foreground">{doneCount}/{activeTasks.length} done</span>}
            </div>
            {activeTasks.length > 0 && (
              <div className="mb-4 h-2 w-full overflow-hidden rounded-full bg-accent">
                <div className="h-full bg-blue-600 transition-all" style={{ width: `${(doneCount / activeTasks.length) * 100}%` }} />
              </div>
            )}
            <ul className="space-y-2">
              {rows.map(r => (
                <li key={r.type + ':' + r.id} className="flex items-center gap-2 text-sm">
                  <StatusIcon status={r.status} />
                  <span className="text-card-foreground">{r.name}</span>
                  {r.skipped && <span className="text-xs text-muted-foreground">— {r.reason || 'skipped'}</span>}
                  {r.status === 'error' && <span className="text-xs text-red-400">— failed</span>}
                </li>
              ))}
            </ul>
          </div>

          {currentOutput && (
            <div className="rounded-lg border border-border bg-card p-3">
              <p className="mb-2 text-xs font-semibold text-muted-foreground">Output</p>
              <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words rounded bg-background p-3 font-mono text-[11px] text-muted-foreground">{currentOutput}</pre>
            </div>
          )}

          <div className="flex justify-end gap-2">
            {(allFinished || noopFinished) && (
              <button onClick={() => { setStep(1); setResults([]); setTasks({}); load(); }}
                className="flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700">
                <Check size={15} /> Done
              </button>
            )}
            {!allFinished && !noopFinished && (
              <span className="flex items-center gap-2 px-4 py-2 text-sm text-muted-foreground"><Loader2 size={14} className="animate-spin" /> Working...</span>
            )}
          </div>
        </>
      )}
    </div>
  );
}
