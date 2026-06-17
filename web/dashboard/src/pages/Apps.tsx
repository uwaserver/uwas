import { useState, useCallback, useEffect, useMemo } from 'react';
import {
  Play, Square, RefreshCw, Plus, Trash2, FileText, Edit2, X,
  Container, Cpu, AlertCircle, CheckCircle, Circle, ArrowRight,
  GitBranch, Activity, Key, Copy,
} from 'lucide-react';
import { usePolling } from '@/hooks/usePolling';
import { useConfirm } from '@/components/useConfirm';
import {
  fetchApps,
  fetchApp,
  createApp,
  updateApp,
  deleteApp,
  startApp,
  stopApp,
  restartApp,
  fetchAppLogs,
  deployApp,
  fetchAppStats,
  fetchAppDeployPreflight,
  fetchAppDeployHistory,
  generateAppDeployKey,
  type App,
  type AppInstance,
  type AppRuntime,
  type AppDeployResult,
  type AppDeployPreflight,
  type AppDeployHistoryEntry,
  type AppDeployKeyResult,
  type AppStats,
} from '@/lib/api';
import { addDebugLog, formatDebugDetail } from '@/lib/debugLog';

// Apps dashboard: list apps, create runnable workdirs, manage lifecycle,
// tail logs, deploy from git, and show lightweight runtime stats.

const runtimeLabel: Record<AppRuntime, string> = {
  node: 'Node.js',
  python: 'Python',
  ruby: 'Ruby',
  go: 'Go',
  custom: 'Custom',
  docker: 'Docker',
};

const runtimeColor: Record<AppRuntime, string> = {
  node: 'bg-green-500/15 text-green-400 border-green-500/30',
  python: 'bg-yellow-500/15 text-yellow-400 border-yellow-500/30',
  ruby: 'bg-red-500/15 text-red-400 border-red-500/30',
  go: 'bg-cyan-500/15 text-cyan-400 border-cyan-500/30',
  docker: 'bg-blue-500/15 text-blue-400 border-blue-500/30',
  custom: 'bg-slate-500/15 text-slate-400 border-slate-500/30',
};

const runtimeOptions: AppRuntime[] = ['node', 'python', 'ruby', 'go', 'docker', 'custom'];

interface CreateForm {
  sourceMode: 'blank' | 'git';
  name: string;
  description: string;
  runtime: AppRuntime;
  command: string;
  work_dir: string;
  port: string;
  envText: string;
  git_url: string;
  git_branch: string;
  build_cmd: string;
  health_path: string;
  ssh_key_path: string;
  git_token: string;
  // docker
  docker_image: string;
  docker_container_port: string;
  docker_build_context: string;
  docker_build_dockerfile: string;
}

const blankForm: CreateForm = {
  sourceMode: 'blank',
  name: '',
  description: '',
  runtime: 'node',
  command: '',
  work_dir: '',
  port: '',
  envText: '',
  git_url: '',
  git_branch: '',
  build_cmd: '',
  health_path: '',
  ssh_key_path: '',
  git_token: '',
  docker_image: '',
  docker_container_port: '',
  docker_build_context: '',
  docker_build_dockerfile: '',
};

// envTextToMap and envMapToText keep the form's textarea-based env
// editor symmetrical: KEY=value lines round-trip with the on-disk
// map. Empty or comment lines are skipped on parse.
function envTextToMap(t: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const raw of t.split(/\r?\n/)) {
    const line = raw.trim();
    if (!line || line.startsWith('#')) continue;
    const eq = line.indexOf('=');
    if (eq <= 0) continue;
    const k = line.slice(0, eq).trim();
    const v = line.slice(eq + 1).trim();
    if (k) out[k] = v;
  }
  return out;
}
function envMapToText(m: Record<string, string> | undefined): string {
  if (!m) return '';
  return Object.entries(m).map(([k, v]) => `${k}=${v}`).join('\n');
}

