import { useState, useEffect, useCallback } from 'react';
import { ArrowDownToLine, Loader2, Server, Database, FolderOpen, Key, Lock, Check, AlertTriangle } from 'lucide-react';
import { fetchDomains, migrateSite, type DomainData, type MigrateResult } from '@/lib/api';

type Step = 'form' | 'running' | 'done';

export default function Migration() {
  const [domains, setDomains] = useState<DomainData[]>([]);
  const [loading, setLoading] = useState(true);
  const [step, setStep] = useState<Step>('form');
  const [result, setResult] = useState<MigrateResult | null>(null);
  const [error, setError] = useState('');

  // Form fields
  const [sourceHost, setSourceHost] = useState('');
  const [sourcePort, setSourcePort] = useState('22');
  const [sourcePath, setSourcePath] = useState('');
  const [authMethod, setAuthMethod] = useState<'key' | 'password'>('key');
  const [sshKey, setSSHKey] = useState('/root/.ssh/id_rsa');
  const [sshPass, setSSHPass] = useState('');
  const [domain, setDomain] = useState('');
  const [newDomain, setNewDomain] = useState('');
  const [dbName, setDBName] = useState('');
  const [dbUser, setDBUser] = useState('');
  const [dbPass, setDBPass] = useState('');
  const [dbHost, setDBHost] = useState('localhost');

  const load = useCallback(async () => {
    try {
      const d = await fetchDomains();
      setDomains(d ?? []);
    } catch {} finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  const targetDomain = domain === '__new__' ? newDomain : domain;

  const handleMigrate = async () => {
    if (!sourceHost || !sourcePath || !targetDomain) return;
    setStep('running');
    setError('');
    setResult(null);
    try {
      const res = await migrateSite({
        source_host: sourceHost,
        source_port: sourcePort || '22',
        source_path: sourcePath,
        ssh_key: authMethod === 'key' ? sshKey : undefined,
        ssh_pass: authMethod === 'password' ? sshPass : undefined,
        domain: targetDomain,
        db_name: dbName || undefined,
        db_user: dbUser || undefined,
        db_pass: dbPass || undefined,
        db_host: dbHost || undefined,
      });
      setResult(res);
      if (res.status === 'error' && res.error) {
        setError(res.error);
      }
      setStep('done');
    } catch (e) {
      setError((e as Error).message);
      setStep('done');
    }
  };

  const reset = () => {
    setStep('form');
    setResult(null);
    setError('');
  };

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl font-bold sm:text-2xl text-foreground">Site Migration</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Migrate a site from a remote server via SSH. Files are synced with rsync, database is dumped and imported.
        </p>
      </div>

      {error && (
        <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">{error}</div>
      )}

      {/* Running state */}
      {step === 'running' && (
        <div className="rounded-lg border border-border bg-card p-8 text-center">
          <Loader2 size={40} className="mx-auto mb-4 animate-spin text-blue-400" />
          <h2 className="text-lg font-semibold text-foreground">Migrating...</h2>
          <p className="mt-2 text-sm text-muted-foreground">
            Syncing files and database from {sourceHost}. This may take a while for large sites.
          </p>
        </div>
      )}

      {/* Form */}
      {step === 'form' && (
        <div className="space-y-5">
          {/* Step 1: SSH Connection */}
          <div className="rounded-lg border border-border bg-card p-5">
            <div className="flex items-center gap-2 mb-4">
              <Server size={16} className="text-blue-400" />
              <h2 className="text-sm font-semibold text-foreground">1. Remote Server</h2>
            </div>

            <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
              <div className="sm:col-span-2">
                <label className="mb-1.5 block text-xs font-medium text-muted-foreground">SSH Host</label>
                <input
                  type="text" value={sourceHost} onChange={e => setSourceHost(e.target.value)}
                  placeholder="root@192.168.1.100"
                  className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none placeholder:text-muted-foreground focus:border-blue-500"
                />
              </div>
              <div>
                <label className="mb-1.5 block text-xs font-medium text-muted-foreground">Port</label>
                <input
                  type="text" value={sourcePort} onChange={e => setSourcePort(e.target.value)}
                  placeholder="22"
                  className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none placeholder:text-muted-foreground focus:border-blue-500"
                />
              </div>
            </div>

            <div className="mt-4">
              <label className="mb-1.5 block text-xs font-medium text-muted-foreground">Remote Web Root</label>
              <input
                type="text" value={sourcePath} onChange={e => setSourcePath(e.target.value)}
                placeholder="/var/www/example.com/public_html"
                className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none placeholder:text-muted-foreground focus:border-blue-500"
              />
            </div>

            <div className="mt-4">
              <label className="mb-1.5 block text-xs font-medium text-muted-foreground">Authentication</label>
              <div className="flex gap-3 mt-1.5">
                <button
                  onClick={() => setAuthMethod('key')}
                  className={`flex items-center gap-1.5 rounded-md px-3 py-2 text-xs font-medium transition-colors ${
                    authMethod === 'key' ? 'bg-blue-600/20 text-blue-400 border border-blue-500/30' : 'bg-muted text-muted-foreground border border-border hover:text-foreground'
                  }`}
                >
                  <Key size={13} /> SSH Key
                </button>
                <button
                  onClick={() => setAuthMethod('password')}
                  className={`flex items-center gap-1.5 rounded-md px-3 py-2 text-xs font-medium transition-colors ${
                    authMethod === 'password' ? 'bg-blue-600/20 text-blue-400 border border-blue-500/30' : 'bg-muted text-muted-foreground border border-border hover:text-foreground'
                  }`}
                >
                  <Lock size={13} /> Password
                </button>
              </div>
              <div className="mt-3">
                {authMethod === 'key' ? (
                  <input
                    type="text" value={sshKey} onChange={e => setSSHKey(e.target.value)}
                    placeholder="/root/.ssh/id_rsa"
                    className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none placeholder:text-muted-foreground focus:border-blue-500"
                  />
                ) : (
                  <input
                    type="password" value={sshPass} onChange={e => setSSHPass(e.target.value)}
                    placeholder="SSH password"
                    className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none placeholder:text-muted-foreground focus:border-blue-500"
                  />
                )}
              </div>
            </div>
          </div>

          {/* Step 2: Target */}
          <div className="rounded-lg border border-border bg-card p-5">
            <div className="flex items-center gap-2 mb-4">
              <FolderOpen size={16} className="text-emerald-400" />
              <h2 className="text-sm font-semibold text-foreground">2. Target Domain</h2>
            </div>

            <div>
              <label className="mb-1.5 block text-xs font-medium text-muted-foreground">Domain</label>
              <select
                value={domain} onChange={e => setDomain(e.target.value)}
                className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none focus:border-blue-500"
              >
                <option value="">Select existing domain...</option>
                {domains.map(d => (
                  <option key={d.host} value={d.host}>{d.host}</option>
                ))}
                <option value="__new__">+ Enter new domain</option>
              </select>
            </div>

            {domain === '__new__' && (
              <div className="mt-3">
                <input
                  type="text" value={newDomain} onChange={e => setNewDomain(e.target.value)}
                  placeholder="example.com"
                  className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none placeholder:text-muted-foreground focus:border-blue-500"
                />
              </div>
            )}
          </div>

          {/* Step 3: Database (optional) */}
          <div className="rounded-lg border border-border bg-card p-5">
            <div className="flex items-center gap-2 mb-1">
              <Database size={16} className="text-amber-400" />
              <h2 className="text-sm font-semibold text-foreground">3. Remote Database</h2>
            </div>
            <p className="text-xs text-muted-foreground mb-4">Optional. Leave empty to skip database migration or auto-detect from wp-config.php.</p>

            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div>
                <label className="mb-1.5 block text-xs font-medium text-muted-foreground">DB Name</label>
                <input
                  type="text" value={dbName} onChange={e => setDBName(e.target.value)}
                  placeholder="wp_database"
                  className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none placeholder:text-muted-foreground focus:border-blue-500"
                />
              </div>
              <div>
                <label className="mb-1.5 block text-xs font-medium text-muted-foreground">DB Host</label>
                <input
                  type="text" value={dbHost} onChange={e => setDBHost(e.target.value)}
                  placeholder="localhost"
                  className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none placeholder:text-muted-foreground focus:border-blue-500"
                />
              </div>
              <div>
                <label className="mb-1.5 block text-xs font-medium text-muted-foreground">DB User</label>
                <input
                  type="text" value={dbUser} onChange={e => setDBUser(e.target.value)}
                  placeholder="root"
                  className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none placeholder:text-muted-foreground focus:border-blue-500"
                />
              </div>
              <div>
                <label className="mb-1.5 block text-xs font-medium text-muted-foreground">DB Password</label>
                <input
                  type="password" value={dbPass} onChange={e => setDBPass(e.target.value)}
                  placeholder="password"
                  className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none placeholder:text-muted-foreground focus:border-blue-500"
                />
              </div>
            </div>
          </div>

          {/* Submit */}
          <button
            onClick={handleMigrate}
            disabled={!sourceHost || !sourcePath || !targetDomain}
            className="flex items-center gap-2 rounded-md bg-blue-600 px-5 py-2.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          >
            <ArrowDownToLine size={14} />
            Start Migration
          </button>
        </div>
      )}

      {/* Result */}
      {step === 'done' && result && (
        <div className="space-y-5">
          {result.status === 'done' ? (
            <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-5">
              <div className="flex items-center gap-2 mb-3">
                <Check size={18} className="text-emerald-400" />
                <h3 className="text-sm font-semibold text-emerald-400">Migration Complete</h3>
              </div>

              <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                <div className="rounded bg-background px-3 py-2">
                  <span className="text-xs text-muted-foreground">Domain</span>
                  <p className="font-mono text-sm text-foreground">{result.domain}</p>
                </div>
                <div className="rounded bg-background px-3 py-2">
                  <span className="text-xs text-muted-foreground">Duration</span>
                  <p className="font-mono text-sm text-foreground">{result.duration || '-'}</p>
                </div>
                <div className="rounded bg-background px-3 py-2">
                  <span className="text-xs text-muted-foreground">Files</span>
                  <p className={`font-mono text-sm ${result.files_sync === 'ok' ? 'text-emerald-400' : 'text-red-400'}`}>
                    {result.files_sync}
                  </p>
                </div>
                <div className="rounded bg-background px-3 py-2">
                  <span className="text-xs text-muted-foreground">Database</span>
                  <p className={`font-mono text-sm ${result.db_import === 'ok' ? 'text-emerald-400' : result.db_import === 'skipped' ? 'text-muted-foreground' : 'text-red-400'}`}>
                    {result.db_import}
                  </p>
                </div>
              </div>
            </div>
          ) : (
            <div className="rounded-lg border border-red-500/30 bg-red-500/5 p-5">
              <div className="flex items-center gap-2 mb-2">
                <AlertTriangle size={18} className="text-red-400" />
                <h3 className="text-sm font-semibold text-red-400">Migration Failed</h3>
              </div>
              <p className="text-sm text-red-300">{result.error}</p>
            </div>
          )}

          {/* Operation log */}
          {result.output && (
            <div className="rounded-lg border border-border bg-card p-5">
              <h3 className="text-sm font-semibold text-foreground mb-3">Operation Log</h3>
              <pre className="max-h-72 overflow-auto rounded bg-background p-4 font-mono text-xs text-muted-foreground whitespace-pre-wrap">
                {result.output}
              </pre>
            </div>
          )}

          <button
            onClick={reset}
            className="rounded-md border border-border bg-card px-4 py-2 text-sm text-foreground hover:bg-accent"
          >
            New Migration
          </button>
        </div>
      )}

      {loading && (
        <div className="text-center text-sm text-muted-foreground py-12">Loading...</div>
      )}
    </div>
  );
}
