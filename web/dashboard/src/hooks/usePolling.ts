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
export function usePolling(fn: () => void | Promise<void>, intervalMs: number, deps: ReadonlyArray<unknown> = []) {
  const fnRef = useRef(fn);
  fnRef.current = fn;

  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => {
    let id: ReturnType<typeof setInterval> | null = null;

    const tick = () => {
      void fnRef.current();
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
  }, [intervalMs, ...deps]);
}
