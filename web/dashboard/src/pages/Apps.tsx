import { useState, useEffect, useCallback, useRef } from 'react';
import {
  Play, Square, RefreshCw, Clock, Hash, Terminal, Cpu, Rocket, GitBranch,
  X, CheckCircle, ChevronRight, Save, FileText, Settings, Copy, Plus,
  Trash2, Eye, EyeOff, ArrowRight, Circle, Globe,
  GitCommit, Container, Zap, AlertCircle, Server,
} from 'lucide-react';
import {
  fetchApps, startApp, stopApp, restartApp, deployApp, fetchDeployStatus,
  updateAppEnv, fetchAppLogs, fetchAppStats,
  type AppInstance, type DeployStatus, type AppStats,
} from '@/lib/api';

/* ═══════════════════════════════════════════════════════════════════ */
/*  Constants                                                         */
/* ═══════════════════════════════════════════════════════════════════ */

const runtimeMeta: Record<string, { color: string; bg: string; icon: string }> = {
  node:   { color: 'text-green-400',  bg: 'bg-green-500/15',  icon: 'N' },
  python: { color: 'text-yellow-400', bg: 'bg-yellow-500/15', icon: 'Py' },
  ruby:   { color: 'text-red-400',    bg: 'bg-red-500/15',    icon: 'Rb' },
  go:     { color: 'text-cyan-400',   bg: 'bg-cyan-500/15',   icon: 'Go' },
  custom: { color: 'text-slate-400',  bg: 'bg-slate-500/15',  icon: '?' },
};

const SENSITIVE_KEYS = ['SECRET', 'TOKEN', 'PASSWORD', 'KEY', 'PRIVATE', 'CREDENTIAL', 'API_KEY'];

function isSensitiveKey(key: string): boolean {
  const upper = key.toUpperCase();
  return SENSITIVE_KEYS.some(s => upper.includes(s));
}

function timeAgo(dateStr?: string): string {
  if (!dateStr) return '';
  const diff = Date.now() - new Date(dateStr).getTime();
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return 'just now';
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  return `${days}d ago`;
}

/* ═══════════════════════════════════════════════════════════════════ */
/*  EnvEditor — Key-Value rows with add/remove                       */
/* ═══════════════════════════════════════════════════════════════════ */

interface EnvRow { key: string; value: string }

function EnvEditor({ rows, onChange }: { rows: EnvRow[]; onChange: (rows: EnvRow[]) => void }) {
  const [revealed, setRevealed] = useState<Set<number>>(new Set());

  const update = (i: number, field: 'key' | 'value', val: string) => {
    const next = rows.map((r, j) => j === i ? { ...r, [field]: val } : r);
    onChange(next);
  };
  const remove = (i: number) => {
    onChange(rows.filter((_, j) => j !== i));
    setRevealed(prev => { const s = new Set(prev); s.delete(i); return s; });
  };
  const add = () => onChange([...rows, { key: '', value: '' }]);
  const toggleReveal = (i: number) =>
    setRevealed(prev => {
      const s = new Set(prev);
      if (s.has(i)) s.delete(i);
      else s.add(i);
      return s;
    });

  return (
    <div className="space-y-2">
      <div className="grid grid-cols-[1fr_1fr_72px] gap-2 text-[10px] font-medium text-muted-foreground uppercase tracking-wider px-1">
        <span>Key</span><span>Value</span><span />
      </div>
      {rows.map((row, i) => {
        const sensitive = isSensitiveKey(row.key);
        const hidden = sensitive && !revealed.has(i);
        return (
          <div key={i} className="grid grid-cols-[1fr_1fr_72px] gap-2 items-center group">
            <input
              value={row.key} onChange={e => update(i, 'key', e.target.value)}
              placeholder="KEY"
              className="rounded-md border border-border bg-background px-3 py-1.5 text-xs font-mono text-foreground outline-none focus:border-blue-500/50 transition-colors"
            />
            <div className="relative">
              <input
                type={hidden ? 'password' : 'text'}
                value={row.value} onChange={e => update(i, 'value', e.target.value)}
                placeholder="value"
                className="w-full rounded-md border border-border bg-background px-3 py-1.5 pr-8 text-xs font-mono text-foreground outline-none focus:border-blue-500/50 transition-colors"
              />
              {sensitive && (
                <button onClick={() => toggleReveal(i)}
                  className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground">
                  {hidden ? <EyeOff size={12} /> : <Eye size={12} />}
                </button>
              )}
            </div>
            <div className="flex gap-1 justify-end">
              <button onClick={() => remove(i)}
                className="rounded-md p-1.5 text-muted-foreground hover:text-red-400 hover:bg-red-500/10 opacity-0 group-hover:opacity-100 transition-opacity">
                <Trash2 size={13} />
              </button>
            </div>
          </div>
        );
      })}
      <button onClick={add}
        className="flex items-center gap-1.5 rounded-md border border-dashed border-border px-3 py-2 text-xs text-muted-foreground hover:text-foreground hover:border-foreground/30 transition-colors w-full justify-center">
        <Plus size={12} /> Add Variable
      </button>
    </div>
  );
}

