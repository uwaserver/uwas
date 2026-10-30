import { useEffect, useRef } from 'react';

/**
 * Calls `fn` immediately, then on a fixed interval. Pauses while the
 * document is hidden (background tab) and resumes — with an immediate
 * fresh call — when the tab becomes visible again.
 *
 * Use this instead of bare setInterval anywhere we poll a UWAS endpoint
 * for stats/lists. A panel left open in a background tab should not be
 * polling the server every few seconds for nothing.
 *
 * The `fn` reference is captured fresh on every render via a ref, so the
 * callback can close over up-to-date state without re-subscribing the
 * interval (which would cause skips and duplicate fetches).
 */
export function usePolling(fn: () => void | Promise<void>, intervalMs: number | null, deps: ReadonlyArray<unknown> = []) {
  const fnRef = useRef(fn);
  fnRef.current = fn;

  useEffect(() => {
    if (intervalMs == null) return;
    let id: ReturnType<typeof setInterval> | null = null;

    // A tick is skipped while the previous call is still in flight, so a slow
    // endpoint is not hit by a pile of overlapping requests whose answers can
    // arrive out of order. The skip is bounded: a request that never settles
    // must not stop the poll for good.
    let pendingSince: number | null = null;
    const maxPendingMs = Math.max(intervalMs * 3, 30_000);

    const tick = () => {
      if (pendingSince !== null && Date.now() - pendingSince < maxPendingMs) return;
      pendingSince = null;
      const result = fnRef.current();
      if (result && typeof (result as Promise<void>).then === 'function') {
        const startedAt = Date.now();
        pendingSince = startedAt;
        const done = () => {
          if (pendingSince === startedAt) pendingSince = null;
        };
        (result as Promise<void>).then(done, done);
      }
    };

    const start = () => {
      if (id !== null) return;
      tick();
      id = setInterval(tick, intervalMs);
    };

    const stop = () => {
      if (id !== null) {
        clearInterval(id);
        id = null;
      }
    };

    const onVisibility = () => {
      if (document.hidden) {
        stop();
      } else {
        start();
      }
    };

    if (!document.hidden) {
      start();
    }
    document.addEventListener('visibilitychange', onVisibility);

    return () => {
      stop();
      document.removeEventListener('visibilitychange', onVisibility);
    };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [intervalMs, ...deps]);
}
