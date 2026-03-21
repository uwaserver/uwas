import { useState, useEffect, useCallback } from 'react';
import { fetchStats, fetchHealth, type StatsData, type HealthData } from '@/lib/api';

export function useStats(interval = 3000) {
  const [stats, setStats] = useState<StatsData | null>(null);
  const [health, setHealth] = useState<HealthData | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [history, setHistory] = useState<{ time: string; requests: number; cacheHits: number }[]>([]);

  const refresh = useCallback(async () => {
    try {
      const [s, h] = await Promise.all([fetchStats(), fetchHealth()]);
      setStats(s);
      setHealth(h);
      setError(null);
      setHistory(prev => {
        const next = [...prev, {
          time: new Date().toLocaleTimeString(),
          requests: s.requests_total,
          cacheHits: s.cache_hits,
        }];
        return next.slice(-30); // keep last 30 data points
      });
    } catch (e) {
      setError((e as Error).message);
    }
  }, []);

  useEffect(() => {
    refresh();
    const id = setInterval(refresh, interval);
    return () => clearInterval(id);
  }, [refresh, interval]);

  return { stats, health, error, history, refresh };
}
