import { useState } from 'react';
import { Database, Trash2, Tag, CheckCircle, XCircle } from 'lucide-react';
import { useStats } from '@/hooks/useStats';
import { triggerPurge } from '@/lib/api';
import Card from '@/components/Card';

export default function Cache() {
  const { stats } = useStats(5000);
  const [tag, setTag] = useState('');
  const [confirmAll, setConfirmAll] = useState(false);
  const [status, setStatus] = useState<{ ok: boolean; message: string } | null>(
    null,
  );
  const [purging, setPurging] = useState(false);

  const total = (stats?.cache_hits ?? 0) + (stats?.cache_misses ?? 0);
  const hitRate = total > 0 ? ((stats!.cache_hits / total) * 100).toFixed(1) : '0';

  const handlePurgeTag = async () => {
    if (!tag.trim()) return;
    setPurging(true);
    setStatus(null);
    try {
      await triggerPurge(tag.trim());
      setStatus({ ok: true, message: `Purged entries with tag "${tag.trim()}"` });
      setTag('');
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setPurging(false);
    }
  };

  const handlePurgeAll = async () => {
    setPurging(true);
    setStatus(null);
    try {
      await triggerPurge();
      setStatus({ ok: true, message: 'All cache entries purged' });
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setPurging(false);
      setConfirmAll(false);
    }
  };

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold text-slate-100">Cache</h1>
        <p className="text-sm text-slate-400">
          Cache statistics and purge controls
        </p>
      </div>

      {/* Stats */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <Card
          icon={<CheckCircle size={20} />}
          label="Cache Hits"
          value={stats?.cache_hits.toLocaleString() ?? '--'}
        />
        <Card
          icon={<XCircle size={20} />}
          label="Cache Misses"
          value={stats?.cache_misses.toLocaleString() ?? '--'}
        />
        <Card
          icon={<Database size={20} />}
          label="Hit Rate"
          value={`${hitRate}%`}
        />
      </div>

      {/* Purge controls */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5 shadow-md">
        <h2 className="mb-4 text-sm font-semibold text-slate-300">
          Purge Cache
        </h2>

        {/* Status message */}
        {status && (
          <div
            className={`mb-4 flex items-center gap-2 rounded-md px-3 py-2 text-sm ${
              status.ok
                ? 'bg-emerald-500/10 text-emerald-400'
                : 'bg-red-500/10 text-red-400'
            }`}
          >
            {status.ok ? <CheckCircle size={14} /> : <XCircle size={14} />}
            {status.message}
          </div>
        )}

        {/* Purge by tag */}
        <div className="mb-4 flex gap-3">
          <div className="relative flex-1">
            <Tag
              size={14}
              className="absolute top-1/2 left-3 -translate-y-1/2 text-slate-500"
            />
            <input
              type="text"
              value={tag}
              onChange={(e) => setTag(e.target.value)}
              placeholder="Enter cache tag to purge"
              className="w-full rounded-md border border-[#334155] bg-[#0f172a] py-2 pr-3 pl-9 text-sm text-slate-200 placeholder-slate-500 outline-none transition focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
              onKeyDown={(e) => e.key === 'Enter' && handlePurgeTag()}
            />
          </div>
          <button
            onClick={handlePurgeTag}
            disabled={purging || !tag.trim()}
            className="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
          >
            Purge Tag
          </button>
        </div>

        {/* Purge all */}
        <div className="border-t border-[#334155] pt-4">
          {!confirmAll ? (
            <button
              onClick={() => setConfirmAll(true)}
              className="flex items-center gap-2 rounded-md bg-red-600/15 px-4 py-2 text-sm font-medium text-red-400 transition hover:bg-red-600/25"
            >
              <Trash2 size={14} />
              Purge All Cache
            </button>
          ) : (
            <div className="flex items-center gap-3">
              <span className="text-sm text-slate-400">
                Are you sure? This cannot be undone.
              </span>
              <button
                onClick={handlePurgeAll}
                disabled={purging}
                className="rounded-md bg-red-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-red-700 disabled:opacity-50"
              >
                {purging ? 'Purging...' : 'Yes, Purge All'}
              </button>
              <button
                onClick={() => setConfirmAll(false)}
                className="rounded-md bg-[#334155] px-4 py-2 text-sm font-medium text-slate-300 transition hover:bg-[#475569]"
              >
                Cancel
              </button>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
