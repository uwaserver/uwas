import { useState, useEffect, useCallback } from 'react';
import {
  Clock,
  Plus,
  Trash2,
  RefreshCw,
  Terminal,
  Globe,
} from 'lucide-react';
import {
  fetchCronJobs,
  addCronJob,
  deleteCronJob,
  fetchDomains,
  type CronJob,
  type DomainData,
} from '@/lib/api';

const SCHEDULE_PRESETS = [
  { label: 'Every 5 minutes', value: '*/5 * * * *' },
  { label: 'Every 15 minutes', value: '*/15 * * * *' },
  { label: 'Every 30 minutes', value: '*/30 * * * *' },
  { label: 'Hourly', value: '0 * * * *' },
  { label: 'Daily at midnight', value: '0 0 * * *' },
  { label: 'Daily at 3 AM', value: '0 3 * * *' },
  { label: 'Weekly (Sunday)', value: '0 0 * * 0' },
  { label: 'Monthly (1st)', value: '0 0 1 * *' },
  { label: 'Custom', value: '' },
];

export default function CronJobs() {
  const [jobs, setJobs] = useState<CronJob[]>([]);
  const [domains, setDomains] = useState<DomainData[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [status, setStatus] = useState('');

  // Form state
  const [preset, setPreset] = useState(SCHEDULE_PRESETS[0].value);
  const [customSchedule, setCustomSchedule] = useState('');
  const [command, setCommand] = useState('');
  const [domain, setDomain] = useState('');
  const [comment, setComment] = useState('');
  const [adding, setAdding] = useState(false);

  // Delete confirmation
  const [confirmDelete, setConfirmDelete] = useState<CronJob | null>(null);
  const [deleting, setDeleting] = useState(false);

  const load = useCallback(async () => {
    try {
      const [j, d] = await Promise.all([fetchCronJobs(), fetchDomains()]);
      setJobs(j ?? []);
      setDomains(d ?? []);
      setError('');
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const schedule = preset === '' ? customSchedule : preset;

  const handleAdd = async () => {
    if (!schedule.trim() || !command.trim()) return;
    setAdding(true);
    setError('');
    setStatus('');
    try {
      await addCronJob({
        schedule: schedule.trim(),
        command: command.trim(),
        domain: domain || undefined,
        comment: comment || undefined,
      });
      setCommand('');
      setComment('');
      setStatus('Cron job added successfully.');
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setAdding(false);
    }
  };

  const handleDelete = async (job: CronJob) => {
    setDeleting(true);
    setError('');
    setStatus('');
    try {
      await deleteCronJob(job.schedule, job.command);
      setConfirmDelete(null);
      setStatus('Cron job deleted.');
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setDeleting(false);
    }
  };

  if (loading) {
    return (
      <div className="flex h-96 items-center justify-center text-slate-400">Loading cron jobs...</div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">Cron Jobs</h1>
          <p className="text-sm text-slate-400">Manage scheduled tasks ({jobs.length} jobs)</p>
        </div>
        <button
          onClick={load}
          className="flex items-center gap-1.5 rounded-md bg-[#334155] px-3 py-1.5 text-xs text-slate-300 hover:bg-[#475569]"
        >
          <RefreshCw size={12} /> Refresh
        </button>
      </div>

      {error && (
        <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">{error}</div>
      )}
      {status && (
        <div className="rounded-md bg-emerald-500/10 px-4 py-3 text-sm text-emerald-400">{status}</div>
      )}

      {/* Add cron job form */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5 shadow-md">
        <div className="mb-4 flex items-center gap-2">
          <Plus size={18} className="text-blue-400" />
          <h2 className="text-sm font-semibold text-slate-300">Add Cron Job</h2>
        </div>

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {/* Schedule preset */}
          <div>
            <label className="mb-1.5 block text-xs font-medium uppercase text-slate-500">Schedule</label>
            <select
              value={preset}
              onChange={e => setPreset(e.target.value)}
              className="w-full rounded-md border border-[#334155] bg-[#0f172a] px-3 py-2.5 text-sm text-slate-200 outline-none focus:border-blue-500"
            >
              {SCHEDULE_PRESETS.map(p => (
                <option key={p.label} value={p.value}>{p.label}</option>
              ))}
            </select>
          </div>

          {/* Custom schedule input */}
          {preset === '' && (
            <div>
              <label className="mb-1.5 block text-xs font-medium uppercase text-slate-500">Cron Expression</label>
              <input
                type="text"
                value={customSchedule}
                onChange={e => setCustomSchedule(e.target.value)}
                placeholder="* * * * *"
                className="w-full rounded-md border border-[#334155] bg-[#0f172a] px-3 py-2.5 font-mono text-sm text-slate-200 outline-none focus:border-blue-500"
              />
            </div>
          )}

          {/* Command */}
          <div className={preset === '' ? '' : 'sm:col-span-1'}>
            <label className="mb-1.5 block text-xs font-medium uppercase text-slate-500">Command</label>
            <input
              type="text"
              value={command}
              onChange={e => setCommand(e.target.value)}
              placeholder="/usr/bin/php /home/user/cron.php"
              className="w-full rounded-md border border-[#334155] bg-[#0f172a] px-3 py-2.5 text-sm text-slate-200 outline-none focus:border-blue-500"
            />
          </div>

          {/* Domain */}
          <div>
            <label className="mb-1.5 block text-xs font-medium uppercase text-slate-500">Domain (optional)</label>
            <select
              value={domain}
              onChange={e => setDomain(e.target.value)}
              className="w-full rounded-md border border-[#334155] bg-[#0f172a] px-3 py-2.5 text-sm text-slate-200 outline-none focus:border-blue-500"
            >
              <option value="">Global</option>
              {domains.map(d => (
                <option key={d.host} value={d.host}>{d.host}</option>
              ))}
            </select>
          </div>
        </div>

        {/* Comment + submit */}
        <div className="mt-4 flex items-end gap-4">
          <div className="flex-1">
            <label className="mb-1.5 block text-xs font-medium uppercase text-slate-500">Comment (optional)</label>
            <input
              type="text"
              value={comment}
              onChange={e => setComment(e.target.value)}
              placeholder="Daily backup cleanup..."
              className="w-full rounded-md border border-[#334155] bg-[#0f172a] px-3 py-2.5 text-sm text-slate-200 outline-none focus:border-blue-500"
            />
          </div>
          <button
            onClick={handleAdd}
            disabled={adding || !schedule.trim() || !command.trim()}
            className="flex items-center gap-1.5 rounded-md bg-blue-600 px-5 py-2.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          >
            {adding ? <RefreshCw size={14} className="animate-spin" /> : <Plus size={14} />}
            {adding ? 'Adding...' : 'Add Job'}
          </button>
        </div>

        {/* Schedule hint */}
        <p className="mt-3 text-xs text-slate-500">
          Format: minute hour day-of-month month day-of-week (e.g. <code className="text-slate-400">0 3 * * *</code> = daily at 3 AM)
        </p>
      </div>

      {/* Jobs table */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] shadow-md">
        <div className="border-b border-[#334155] px-5 py-4">
          <h2 className="text-sm font-semibold text-slate-300">Active Jobs ({jobs.length})</h2>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-[#334155] text-slate-400">
                <th className="px-5 py-3 font-medium">Schedule</th>
                <th className="px-5 py-3 font-medium">Command</th>
                <th className="px-5 py-3 font-medium">Domain</th>
                <th className="px-5 py-3 font-medium">Comment</th>
                <th className="px-5 py-3 font-medium text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {jobs.map((job, i) => (
                <tr
                  key={i}
                  className="border-b border-[#334155]/50 text-slate-300 transition hover:bg-[#334155]/30"
                >
                  <td className="px-5 py-3">
                    <div className="flex items-center gap-2">
                      <Clock size={14} className="text-blue-400 shrink-0" />
                      <code className="font-mono text-xs text-slate-200">{job.schedule}</code>
                    </div>
                  </td>
                  <td className="px-5 py-3">
                    <div className="flex items-center gap-2 max-w-xs">
                      <Terminal size={14} className="text-slate-500 shrink-0" />
                      <span className="font-mono text-xs text-slate-300 truncate">{job.command}</span>
                    </div>
                  </td>
                  <td className="px-5 py-3">
                    {job.domain ? (
                      <span className="flex items-center gap-1 text-xs text-slate-400">
                        <Globe size={12} />
                        {job.domain}
                      </span>
                    ) : (
                      <span className="text-xs text-slate-500">Global</span>
                    )}
                  </td>
                  <td className="px-5 py-3 text-xs text-slate-500 max-w-xs truncate">
                    {job.comment || '--'}
                  </td>
                  <td className="px-5 py-3 text-right">
                    {confirmDelete === job ? (
                      <span className="flex items-center justify-end gap-2">
                        <span className="text-xs text-red-400">Delete?</span>
                        <button
                          onClick={() => handleDelete(job)}
                          disabled={deleting}
                          className="rounded bg-red-600 px-2 py-1 text-xs text-white hover:bg-red-700 disabled:opacity-50"
                        >
                          {deleting ? '...' : 'Yes'}
                        </button>
                        <button
                          onClick={() => setConfirmDelete(null)}
                          className="rounded bg-[#334155] px-2 py-1 text-xs text-slate-300"
                        >
                          No
                        </button>
                      </span>
                    ) : (
                      <button
                        onClick={() => setConfirmDelete(job)}
                        className="flex items-center gap-1 rounded-md bg-red-500/15 px-2.5 py-1.5 text-xs font-medium text-red-400 hover:bg-red-500/25"
                      >
                        <Trash2 size={12} /> Delete
                      </button>
                    )}
                  </td>
                </tr>
              ))}
              {jobs.length === 0 && (
                <tr>
                  <td colSpan={5} className="px-5 py-12 text-center text-slate-500">
                    <Clock size={32} className="mx-auto mb-3 opacity-40" />
                    No cron jobs configured. Add one above.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
