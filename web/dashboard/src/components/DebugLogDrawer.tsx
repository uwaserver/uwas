import { useEffect, useSyncExternalStore, useState } from 'react';
import { createPortal } from 'react-dom';
import { Bug, ChevronDown, Copy, Trash2 } from 'lucide-react';
import {
  addDebugLog,
  clearDebugLog,
  formatDebugDetail,
  getDebugLogSnapshot,
  setDebugLogEnabled,
  subscribeDebugLog,
  type DebugLogEntry,
} from '@/lib/debugLog';
import { copyText } from '@/lib/clipboard';

function levelClass(level: DebugLogEntry['level']) {
  switch (level) {
    case 'success':
      return 'text-emerald-300 bg-emerald-500/10';
    case 'warn':
      return 'text-amber-300 bg-amber-500/10';
    case 'error':
      return 'text-red-300 bg-red-500/10';
    default:
      return 'text-blue-300 bg-blue-500/10';
  }
}

function formatTime(iso: string) {
  return new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

export default function DebugLogDrawer() {
  const snapshot = useSyncExternalStore(subscribeDebugLog, getDebugLogSnapshot, getDebugLogSnapshot);
  const [open, setOpen] = useState(false);
  const entries = snapshot.entries;

  useEffect(() => {
    const originalFetch = window.fetch;
    window.fetch = async (input, init) => {
      const started = performance.now();
      const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url;
      const method = init?.method || (typeof input !== 'string' && !(input instanceof URL) ? input.method : 'GET');
      addDebugLog({
        level: 'info',
        scope: 'fetch',
        message: `${method} ${url}`,
        detail: formatDebugDetail(init?.body),
      });
      try {
        const response = await originalFetch(input, init);
        addDebugLog({
          level: response.ok ? 'success' : 'error',
          scope: 'fetch',
          message: `${method} ${url} -> ${response.status}`,
          duration_ms: Math.round(performance.now() - started),
        });
        return response;
      } catch (e) {
        addDebugLog({
          level: 'error',
          scope: 'fetch',
          message: `${method} ${url} network error`,
          detail: e instanceof Error ? e.message : String(e),
          duration_ms: Math.round(performance.now() - started),
        });
        throw e;
      }
    };
    return () => {
      window.fetch = originalFetch;
    };
  }, []);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open]);

  const copyLogs = async () => {
    const text = entries
      .slice()
      .reverse()
      .map(e => {
        const base = `[${e.time}] ${e.level.toUpperCase()} ${e.scope}: ${e.message}${e.duration_ms !== undefined ? ` (${e.duration_ms}ms)` : ''}`;
        return e.detail ? `${base}\n${e.detail}` : base;
      })
      .join('\n\n');
    await copyText(text);
  };

  return (
    <>
      {/* Lives in the sidebar footer, beside the theme toggle and logout.
          It used to float at right-3 top-3, on top of the fixed system stats
          bar, covering the version number at that bar's right end on every
          page. A control that is always present belongs in the chrome. */}
      <div className="flex items-center gap-2 rounded-md px-3 py-2">
        <button
          type="button"
          onClick={() => setOpen(true)}
          className="flex min-w-0 flex-1 items-center gap-3 text-sm font-medium text-muted-foreground transition-colors hover:text-foreground"
          title="Open debug log"
        >
          <Bug size={18} className={snapshot.enabled ? 'text-emerald-400' : ''} />
          <span className="truncate">Debug log</span>
          <span className="ml-auto rounded bg-muted px-1.5 py-0.5 font-mono text-[10px]">
            {entries.length}
          </span>
        </button>
        <button
          type="button"
          onClick={() => setDebugLogEnabled(!snapshot.enabled)}
          className={`relative h-5 w-9 shrink-0 rounded-full transition ${snapshot.enabled ? 'bg-emerald-500' : 'bg-muted'}`}
          title="Toggle debug log"
          aria-label="Toggle debug log"
        >
          <span
            className={`absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white transition-transform ${
              snapshot.enabled ? 'translate-x-4' : 'translate-x-0'
            }`}
          />
        </button>
      </div>

      {/* Portalled to <body> on purpose. The sidebar <aside> carries
          transition-transform and a translate-x utility, and a transformed
          ancestor becomes the containing block for its fixed descendants —
          so rendering the panel here left it pinned inside the 15rem sidebar
          instead of the viewport, which is why the log used to stream in that
          narrow column. A portal escapes the transform entirely.

          Docked to the content area rather than overlaid: left-0 on mobile,
          where the sidebar is off-canvas, and lg:left-60 on desktop so it
          lines up with the wide column. No dimming backdrop either — a debug
          log is for watching while you use the app, not a modal. */}
      {open && createPortal(
        <div className="fixed bottom-0 left-0 right-0 z-40 lg:left-60">
          <div className="w-full border-t border-border bg-card shadow-2xl">
            <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-3">
              <div className="min-w-0">
                <h2 className="text-sm font-semibold text-foreground">Debug Log</h2>
                <p className="text-xs text-muted-foreground">
                  {snapshot.enabled ? 'Live capture is on' : 'Live capture is off'} · {entries.length} events
                </p>
              </div>
              <div className="flex items-center gap-2">
                <button
                  type="button"
                  onClick={copyLogs}
                  disabled={!entries.length}
                  className="inline-flex items-center gap-1 rounded-md border border-border px-2.5 py-1.5 text-xs hover:bg-muted disabled:opacity-50"
                >
                  <Copy size={13} /> Copy
                </button>
                <button
                  type="button"
                  onClick={clearDebugLog}
                  disabled={!entries.length}
                  className="inline-flex items-center gap-1 rounded-md border border-border px-2.5 py-1.5 text-xs hover:bg-muted disabled:opacity-50"
                >
                  <Trash2 size={13} /> Clear
                </button>
                <button
                  type="button"
                  onClick={() => setOpen(false)}
                  className="inline-flex items-center gap-1 rounded-md border border-border px-2.5 py-1.5 text-xs hover:bg-muted"
                >
                  <ChevronDown size={13} /> Close
                </button>
              </div>
            </div>

            <div className="max-h-[45vh] overflow-y-auto px-4 py-3">
              {entries.length ? (
                <div className="space-y-2">
                  {entries.map(entry => (
                    <div key={entry.id} className="rounded-md border border-border bg-background/60 p-2 text-xs">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="font-mono text-muted-foreground">{formatTime(entry.time)}</span>
                        <span className={`rounded px-1.5 py-0.5 font-medium ${levelClass(entry.level)}`}>
                          {entry.level}
                        </span>
                        <span className="font-mono text-foreground">{entry.scope}</span>
                        {entry.duration_ms !== undefined && (
                          <span className="font-mono text-muted-foreground">{entry.duration_ms}ms</span>
                        )}
                      </div>
                      <div className="mt-1 text-foreground">{entry.message}</div>
                      {entry.detail && (
                        <pre className="mt-2 max-h-44 overflow-auto rounded bg-card p-2 font-mono text-[11px] text-muted-foreground whitespace-pre-wrap">
                          {entry.detail}
                        </pre>
                      )}
                    </div>
                  ))}
                </div>
              ) : (
                <div className="flex h-32 items-center justify-center text-sm text-muted-foreground">
                  Turn debug on and run an action to see events here.
                </div>
              )}
            </div>
          </div>
        </div>,
        document.body,
      )}
    </>
  );
}