// formatBytes renders an int byte count as a short human-readable
// string. Used for the inline stats display so a 524288000-byte RSS
// shows as "500 MB" instead of a wall of digits.
function formatBytes(n: number): string {
  if (n <= 0) return '0';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

export default function Apps() {
  const { confirmAction } = useConfirm();
  const [apps, setApps] = useState<AppInstance[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [status, setStatus] = useState<{ ok: boolean; message: string } | null>(null);
  const [busyName, setBusyName] = useState<string | null>(null);

  // Create / edit modal state.
  const [editing, setEditing] = useState<{ mode: 'create' | 'edit'; name?: string } | null>(null);
  const [form, setForm] = useState<CreateForm>(blankForm);
  const [submitting, setSubmitting] = useState(false);

  // Logs modal state.
  const [logsFor, setLogsFor] = useState<string | null>(null);
  const [logsContent, setLogsContent] = useState('');
  const [logsKind, setLogsKind] = useState<'runtime' | 'build'>('runtime');

  // Deploy-from-git modal state. The webhook secret + branch_filter
  // live alongside the per-deploy fields because operators usually
  // set up auto-deploy as part of their first manual deploy.
  const [deployFor, setDeployFor] = useState<string | null>(null);
  const [deployForm, setDeployForm] = useState({
    git_url: '',
    git_branch: '',
    build_cmd: '',
    health_path: '',
    ssh_key_path: '',
    git_token: '',
    webhook_secret: '',
    branch_filter: '',
  });
  const [deployRunning, setDeployRunning] = useState(false);
  const [deployResult, setDeployResult] = useState<AppDeployResult | null>(null);
  const [deployPreflight, setDeployPreflight] = useState<AppDeployPreflight | null>(null);
  const [deployHistory, setDeployHistory] = useState<AppDeployHistoryEntry[]>([]);
  const [deployKeyResult, setDeployKeyResult] = useState<AppDeployKeyResult | null>(null);
  const [deployKeyGenerating, setDeployKeyGenerating] = useState(false);
  const [deployMetaLoading, setDeployMetaLoading] = useState(false);

  // Per-app stats cache. Keyed by name; only running apps with the
  // stats panel toggled open are kept in here. Polling each running
  // app independently would multiply load on the docker daemon, so
  // stats are fetched on-demand when the operator clicks the chip.
  const [statsByName, setStatsByName] = useState<Record<string, AppStats | undefined>>({});
  const [statsLoadingFor, setStatsLoadingFor] = useState<string | null>(null);

  // Inline post-create banner with start outcome + log shortcut. Sits
  // below the toast so an operator who created a "saved but didn't
  // start" app can click straight through to the logs without
  // hunting for the card.
  const [createOutcome, setCreateOutcome] = useState<{
    name: string;
    started: boolean;
    error?: string;
  } | null>(null);

  const load = useCallback(async () => {
    try {
      const data = await fetchApps();
      setApps(data ?? []);
      setError('');
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);

  usePolling(load, 8_000);

  useEffect(() => {
    if (status?.ok) {
      const id = window.setTimeout(() => setStatus(s => s === status ? null : s), 4000);
      return () => window.clearTimeout(id);
    }
  }, [status]);

  const isDocker = form.runtime === 'docker';
  const createFromGit = editing?.mode === 'create' && form.sourceMode === 'git';
  const showGitSettings = createFromGit || editing?.mode === 'edit';

  const sortedApps = useMemo(
    () => [...apps].sort((a, b) => a.name.localeCompare(b.name)),
    [apps],
  );
  const deployTargetApp = useMemo(
    () => apps.find(app => app.name === deployFor),
    [apps, deployFor],
  );
  const deployTargetIsDocker = deployTargetApp?.runtime === 'docker';
  const deployUsesSSHKeyWithHTTPS =
    Boolean(deployForm.ssh_key_path.trim()) &&
    !deployForm.git_token.trim() &&
    deployForm.git_url.trim().toLowerCase().startsWith('https://');

  const setStatusErr = (e: unknown) =>
    setStatus({ ok: false, message: e instanceof Error ? e.message : String(e) });

  // Build the request body from form, validating that required fields
  // are present. Returns null + sets status on error.
  const formToApp = (): Partial<App> | null => {
    if (!form.name.trim()) {
      setStatus({ ok: false, message: 'Name is required' });
      return null;
    }
    const env = form.envText ? envTextToMap(form.envText) : undefined;
    const portNum = form.port ? parseInt(form.port, 10) : 0;
    if (form.port && (isNaN(portNum) || portNum < 0 || portNum > 65535)) {
      setStatus({ ok: false, message: 'Port must be 0–65535 (0 = auto-assign)' });
      return null;
    }

    const body: Partial<App> = {
      name: form.name.trim(),
      description: form.description.trim() || undefined,
      runtime: form.runtime,
      work_dir: form.work_dir.trim() || undefined,
      port: portNum || undefined,
      env,
    };
    if (showGitSettings) {
      if (!form.git_url.trim()) {
        if (createFromGit) {
          setStatus({ ok: false, message: 'Git URL is required' });
          return null;
        }
      } else {
        body.deploy = {
          git_url: form.git_url.trim(),
          git_branch: form.git_branch.trim() || undefined,
          build_cmd: form.build_cmd.trim() || undefined,
          health_path: form.health_path.trim() || undefined,
          ssh_key_path: form.ssh_key_path.trim() || undefined,
          git_token: form.git_token.trim() || undefined,
        };
      }
    }

    if (isDocker) {
      const cport = parseInt(form.docker_container_port, 10);
      if (isNaN(cport) || cport <= 0) {
        setStatus({ ok: false, message: 'Docker container_port is required (1–65535)' });
        return null;
      }
      body.docker = {
        image: form.docker_image.trim() || undefined,
        container_port: cport,
      };
      const dockerBuildContext = form.docker_build_context.trim() || (createFromGit ? '.' : '');
      if (dockerBuildContext) {
        body.docker.build = {
          context: dockerBuildContext,
          dockerfile: form.docker_build_dockerfile.trim() || undefined,
        };
      }
      if (!body.docker.image && !body.docker.build?.context) {
        setStatus({ ok: false, message: 'Docker apps need either an image or a build context' });
        return null;
      }
    } else {
      body.command = createFromGit ? undefined : form.command.trim() || undefined;
    }
    return body;
  };

  const openCreate = () => {
    setForm(blankForm);
    setEditing({ mode: 'create' });
  };

  const closeEditor = () => {
    setEditing(null);
    setForm(blankForm);
  };

  const openEdit = async (name: string) => {
    try {
      const { app } = await fetchApp(name);
      setForm({
        sourceMode: 'blank',
        name: app.name,
        description: app.description ?? '',
        runtime: app.runtime,
        command: app.command ?? '',
        work_dir: app.work_dir ?? '',
        port: app.port ? String(app.port) : '',
        envText: envMapToText(app.env),
        git_url: app.deploy?.git_url ?? '',
        git_branch: app.deploy?.git_branch ?? '',
        build_cmd: app.deploy?.build_cmd ?? '',
        health_path: app.deploy?.health_path ?? '',
        ssh_key_path: app.deploy?.ssh_key_path ?? '',
        git_token: '',
        docker_image: app.docker?.image ?? '',
        docker_container_port: app.docker?.container_port ? String(app.docker.container_port) : '',
        docker_build_context: app.docker?.build?.context ?? '',
        docker_build_dockerfile: app.docker?.build?.dockerfile ?? '',
      });
      setEditing({ mode: 'edit', name });
    } catch (e) {
      setStatusErr(e);
    }
  };

  const submit = async () => {
    const body = formToApp();
    if (!body) return;
    setSubmitting(true);
    try {
      if (editing?.mode === 'edit' && editing.name) {
        // Update endpoint mirrors Create: it stops the app, applies
        // the patch, and tries to start. Surface a failed restart
        // OR a successful start that didn't bind to its port — both
        // are deploy-time problems the operator needs to see.
        const res = await updateApp(editing.name, body);
        if (!res.started && !res.start_error?.includes('disabled')) {
          setStatus({
            ok: false,
            message: `Updated "${editing.name}" but the restart failed — check logs`,
          });
          setCreateOutcome({
            name: editing.name,
            started: false,
            error: res.start_error,
          });
        } else if (res.started && res.listening === false) {
          setStatus({
            ok: false,
            message: `Updated "${editing.name}" — process is running but not listening on its port`,
          });
          setCreateOutcome({
            name: editing.name,
            started: true,
            error: res.listening_warning,
          });
        } else {
          setStatus({ ok: true, message: `Updated "${editing.name}"` });
        }
        setEditing(null);
      } else {
        // Create attempts auto-start AND a port-readiness probe.
        const res = await createApp(body, createFromGit ? { start: false } : undefined);
        const createdName = res.app.name || body.name!;
        const demoNote = res.scaffolded ? ' with demo files' : '';
        if (createFromGit) {
          const deploy = await deployApp(createdName, {
            git_url: form.git_url.trim(),
            git_branch: form.git_branch.trim() || undefined,
            build_cmd: isDocker ? undefined : form.build_cmd.trim() || undefined,
            health_path: form.health_path.trim() || undefined,
            ssh_key_path: form.ssh_key_path.trim() || undefined,
            git_token: form.git_token.trim() || undefined,
          });
          if (deploy.ok) {
            setStatus({
              ok: true,
              message: `Created "${createdName}" from Git${deploy.commit_sha ? ` @ ${deploy.commit_sha.slice(0, 7)}` : ''}`,
            });
            setCreateOutcome({ name: createdName, started: true });
          } else {
            setStatus({
              ok: false,
              message: `Created "${createdName}" but Git deploy failed`,
            });
            setCreateOutcome({
              name: createdName,
              started: false,
              error: deploy.error || deploy.log,
            });
          }
        } else if (!res.started) {
          setStatus({
            ok: false,
            message: `Created "${createdName}"${demoNote} but the start failed — check logs`,
          });
          setCreateOutcome({
            name: createdName,
            started: false,
            error: res.start_error,
          });
        } else if (res.listening === false) {
          setStatus({
            ok: false,
            message: `Created "${createdName}"${demoNote} — process started but is not listening on port ${res.app.port}`,
          });
          setCreateOutcome({
            name: createdName,
            started: true,
            error: res.listening_warning,
          });
        } else {
          setStatus({
            ok: true,
            message: `Created "${createdName}"${demoNote} — started and listening on port ${res.app.port}`,
          });
          setCreateOutcome({ name: createdName, started: true });
        }
        setEditing(null);
      }
      await load();
    } catch (e) {
      setStatusErr(e);
    } finally {
      setSubmitting(false);
    }
  };

  const openDeploy = async (name: string) => {
    // Pre-fill from the app's stored DeployConfig so a follow-up
    // deploy doesn't make the operator re-type git URL / branch /
    // build cmd. Webhook secret is also pre-filled so they can see
    // it (and rotate by typing a new value).
    setDeployFor(name);
    setDeployResult(null);
    setDeployPreflight(null);
    setDeployHistory([]);
    setDeployKeyResult(null);
    setDeployMetaLoading(true);
    try {
      const [{ app }, preflight, history] = await Promise.all([
        fetchApp(name),
        fetchAppDeployPreflight(name).catch(() => null),
        fetchAppDeployHistory(name).catch(() => ({ name, items: [] })),
      ]);
      setDeployForm({
        git_url: app.deploy?.git_url ?? '',
        git_branch: app.deploy?.git_branch ?? '',
        build_cmd: app.deploy?.build_cmd ?? '',
        health_path: app.deploy?.health_path ?? '',
        ssh_key_path: app.deploy?.ssh_key_path ?? '',
        git_token: '',
        webhook_secret: app.deploy?.webhook_secret ?? '',
        branch_filter: app.deploy?.branch_filter ?? '',
      });
      setDeployPreflight(preflight);
      setDeployHistory(history.items ?? []);
    } catch {
      // Fall back to empty form on fetch error.
      setDeployForm({ git_url: '', git_branch: '', build_cmd: '', health_path: '', ssh_key_path: '', git_token: '', webhook_secret: '', branch_filter: '' });
      setDeployHistory([]);
    } finally {
      setDeployMetaLoading(false);
    }
  };

  const generateDeployKey = async () => {
    if (!deployFor) return;
    setDeployKeyGenerating(true);
    setDeployKeyResult(null);
    try {
      const result = await generateAppDeployKey(deployFor);
      setDeployKeyResult(result);
      setDeployForm(f => ({ ...f, ssh_key_path: result.private_key_path }));
      setStatus({ ok: true, message: `Generated deploy key for ${deployFor}` });
      try {
        const preflight = await fetchAppDeployPreflight(deployFor);
        setDeployPreflight(preflight);
      } catch {
        // Key generation succeeded; preflight refresh is only informational.
      }
    } catch (e) {
      setStatusErr(e);
    } finally {
      setDeployKeyGenerating(false);
    }
  };

  // saveWebhookConfig persists the webhook_secret + branch_filter
  // fields to the app's DeployConfig via the PUT endpoint. Kept
  // separate from runDeploy because operators often want to
  // configure the hook BEFORE pushing code that would trigger a
  // deploy.
  const saveWebhookConfig = async () => {
    if (!deployFor) return;
    try {
      await updateApp(deployFor, {
        deploy: {
          git_url: deployForm.git_url.trim() || undefined,
          git_branch: deployForm.git_branch.trim() || undefined,
          build_cmd: deployForm.build_cmd.trim() || undefined,
          health_path: deployForm.health_path.trim() || undefined,
          ssh_key_path: deployForm.ssh_key_path.trim() || undefined,
          git_token: deployForm.git_token.trim() || undefined,
          webhook_secret: deployForm.webhook_secret.trim() || undefined,
          branch_filter: deployForm.branch_filter.trim() || undefined,
        },
      });
      setStatus({ ok: true, message: `Saved webhook config for ${deployFor}` });
    } catch (e) {
      setStatusErr(e);
    }
  };

  const runDeploy = async () => {
    if (!deployFor) return;
    if (!deployForm.git_url.trim()) {
      setStatus({ ok: false, message: 'Git URL is required' });
      return;
    }
    setDeployRunning(true);
    setDeployResult(null);
    addDebugLog({
      level: 'info',
      scope: 'deploy',
      message: `Deploy requested for ${deployFor}`,
      detail: formatDebugDetail({
        git_url: deployForm.git_url.trim(),
        git_branch: deployForm.git_branch.trim() || undefined,
        build_cmd: deployTargetIsDocker ? undefined : deployForm.build_cmd.trim() || undefined,
        health_path: deployForm.health_path.trim() || undefined,
        ssh_key_path: deployForm.ssh_key_path.trim() || undefined,
        has_git_token: Boolean(deployForm.git_token.trim()),
      }),
    });
    try {
      const r = await deployApp(deployFor, {
        git_url: deployForm.git_url.trim(),
        git_branch: deployForm.git_branch.trim() || undefined,
        build_cmd: deployTargetIsDocker ? undefined : deployForm.build_cmd.trim() || undefined,
        health_path: deployForm.health_path.trim() || undefined,
        ssh_key_path: deployForm.ssh_key_path.trim() || undefined,
        git_token: deployForm.git_token.trim() || undefined,
      });
      setDeployResult(r);
      if (r.ok) {
        addDebugLog({
          level: 'success',
          scope: 'deploy',
          message: `Deploy completed for ${deployFor}${r.commit_sha ? ` @ ${r.commit_sha.slice(0, 7)}` : ''}`,
          detail: r.log || undefined,
        });
        setStatus({
          ok: true,
          message: `Deployed ${deployFor}${r.commit_sha ? ` @ ${r.commit_sha.slice(0, 7)}` : ''}`,
        });
        try {
          const history = await fetchAppDeployHistory(deployFor);
          setDeployHistory(history.items ?? []);
        } catch {
          // Non-critical; deploy result already carries the authoritative outcome.
        }
        await load();
      } else {
        addDebugLog({
          level: 'error',
          scope: 'deploy',
          message: `Deploy failed for ${deployFor}: ${r.error || 'unknown error'}`,
          detail: r.log || undefined,
        });
        setStatus({
          ok: false,
          message: r.error || 'Deploy failed (see log in modal)',
        });
      }
    } catch (e) {
      addDebugLog({
        level: 'error',
        scope: 'deploy',
        message: `Deploy request crashed for ${deployFor}`,
        detail: e instanceof Error ? e.message : String(e),
      });
      setStatusErr(e);
    } finally {
      setDeployRunning(false);
    }
  };

  const doAction = async (name: string, action: 'start' | 'stop' | 'restart') => {
    setBusyName(name);
    try {
      const res = action === 'start'
        ? await startApp(name)
        : action === 'stop'
          ? await stopApp(name)
          : await restartApp(name);
      if (res.listening === false) {
        setStatus({
          ok: false,
          message: `${name}: ${action} completed but the app is not listening on its port`,
        });
        setCreateOutcome({
          name,
          started: true,
          error: res.listening_warning,
        });
      } else {
        setStatus({ ok: true, message: `${name}: ${action} ok` });
      }
      await load();
    } catch (e) {
      setStatusErr(e);
    } finally {
      setBusyName(null);
    }
  };

  const doDelete = async (name: string) => {
    const ok = await confirmAction({
      title: `Delete app "${name}"?`,
      message: 'The YAML and process will be removed; the workdir is left in place.',
      confirmLabel: 'Delete app',
      variant: 'danger',
    });
    if (!ok) return;
    setBusyName(name);
    try {
      await deleteApp(name);
      setStatus({ ok: true, message: `Deleted "${name}"` });
      await load();
    } catch (e) {
      setStatusErr(e);
    } finally {
      setBusyName(null);
    }
  };

  const loadStats = async (name: string) => {
    setStatsLoadingFor(name);
    try {
      const s = await fetchAppStats(name);
      setStatsByName(prev => ({ ...prev, [name]: s }));
    } catch (e) {
      setStatusErr(e);
    } finally {
      setStatsLoadingFor(null);
    }
  };

  const openLogs = async (name: string) => {
    setLogsFor(name);
    setLogsContent('Loading…');
    try {
      const r = await fetchAppLogs(name);
      setLogsContent(r.log || '(no log output yet)');
      setLogsKind(r.kind);
    } catch (e) {
      setLogsContent(`Failed to load log: ${(e as Error).message}`);
    }
  };

  if (loading) {
    return (
      <div className="flex h-96 items-center justify-center text-muted-foreground">
        Loading apps…
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <header className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Applications</h1>
          <p className="text-sm text-muted-foreground">
             apps under <code className="text-xs">/etc/uwas/apps.d/</code>.
            Domains reach them via reverse proxy with <code className="text-xs">apps://&lt;name&gt;</code> upstream.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={openCreate}
            className="text-xs rounded-md bg-primary text-primary-foreground px-3 py-1.5 inline-flex items-center gap-1.5 hover:bg-primary/90 transition-colors"
          >
            <Plus size={14} /> New app
          </button>
        </div>
      </header>

      {status && (
        <div
          className={`rounded-md border px-3 py-2 text-sm flex items-start gap-2 ${
            status.ok
              ? 'border-green-500/30 bg-green-500/10 text-green-400'
              : 'border-red-500/30 bg-red-500/10 text-red-400'
          }`}
        >
          {status.ok ? <CheckCircle size={16} className="mt-0.5" /> : <AlertCircle size={16} className="mt-0.5" />}
          <span className="flex-1">{status.message}</span>
          <button onClick={() => setStatus(null)} className="opacity-60 hover:opacity-100">
            <X size={14} />
          </button>
        </div>
      )}

      {error && (
        <div className="rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">
          {error}
        </div>
      )}

      {editing && (
        <section className="rounded-lg border border-border bg-card p-4 shadow-sm">
          <div className="mb-4 flex items-start justify-between gap-3">
            <div>
              <h2 className="text-base font-semibold">
                {editing.mode === 'edit' ? `Edit "${editing.name}"` : 'Create application'}
              </h2>
              <div className="mt-1 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                <span>{runtimeLabel[form.runtime]}</span>
                <span>port {form.port || 'auto'}</span>
                <span>{form.work_dir || `/var/lib/uwas/apps/${form.name || '<name>'}`}</span>
              </div>
            </div>
            <button
              onClick={closeEditor}
              className="rounded-md p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
              aria-label="Close app editor"
            >
              <X size={18} />
            </button>
          </div>

          <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_280px]">
            <div className="space-y-4">
              {editing.mode === 'create' && (
                <div className="grid gap-2 sm:grid-cols-2">
                  <button
                    type="button"
                    onClick={() => setForm(f => ({ ...f, sourceMode: 'blank' }))}
                    className={`flex items-start gap-3 rounded-md border p-3 text-left transition ${
                      form.sourceMode === 'blank'
                        ? 'border-primary/50 bg-primary/10 text-foreground'
                        : 'border-border bg-background text-muted-foreground hover:bg-accent hover:text-foreground'
                    }`}
                  >
                    <Plus size={16} className="mt-0.5" />
                    <span>
                      <span className="block text-sm font-medium">Blank app</span>
                      <span className="block text-xs">Create workdir and demo files</span>
                    </span>
                  </button>
                  <button
                    type="button"
                    onClick={() => setForm(f => ({
                      ...f,
                      sourceMode: 'git',
                      docker_container_port: f.runtime === 'docker' && !f.docker_container_port ? '80' : f.docker_container_port,
                      docker_build_context: f.runtime === 'docker' && !f.docker_build_context ? '.' : f.docker_build_context,
                    }))}
                    className={`flex items-start gap-3 rounded-md border p-3 text-left transition ${
                      form.sourceMode === 'git'
                        ? 'border-blue-500/50 bg-blue-500/10 text-blue-200'
                        : 'border-border bg-background text-muted-foreground hover:bg-accent hover:text-foreground'
                    }`}
                  >
                    <GitBranch size={16} className="mt-0.5" />
                    <span>
                      <span className="block text-sm font-medium">Git source</span>
                      <span className="block text-xs">Create app and deploy repo</span>
                    </span>
                  </button>
                </div>
              )}

              <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-6">
                {runtimeOptions.map(rt => (
                  <button
                    key={rt}
                    type="button"
                    onClick={() => setForm(f => ({
                      ...f,
                      runtime: rt,
                      docker_container_port: rt === 'docker' && !f.docker_container_port ? '80' : f.docker_container_port,
                      docker_build_context: rt === 'docker' && f.sourceMode === 'git' && !f.docker_build_context ? '.' : f.docker_build_context,
                    }))}
                    className={`flex min-h-16 flex-col items-center justify-center gap-1 rounded-md border px-2 py-2 text-xs transition ${
                      form.runtime === rt
                        ? runtimeColor[rt]
                        : 'border-border bg-background text-muted-foreground hover:bg-accent hover:text-foreground'
                    }`}
                  >
                    {rt === 'docker' ? <Container size={16} /> : <Cpu size={16} />}
                    {runtimeLabel[rt]}
                  </button>
                ))}
              </div>

              <div className="grid gap-3 md:grid-cols-2">
                <label className="space-y-1">
                  <span className="text-xs text-muted-foreground">Name</span>
                  <input
                    value={form.name}
                    onChange={e => setForm(f => ({ ...f, name: e.target.value }))}
                    disabled={editing.mode === 'edit'}
                    placeholder="my-api"
                    className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm font-mono disabled:opacity-60"
                  />
                </label>
                <label className="space-y-1">
                  <span className="text-xs text-muted-foreground">Port</span>
                  <input
                    value={form.port}
                    onChange={e => setForm(f => ({ ...f, port: e.target.value }))}
                    placeholder="auto"
                    className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm font-mono"
                  />
                </label>
                <label className="space-y-1 md:col-span-2">
                  <span className="text-xs text-muted-foreground">Description</span>
                  <input
                    value={form.description}
                    onChange={e => setForm(f => ({ ...f, description: e.target.value }))}
                    placeholder="Internal API"
                    className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm"
                  />
                </label>
              </div>

              {showGitSettings && (
                <div className="rounded-md border border-blue-500/30 bg-blue-500/10 p-3">
                  <div className="grid gap-3 md:grid-cols-2">
                    <label className="space-y-1 md:col-span-2">
                      <span className="text-xs text-blue-200">Git URL</span>
                      <input
                        value={form.git_url}
                        onChange={e => setForm(f => ({ ...f, git_url: e.target.value }))}
                        placeholder="https://github.com/user/repo.git"
                        className="w-full rounded-md border border-blue-500/30 bg-background px-3 py-2 text-sm font-mono"
                      />
                    </label>
                    <label className="space-y-1">
                      <span className="text-xs text-blue-200">Branch</span>
                      <input
                        value={form.git_branch}
                        onChange={e => setForm(f => ({ ...f, git_branch: e.target.value }))}
                        placeholder="main"
                        className="w-full rounded-md border border-blue-500/30 bg-background px-3 py-2 text-sm font-mono"
                      />
                    </label>
                    {!isDocker && (
                      <label className="space-y-1">
                        <span className="text-xs text-blue-200">Build command</span>
                        <input
                          value={form.build_cmd}
                          onChange={e => setForm(f => ({ ...f, build_cmd: e.target.value }))}
                          placeholder="auto"
                          className="w-full rounded-md border border-blue-500/30 bg-background px-3 py-2 text-sm font-mono"
                        />
                      </label>
                    )}
                    <label className="space-y-1">
                      <span className="text-xs text-blue-200">Health path</span>
                      <input
                        value={form.health_path}
                        onChange={e => setForm(f => ({ ...f, health_path: e.target.value }))}
                        placeholder="/health"
                        className="w-full rounded-md border border-blue-500/30 bg-background px-3 py-2 text-sm font-mono"
                      />
                    </label>
                    <label className="space-y-1">
                      <span className="text-xs text-blue-200">SSH key path</span>
                      <input
                        value={form.ssh_key_path}
                        onChange={e => setForm(f => ({ ...f, ssh_key_path: e.target.value }))}
                        placeholder="/home/uwas/.ssh/deploy_key"
                        className="w-full rounded-md border border-blue-500/30 bg-background px-3 py-2 text-sm font-mono"
                      />
                    </label>
                    <label className="space-y-1">
                      <span className="text-xs text-blue-200">HTTPS token</span>
                      <input
                        type="password"
                        value={form.git_token}
                        onChange={e => setForm(f => ({ ...f, git_token: e.target.value }))}
                        placeholder={editing?.mode === 'edit' ? 'leave blank to keep stored token' : 'token for private HTTPS repos'}
                        className="w-full rounded-md border border-blue-500/30 bg-background px-3 py-2 text-sm font-mono"
                      />
                    </label>
                  </div>
                  <p className="mt-2 text-[10px] text-blue-200/80">
                    Use an HTTPS token for private HTTPS repos, or an absolute SSH key path for git@ / ssh:// repos. Leave token empty while editing to keep the stored credential.
                  </p>
                </div>
              )}

              {!isDocker && (
                <div className="grid gap-3 md:grid-cols-2">
                  <label className={`space-y-1 ${createFromGit ? 'opacity-50' : ''}`}>
                    <span className="text-xs text-muted-foreground">Start command</span>
                    <input
                      value={form.command}
                      onChange={e => setForm(f => ({ ...f, command: e.target.value }))}
                      disabled={createFromGit}
                      placeholder={form.runtime === 'node' ? 'node index.js' : ''}
                      className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm font-mono"
                    />
                  </label>
                  <label className="space-y-1">
                    <span className="text-xs text-muted-foreground">Work directory</span>
                    <input
                      value={form.work_dir}
                      onChange={e => setForm(f => ({ ...f, work_dir: e.target.value }))}
                      placeholder={`/var/lib/uwas/apps/${form.name || '<name>'}`}
                      className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm font-mono"
                    />
                  </label>
                </div>
              )}

              {isDocker && (
                <div className="grid gap-3 md:grid-cols-2">
                  <label className="space-y-1">
                    <span className="text-xs text-muted-foreground">Docker image</span>
                    <input
                      value={form.docker_image}
                      onChange={e => setForm(f => ({ ...f, docker_image: e.target.value }))}
                      placeholder={createFromGit ? 'optional: uwas-app/my-api:latest' : 'nginx:latest'}
                      className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm font-mono"
                    />
                  </label>
                  <label className="space-y-1">
                    <span className="text-xs text-muted-foreground">Container port</span>
                    <input
                      value={form.docker_container_port}
                      onChange={e => setForm(f => ({ ...f, docker_container_port: e.target.value }))}
                      placeholder="80"
                      className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm font-mono"
                    />
                  </label>
                  <label className="space-y-1">
                    <span className="text-xs text-muted-foreground">Build context</span>
                    <input
                      value={form.docker_build_context}
                      onChange={e => setForm(f => ({ ...f, docker_build_context: e.target.value }))}
                      placeholder={createFromGit ? '.' : '.'}
                      className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm font-mono"
                    />
                  </label>
                  <label className="space-y-1">
                    <span className="text-xs text-muted-foreground">Dockerfile</span>
                    <input
                      value={form.docker_build_dockerfile}
                      onChange={e => setForm(f => ({ ...f, docker_build_dockerfile: e.target.value }))}
                      placeholder="Dockerfile"
                      className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm font-mono"
                    />
                  </label>
                </div>
              )}
            </div>

            <div className="flex flex-col gap-3">
              <label className="flex min-h-48 flex-1 flex-col gap-1">
                <span className="text-xs text-muted-foreground">Environment</span>
                <textarea
                  value={form.envText}
                  onChange={e => setForm(f => ({ ...f, envText: e.target.value }))}
                  placeholder="NODE_ENV=production"
                  className="min-h-40 flex-1 resize-y rounded-md border border-border bg-background px-3 py-2 text-xs font-mono"
                />
              </label>
              <div className="flex items-center justify-end gap-2">
                <button
                  onClick={closeEditor}
                  disabled={submitting}
                  className="rounded-md border border-border px-3 py-2 text-sm hover:bg-muted disabled:opacity-50"
                >
                  Cancel
                </button>
                <button
                  onClick={submit}
                  disabled={submitting}
                  className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
                >
                  {submitting ? 'Saving...' : editing.mode === 'edit' ? 'Save' : createFromGit ? 'Create & deploy' : 'Create'}
                  <ArrowRight size={14} />
                </button>
              </div>
            </div>
          </div>
        </section>
      )}

      {sortedApps.length === 0 ? (
        <div className="rounded-md border border-dashed border-border p-12 text-center text-sm text-muted-foreground">
          <button
            onClick={openCreate}
            className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground hover:bg-primary/90"
          >
            <Plus size={14} /> New app
          </button>
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-3">
          {sortedApps.map(app => (
            <div
              key={app.name}
              className="rounded-lg border border-border bg-card p-4 space-y-3"
            >
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <h3 className="font-medium truncate" title={app.name}>{app.name}</h3>
                    {app.running ? (
                      <span className="text-[10px] inline-flex items-center gap-1 rounded-full bg-green-500/15 border border-green-500/30 px-2 py-0.5 text-green-400">
                        <Circle size={6} className="fill-green-400 stroke-none" /> running
                      </span>
                    ) : app.crashloop_gave_up ? (
                      <span
                        className="text-[10px] inline-flex items-center gap-1 rounded-full bg-orange-500/15 border border-orange-500/40 px-2 py-0.5 text-orange-400"
                        title="The supervisor gave up auto-restarting after too many consecutive crashes. Check logs, fix the underlying issue, then click Start."
                      >
                        <AlertCircle size={9} /> crashloop
                      </span>
                    ) : app.disabled ? (
                      <span className="text-[10px] inline-flex items-center gap-1 rounded-full bg-slate-500/15 border border-slate-500/30 px-2 py-0.5 text-slate-400">
                        disabled
                      </span>
                    ) : (
                      <span className="text-[10px] inline-flex items-center gap-1 rounded-full bg-red-500/15 border border-red-500/30 px-2 py-0.5 text-red-400">
                        stopped
                      </span>
                    )}
                    {app.running && app.restart_count != null && app.restart_count > 0 && (
                      <span
                        className="text-[10px] inline-flex items-center gap-1 rounded-full bg-amber-500/15 border border-amber-500/30 px-2 py-0.5 text-amber-400"
                        title={`Recovered from ${app.restart_count} recent crashes — watch for stability.`}
                      >
                        unstable
                      </span>
                    )}
                  </div>
                  <div className="flex items-center gap-1.5 mt-1">
                    <span className={`text-[10px] inline-flex items-center gap-1 rounded border px-1.5 py-0.5 ${runtimeColor[app.runtime] ?? runtimeColor.custom}`}>
                      {app.runtime === 'docker' ? <Container size={10} /> : <Cpu size={10} />}
                      {runtimeLabel[app.runtime] ?? app.runtime}
                    </span>
                    <span className="text-[10px] text-muted-foreground">port {app.port}</span>
                    {app.uptime && <span className="text-[10px] text-muted-foreground">· up {app.uptime}</span>}
                  </div>
                </div>
              </div>

              {app.command && (
                <code className="block text-[10px] text-muted-foreground bg-muted/50 rounded px-2 py-1 truncate" title={app.command}>
                  {app.command}
                </code>
              )}
              {app.docker_image && (
                <code className="block text-[10px] text-muted-foreground bg-muted/50 rounded px-2 py-1 truncate" title={app.docker_image}>
                  {app.docker_image}
                </code>
              )}

              {app.running && statsByName[app.name] && (
                <div className="flex items-center gap-3 text-[10px] text-muted-foreground bg-muted/30 rounded px-2 py-1">
                  <span title="CPU usage (% of one core)">
                    CPU {statsByName[app.name]!.cpu_percent.toFixed(1)}%
                  </span>
                  <span title="Resident set size">
                    RSS {formatBytes(statsByName[app.name]!.memory_rss)}
                  </span>
                  {statsByName[app.name]!.pid ? (
                    <span title="OS process ID">PID {statsByName[app.name]!.pid}</span>
                  ) : null}
                  <button
                    onClick={() => loadStats(app.name)}
                    disabled={statsLoadingFor === app.name}
                    className="ml-auto opacity-60 hover:opacity-100 disabled:opacity-30"
                    title="Refresh"
                  >
                    <RefreshCw size={10} />
                  </button>
                </div>
              )}

              <div className="flex items-center gap-1 pt-1">
                {app.running ? (
                  <button
                    onClick={() => doAction(app.name, 'stop')}
                    disabled={busyName === app.name}
                    className="text-xs rounded-md border border-border px-2 py-1 hover:bg-muted disabled:opacity-50 inline-flex items-center gap-1"
                  >
                    <Square size={12} /> Stop
                  </button>
                ) : (
                  <button
                    onClick={() => doAction(app.name, 'start')}
                    disabled={busyName === app.name}
                    className="text-xs rounded-md border border-green-500/30 bg-green-500/10 text-green-400 px-2 py-1 hover:bg-green-500/20 disabled:opacity-50 inline-flex items-center gap-1"
                  >
                    <Play size={12} /> Start
                  </button>
                )}
                <button
                  onClick={() => doAction(app.name, 'restart')}
                  disabled={busyName === app.name}
                  className="text-xs rounded-md border border-border px-2 py-1 hover:bg-muted disabled:opacity-50 inline-flex items-center gap-1"
                >
                  <RefreshCw size={12} /> Restart
                </button>
                <button
                  onClick={() => openLogs(app.name)}
                  className="text-xs rounded-md border border-border px-2 py-1 hover:bg-muted inline-flex items-center gap-1"
                >
                  <FileText size={12} /> Logs
                </button>
                <button
                  onClick={() => openEdit(app.name)}
                  className="text-xs rounded-md border border-border px-2 py-1 hover:bg-muted inline-flex items-center gap-1"
                >
                  <Edit2 size={12} /> Edit
                </button>
                <button
                  onClick={() => openDeploy(app.name)}
                  className="text-xs rounded-md border border-border px-2 py-1 hover:bg-muted inline-flex items-center gap-1"
                >
                  <GitBranch size={12} /> Deploy
                </button>
                {app.running && !statsByName[app.name] && (
                  <button
                    onClick={() => loadStats(app.name)}
                    disabled={statsLoadingFor === app.name}
                    className="text-xs rounded-md border border-border px-2 py-1 hover:bg-muted disabled:opacity-50 inline-flex items-center gap-1"
                    title="Fetch CPU / memory stats"
                  >
                    <Activity size={12} />
                  </button>
                )}
                <button
                  onClick={() => doDelete(app.name)}
                  disabled={busyName === app.name}
                  className="ml-auto text-xs rounded-md border border-border px-2 py-1 hover:bg-red-500/10 hover:border-red-500/30 hover:text-red-400 disabled:opacity-50 inline-flex items-center gap-1"
                >
                  <Trash2 size={12} />
                </button>
              </div>
            </div>
          ))}
        </div>
      )}

      {createOutcome && (createOutcome.error || !createOutcome.started) && (
        <div className={`rounded-md border p-3 text-sm space-y-2 ${
          createOutcome.started
            ? 'border-amber-500/30 bg-amber-500/10'
            : 'border-red-500/30 bg-red-500/10'
        }`}>
          <div className="flex items-center justify-between">
            <span className={`font-medium ${createOutcome.started ? 'text-amber-300' : 'text-red-300'}`}>
              {createOutcome.started
                ? `"${createOutcome.name}" started but is not listening on its port`
                : `"${createOutcome.name}" was saved but failed to start`}
            </span>
            <button
              onClick={() => setCreateOutcome(null)}
              className="text-muted-foreground hover:text-foreground"
            >
              <X size={14} />
            </button>
          </div>
          {createOutcome.error && (
            <pre className="text-[11px] font-mono whitespace-pre-wrap bg-background/50 rounded p-2 max-h-40 overflow-auto">
              {createOutcome.error}
            </pre>
          )}
          <div className="flex items-center gap-2">
            <button
              onClick={() => {
                if (createOutcome) openLogs(createOutcome.name);
              }}
              className="text-xs rounded-md border border-border px-2 py-1 hover:bg-muted inline-flex items-center gap-1"
            >
              <FileText size={12} /> View logs
            </button>
            <button
              onClick={() => {
                if (createOutcome) doAction(createOutcome.name, 'start');
                setCreateOutcome(null);
              }}
              className="text-xs rounded-md border border-green-500/30 bg-green-500/10 text-green-400 px-2 py-1 hover:bg-green-500/20 inline-flex items-center gap-1"
            >
              <Play size={12} /> Retry start
            </button>
          </div>
        </div>
      )}

      {deployFor && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
          <div className="w-full max-w-2xl max-h-[90vh] overflow-y-auto rounded-lg border border-border bg-card p-5 space-y-4">
            <div className="flex items-center justify-between">
              <h2 className="text-lg font-medium inline-flex items-center gap-2">
                <GitBranch size={16} /> Deploy {deployFor}
              </h2>
              <button
                onClick={() => { setDeployFor(null); setDeployResult(null); setDeployKeyResult(null); }}
                className="opacity-60 hover:opacity-100"
              >
                <X size={18} />
              </button>
            </div>

            <p className="text-xs text-muted-foreground">
              {deployTargetIsDocker
                ? "Clones (or fast-forwards) a git repo into the app's workdir, then restarts the Docker app. The restart packages the repo with BuildKit via docker buildx build --load."
                : "Clones (or fast-forwards) a git repo into the app's workdir, runs the optional build command, then restarts the supervisor. Times out after 5 minutes."}
            </p>
            {deployUsesSSHKeyWithHTTPS && (
              <div className="rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-100">
                SSH key path is set with an HTTPS repo URL. UWAS will use the key by cloning through SSH for GitHub/GitLab/Bitbucket. If this is another Git host, use a <code className="font-mono">git@host:owner/repo.git</code> or <code className="font-mono">ssh://</code> URL.
              </div>
            )}

            <div className="space-y-2">
              <label className="space-y-1 block">
                <span className="text-xs text-muted-foreground">Git URL</span>
                <input
                  value={deployForm.git_url}
                  onChange={e => setDeployForm(f => ({ ...f, git_url: e.target.value }))}
                  placeholder="https://github.com/user/repo.git"
                  className="w-full rounded-md border border-border bg-background px-3 py-1.5 text-sm font-mono"
                />
              </label>
              <label className="space-y-1 block">
                <span className="text-xs text-muted-foreground">Branch (optional)</span>
                <input
                  value={deployForm.git_branch}
                  onChange={e => setDeployForm(f => ({ ...f, git_branch: e.target.value }))}
                  placeholder="main"
                  className="w-full rounded-md border border-border bg-background px-3 py-1.5 text-sm font-mono"
                />
              </label>
              {!deployTargetIsDocker && (
                <label className="space-y-1 block">
                  <span className="text-xs text-muted-foreground">Build command (optional)</span>
                  <input
                    value={deployForm.build_cmd}
                    onChange={e => setDeployForm(f => ({ ...f, build_cmd: e.target.value }))}
                    placeholder="auto"
                    className="w-full rounded-md border border-border bg-background px-3 py-1.5 text-sm font-mono"
                  />
                </label>
              )}
              <label className="space-y-1 block">
                <span className="text-xs text-muted-foreground">Health path (optional)</span>
                <input
                  value={deployForm.health_path}
                  onChange={e => setDeployForm(f => ({ ...f, health_path: e.target.value }))}
                  placeholder="/health"
                  className="w-full rounded-md border border-border bg-background px-3 py-1.5 text-sm font-mono"
                />
              </label>
              <div className="grid gap-2 md:grid-cols-2">
                <label className="space-y-1 block">
                  <span className="text-xs text-muted-foreground">SSH key path for private SSH repos</span>
                  <div className="flex gap-2">
                    <input
                      value={deployForm.ssh_key_path}
                      onChange={e => setDeployForm(f => ({ ...f, ssh_key_path: e.target.value }))}
                      placeholder="/etc/uwas/apps.d/deploy-keys/app/id_ed25519"
                      className="min-w-0 flex-1 rounded-md border border-border bg-background px-3 py-1.5 text-sm font-mono"
                    />
                    <button
                      type="button"
                      onClick={generateDeployKey}
                      disabled={deployKeyGenerating}
                      className="inline-flex items-center gap-1 rounded-md border border-border px-2.5 py-1.5 text-xs hover:bg-muted disabled:opacity-50"
                      title="Generate an app-specific SSH deploy key"
                    >
                      {deployKeyGenerating ? <RefreshCw size={12} className="animate-spin" /> : <Key size={12} />}
                      Generate
                    </button>
                  </div>
                </label>
                <label className="space-y-1 block">
                  <span className="text-xs text-muted-foreground">HTTPS token for private repos</span>
                  <input
                    type="password"
                    value={deployForm.git_token}
                    onChange={e => setDeployForm(f => ({ ...f, git_token: e.target.value }))}
                    placeholder="leave blank to keep saved token"
                    className="w-full rounded-md border border-border bg-background px-3 py-1.5 text-sm font-mono"
                  />
                </label>
              </div>
              <p className="text-[10px] text-muted-foreground">
                Empty build command uses auto-detect. Health path is checked over HTTP on 127.0.0.1 after the process is listening.
              </p>
              {deployKeyResult && (
                <div className="rounded-md border border-emerald-500/30 bg-emerald-500/10 p-3 text-xs">
                  <div className="mb-2 flex items-center justify-between gap-2">
                    <span className="font-medium text-emerald-300">Deploy key generated</span>
                    <button
                      type="button"
                      onClick={() => navigator.clipboard.writeText(deployKeyResult.public_key)}
                      className="inline-flex items-center gap-1 rounded border border-emerald-500/30 px-2 py-1 text-emerald-200 hover:bg-emerald-500/10"
                    >
                      <Copy size={12} /> Copy public key
                    </button>
                  </div>
                  <code className="block max-h-24 overflow-auto break-all rounded bg-background/70 p-2 font-mono text-[11px] text-foreground">
                    {deployKeyResult.public_key}
                  </code>
                  <p className="mt-2 text-[10px] text-emerald-100/80">
                    Add this public key to the private repository as a read-only deploy key, then use a git@ or ssh:// Git URL.
                  </p>
                </div>
              )}
            </div>

            <details className="rounded-md border border-border p-3 text-xs">
              <summary className="cursor-pointer font-medium">
                Auto-deploy on git push (webhook)
              </summary>
              <div className="space-y-2 mt-3">
                <p className="text-muted-foreground">
                  Set a shared secret here, then add a webhook in your repo
                  pointing at the URL below with that secret. Pushes will
                  auto-trigger a redeploy.
                </p>
                <label className="space-y-1 block">
                  <span className="text-muted-foreground">Webhook secret</span>
                  <input
                    type="text"
                    value={deployForm.webhook_secret}
                    onChange={e => setDeployForm(f => ({ ...f, webhook_secret: e.target.value }))}
                    placeholder="any random string"
                    className="w-full rounded-md border border-border bg-background px-3 py-1.5 font-mono"
                  />
                </label>
                <label className="space-y-1 block">
                  <span className="text-muted-foreground">Only deploy when push is on this branch (optional)</span>
                  <input
                    type="text"
                    value={deployForm.branch_filter}
                    onChange={e => setDeployForm(f => ({ ...f, branch_filter: e.target.value }))}
                    placeholder="main"
                    className="w-full rounded-md border border-border bg-background px-3 py-1.5 font-mono"
                  />
                </label>
                <div className="space-y-1">
                  <span className="text-muted-foreground">Webhook URL</span>
                  <code className="block bg-muted/50 rounded px-2 py-1.5 break-all">
                    {`${window.location.origin}/api/v1/apps/${encodeURIComponent(deployFor ?? '')}/webhook`}
                  </code>
                  <span className="text-[10px] text-muted-foreground">
                    GitHub: Content type <code>application/json</code>, secret as above.
                    GitLab: pass the secret as the <code>X-Gitlab-Token</code> header.
                  </span>
                </div>
                <button
                  onClick={saveWebhookConfig}
                  className="text-xs rounded-md border border-border px-3 py-1.5 hover:bg-muted"
                >
                  Save webhook config
                </button>
              </div>
            </details>

            <div className="grid gap-3 md:grid-cols-2">
              <div className="rounded-md border border-border p-3 text-xs">
                <div className="mb-2 flex items-center justify-between gap-2">
                  <span className="font-medium">Preflight</span>
                  {deployMetaLoading ? (
                    <span className="text-muted-foreground">Loading…</span>
                  ) : deployPreflight ? (
                    <span className={deployPreflight.ok ? 'text-green-400' : 'text-red-400'}>
                      {deployPreflight.ok ? 'ready' : 'needs attention'}
                    </span>
                  ) : (
                    <span className="text-muted-foreground">unavailable</span>
                  )}
                </div>
                <div className="space-y-1">
                  {deployPreflight?.checks.length ? deployPreflight.checks.map(check => (
                    <div key={`${check.name}-${check.message}`} className="flex items-start gap-2">
                      {check.ok ? (
                        <CheckCircle size={12} className="mt-0.5 shrink-0 text-green-400" />
                      ) : (
                        <AlertCircle size={12} className={`mt-0.5 shrink-0 ${check.required ? 'text-red-400' : 'text-amber-400'}`} />
                      )}
                      <span className="min-w-0 flex-1">
                        <span className="font-mono">{check.name}</span>
                        {check.message ? <span className="text-muted-foreground"> · {check.message}</span> : null}
                      </span>
                    </div>
                  )) : (
                    <div className="text-muted-foreground">No preflight data yet.</div>
                  )}
                </div>
              </div>

              <div className="rounded-md border border-border p-3 text-xs">
                <div className="mb-2 flex items-center justify-between gap-2">
                  <span className="font-medium">Recent deploys</span>
                  <span className="text-muted-foreground">{deployHistory.length}</span>
                </div>
                <div className="space-y-1">
                  {deployHistory.length ? deployHistory.slice(0, 5).map(item => (
                    <div key={`${item.started_at}-${item.commit_sha ?? item.error ?? item.source}`} className="flex items-start gap-2">
                      {item.ok ? (
                        <CheckCircle size={12} className="mt-0.5 shrink-0 text-green-400" />
                      ) : (
                        <AlertCircle size={12} className="mt-0.5 shrink-0 text-red-400" />
                      )}
                      <span className="min-w-0 flex-1">
                        <span className="font-medium">{item.source}</span>
                        {item.mode ? <span className="text-muted-foreground"> · {item.mode}</span> : null}
                        {item.commit_sha ? <span className="font-mono text-muted-foreground"> · {item.commit_sha.slice(0, 7)}</span> : null}
                        {item.rolled_back ? <span className="font-mono text-amber-400"> · rollback{item.rollback_sha ? ` ${item.rollback_sha.slice(0, 7)}` : ''}</span> : null}
                        <span className="text-muted-foreground"> · {new Date(item.started_at).toLocaleString()}</span>
                        {item.rollback_note ? <span className="block truncate text-amber-400" title={item.rollback_note}>{item.rollback_note}</span> : null}
                        {item.error ? <span className="block truncate text-red-400" title={item.error}>{item.error}</span> : null}
                      </span>
                    </div>
                  )) : (
                    <div className="text-muted-foreground">No deploys recorded in this process.</div>
                  )}
                </div>
              </div>
            </div>

            {deployResult && (
              <div className={`rounded-md border p-2 text-xs space-y-1 ${
                deployResult.ok
                  ? 'border-green-500/30 bg-green-500/5'
                  : 'border-red-500/30 bg-red-500/5'
              }`}>
                <div className="flex items-center gap-2 font-medium">
                  {deployResult.ok
                    ? <CheckCircle size={12} className="text-green-400" />
                    : <AlertCircle size={12} className="text-red-400" />}
                  <span>
                    {deployResult.ok ? `${deployResult.mode} ok` : 'Failed'}
                  </span>
                  {deployResult.commit_sha && (
                    <span className="font-mono text-muted-foreground">
                      @ {deployResult.commit_sha.slice(0, 7)}
                    </span>
                  )}
                </div>
                {deployResult.error && (
                  <div className="text-red-400">{deployResult.error}</div>
                )}
                {deployResult.rollback_note && (
                  <div className={deployResult.rolled_back ? 'text-amber-400' : 'text-red-400'}>
                    {deployResult.rollback_note}
                    {deployResult.rollback_sha ? ` (${deployResult.rollback_sha.slice(0, 7)})` : ''}
                  </div>
                )}
                <pre className="text-[10px] font-mono whitespace-pre-wrap bg-background/50 rounded p-2 max-h-60 overflow-auto">
                  {deployResult.log || '(no output)'}
                </pre>
              </div>
            )}

            <div className="flex items-center justify-end gap-2">
              <button
                onClick={() => { setDeployFor(null); setDeployResult(null); }}
                disabled={deployRunning}
                className="text-sm rounded-md border border-border px-3 py-1.5 hover:bg-muted disabled:opacity-50"
              >
                Close
              </button>
              <button
                onClick={runDeploy}
                disabled={deployRunning || !deployForm.git_url.trim()}
                className="text-sm rounded-md bg-primary text-primary-foreground px-3 py-1.5 hover:bg-primary/90 disabled:opacity-50 inline-flex items-center gap-1.5"
              >
                {deployRunning ? 'Deploying…' : 'Deploy now'}
                <ArrowRight size={14} />
              </button>
            </div>
          </div>
        </div>
      )}

      {logsFor && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
          <div className="w-full max-w-4xl max-h-[80vh] flex flex-col rounded-lg border border-border bg-card">
            <div className="flex items-center justify-between p-4 border-b border-border">
              <div>
                <h2 className="text-lg font-medium">{logsFor} logs</h2>
                <span className="text-xs text-muted-foreground">
                  {logsKind === 'build' ? 'Build log (no runtime log yet)' : 'Runtime log (last 100 KB)'}
                </span>
              </div>
              <button onClick={() => setLogsFor(null)} className="opacity-60 hover:opacity-100">
                <X size={18} />
              </button>
            </div>
            <pre className="flex-1 overflow-auto p-4 text-[11px] font-mono whitespace-pre-wrap text-foreground bg-background">
              {logsContent}
            </pre>
          </div>
        </div>
      )}
    </div>
  );
}
