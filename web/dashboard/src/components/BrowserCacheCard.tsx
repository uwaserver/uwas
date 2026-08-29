import { useState } from 'react';
import { Search } from 'lucide-react';
import { updateDomain, previewCacheControl, type DomainDetail, type CacheControlPreview } from '@/lib/api';

/**
 * BrowserCacheCard edits a domain's browser_cache settings and probes what a
 * given path will actually receive.
 *
 * The two halves answer different questions. The form asks what you want sent;
 * the probe answers what will be sent, which is not the same thing — a cache
 * rule overrides a location, and an HTML page under an immutable_paths prefix
 * still revalidates. Neither is visible from the form alone, so the probe asks
 * the server rather than reimplementing the precedence here.
 */
export default function BrowserCacheCard({
  host,
  detail,
  onSaved,
}: {
  host: string;
  detail: DomainDetail;
  onSaved: () => void;
}) {
  const bc = detail.browser_cache ?? {};
  const [enabled, setEnabled] = useState(bc.enabled !== false);
  const [html, setHtml] = useState(bc.html ?? 'no-cache');
  const [assets, setAssets] = useState(bc.assets ?? '');
  const [paths, setPaths] = useState((bc.immutable_paths ?? []).join('\n'));
  const [saving, setSaving] = useState(false);
  const [status, setStatus] = useState<{ ok: boolean; message: string } | null>(null);

  const [probePath, setProbePath] = useState('/');
  const [probe, setProbe] = useState<CacheControlPreview | null>(null);
  const [probing, setProbing] = useState(false);

  const save = async () => {
    setSaving(true);
    setStatus(null);
    try {
      await updateDomain(host, {
        browser_cache: {
          enabled,
          html: html.trim(),
          assets: assets.trim(),
          immutable_paths: paths.split('\n').map((p: string) => p.trim()).filter(Boolean),
        },
      });
      setStatus({ ok: true, message: 'Saved' });
      onSaved();
      if (probe) void runProbe();
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setSaving(false);
    }
  };

  const runProbe = async () => {
    setProbing(true);
    try {
      setProbe(await previewCacheControl(host, probePath || '/'));
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setProbing(false);
    }
  };

  const sourceLabel: Record<string, string> = {
    location: 'a locations[] entry',
    headers: 'a headers entry',
    cache_rule: 'a cache rule',
    browser_cache: 'browser cache',
    none: 'nothing',
  };

  return (
    <div className="rounded-lg border border-border bg-card p-5">
      <h3 className="mb-1 text-sm font-semibold text-card-foreground">Browser Cache</h3>
      <p className="mb-4 text-xs text-muted-foreground">
        How long a visitor&apos;s browser may reuse a file before asking again.
        Separate from the server cache, which makes a response cheap to produce
        — this one removes the request.
      </p>

      <div className="grid gap-5 md:grid-cols-2">
        <div className="space-y-3 text-xs">
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={enabled} onChange={e => setEnabled(e.target.checked)} />
            <span>Send Cache-Control headers</span>
          </label>

          <label className="block space-y-1">
            <span className="text-muted-foreground">Pages (.html and extensionless)</span>
            <input
              value={html}
              onChange={e => setHtml(e.target.value)}
              placeholder="no-cache"
              className="w-full rounded-md border border-border bg-background px-3 py-1.5 font-mono"
            />
            <span className="block text-[10px] text-muted-foreground">
              <code>no-cache</code> lets the browser keep a copy but revalidate
              first, which the ETag turns into a cheap 304.
            </span>
          </label>

          <label className="block space-y-1">
            <span className="text-muted-foreground">Immutable paths (one per line)</span>
            <textarea
              value={paths}
              onChange={e => setPaths(e.target.value)}
              rows={3}
              placeholder={'/assets/*\n/_next/static/*'}
              className="w-full rounded-md border border-border bg-background px-3 py-1.5 font-mono"
            />
            <span className="block rounded border border-amber-500/40 bg-amber-500/10 px-2 py-1.5 text-[10px] text-amber-300">
              Matches are cached for a year and never revalidated. Only list
              build output whose filenames contain a content hash — something
              like <code>app.4f2a1c.js</code>, where the name changes whenever
              the bytes do. Point this at files you edit in place and visitors
              keep the old copy for a year, with no way for you to recall it.
            </span>
          </label>

          <label className="block space-y-1">
            <span className="text-muted-foreground">Other assets (optional)</span>
            <input
              value={assets}
              onChange={e => setAssets(e.target.value)}
              placeholder="empty — send no header"
              className="w-full rounded-md border border-border bg-background px-3 py-1.5 font-mono"
            />
          </label>

          <button
            onClick={save}
            disabled={saving}
            className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted disabled:opacity-50"
          >
            {saving ? 'Saving…' : 'Save'}
          </button>
          {status && (
            <p className={`text-xs ${status.ok ? 'text-emerald-400' : 'text-red-400'}`}>{status.message}</p>
          )}
        </div>

        <div className="space-y-2 text-xs">
          <span className="text-muted-foreground">What will this path get?</span>
          <div className="flex gap-2">
            <input
              value={probePath}
              onChange={e => setProbePath(e.target.value)}
              onKeyDown={e => { if (e.key === 'Enter') void runProbe(); }}
              placeholder="/assets/app.4f2a1c.js"
              className="min-w-0 flex-1 rounded-md border border-border bg-background px-3 py-1.5 font-mono"
            />
            <button
              onClick={runProbe}
              disabled={probing}
              className="inline-flex shrink-0 items-center gap-1 rounded-md border border-border px-3 py-1.5 hover:bg-muted disabled:opacity-50"
            >
              <Search size={13} /> Check
            </button>
          </div>

          {probe && (
            <div className="space-y-2 rounded-md border border-border bg-background/60 p-3">
              <div>
                <span className="text-muted-foreground">Header sent</span>
                <code className="mt-0.5 block break-all rounded bg-muted/50 px-2 py-1.5">
                  {probe.value || 'none — the browser revalidates on its own'}
                </code>
              </div>
              <div>
                <span className="text-muted-foreground">Decided by</span>
                <p className="mt-0.5">
                  {sourceLabel[probe.source] ?? probe.source}
                  {probe.detail ? <code className="ml-1 rounded bg-muted/50 px-1.5 py-0.5">{probe.detail}</code> : null}
                </p>
              </div>
              {probe.source !== 'browser_cache' && probe.source !== 'none' && (
                <p className="text-[10px] text-muted-foreground">
                  This path is decided elsewhere in the domain config, so the
                  settings on the left do not apply to it.
                </p>
              )}
            </div>
          )}

          <p className="text-[10px] text-muted-foreground">
            Precedence, highest first: cache rules, then headers, then
            locations, then these settings. Pages always win over immutable
            paths, so an HTML file under <code>/assets/*</code> still
            revalidates.
          </p>
        </div>
      </div>
    </div>
  );
}
