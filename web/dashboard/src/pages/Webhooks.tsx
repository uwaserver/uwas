import { useState, useEffect, useCallback } from 'react';
import { Webhook, Plus, Trash2, RefreshCw, Send, Check } from 'lucide-react';
import { fetchWebhooks, createWebhook, deleteWebhook, testWebhook, fetchFeatures, type WebhookEntry, type FeatureStatus } from '@/lib/api';
import FeatureBanner from '@/components/FeatureBanner';

const EVENT_TYPES = [
  'domain.add', 'domain.delete', 'domain.update',
  'cert.renewed', 'cert.expiry',
  'backup.completed', 'backup.failed',
  'php.crashed', 'cron.failed',
  'login.success', 'login.failed',
];

export default function Webhooks() {
  const [webhooks, setWebhooks] = useState<WebhookEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [status, setStatus] = useState('');
  const [showForm, setShowForm] = useState(false);
  const [url, setUrl] = useState('');
  const [secret, setSecret] = useState('');
  const [events, setEvents] = useState<string[]>([]);
  const [adding, setAdding] = useState(false);
  const [testing, setTesting] = useState<number | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<number | null>(null);
  const [featureStatus, setFeatureStatus] = useState<FeatureStatus | null>(null);

  useEffect(() => {
    fetchFeatures().then(f => setFeatureStatus(f.webhooks ?? null)).catch(() => {});
  }, []);

  const load = useCallback(async () => {
    try {
      const data = await fetchWebhooks();
      setWebhooks(data ?? []);
      setError('');
    } catch (e) { setError((e as Error).message); }
    finally { setLoading(false); }
  }, []);

  useEffect(() => { load(); }, [load]);

  const handleCreate = async () => {
    if (!url) return;
    setAdding(true);
    try {
      await createWebhook({ url, secret: secret || undefined, events: events.length > 0 ? events : undefined, enabled: true });
      setUrl(''); setSecret(''); setEvents([]); setShowForm(false);
      setStatus('Webhook created');
      await load();
    } catch (e) { setError((e as Error).message); }
    finally { setAdding(false); }
  };

  const handleDelete = async (idx: number) => {
    try {
      await deleteWebhook(idx);
      setConfirmDelete(null);
      setStatus('Webhook deleted');
      await load();
    } catch (e) { setError((e as Error).message); }
  };

  const handleTest = async (idx: number, whUrl: string) => {
    setTesting(idx);
    try {
      const res = await testWebhook(whUrl);
      setStatus(res.message || 'Test event fired');
    } catch (e) { setError((e as Error).message); }
    finally { setTesting(null); }
  };

  const toggleEvent = (ev: string) => {
    setEvents(prev => prev.includes(ev) ? prev.filter(e => e !== ev) : [...prev, ev]);
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">Webhooks</h1>
          <p className="mt-1 text-sm text-muted-foreground">Receive notifications when events occur (domain changes, backups, cert renewals, etc.)</p>
        </div>
        <button onClick={load} className="flex items-center gap-2 rounded-md border border-border bg-card px-3 py-2 text-sm text-card-foreground hover:bg-accent">
          <RefreshCw size={14} /> Refresh
        </button>
      </div>

      <FeatureBanner feature="webhooks" status={featureStatus} label="Webhook delivery" />

      {error && <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">{error}</div>}
      {status && <div className="rounded-md bg-emerald-500/10 px-4 py-3 text-sm text-emerald-400 flex items-center gap-2"><Check size={14} />{status}</div>}

      {/* Add form */}
      <div className="rounded-lg border border-border bg-card p-5">
        {!showForm ? (
          <button onClick={() => setShowForm(true)} className="flex items-center gap-2 rounded-md border border-dashed border-border px-4 py-2.5 text-sm text-muted-foreground hover:text-foreground hover:border-foreground/30">
            <Plus size={14} /> Add Webhook
          </button>
        ) : (
          <div className="space-y-4">
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div>
                <label className="mb-1.5 block text-xs font-medium text-muted-foreground">URL</label>
                <input type="url" value={url} onChange={e => setUrl(e.target.value)} placeholder="https://example.com/webhook"
                  className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none placeholder:text-muted-foreground focus:border-blue-500" />
              </div>
              <div>
                <label className="mb-1.5 block text-xs font-medium text-muted-foreground">Secret (HMAC-SHA256)</label>
                <input type="text" value={secret} onChange={e => setSecret(e.target.value)} placeholder="Optional signing secret"
                  className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none placeholder:text-muted-foreground focus:border-blue-500" />
              </div>
            </div>
            <div>
              <label className="mb-1.5 block text-xs font-medium text-muted-foreground">Events (empty = all)</label>
              <div className="flex flex-wrap gap-2">
                {EVENT_TYPES.map(ev => (
                  <button key={ev} onClick={() => toggleEvent(ev)}
                    className={`rounded-full px-3 py-1 text-xs font-medium transition ${events.includes(ev) ? 'bg-blue-600/20 text-blue-400 border border-blue-500/30' : 'bg-muted text-muted-foreground border border-border hover:text-foreground'}`}>
                    {ev}
                  </button>
                ))}
              </div>
            </div>
            <div className="flex gap-2">
              <button onClick={handleCreate} disabled={adding || !url}
                className="flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50">
                {adding ? <RefreshCw size={14} className="animate-spin" /> : <Plus size={14} />} Create
              </button>
              <button onClick={() => { setShowForm(false); setUrl(''); setSecret(''); setEvents([]); }}
                className="px-4 py-2 text-sm text-muted-foreground hover:text-foreground">Cancel</button>
            </div>
          </div>
        )}
      </div>

      {/* Webhook list */}
      {loading ? (
        <div className="text-center text-sm text-muted-foreground py-12">Loading...</div>
      ) : webhooks.length === 0 ? (
        <div className="rounded-lg border border-border bg-card px-6 py-12 text-center">
          <Webhook size={40} className="mx-auto mb-3 text-muted-foreground" />
          <p className="text-card-foreground font-medium">No webhooks configured</p>
          <p className="text-sm text-muted-foreground mt-1">Add a webhook above to receive event notifications.</p>
        </div>
      ) : (
        <div className="space-y-3">
          {webhooks.map((wh, idx) => (
            <div key={idx} className="rounded-lg border border-border bg-card p-4">
              <div className="flex items-center justify-between">
                <div className="min-w-0 flex-1">
                  <p className="font-mono text-sm text-foreground truncate">{wh.url}</p>
                  <div className="mt-1 flex flex-wrap gap-1">
                    {(wh.events && wh.events.length > 0) ? wh.events.map(ev => (
                      <span key={ev} className="rounded bg-accent px-2 py-0.5 text-[10px] text-muted-foreground">{ev}</span>
                    )) : (
                      <span className="rounded bg-blue-600/10 px-2 py-0.5 text-[10px] text-blue-400">all events</span>
                    )}
                  </div>
                  <p className="mt-1 text-[10px] text-muted-foreground">
                    Secret: {wh.secret || 'none'} | Retry: {wh.retry || 3} | {wh.enabled ? 'Enabled' : 'Disabled'}
                  </p>
                </div>
                <div className="flex items-center gap-2 ml-3">
                  <button onClick={() => handleTest(idx, wh.url)} disabled={testing === idx}
                    className="flex items-center gap-1 rounded-md bg-accent/50 px-2.5 py-1.5 text-xs text-card-foreground hover:bg-accent disabled:opacity-50">
                    {testing === idx ? <RefreshCw size={12} className="animate-spin" /> : <Send size={12} />} Test
                  </button>
                  {confirmDelete === idx ? (
                    <span className="flex items-center gap-2">
                      <button onClick={() => handleDelete(idx)} className="rounded bg-red-600 px-2 py-1 text-xs text-white">Yes</button>
                      <button onClick={() => setConfirmDelete(null)} className="rounded bg-accent px-2 py-1 text-xs text-card-foreground">No</button>
                    </span>
                  ) : (
                    <button onClick={() => setConfirmDelete(idx)} className="flex items-center gap-1 rounded-md bg-red-500/15 px-2.5 py-1.5 text-xs text-red-400 hover:bg-red-500/25">
                      <Trash2 size={12} /> Delete
                    </button>
                  )}
                </div>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