/* ═══════════════════════════════════════════════════════════════════ */
/*  RoutingDiagram — visual domain → UWAS → container:port           */
/* ═══════════════════════════════════════════════════════════════════ */

function RoutingDiagram({ domain, port, running }: { domain: string; port: number; running: boolean }) {
  return (
    <div className="flex items-center gap-3 rounded-lg bg-background/50 border border-border/50 px-4 py-3">
      <div className="flex items-center gap-2">
        <Globe size={14} className="text-blue-400" />
        <span className="text-xs font-mono text-foreground">{domain}</span>
      </div>
      <ArrowRight size={14} className="text-muted-foreground" />
      <div className="flex items-center gap-2 rounded-md bg-purple-500/10 px-2.5 py-1">
        <Server size={12} className="text-purple-400" />
        <span className="text-[10px] font-medium text-purple-400">UWAS</span>
      </div>
      <ArrowRight size={14} className="text-muted-foreground" />
      <div className={`flex items-center gap-2 rounded-md px-2.5 py-1 ${running ? 'bg-emerald-500/10' : 'bg-red-500/10'}`}>
        <Container size={12} className={running ? 'text-emerald-400' : 'text-red-400'} />
        <span className={`text-[10px] font-mono font-medium ${running ? 'text-emerald-400' : 'text-red-400'}`}>
          127.0.0.1:{port}
        </span>
      </div>
    </div>
  );
}

/* ═══════════════════════════════════════════════════════════════════ */
/*  ResourceGauge — CPU/Memory visual bar                            */
/* ═══════════════════════════════════════════════════════════════════ */

const gaugeColors = {
  blue:   { bar: 'bg-blue-500',   bg: 'bg-blue-500/10',   text: 'text-blue-400' },
  purple: { bar: 'bg-purple-500', bg: 'bg-purple-500/10', text: 'text-purple-400' },
  cyan:   { bar: 'bg-cyan-500',   bg: 'bg-cyan-500/10',   text: 'text-cyan-400' },
};

function ResourceGauge({ label, value, unit, max, color }: {
  label: string; value: number; unit: string; max: number; color: keyof typeof gaugeColors;
}) {
  const pct = max > 0 ? Math.min((value / max) * 100, 100) : 0;
  const c = gaugeColors[color];
  const display = value < 10 ? value.toFixed(1) : Math.round(value);

  return (
    <div className="rounded-lg bg-background/50 border border-border/50 p-3">
      <div className="flex items-center justify-between mb-2">
        <span className="text-[10px] font-medium text-muted-foreground">{label}</span>
        <span className={`text-xs font-semibold font-mono ${c.text}`}>{display}{unit}</span>
      </div>
      <div className={`h-2 rounded-full ${c.bg} overflow-hidden`}>
        <div
          className={`h-full rounded-full ${c.bar} transition-all duration-500 ease-out`}
          style={{ width: `${pct}%` }}
        />
      </div>
      <div className="flex justify-end mt-1">
        <span className="text-[9px] text-muted-foreground">{pct.toFixed(0)}%</span>
      </div>
    </div>
  );
}

/* ═══════════════════════════════════════════════════════════════════ */
/*  DeployWizard — Step-by-step deploy modal                         */
/* ═══════════════════════════════════════════════════════════════════ */

interface WizardProps {
  domain: string;
  deploying: boolean;
  deployStatus: DeployStatus | null;
  onDeploy: (form: { gitUrl: string; branch: string; buildCmd: string; dockerfile: string; sshKey: string; gitToken: string }) => void;
  onClose: () => void;
}

