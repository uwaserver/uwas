import { useEffect, useSyncExternalStore, useState } from 'react';
import { Activity, Bug, ChevronDown, Copy, Trash2 } from 'lucide-react';
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
      <div className="fixed right-3 top-3 z-50 flex items-center gap-2 rounded-md border border-border bg-card/95 px-2.5 py-1.5 shadow-sm backdrop-blur">
        <Bug size={14} className={snapshot.enabled ? 'text-emerald-400' : 'text-muted-foreground'} />
        <button
          type="button"
          onClick={() => setDebugLogEnabled(!snapshot.enabled)}
          className={`relative h-5 w-9 rounded-full transition ${snapshot.enabled ? 'bg-emerald-500' : 'bg-muted'}`}
          title="Toggle debug log"
          aria-label="Toggle debug log"
        >
          <span
            className={`absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white transition-transform ${
              snapshot.enabled ? 'translate-x-4' : 'translate-x-0'
            }`}
          />
        </button>
        <button
          type="button"
          onClick={() => setOpen(true)}
          className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground"
          title="Open debug log"
        >
          <Activity size={13} />
          {entries.length}
        </button>
      </div>

      {open && (
        <div className="fixed inset-0 z-50 flex items-end bg-black/30">
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
        </div>
      )}
    </>
  );
}
