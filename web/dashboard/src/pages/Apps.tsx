import { useState, useEffect, useCallback } from 'react';
import { Play, Square, RefreshCw, Box, Clock, Hash, Terminal, Cpu, Rocket, GitBranch, X, CheckCircle, ChevronDown, ChevronUp, Save, FileText, Settings } from 'lucide-react';
import { fetchApps, startApp, stopApp, restartApp, deployApp, fetchDeployStatus, updateAppEnv, fetchAppLogs, type AppInstance, type DeployStatus } from '@/lib/api';

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
  const [deployDomain, setDeployDomain] = useState('');
  const [deployForm, setDeployForm] = useState({ gitUrl: '', branch: 'main', buildCmd: '', dockerfile: '', sshKey: '', gitToken: '' });
  const [deploying, setDeploying] = useState(false);
  const [deployStatus, setDeployStatus] = useState<DeployStatus | null>(null);
  const [expanded, setExpanded] = useState('');
  const [appTab, setAppTab] = useState<'config' | 'logs'>('config');
  const [envText, setEnvText] = useState('');
  const [editCmd, setEditCmd] = useState('');
  const [editPort, setEditPort] = useState('');
  const [appLog, setAppLog] = useState('');
  const [savingEnv, setSavingEnv] = useState(false);

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

  const toggleExpand = (domain: string) => {
    if (expanded === domain) { setExpanded(''); return; }
    setExpanded(domain);
    setAppTab('config');
    const app = apps.find(a => a.domain === domain);
    if (app) {
      const envLines = Object.entries(app.env || {}).map(([k, v]) => `${k}=${v}`).join('\n');
      setEnvText(envLines);
      setEditCmd(app.command);
      setEditPort(String(app.port));
    }
    fetchAppLogs(domain).then(r => setAppLog(r?.log || '')).catch(() => setAppLog(''));
  };

  const handleSaveConfig = async () => {
    if (!expanded) return;
    setSavingEnv(true);
    try {
      const env: Record<string, string> = {};
      envText.split('\n').forEach(line => {
        const eq = line.indexOf('=');
        if (eq > 0) env[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
      });
      await updateAppEnv(expanded, env, editCmd || undefined, parseInt(editPort) || undefined);
      showStatus(true, `Config saved for ${expanded}. Restart to apply.`);
      await load();
    } catch (e) { showStatus(false, (e as Error).message); }
    finally { setSavingEnv(false); }
  };

  const handleDeploy = async () => {
    if (!deployDomain) return;
    setDeploying(true);
    setDeployStatus(null);
    try {
      await deployApp(deployDomain, {
        git_url: deployForm.gitUrl || undefined,
        git_branch: deployForm.branch || 'main',
        build_cmd: deployForm.buildCmd || undefined,
        dockerfile: deployForm.dockerfile || undefined,
        ssh_key_path: deployForm.sshKey || undefined,
        git_token: deployForm.gitToken || undefined,
      });
      showStatus(true, `Deploy started for ${deployDomain}`);
      // Poll for status
      const poll = setInterval(async () => {
        try {
          const st = await fetchDeployStatus(deployDomain);
          setDeployStatus(st);
          if (st.status === 'running' || st.status === 'failed') {
            clearInterval(poll);
            setDeploying(false);
            await load();
            if (st.status === 'running') showStatus(true, `Deploy complete: ${deployDomain} (${st.duration})`);
            else showStatus(false, `Deploy failed: ${st.error}`);
          }
        } catch { clearInterval(poll); setDeploying(false); }
      }, 2000);
    } catch (e) {
      showStatus(false, (e as Error).message);
      setDeploying(false);
    }
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
                <button onClick={() => { setDeployDomain(app.domain); setDeployForm({ gitUrl: '', branch: 'main', buildCmd: '', dockerfile: '', sshKey: '', gitToken: '' }); setDeployStatus(null); }}
                  className="flex items-center gap-1 rounded-md bg-purple-600/15 px-3 py-1.5 text-xs font-medium text-purple-400 hover:bg-purple-600/25">
                  <Rocket size={11} /> Deploy
                </button>
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

            {/* Details row — clickable to expand */}
            <div className="mt-3 flex items-center justify-between cursor-pointer" onClick={() => toggleExpand(app.domain)}>
              <div className="flex gap-6 text-xs text-muted-foreground">
                <span className="flex items-center gap-1"><Hash size={11} /> Port {app.port}</span>
                {app.pid > 0 && <span className="flex items-center gap-1"><Terminal size={11} /> PID {app.pid}</span>}
                {app.uptime && <span className="flex items-center gap-1"><Clock size={11} /> {app.uptime}</span>}
              </div>
              {expanded === app.domain ? <ChevronUp size={14} className="text-muted-foreground" /> : <ChevronDown size={14} className="text-muted-foreground" />}
            </div>

            {/* Expanded panel */}
            {expanded === app.domain && (
              <div className="mt-4 border-t border-border pt-4 space-y-4">
                {/* Tab selector */}
                <div className="flex gap-1 rounded-md border border-border overflow-hidden w-fit">
                  <button onClick={() => setAppTab('config')}
                    className={`flex items-center gap-1 px-3 py-1.5 text-xs font-medium ${appTab === 'config' ? 'bg-blue-600 text-white' : 'bg-card text-muted-foreground hover:text-foreground'}`}>
                    <Settings size={11} /> Config & ENV
                  </button>
                  <button onClick={() => { setAppTab('logs'); fetchAppLogs(app.domain).then(r => setAppLog(r?.log || '')).catch(() => setAppLog('')); }}
                    className={`flex items-center gap-1 px-3 py-1.5 text-xs font-medium ${appTab === 'logs' ? 'bg-blue-600 text-white' : 'bg-card text-muted-foreground hover:text-foreground'}`}>
                    <FileText size={11} /> Logs
                  </button>
                </div>

                {appTab === 'config' && (
                  <div className="space-y-3">
                    <div className="grid grid-cols-2 gap-3">
                      <div>
                        <label className="text-[10px] text-muted-foreground">Start Command</label>
                        <input value={editCmd} onChange={e => setEditCmd(e.target.value)}
                          className="w-full rounded-md border border-border bg-background px-3 py-1.5 text-sm font-mono text-foreground outline-none" />
                      </div>
                      <div>
                        <label className="text-[10px] text-muted-foreground">Port</label>
                        <input type="number" value={editPort} onChange={e => setEditPort(e.target.value)}
                          className="w-full rounded-md border border-border bg-background px-3 py-1.5 text-sm text-foreground outline-none" />
                      </div>
                    </div>
                    <div>
                      <label className="text-[10px] text-muted-foreground">Environment Variables (KEY=value, one per line)</label>
                      <textarea rows={5} value={envText} onChange={e => setEnvText(e.target.value)}
                        placeholder={"NODE_ENV=production\nDATABASE_URL=postgres://localhost/mydb\nPORT=3000"}
                        className="w-full rounded-md border border-border bg-background px-3 py-2 text-xs font-mono text-foreground outline-none" />
                    </div>
                    <div className="flex justify-end gap-2">
                      <button onClick={handleSaveConfig} disabled={savingEnv}
                        className="flex items-center gap-1 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50">
                        {savingEnv ? <RefreshCw size={12} className="animate-spin" /> : <Save size={12} />} Save Config
                      </button>
                      <button onClick={() => handleAction(app.domain, 'restart')} disabled={acting === app.domain}
                        className="flex items-center gap-1 rounded-md bg-emerald-600 px-4 py-2 text-sm font-medium text-white hover:bg-emerald-700 disabled:opacity-50">
                        <RefreshCw size={12} /> Restart to Apply
                      </button>
                    </div>
                  </div>
                )}

                {appTab === 'logs' && (
                  <div className="rounded-md bg-[#0d1117] p-3 max-h-64 overflow-auto">
                    <div className="flex items-center justify-between mb-2">
                      <span className="text-[10px] text-muted-foreground">app.log (last 100KB)</span>
                      <button onClick={() => fetchAppLogs(app.domain).then(r => setAppLog(r?.log || ''))}
                        className="text-[10px] text-blue-400 hover:text-blue-300"><RefreshCw size={10} className="inline" /> Refresh</button>
                    </div>
                    <pre className="text-[10px] text-green-400 font-mono whitespace-pre-wrap leading-4">
                      {appLog || 'No logs yet'}
                    </pre>
                  </div>
                )}
              </div>
            )}
          </div>
        ))}
      </div>

      {/* Deploy Modal */}
      {deployDomain && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm" onClick={() => !deploying && setDeployDomain('')}>
          <div className="w-full max-w-lg rounded-xl border border-border bg-card p-6 shadow-2xl" onClick={e => e.stopPropagation()}>
            <div className="flex items-center justify-between mb-4">
              <div className="flex items-center gap-2">
                <Rocket size={18} className="text-purple-400" />
                <h2 className="text-sm font-semibold text-foreground">Deploy {deployDomain}</h2>
              </div>
              {!deploying && <button onClick={() => setDeployDomain('')} className="text-muted-foreground hover:text-foreground"><X size={16} /></button>}
            </div>

            <div className="space-y-3">
              <div>
                <label className="text-xs text-muted-foreground">Git Repository URL</label>
                <input value={deployForm.gitUrl} onChange={e => setDeployForm(f => ({ ...f, gitUrl: e.target.value }))}
                  placeholder="https://github.com/user/repo.git (leave empty for git pull)"
                  className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground outline-none font-mono" />
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className="text-xs text-muted-foreground">Branch</label>
                  <input value={deployForm.branch} onChange={e => setDeployForm(f => ({ ...f, branch: e.target.value }))}
                    placeholder="main"
                    className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground outline-none" />
                </div>
                <div>
                  <label className="text-xs text-muted-foreground">Dockerfile (optional)</label>
                  <input value={deployForm.dockerfile} onChange={e => setDeployForm(f => ({ ...f, dockerfile: e.target.value }))}
                    placeholder="Dockerfile"
                    className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground outline-none" />
                </div>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className="text-xs text-muted-foreground">SSH Key Path (private repos)</label>
                  <input value={deployForm.sshKey} onChange={e => setDeployForm(f => ({ ...f, sshKey: e.target.value }))}
                    placeholder="/root/.ssh/deploy_key"
                    className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground outline-none font-mono" />
                </div>
                <div>
                  <label className="text-xs text-muted-foreground">Access Token (GitHub/GitLab)</label>
                  <input type="password" value={deployForm.gitToken} onChange={e => setDeployForm(f => ({ ...f, gitToken: e.target.value }))}
                    placeholder="ghp_xxxx or glpat-xxxx"
                    className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground outline-none font-mono" />
                </div>
              </div>
              <div>
                <label className="text-xs text-muted-foreground">Build Command (auto-detected if empty)</label>
                <input value={deployForm.buildCmd} onChange={e => setDeployForm(f => ({ ...f, buildCmd: e.target.value }))}
                  placeholder="npm install && npm run build"
                  className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground outline-none font-mono" />
              </div>

              {/* Deploy log */}
              {deployStatus && (
                <div className="rounded-md bg-[#0d1117] p-3 max-h-48 overflow-auto">
                  <div className="flex items-center gap-2 mb-2">
                    {deployStatus.status === 'deploying' || deployStatus.status === 'building' ? (
                      <RefreshCw size={12} className="animate-spin text-blue-400" />
                    ) : deployStatus.status === 'running' ? (
                      <CheckCircle size={12} className="text-emerald-400" />
                    ) : (
                      <X size={12} className="text-red-400" />
                    )}
                    <span className="text-xs text-muted-foreground">{deployStatus.status} {deployStatus.commit_sha && `• ${deployStatus.commit_sha}`} {deployStatus.duration && `• ${deployStatus.duration}`}</span>
                  </div>
                  <pre className="text-[10px] text-green-400 font-mono whitespace-pre-wrap">{deployStatus.log || 'Waiting...'}</pre>
                  {deployStatus.error && <p className="mt-2 text-xs text-red-400">{deployStatus.error}</p>}
                </div>
              )}

              <div className="flex justify-end gap-2 pt-2">
                {!deploying && (
                  <button onClick={() => setDeployDomain('')}
                    className="rounded-md border border-border bg-card px-4 py-2 text-sm text-card-foreground hover:bg-accent">
                    Cancel
                  </button>
                )}
                <button onClick={handleDeploy} disabled={deploying}
                  className="flex items-center gap-1.5 rounded-md bg-purple-600 px-4 py-2 text-sm font-medium text-white hover:bg-purple-700 disabled:opacity-50">
                  {deploying ? <RefreshCw size={14} className="animate-spin" /> : <Rocket size={14} />}
                  {deploying ? 'Deploying...' : 'Deploy'}
                </button>
              </div>

              <p className="text-[10px] text-muted-foreground">
                <GitBranch size={10} className="inline" /> Webhook URL for auto-deploy: <code className="bg-accent px-1 rounded">/api/v1/apps/{deployDomain}/webhook</code>
              </p>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