function DeployWizard({ domain, deploying, deployStatus, onDeploy, onClose }: WizardProps) {
  const [step, setStep] = useState(0);
  const [form, setForm] = useState({ gitUrl: '', branch: 'main', buildCmd: '', dockerfile: '', sshKey: '', gitToken: '' });
  const logEndRef = useRef<HTMLDivElement>(null);
  const activeStep = deployStatus ? 2 : step;

  useEffect(() => {
    logEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [deployStatus?.log]);

  const steps = [
    { label: 'Source', icon: <GitBranch size={14} /> },
    { label: 'Build', icon: <Zap size={14} /> },
    { label: 'Deploy', icon: <Rocket size={14} /> },
  ];

  const canNext = activeStep < 2;
  const canBack = activeStep > 0 && !deploying && !deployStatus;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm" onClick={() => !deploying && onClose()}>
      <div className="w-full max-w-2xl rounded-xl border border-border bg-card shadow-2xl" onClick={e => e.stopPropagation()}>
        {/* Header */}
        <div className="flex items-center justify-between border-b border-border px-6 py-4">
          <div className="flex items-center gap-3">
            <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-purple-500/15">
              <Rocket size={16} className="text-purple-400" />
            </div>
            <div>
              <h2 className="text-sm font-semibold text-foreground">Deploy Application</h2>
              <p className="text-xs text-muted-foreground font-mono">{domain}</p>
            </div>
          </div>
          {!deploying && (
            <button onClick={onClose} className="rounded-md p-1.5 text-muted-foreground hover:text-foreground hover:bg-accent transition-colors">
              <X size={16} />
            </button>
          )}
        </div>

        {/* Step indicator */}
        <div className="flex items-center gap-0 px-6 py-4 border-b border-border">
          {steps.map((s, i) => (
            <div key={i} className="flex items-center">
              <div className={`flex items-center gap-2 rounded-full px-3 py-1.5 text-xs font-medium transition-colors ${
                i === activeStep ? 'bg-blue-600 text-white' :
                i < activeStep ? 'bg-emerald-500/15 text-emerald-400' :
                'bg-accent text-muted-foreground'
              }`}>
                {i < activeStep ? <CheckCircle size={12} /> : s.icon}
                {s.label}
              </div>
              {i < steps.length - 1 && (
                <ChevronRight size={14} className="mx-2 text-muted-foreground" />
              )}
            </div>
          ))}
        </div>

        {/* Content */}
        <div className="px-6 py-5 space-y-4 max-h-[60vh] overflow-auto">
          {/* Step 0: Source */}
          {activeStep === 0 && (
            <>
              <div>
                <label className="text-xs font-medium text-muted-foreground mb-1.5 block">Git Repository URL</label>
                <input value={form.gitUrl} onChange={e => setForm(f => ({ ...f, gitUrl: e.target.value }))}
                  placeholder="https://github.com/user/repo.git (leave empty for git pull)"
                  className="w-full rounded-lg border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none font-mono focus:border-blue-500/50 transition-colors" />
                <p className="mt-1 text-[10px] text-muted-foreground">Leave empty to pull latest from existing repository</p>
              </div>
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="text-xs font-medium text-muted-foreground mb-1.5 block">Branch</label>
                  <div className="relative">
                    <GitBranch size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground" />
                    <input value={form.branch} onChange={e => setForm(f => ({ ...f, branch: e.target.value }))}
                      placeholder="main"
                      className="w-full rounded-lg border border-border bg-background pl-9 pr-3 py-2.5 text-sm text-foreground outline-none focus:border-blue-500/50 transition-colors" />
                  </div>
                </div>
                <div>
                  <label className="text-xs font-medium text-muted-foreground mb-1.5 block">Access Token</label>
                  <input type="password" value={form.gitToken} onChange={e => setForm(f => ({ ...f, gitToken: e.target.value }))}
                    placeholder="ghp_xxxx or glpat-xxxx"
                    className="w-full rounded-lg border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none font-mono focus:border-blue-500/50 transition-colors" />
                </div>
              </div>
              <div>
                <label className="text-xs font-medium text-muted-foreground mb-1.5 block">SSH Key Path (for private repos)</label>
                <input value={form.sshKey} onChange={e => setForm(f => ({ ...f, sshKey: e.target.value }))}
                  placeholder="/root/.ssh/deploy_key"
                  className="w-full rounded-lg border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none font-mono focus:border-blue-500/50 transition-colors" />
              </div>
            </>
          )}

          {/* Step 1: Build */}
          {activeStep === 1 && (
            <>
              <div>
                <label className="text-xs font-medium text-muted-foreground mb-1.5 block">Build Command</label>
                <input value={form.buildCmd} onChange={e => setForm(f => ({ ...f, buildCmd: e.target.value }))}
                  placeholder="npm install && npm run build (auto-detected if empty)"
                  className="w-full rounded-lg border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none font-mono focus:border-blue-500/50 transition-colors" />
                <p className="mt-1 text-[10px] text-muted-foreground">Leave empty for auto-detection based on package.json / requirements.txt / Gemfile</p>
              </div>
              <div>
                <label className="text-xs font-medium text-muted-foreground mb-1.5 block">Dockerfile (optional)</label>
                <input value={form.dockerfile} onChange={e => setForm(f => ({ ...f, dockerfile: e.target.value }))}
                  placeholder="Dockerfile"
                  className="w-full rounded-lg border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none font-mono focus:border-blue-500/50 transition-colors" />
                <p className="mt-1 text-[10px] text-muted-foreground">If set, UWAS will build and run a Docker container instead of bare-metal process</p>
              </div>

              {/* Visual deploy pipeline */}
              <div className="mt-4 rounded-lg bg-background/50 border border-border/50 p-4">
                <p className="text-[10px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Deploy Pipeline</p>
                <div className="flex items-center justify-between">
                  <div className="flex flex-col items-center gap-1.5">
                    <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-slate-500/15">
                      <GitBranch size={16} className="text-slate-400" />
                    </div>
                    <span className="text-[10px] text-muted-foreground">Clone</span>
                  </div>
                  <div className="h-px flex-1 mx-3 bg-gradient-to-r from-slate-500/30 via-blue-500/30 to-blue-500/30" />
                  <div className="flex flex-col items-center gap-1.5">
                    <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-blue-500/15">
                      <Zap size={16} className="text-blue-400" />
                    </div>
                    <span className="text-[10px] text-muted-foreground">Build</span>
                  </div>
                  <div className="h-px flex-1 mx-3 bg-gradient-to-r from-blue-500/30 via-purple-500/30 to-purple-500/30" />
                  <div className="flex flex-col items-center gap-1.5">
                    <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-purple-500/15">
                      <Container size={16} className="text-purple-400" />
                    </div>
                    <span className="text-[10px] text-muted-foreground">{form.dockerfile ? 'Container' : 'Process'}</span>
                  </div>
                  <div className="h-px flex-1 mx-3 bg-gradient-to-r from-purple-500/30 via-emerald-500/30 to-emerald-500/30" />
                  <div className="flex flex-col items-center gap-1.5">
                    <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-emerald-500/15">
                      <CheckCircle size={16} className="text-emerald-400" />
                    </div>
                    <span className="text-[10px] text-muted-foreground">Live</span>
                  </div>
                </div>
              </div>
            </>
          )}

          {/* Step 2: Deploy (live output) */}
          {activeStep === 2 && (
            <>
              {deployStatus && (
                <div className="space-y-3">
                  {/* Status banner */}
                  <div className={`flex items-center gap-2.5 rounded-lg px-4 py-3 ${
                    deployStatus.status === 'running' ? 'bg-emerald-500/10 border border-emerald-500/20' :
                    deployStatus.status === 'failed' ? 'bg-red-500/10 border border-red-500/20' :
                    'bg-blue-500/10 border border-blue-500/20'
                  }`}>
                    {(deployStatus.status === 'deploying' || deployStatus.status === 'building') ? (
                      <RefreshCw size={14} className="animate-spin text-blue-400" />
                    ) : deployStatus.status === 'running' ? (
                      <CheckCircle size={14} className="text-emerald-400" />
                    ) : (
                      <AlertCircle size={14} className="text-red-400" />
                    )}
                    <span className="text-xs font-medium text-foreground capitalize">{deployStatus.status}</span>
                    {deployStatus.commit_sha && (
                      <span className="flex items-center gap-1 text-[10px] text-muted-foreground font-mono">
                        <GitCommit size={10} /> {deployStatus.commit_sha.slice(0, 7)}
                      </span>
                    )}
                    {deployStatus.duration && (
                      <span className="flex items-center gap-1 text-[10px] text-muted-foreground">
                        <Clock size={10} /> {deployStatus.duration}
                      </span>
                    )}
                  </div>

                  {/* Live build log */}
                  <div className="rounded-lg bg-[#0a0e14] border border-border/50 overflow-hidden">
                    <div className="flex items-center justify-between px-4 py-2 bg-[#0d1117] border-b border-border/30">
                      <span className="text-[10px] font-medium text-muted-foreground flex items-center gap-1.5">
                        <Terminal size={10} /> Build Output
                      </span>
                      {(deployStatus.status === 'deploying' || deployStatus.status === 'building') && (
                        <span className="flex items-center gap-1.5 text-[10px] text-blue-400">
                          <Circle size={6} className="fill-blue-400 animate-pulse" /> streaming
                        </span>
                      )}
                    </div>
                    <pre className="p-4 text-[11px] text-green-400 font-mono whitespace-pre-wrap leading-5 max-h-64 overflow-auto">
                      {deployStatus.log || 'Waiting for build output...'}
                      <div ref={logEndRef} />
                    </pre>
                  </div>

                  {deployStatus.error && (
                    <div className="flex items-start gap-2 rounded-lg bg-red-500/10 border border-red-500/20 px-4 py-3">
                      <AlertCircle size={14} className="text-red-400 mt-0.5 shrink-0" />
                      <p className="text-xs text-red-400">{deployStatus.error}</p>
                    </div>
                  )}
                </div>
              )}

              {!deployStatus && (
                <div className="text-center py-8">
                  <Rocket size={32} className="mx-auto mb-3 text-muted-foreground" />
                  <p className="text-sm text-muted-foreground">Ready to deploy</p>
                </div>
              )}
            </>
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-between border-t border-border px-6 py-4">
          <div className="flex items-center gap-2 text-[10px] text-muted-foreground">
            <GitBranch size={10} />
            <span>Webhook:</span>
            <WebhookCopy url={`/api/v1/apps/${domain}/webhook`} />
          </div>
          <div className="flex gap-2">
            {canBack && (
              <button onClick={() => setStep(s => s - 1)}
                className="rounded-lg border border-border px-4 py-2 text-sm text-card-foreground hover:bg-accent transition-colors">
                Back
              </button>
            )}
            {activeStep < 2 && !deploying && (
              <button onClick={() => canNext ? (activeStep === 1 ? onDeploy(form) : setStep(s => s + 1)) : undefined}
                className={`flex items-center gap-1.5 rounded-lg px-4 py-2 text-sm font-medium text-white transition-colors ${
                  activeStep === 1 ? 'bg-purple-600 hover:bg-purple-700' : 'bg-blue-600 hover:bg-blue-700'
                }`}>
                {activeStep === 1 ? <><Rocket size={14} /> Deploy</> : <>Next <ChevronRight size={14} /></>}
              </button>
            )}
            {deploying && (
              <button disabled className="flex items-center gap-1.5 rounded-lg bg-purple-600/50 px-4 py-2 text-sm font-medium text-white cursor-not-allowed">
                <RefreshCw size={14} className="animate-spin" /> Deploying...
              </button>
            )}
            {deployStatus && !deploying && (
              <button onClick={onClose}
                className="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 transition-colors">
                Done
              </button>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

/* ═══════════════════════════════════════════════════════════════════ */
/*  WebhookCopy — Copy webhook URL to clipboard                      */
/* ═══════════════════════════════════════════════════════════════════ */

function WebhookCopy({ url }: { url: string }) {
  const [copied, setCopied] = useState(false);
  const fullUrl = `${window.location.origin}${url}`;
  const copy = () => {
    navigator.clipboard.writeText(fullUrl).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  };
  return (
    <button onClick={copy} className="flex items-center gap-1 rounded bg-accent px-2 py-0.5 font-mono hover:bg-accent/80 transition-colors">
      <code className="text-[10px]">{url}</code>
      {copied ? <CheckCircle size={10} className="text-emerald-400" /> : <Copy size={10} />}
    </button>
  );
}

/* ═══════════════════════════════════════════════════════════════════ */
/*  AppCard — Single application card (Vercel-style)                 */
/* ═══════════════════════════════════════════════════════════════════ */

interface AppCardProps {
  app: AppInstance;
  acting: boolean;
  onAction: (action: 'start' | 'stop' | 'restart') => void;
  onDeploy: () => void;
  onSaveConfig: (env: Record<string, string>, command?: string, port?: number) => Promise<void>;
}

function AppCard({ app, acting, onAction, onDeploy, onSaveConfig }: AppCardProps) {
  const [expanded, setExpanded] = useState(false);
  const [activeTab, setActiveTab] = useState<'overview' | 'env' | 'logs'>('overview');
  const [envRows, setEnvRows] = useState<EnvRow[]>([]);
  const [editCmd, setEditCmd] = useState(app.command);
  const [editPort, setEditPort] = useState(String(app.port));
  const [appLog, setAppLog] = useState('');
  const [saving, setSaving] = useState(false);
  const [stats, setStats] = useState<AppStats | null>(null);
  const statsInterval = useRef<ReturnType<typeof setInterval> | null>(null);

  const rm = runtimeMeta[app.runtime] || runtimeMeta.custom;

  const loadStats = useCallback(() => {
    if (app.running) {
      fetchAppStats(app.domain).then(setStats).catch(() => {});
    }
  }, [app.domain, app.running]);

  const expand = () => {
    if (!expanded) {
      const rows = Object.entries(app.env || {}).map(([key, value]) => ({ key, value }));
      if (rows.length === 0) rows.push({ key: '', value: '' });
      setEnvRows(rows);
      setEditCmd(app.command);
      setEditPort(String(app.port));
      fetchAppLogs(app.domain).then(r => setAppLog(r?.log || '')).catch(() => setAppLog(''));
      loadStats();
    }
    setExpanded(!expanded);
  };

  // Poll stats every 5s while expanded on overview tab
  useEffect(() => {
    if (expanded && activeTab === 'overview' && app.running) {
      loadStats();
      statsInterval.current = setInterval(loadStats, 5000);
      return () => { if (statsInterval.current) clearInterval(statsInterval.current); };
    }
    return () => { if (statsInterval.current) clearInterval(statsInterval.current); };
  }, [expanded, activeTab, app.running, loadStats]);

  const save = async () => {
    setSaving(true);
    try {
      const env: Record<string, string> = {};
      envRows.forEach(r => { if (r.key.trim()) env[r.key.trim()] = r.value; });
      await onSaveConfig(env, editCmd || undefined, parseInt(editPort) || undefined);
    } finally { setSaving(false); }
  };

  return (
    <div className={`rounded-xl border bg-card transition-all duration-200 ${
      app.running ? 'border-emerald-500/20 shadow-lg shadow-emerald-500/5' : 'border-border'
    }`}>
      {/* Card header */}
      <div className="p-5">
        <div className="flex items-start justify-between">
          <div className="flex items-center gap-3.5">
            {/* Runtime icon */}
            <div className={`flex h-11 w-11 items-center justify-center rounded-xl ${rm.bg} font-bold text-sm ${rm.color}`}>
              {rm.icon}
            </div>
            <div>
              <div className="flex items-center gap-2.5">
                <h3 className="text-sm font-semibold text-foreground">{app.domain}</h3>
                <span className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-medium ${
                  app.running ? 'bg-emerald-500/15 text-emerald-400' : 'bg-red-500/10 text-red-400'
                }`}>
                  <Circle size={6} className={`fill-current ${app.running ? 'animate-pulse' : ''}`} />
                  {app.running ? 'Running' : 'Stopped'}
                </span>
              </div>
              <div className="mt-1 flex items-center gap-3 text-xs text-muted-foreground">
                <span className="flex items-center gap-1 font-mono">
                  <Terminal size={11} /> {app.command}
                </span>
              </div>
            </div>
          </div>

          {/* Actions */}
          <div className="flex items-center gap-1.5">
            <button onClick={onDeploy}
              className="flex items-center gap-1.5 rounded-lg bg-purple-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-purple-700 transition-colors shadow-sm">
              <Rocket size={12} /> Deploy
            </button>
            {!app.running ? (
              <button onClick={() => onAction('start')} disabled={acting}
                className="flex items-center gap-1 rounded-lg bg-emerald-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-emerald-700 transition-colors disabled:opacity-50">
                {acting ? <RefreshCw size={12} className="animate-spin" /> : <Play size={12} />} Start
              </button>
            ) : (
              <>
                <button onClick={() => onAction('restart')} disabled={acting}
                  className="flex items-center gap-1 rounded-lg bg-accent px-3 py-1.5 text-xs font-medium text-foreground hover:bg-accent/80 transition-colors disabled:opacity-50">
                  {acting ? <RefreshCw size={12} className="animate-spin" /> : <RefreshCw size={12} />} Restart
                </button>
                <button onClick={() => onAction('stop')} disabled={acting}
                  className="flex items-center gap-1 rounded-lg px-2.5 py-1.5 text-xs text-muted-foreground hover:text-red-400 hover:bg-red-500/10 transition-colors disabled:opacity-50">
                  <Square size={12} />
                </button>
              </>
            )}
          </div>
        </div>

        {/* Info bar */}
        <div className="mt-4 flex items-center justify-between">
          <div className="flex items-center gap-4 text-xs text-muted-foreground">
            <span className="flex items-center gap-1.5 rounded-md bg-accent/50 px-2 py-1">
              <Hash size={11} /> Port {app.port}
            </span>
            {app.pid > 0 && (
              <span className="flex items-center gap-1.5">
                <Cpu size={11} /> PID {app.pid}
              </span>
            )}
            {app.uptime && (
              <span className="flex items-center gap-1.5">
                <Clock size={11} /> {app.uptime}
              </span>
            )}
            {app.started_at && (
              <span className="flex items-center gap-1.5">
                <Circle size={6} className="fill-current text-emerald-400" /> Started {timeAgo(app.started_at)}
              </span>
            )}
          </div>
          <button onClick={expand}
            className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors">
            {expanded ? 'Collapse' : 'Details'}
            <ChevronRight size={12} className={`transition-transform ${expanded ? 'rotate-90' : ''}`} />
          </button>
        </div>
      </div>

      {/* Expanded detail panel */}
      {expanded && (
        <div className="border-t border-border">
          {/* Tabs */}
          <div className="flex border-b border-border">
            {([
              { key: 'overview' as const, label: 'Overview', icon: <Globe size={12} /> },
              { key: 'env' as const, label: 'Environment', icon: <Settings size={12} /> },
              { key: 'logs' as const, label: 'Logs', icon: <FileText size={12} /> },
            ]).map(tab => (
              <button key={tab.key}
                onClick={() => {
                  setActiveTab(tab.key);
                  if (tab.key === 'logs') fetchAppLogs(app.domain).then(r => setAppLog(r?.log || '')).catch(() => setAppLog(''));
                }}
                className={`flex items-center gap-1.5 px-4 py-3 text-xs font-medium transition-colors border-b-2 ${
                  activeTab === tab.key
                    ? 'border-blue-500 text-foreground'
                    : 'border-transparent text-muted-foreground hover:text-foreground'
                }`}>
                {tab.icon} {tab.label}
              </button>
            ))}
          </div>

          <div className="p-5">
            {/* Overview tab */}
            {activeTab === 'overview' && (
              <div className="space-y-4">
                <div>
                  <p className="text-[10px] font-medium text-muted-foreground uppercase tracking-wider mb-2">Request Routing</p>
                  <RoutingDiagram domain={app.domain} port={app.port} running={app.running} />
                </div>
                {/* Resource monitoring */}
                {app.running && (
                  <div>
                    <p className="text-[10px] font-medium text-muted-foreground uppercase tracking-wider mb-2">Resource Usage</p>
                    <div className="grid grid-cols-3 gap-3">
                      <ResourceGauge
                        label="CPU"
                        value={stats?.cpu_percent ?? 0}
                        unit="%"
                        max={100}
                        color="blue"
                      />
                      <ResourceGauge
                        label="Memory (RSS)"
                        value={stats ? stats.memory_rss / (1024 * 1024) : 0}
                        unit="MB"
                        max={stats && stats.memory_vms > 0 ? stats.memory_vms / (1024 * 1024) : 512}
                        color="purple"
                      />
                      <ResourceGauge
                        label="Virtual Memory"
                        value={stats ? stats.memory_vms / (1024 * 1024) : 0}
                        unit="MB"
                        max={stats && stats.memory_vms > 0 ? stats.memory_vms / (1024 * 1024) * 1.2 : 1024}
                        color="cyan"
                      />
                    </div>
                  </div>
                )}

                <div className="grid grid-cols-2 gap-4">
                  <div>
                    <p className="text-[10px] font-medium text-muted-foreground uppercase tracking-wider mb-2">Configuration</p>
                    <div className="space-y-2 text-xs">
                      <div className="flex justify-between rounded-md bg-background/50 px-3 py-2">
                        <span className="text-muted-foreground">Runtime</span>
                        <span className={`font-medium ${rm.color}`}>{app.runtime}</span>
                      </div>
                      <div className="flex justify-between rounded-md bg-background/50 px-3 py-2">
                        <span className="text-muted-foreground">Port</span>
                        <span className="font-mono font-medium text-foreground">{app.port}</span>
                      </div>
                      <div className="flex justify-between rounded-md bg-background/50 px-3 py-2">
                        <span className="text-muted-foreground">Command</span>
                        <span className="font-mono text-foreground truncate max-w-[200px]">{app.command}</span>
                      </div>
                    </div>
                  </div>
                  <div>
                    <p className="text-[10px] font-medium text-muted-foreground uppercase tracking-wider mb-2">Webhook (Auto-Deploy)</p>
                    <div className="space-y-2">
                      <div className="rounded-lg bg-background/50 border border-border/50 p-3">
                        <p className="text-[10px] text-muted-foreground mb-2">Add this URL to your Git provider:</p>
                        <WebhookCopy url={`/api/v1/apps/${app.domain}/webhook`} />
                        <p className="mt-2 text-[10px] text-muted-foreground">
                          Triggers deploy on push to configured branch
                        </p>
                      </div>
                    </div>
                  </div>
                </div>
              </div>
            )}

            {/* ENV tab */}
            {activeTab === 'env' && (
              <div className="space-y-4">
                <div className="grid grid-cols-2 gap-4">
                  <div>
                    <label className="text-xs font-medium text-muted-foreground mb-1.5 block">Start Command</label>
                    <input value={editCmd} onChange={e => setEditCmd(e.target.value)}
                      className="w-full rounded-lg border border-border bg-background px-3 py-2 text-sm font-mono text-foreground outline-none focus:border-blue-500/50 transition-colors" />
                  </div>
                  <div>
                    <label className="text-xs font-medium text-muted-foreground mb-1.5 block">Port</label>
                    <input type="number" value={editPort} onChange={e => setEditPort(e.target.value)}
                      className="w-full rounded-lg border border-border bg-background px-3 py-2 text-sm text-foreground outline-none focus:border-blue-500/50 transition-colors" />
                  </div>
                </div>

                <div>
                  <label className="text-xs font-medium text-muted-foreground mb-2 block">Environment Variables</label>
                  <EnvEditor rows={envRows} onChange={setEnvRows} />
                </div>

                <div className="flex items-center justify-end gap-2 pt-2 border-t border-border">
                  <button onClick={save} disabled={saving}
                    className="flex items-center gap-1.5 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 transition-colors disabled:opacity-50">
                    {saving ? <RefreshCw size={12} className="animate-spin" /> : <Save size={12} />} Save Changes
                  </button>
                  <button onClick={() => onAction('restart')} disabled={acting}
                    className="flex items-center gap-1.5 rounded-lg bg-emerald-600 px-4 py-2 text-sm font-medium text-white hover:bg-emerald-700 transition-colors disabled:opacity-50">
                    <RefreshCw size={12} /> Restart to Apply
                  </button>
                </div>
              </div>
            )}

            {/* Logs tab */}
            {activeTab === 'logs' && (
              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <span className="text-[10px] font-medium text-muted-foreground uppercase tracking-wider">Application Logs</span>
                  <button onClick={() => fetchAppLogs(app.domain).then(r => setAppLog(r?.log || '')).catch(() => setAppLog(''))}
                    className="flex items-center gap-1 text-[10px] text-blue-400 hover:text-blue-300 transition-colors">
                    <RefreshCw size={10} /> Refresh
                  </button>
                </div>
                <div className="rounded-lg bg-[#0a0e14] border border-border/50 overflow-hidden">
                  <pre className="p-4 text-[11px] text-green-400 font-mono whitespace-pre-wrap leading-5 max-h-80 overflow-auto">
                    {appLog || 'No logs available'}
                  </pre>
                </div>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

/* ═══════════════════════════════════════════════════════════════════ */
/*  Main Page                                                         */
/* ═══════════════════════════════════════════════════════════════════ */

export default function Apps() {
  const [apps, setApps] = useState<AppInstance[]>([]);
  const [loading, setLoading] = useState(true);
  const [acting, setActing] = useState<string | null>(null);
  const [status, setStatus] = useState<{ ok: boolean; msg: string } | null>(null);

  // Deploy wizard state
  const [wizardDomain, setWizardDomain] = useState('');
  const [deploying, setDeploying] = useState(false);
  const [deployStatusData, setDeployStatusData] = useState<DeployStatus | null>(null);

  const load = useCallback(async () => {
    try {
      const data = await fetchApps();
      setApps(data ?? []);
    } catch { /* ignore */ } finally { setLoading(false); }
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
      showStatus(true, `${domain}: ${action}ed successfully`);
      await load();
    } catch (e) { showStatus(false, (e as Error).message); }
    finally { setActing(null); }
  };

  const handleSaveConfig = async (domain: string, env: Record<string, string>, command?: string, port?: number) => {
    await updateAppEnv(domain, env, command, port);
    showStatus(true, `Config saved for ${domain}. Restart to apply.`);
    await load();
  };

  const handleDeploy = async (form: { gitUrl: string; branch: string; buildCmd: string; dockerfile: string; sshKey: string; gitToken: string }) => {
    if (!wizardDomain) return;
    setDeploying(true);
    setDeployStatusData(null);
    try {
      await deployApp(wizardDomain, {
        git_url: form.gitUrl || undefined,
        git_branch: form.branch || 'main',
        build_cmd: form.buildCmd || undefined,
        dockerfile: form.dockerfile || undefined,
        ssh_key_path: form.sshKey || undefined,
        git_token: form.gitToken || undefined,
      });
      // Poll for status
      const poll = setInterval(async () => {
        try {
          const st = await fetchDeployStatus(wizardDomain);
          setDeployStatusData(st);
          if (st.status === 'running' || st.status === 'failed') {
            clearInterval(poll);
            setDeploying(false);
            await load();
            if (st.status === 'running') showStatus(true, `Deploy complete: ${wizardDomain}`);
            else showStatus(false, `Deploy failed: ${st.error}`);
          }
        } catch { clearInterval(poll); setDeploying(false); }
      }, 2000);
    } catch (e) {
      showStatus(false, (e as Error).message);
      setDeploying(false);
    }
  };

  const runningCount = apps.filter(a => a.running).length;
  const stoppedCount = apps.length - runningCount;

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">Applications</h1>
          <p className="text-sm text-muted-foreground">Deploy and manage application processes</p>
        </div>
        <div className="flex items-center gap-3">
          {apps.length > 0 && (
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <span className="flex items-center gap-1.5 rounded-full bg-emerald-500/10 px-2.5 py-1 text-emerald-400">
                <Circle size={6} className="fill-current" /> {runningCount} running
              </span>
              {stoppedCount > 0 && (
                <span className="flex items-center gap-1.5 rounded-full bg-red-500/10 px-2.5 py-1 text-red-400">
                  <Circle size={6} className="fill-current" /> {stoppedCount} stopped
                </span>
              )}
            </div>
          )}
          <button onClick={() => { setLoading(true); load(); }}
            className="flex items-center gap-1.5 rounded-lg border border-border bg-card px-3 py-2 text-sm text-card-foreground hover:bg-accent transition-colors">
            <RefreshCw size={14} className={loading ? 'animate-spin' : ''} /> Refresh
          </button>
        </div>
      </div>

      {/* Status toast */}
      {status && (
        <div className={`flex items-center gap-2.5 rounded-lg px-4 py-3 text-sm transition-all ${
          status.ok ? 'bg-emerald-500/10 border border-emerald-500/20 text-emerald-400' : 'bg-red-500/10 border border-red-500/20 text-red-400'
        }`}>
          {status.ok ? <CheckCircle size={14} /> : <AlertCircle size={14} />}
          {status.msg}
        </div>
      )}

      {/* Empty state */}
      {apps.length === 0 && !loading && (
        <div className="rounded-xl border border-dashed border-border bg-card p-12 text-center">
          <div className="mx-auto mb-4 flex h-16 w-16 items-center justify-center rounded-2xl bg-purple-500/10">
            <Rocket size={28} className="text-purple-400" />
          </div>
          <h3 className="text-sm font-semibold text-foreground">No applications yet</h3>
          <p className="mt-1.5 text-xs text-muted-foreground max-w-sm mx-auto">
            Add a domain with <code className="rounded bg-accent px-1.5 py-0.5 text-[10px]">type: app</code> in your
            config to get started. UWAS supports Node.js, Python, Ruby, and Go applications.
          </p>
          <div className="mt-6 flex items-center justify-center gap-6 text-muted-foreground">
            <div className="flex flex-col items-center gap-1.5">
              <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-green-500/10">
                <span className="text-xs font-bold text-green-400">N</span>
              </div>
              <span className="text-[10px]">Node.js</span>
            </div>
            <div className="flex flex-col items-center gap-1.5">
              <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-yellow-500/10">
                <span className="text-xs font-bold text-yellow-400">Py</span>
              </div>
              <span className="text-[10px]">Python</span>
            </div>
            <div className="flex flex-col items-center gap-1.5">
              <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-red-500/10">
                <span className="text-xs font-bold text-red-400">Rb</span>
              </div>
              <span className="text-[10px]">Ruby</span>
            </div>
            <div className="flex flex-col items-center gap-1.5">
              <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-cyan-500/10">
                <span className="text-xs font-bold text-cyan-400">Go</span>
              </div>
              <span className="text-[10px]">Go</span>
            </div>
          </div>
        </div>
      )}

      {/* Loading skeleton */}
      {loading && apps.length === 0 && (
        <div className="space-y-4">
          {[1, 2].map(i => (
            <div key={i} className="rounded-xl border border-border bg-card p-5 animate-pulse">
              <div className="flex items-center gap-3.5">
                <div className="h-11 w-11 rounded-xl bg-accent" />
                <div className="space-y-2">
                  <div className="h-4 w-48 rounded bg-accent" />
                  <div className="h-3 w-32 rounded bg-accent" />
                </div>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* App cards */}
      <div className="space-y-4">
        {apps.map(app => (
          <AppCard
            key={app.domain}
            app={app}
            acting={acting === app.domain}
            onAction={action => handleAction(app.domain, action)}
            onDeploy={() => { setWizardDomain(app.domain); setDeployStatusData(null); }}
            onSaveConfig={(env, cmd, port) => handleSaveConfig(app.domain, env, cmd, port)}
          />
        ))}
      </div>

      {/* Deploy wizard modal */}
      {wizardDomain && (
        <DeployWizard
          domain={wizardDomain}
          deploying={deploying}
          deployStatus={deployStatusData}
          onDeploy={handleDeploy}
          onClose={() => { if (!deploying) { setWizardDomain(''); setDeployStatusData(null); } }}
        />
      )}
    </div>
  );
}
