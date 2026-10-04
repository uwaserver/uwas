import { useState, useEffect, useCallback, useRef } from 'react';
import { fetchStats, fetchHealth, sseStatsURL, type StatsData, type HealthData } from '@/lib/api';

export function useStats(interval = 3000) {
  const [stats, setStats] = useState<StatsData | null>(null);
  const [health, setHealth] = useState<HealthData | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [history, setHistory] = useState<{ time: string; requests: number; cacheHits: number; p95: number }[]>([]);
  const usingSSE = useRef(false);
  const generation = useRef(0);
  const refreshSequence = useRef(0);
  const publishedRefresh = useRef(0);

  const pushStats = useCallback((s: StatsData) => {
    setStats(s);
    setError(null);
    setHistory(prev => {
      const next = [...prev, {
        time: new Date().toLocaleTimeString(),
        requests: s.requests_total,
        cacheHits: s.cache_hits,
        p95: s.latency_p95_ms ?? 0,
      }];
      return next.slice(-30);
    });
  }, []);

  // Polling fallback: fetch stats + health together.
  const refresh = useCallback(async () => {
    const currentGeneration = generation.current;
    const currentRefresh = ++refreshSequence.current;
    try {
      const [s, h] = await Promise.all([fetchStats(), fetchHealth()]);
      if (generation.current !== currentGeneration || currentRefresh < publishedRefresh.current) return;
      publishedRefresh.current = currentRefresh;
      pushStats(s);
      setHealth(h);
    } catch (e) {
      if (generation.current !== currentGeneration || currentRefresh < publishedRefresh.current) return;
      publishedRefresh.current = currentRefresh;
      setError((e as Error).message);
    }
  }, [pushStats]);

  useEffect(() => {
    const effectGeneration = generation.current;
    // Two independent timers: `pollingId` drives the stats-polling fallback,
    // `healthId` refreshes health while SSE is active (health isn't in the SSE
    // stream). They must be separate — conflating them previously left the
    // onerror fallback unable to start stats polling, freezing the dashboard.
    let pollingId: ReturnType<typeof setInterval> | null = null;
    let healthId: ReturnType<typeof setInterval> | null = null;
    let es: EventSource | null = null;
    // startSSE is async and is NOT awaited here, so the effect can tear down
    // while it is still suspended on `await sseStatsURL()`. Without this flag
    // the EventSource and the healthId interval are created *after* cleanup has
    // already run, so nothing ever closes them: every unmount leaks one SSE
    // connection and one forever-polling timer.
    let cancelled = false;

    function startPolling() {
      if (pollingId || cancelled) return;
      usingSSE.current = false;
      refresh();
      pollingId = setInterval(refresh, interval);
    }

    async function startSSE() {
      try {
        const url = await sseStatsURL();
        if (cancelled) return;
        es = new EventSource(url);

        es.onmessage = (event) => {
          if (cancelled || es === null) return;
          try {
            const s: StatsData = JSON.parse(event.data);
            pushStats(s);
          } catch {
            // ignore parse errors
          }
        };

        es.onopen = () => {
          if (cancelled || es === null) return;
          usingSSE.current = true;
          // SSE only sends stats; fetch health once and then periodically.
          fetchHealth().then(h => { if (!cancelled) setHealth(h); }).catch(() => {});
        };

        es.onerror = () => {
          if (cancelled || es === null) return;
          // SSE failed — close and fall back to stats polling.
          es?.close();
          es = null;
          startPolling();
        };
      } catch {
        // EventSource constructor failed — fall back to polling.
        startPolling();
        return;
      }

      // Refresh health periodically even when SSE is active (health isn't
      // included in the SSE stream).
      healthId = setInterval(() => {
        fetchHealth().then(h => { if (!cancelled) setHealth(h); }).catch(() => {});
      }, interval);
    }

    startSSE();

    return () => {
      cancelled = true;
      generation.current = effectGeneration + 1;
      if (pollingId) clearInterval(pollingId);
      if (healthId) clearInterval(healthId);
      if (es) es.close();
    };
  }, [interval, refresh, pushStats]);

  return { stats, health, error, history, refresh };
}
