import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { useStats } from './useStats';

// ── Mocks ──────────────────────────────────────────────────────────────────

const mockFetchStats = vi.fn();
const mockFetchHealth = vi.fn();
const mockSseStatsURL = vi.fn();

let currentEventSource: EventSourceMock | null = null;

class EventSourceMock {
  onopen: (() => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onerror: (() => void) | null = null;
  close = vi.fn();
  readyState = 0;

  url: string;

  constructor(url: string) {
    this.url = url;
    // eslint-disable-next-line @typescript-eslint/no-this-alias -- capture mock instance for test assertions
    currentEventSource = this;
    // Defer onopen so the hook has time to attach the handler.
    Promise.resolve().then(() => this.onopen?.());
  }
}

vi.mock('@/lib/api', () => ({
  fetchStats: () => mockFetchStats(),
  fetchHealth: () => mockFetchHealth(),
  sseStatsURL: () => mockSseStatsURL(),
}));

// ── Fixtures ────────────────────────────────────────────────────────────────

const defaultStats = {
  requests_total: 1000,
  cache_hits: 800,
  cache_misses: 200,
  active_conns: 42,
  bytes_sent: 5_000_000,
  uptime: '3d 12h',
  slow_requests: 3,
  latency_p50_ms: 15,
  latency_p95_ms: 50,
  latency_p99_ms: 120,
  latency_max_ms: 500,
};

const defaultHealth = { status: 'healthy', uptime: '3d 12h' };

function sseSend(overrides: Partial<typeof defaultStats> = {}) {
  const data = JSON.stringify({ ...defaultStats, ...overrides });
  currentEventSource?.onmessage?.(new MessageEvent('message', { data }));
}

// ── Tests ───────────────────────────────────────────────────────────────────

describe('useStats', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.stubGlobal('EventSource', EventSourceMock);
    mockSseStatsURL.mockResolvedValue('/api/v1/sse/stats?ticket=abc');
    mockFetchHealth.mockResolvedValue(defaultHealth);
    mockFetchStats.mockResolvedValue(defaultStats);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    currentEventSource = null;
  });

  // ── SSE path ─────────────────────────────────────────────────────────────

  it('connects via SSE on mount', async () => {
    renderHook(() => useStats(3000));

    await waitFor(() => {
      expect(mockSseStatsURL).toHaveBeenCalled();
    });
    expect(currentEventSource).not.toBeNull();
    expect(currentEventSource!.url).toContain('/api/v1/sse/stats');
  });

  it('receives stats via SSE messages', async () => {
    const { result } = renderHook(() => useStats(3000));

    await waitFor(() => expect(currentEventSource).not.toBeNull());

    act(() => sseSend({ requests_total: 500 }));

    expect(result.current.stats?.requests_total).toBe(500);
    expect(result.current.error).toBeNull();
  });

  it('fetches health once on SSE open', async () => {
    mockFetchHealth.mockResolvedValue(defaultHealth);
    renderHook(() => useStats(3000));

    await waitFor(() => {
      expect(mockFetchHealth).toHaveBeenCalled();
    });
  });

  it('reflects health data in returned state', async () => {
    mockFetchHealth.mockResolvedValue({ status: 'degraded', uptime: '3d 12h' });
    const { result } = renderHook(() => useStats(3000));

    await waitFor(() => {
      expect(result.current.health?.status).toBe('degraded');
    });
  });

  it('polls health independently on interval during SSE mode', async () => {
    vi.useFakeTimers();
    mockFetchHealth.mockResolvedValue({ status: 'healthy', uptime: 'initial' });

    const { result } = renderHook(() => useStats(3000));

    // Wait for SSE to connect and health to be fetched on onopen
    await vi.waitFor(() => {
      expect(mockFetchHealth).toHaveBeenCalledTimes(1);
    });
    await vi.waitFor(() => {
      expect(result.current.health?.uptime).toBe('initial');
    });

    // Advance one health interval — should fetch health again
    mockFetchHealth.mockResolvedValue({ status: 'healthy', uptime: 'updated' });
    await vi.advanceTimersByTimeAsync(3000);
    expect(mockFetchHealth).toHaveBeenCalledTimes(2);

    await vi.waitFor(() => {
      expect(result.current.health?.uptime).toBe('updated');
    });

    // Advance another interval to confirm it keeps firing
    mockFetchHealth.mockResolvedValue({ status: 'healthy', uptime: 'another-tick' });
    await vi.advanceTimersByTimeAsync(3000);
    expect(mockFetchHealth).toHaveBeenCalledTimes(3);

    await vi.waitFor(() => {
      expect(result.current.health?.uptime).toBe('another-tick');
    });

    // Verify fetchStats was NEVER called (stats come via SSE, not polling)
    expect(mockFetchStats).not.toHaveBeenCalled();

    // Verify SSE still works — stats come via messages
    act(() => sseSend({ requests_total: 500 }));
    expect(result.current.stats?.requests_total).toBe(500);

    vi.useRealTimers();
  });

  // ── Polling fallback (SSE fails) ────────────────────────────────────────

  it('falls back to polling when sseStatsURL rejects', async () => {
    mockSseStatsURL.mockRejectedValue(new Error('no ticket'));
    mockFetchStats.mockResolvedValue(defaultStats);
    mockFetchHealth.mockResolvedValue(defaultHealth);

    const { result } = renderHook(() => useStats(3000));

    await waitFor(() => {
      expect(result.current.stats?.requests_total).toBe(1000);
    });
    expect(result.current.health?.status).toBe('healthy');
  });

  it('polls on interval after SSE fallback', async () => {
    vi.useFakeTimers();
    mockSseStatsURL.mockRejectedValue(new Error('fail'));
    mockFetchStats.mockResolvedValue({ ...defaultStats, requests_total: 100 });
    mockFetchHealth.mockResolvedValue(defaultHealth);

    const { result } = renderHook(() => useStats(3000));
    await vi.advanceTimersByTimeAsync(10);

    // First poll
    await vi.waitFor(() => {
      expect(result.current.stats?.requests_total).toBe(100);
    });

    // Next poll cycle
    mockFetchStats.mockResolvedValue({ ...defaultStats, requests_total: 200 });
    await vi.advanceTimersByTimeAsync(3000);
    await vi.waitFor(() => {
      expect(result.current.stats?.requests_total).toBe(200);
    });

    vi.useRealTimers();
  });

  it('closes EventSource when falling back to polling', async () => {
    mockFetchStats.mockResolvedValue(defaultStats);
    mockFetchHealth.mockResolvedValue(defaultHealth);

    renderHook(() => useStats(3000));
    await waitFor(() => expect(currentEventSource).not.toBeNull());

    const closeSpy = currentEventSource!.close;

    // Trigger SSE error → fallback
    act(() => { currentEventSource!.onerror?.(); });

    await waitFor(() => {
      expect(mockFetchStats).toHaveBeenCalled();
    });
    expect(closeSpy).toHaveBeenCalledTimes(1);
  });

  // ── Error handling ──────────────────────────────────────────────────────

  it('surfaces error when polling fetch fails', async () => {
    mockSseStatsURL.mockRejectedValue(new Error('fail'));
    mockFetchStats.mockRejectedValue(new Error('server error'));
    mockFetchHealth.mockRejectedValue(new Error('server error'));

    const { result } = renderHook(() => useStats(3000));

    await waitFor(() => {
      expect(result.current.error).toBe('server error');
    });
  });

  it('clears error on subsequent successful fetch', async () => {
    mockSseStatsURL.mockRejectedValue(new Error('fail'));
    mockFetchStats.mockRejectedValue(new Error('temp error'));
    mockFetchHealth.mockRejectedValue(new Error('temp error'));

    const { result } = renderHook(() => useStats(3000));

    await waitFor(() => {
      expect(result.current.error).toBe('temp error');
    });

    // Recover
    mockFetchStats.mockResolvedValue(defaultStats);
    mockFetchHealth.mockResolvedValue(defaultHealth);
    await act(async () => { await result.current.refresh(); });

    await waitFor(() => {
      expect(result.current.error).toBeNull();
    });
  });

  // ── History ──────────────────────────────────────────────────────────────

  it('accumulates stats history from SSE messages', async () => {
    const { result } = renderHook(() => useStats(3000));

    await waitFor(() => expect(currentEventSource).not.toBeNull());

    act(() => sseSend({ requests_total: 100 }));
    act(() => sseSend({ requests_total: 200 }));

    expect(result.current.history).toHaveLength(2);
    expect(result.current.history[0].requests).toBe(100);
    expect(result.current.history[1].requests).toBe(200);
    expect(result.current.history[0].p95).toBe(50);
  });

  it('caps history at 30 entries', async () => {
    const { result } = renderHook(() => useStats(3000));

    await waitFor(() => expect(currentEventSource).not.toBeNull());

    for (let i = 0; i < 35; i++) {
      act(() => sseSend({ requests_total: i }));
    }

    expect(result.current.history).toHaveLength(30);
    expect(result.current.history[29].requests).toBe(34);
  });

  // ── Cleanup ──────────────────────────────────────────────────────────────

  it('closes EventSource on unmount', async () => {
    const { unmount } = renderHook(() => useStats(3000));

    await waitFor(() => expect(currentEventSource).not.toBeNull());
    const closeSpy = currentEventSource!.close;

    unmount();

    expect(closeSpy).toHaveBeenCalled();
  });

  // ── refresh ──────────────────────────────────────────────────────────────

  it('exposes a refresh function to manually fetch stats + health', async () => {
    mockFetchStats.mockResolvedValue(defaultStats);
    mockFetchHealth.mockResolvedValue(defaultHealth);

    const { result } = renderHook(() => useStats(3000));

    await act(async () => {
      await result.current.refresh();
    });

    expect(mockFetchStats).toHaveBeenCalled();
    expect(mockFetchHealth).toHaveBeenCalled();
  });

  it('updates stats after manual refresh', async () => {
    mockFetchStats.mockResolvedValue({ ...defaultStats, requests_total: 777 });
    mockFetchHealth.mockResolvedValue(defaultHealth);

    const { result } = renderHook(() => useStats(3000));

    await act(async () => {
      await result.current.refresh();
    });

    expect(result.current.stats?.requests_total).toBe(777);
  });

  it('pushes history entry on refresh', async () => {
    mockFetchStats.mockResolvedValue(defaultStats);
    mockFetchHealth.mockResolvedValue(defaultHealth);

    const { result } = renderHook(() => useStats(3000));

    await act(async () => {
      await result.current.refresh();
    });

    expect(result.current.history.length).toBe(1);
  });

  // ── Edge cases ──────────────────────────────────────────────────────────

  it('ignores unparseable SSE messages', async () => {
    const { result } = renderHook(() => useStats(3000));

    await waitFor(() => expect(currentEventSource).not.toBeNull());

    act(() => {
      currentEventSource!.onmessage?.(new MessageEvent('message', { data: 'invalid json' }));
    });

    expect(result.current.stats).toBeNull();
  });
});
