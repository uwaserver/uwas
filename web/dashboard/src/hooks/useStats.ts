import { useState, useEffect, useCallback, useRef } from 'react';
import { fetchStats, fetchHealth, sseStatsURL, type StatsData, type HealthData } from '@/lib/api';

export function useStats(interval = 3000) {
  const [stats, setStats] = useState<StatsData | null>(null);
  const [health, setHealth] = useState<HealthData | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [history, setHistory] = useState<{ time: string; requests: number; cacheHits: number; p95: number }[]>([]);
  const usingSSE = useRef(false);

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
    try {
      const [s, h] = await Promise.all([fetchStats(), fetchHealth()]);
      pushStats(s);
      setHealth(h);
    } catch (e) {
      setError((e as Error).message);
    }
  }, [pushStats]);

  useEffect(() => {
    let pollingId: ReturnType<typeof setInterval> | null = null;
    let es: EventSource | null = null;

    function startPolling() {
      usingSSE.current = false;
      refresh();
      pollingId = setInterval(refresh, interval);
    }

    async function startSSE() {
      try {
        const url = await sseStatsURL();
        es = new EventSource(url);

        es.onmessage = (event) => {
          try {
            const s: StatsData = JSON.parse(event.data);
            pushStats(s);
          } catch {
            // ignore parse errors
          }
        };

        es.onopen = () => {
          usingSSE.current = true;
          // SSE only sends stats; fetch health once and then periodically.
          fetchHealth().then(setHealth).catch(() => {});
        };

        es.onerror = () => {
          // SSE failed — close and fall back to polling.
          es?.close();
          es = null;
          if (!pollingId) {
            startPolling();
          }
        };
      } catch {
        // EventSource constructor failed — fall back to polling.
        startPolling();
      }

      // Refresh health periodically even when SSE is active (health isn't
      // included in the SSE stream).
      pollingId = setInterval(() => {
        fetchHealth().then(setHealth).catch(() => {});
      }, interval);
    }

    startSSE();

    return () => {
      if (pollingId) clearInterval(pollingId);
      if (es) es.close();
    };
  }, [interval, refresh, pushStats]);

  return { stats, health, error, history, refresh };
}
