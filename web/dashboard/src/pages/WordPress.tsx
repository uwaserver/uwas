import { useState, useEffect } from 'react';
import { Zap, RefreshCw, Check, Copy, ExternalLink } from 'lucide-react';
import {
  fetchDomains, installWordPress, fetchWPInstallStatus, fetchDBStatus,
  type DomainData,
} from '@/lib/api';

export default function WordPress() {
  const [domains, setDomains] = useState<DomainData[]>([]);
  const [selectedDomain, setSelectedDomain] = useState('');
  const [dbHost, setDbHost] = useState('localhost');
  const [installing, setInstalling] = useState(false);
  const [status, setStatus] = useState<any>(null);
  const [error, setError] = useState('');
  const [mysqlOk, setMysqlOk] = useState(false);
  const [copied, setCopied] = useState('');

  useEffect(() => {
    fetchDomains().then(d => {
      const list = d ?? [];
      setDomains(list);
      const phpDomains = list.filter(dd => dd.type === 'php');
      if (phpDomains.length > 0) setSelectedDomain(phpDomains[0].host);
    }).catch(() => {});
    fetchDBStatus().then(s => setMysqlOk(s?.installed && s?.running)).catch(() => {});
  }, []);

  const phpDomains = domains.filter(d => d.type === 'php');

  const handleInstall = async () => {
    if (!selectedDomain) return;
    setInstalling(true);
    setError('');
    setStatus(null);
    try {
      await installWordPress(selectedDomain, dbHost);
      // Poll for completion
      const poll = setInterval(async () => {
        try {
          const st = await fetchWPInstallStatus();
          setStatus(st);
          if (st.status !== 'running') {
            clearInterval(poll);
            setInstalling(false);
          }
        } catch {
          clearInterval(poll);
          setInstalling(false);
        }
      }, 2000);
    } catch (e) {
      setError((e as Error).message);
      setInstalling(false);
    }
  };

  const copy = (text: string, label: string) => {
    navigator.clipboard.writeText(text);
    setCopied(label);
    setTimeout(() => setCopied(''), 2000);
  };

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold text-slate-100">WordPress Install</h1>
        <p className="mt-1 text-sm text-slate-400">
          One-click WordPress installation — downloads WP, creates database, generates wp-config.php.
        </p>
      </div>

      {/* Prerequisites */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5">
        <h2 className="text-sm font-semibold text-slate-300 mb-3">Prerequisites</h2>
        <div className="space-y-2">
          <div className="flex items-center gap-2 text-sm">
            <span className={`h-2.5 w-2.5 rounded-full ${phpDomains.length > 0 ? 'bg-emerald-400' : 'bg-red-400'}`} />
            <span className={phpDomains.length > 0 ? 'text-emerald-400' : 'text-red-400'}>
              PHP domain {phpDomains.length > 0 ? `(${phpDomains.length} found)` : '— create one in Domains page first'}
            </span>
          </div>
          <div className="flex items-center gap-2 text-sm">
            <span className={`h-2.5 w-2.5 rounded-full ${mysqlOk ? 'bg-emerald-400' : 'bg-red-400'}`} />
            <span className={mysqlOk ? 'text-emerald-400' : 'text-red-400'}>
              MySQL/MariaDB {mysqlOk ? 'running' : '— install from Database page first'}
            </span>
          </div>
        </div>
      </div>

      {/* Install form */}
      {phpDomains.length > 0 && mysqlOk && (
        <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5">
          <h2 className="text-sm font-semibold text-slate-300 mb-4">Install WordPress</h2>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div>
              <label className="mb-1.5 block text-xs text-slate-500">Domain</label>
              <select
                value={selectedDomain}
                onChange={e => setSelectedDomain(e.target.value)}
                className="w-full rounded-md border border-[#334155] bg-[#0f172a] px-3 py-2.5 text-sm text-slate-200 outline-none focus:border-blue-500"
              >
                {phpDomains.map(d => <option key={d.host} value={d.host}>{d.host}</option>)}
              </select>
              <p className="mt-1 text-[10px] text-slate-500">Only PHP-type domains shown</p>
            </div>
            <div>
              <label className="mb-1.5 block text-xs text-slate-500">Database Host</label>
              <input
                value={dbHost}
                onChange={e => setDbHost(e.target.value)}
                className="w-full rounded-md border border-[#334155] bg-[#0f172a] px-3 py-2.5 text-sm text-slate-200 outline-none focus:border-blue-500"
                placeholder="localhost"
              />
            </div>
          </div>
          <button
            onClick={handleInstall}
            disabled={installing || !selectedDomain}
            className="mt-4 flex items-center gap-2 rounded-md bg-amber-600 px-5 py-2.5 text-sm font-medium text-white hover:bg-amber-700 disabled:opacity-50"
          >
            {installing ? (
              <><RefreshCw size={14} className="animate-spin" /> Installing...</>
            ) : (
              <><Zap size={14} /> Install WordPress</>
            )}
          </button>
        </div>
      )}

      {error && (
        <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">{error}</div>
      )}

      {/* Progress / Result */}
      {status && status.status === 'running' && (
        <div className="rounded-lg border border-blue-500/30 bg-blue-500/5 p-5">
          <p className="text-sm text-blue-400 mb-2">Installing WordPress on {status.domain}...</p>
          <div className="h-1.5 w-full bg-[#334155] rounded-full overflow-hidden">
            <div className="h-full bg-blue-500 rounded-full animate-pulse" style={{ width: '60%' }} />
          </div>
          <p className="text-xs text-slate-500 mt-2">Downloading, extracting, configuring...</p>
        </div>
      )}

      {status && status.status === 'done' && (
        <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-5">
          <div className="flex items-center gap-2 text-emerald-400 font-medium mb-3">
            <Check size={16} /> WordPress installed successfully!
          </div>

          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
            {([
              ['Database', status.db_name],
              ['DB User', status.db_user],
              ['DB Password', status.db_pass],
              ['Admin URL', status.admin_url],
            ] as [string, string][]).filter(([,v]) => v).map(([label, value]) => (
              <div key={label} className="flex items-center justify-between rounded bg-[#0f172a] px-3 py-2">
                <div>
                  <span className="text-xs text-slate-500">{label}</span>
                  <p className="font-mono text-xs text-slate-200">{value}</p>
                </div>
                <button onClick={() => copy(value, label)} className="ml-2 rounded p-1 text-slate-500 hover:text-slate-300">
                  {copied === label ? <Check size={12} className="text-emerald-400" /> : <Copy size={12} />}
                </button>
              </div>
            ))}
          </div>

          <div className="mt-3 flex items-center gap-2">
            <a
              href={status.admin_url}
              target="_blank"
              rel="noopener"
              className="flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
            >
              Open WordPress Setup <ExternalLink size={13} />
            </a>
          </div>
          <p className="mt-2 text-xs text-amber-400">Save the database credentials — you'll need them if you reinstall.</p>
        </div>
      )}

      {status && status.status === 'error' && (
        <div className="rounded-lg border border-red-500/30 bg-red-500/5 p-5">
          <p className="text-sm text-red-400 mb-2">Installation failed: {status.error}</p>
          {status.output && (
            <pre className="mt-2 max-h-40 overflow-auto rounded bg-[#0f172a] p-3 text-[10px] text-slate-500 whitespace-pre-wrap">{status.output}</pre>
          )}
        </div>
      )}

      {/* What it does */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5">
        <h2 className="text-sm font-semibold text-slate-300 mb-3">What this does</h2>
        <ol className="space-y-1.5 text-xs text-slate-400 list-decimal list-inside">
          <li>Creates a MySQL database and user with random password</li>
          <li>Downloads latest WordPress from wordpress.org</li>
          <li>Extracts to domain's web root (public_html)</li>
          <li>Generates wp-config.php with DB credentials and security salts</li>
          <li>Sets file permissions (www-data:www-data, 755/644, wp-content 775)</li>
          <li>You complete the setup via WordPress's web installer</li>
        </ol>
      </div>
    </div>
  );
}
