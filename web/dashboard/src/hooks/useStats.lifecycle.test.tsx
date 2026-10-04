import { it, expect, vi, afterEach } from 'vitest';
import { renderHook, act, cleanup } from '@testing-library/react';
import { useStats } from '@/hooks/useStats';

const mocks = vi.hoisted(() => ({ health: vi.fn(), stats: vi.fn(), url: vi.fn() }));
vi.mock('@/lib/api', () => ({ fetchHealth: mocks.health, fetchStats: mocks.stats, sseStatsURL: mocks.url }));
const streams: MockStream[] = [];
class MockStream {
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  close = vi.fn();
  constructor() { streams.push(this); }
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((r, j) => { resolve = r; reject = j; });
  return { promise, resolve, reject };
}
afterEach(() => { cleanup(); vi.useRealTimers(); vi.unstubAllGlobals(); streams.length = 0; vi.resetAllMocks(); });
it('an old effect health response cannot replace a new effect response', async () => {
  vi.useFakeTimers();
  vi.stubGlobal('EventSource', MockStream);
  mocks.url.mockResolvedValue('/sse');
  const old = deferred<{ status: string; uptime: string }>();
  mocks.health.mockReturnValueOnce(old.promise).mockResolvedValue({ status: 'healthy', uptime: 'new' });
  const hook = renderHook(({ interval }) => useStats(interval), { initialProps: { interval: 1000 } });
  await act(async () => { await Promise.resolve(); });
  expect(streams).toHaveLength(1);
  act(() => { streams[0].onopen?.(); });
  hook.rerender({ interval: 2000 });
  await act(async () => { await Promise.resolve(); });
  expect(streams[0].close).toHaveBeenCalled();
  await act(async () => { streams[1].onopen?.(); await Promise.resolve(); });

  expect(hook.result.current.health?.uptime).toBe('new');
  await act(async () => { old.resolve({ status: 'degraded', uptime: 'old' }); await old.promise; });
  const actual = hook.result.current.health?.uptime;

  expect(actual).toBe('new');

});

it('closed stream callbacks cannot start polling', async () => {
  vi.useFakeTimers(); vi.stubGlobal('EventSource', MockStream); mocks.url.mockResolvedValue('/sse');
  const hook = renderHook(() => useStats(1000));
  await act(async () => { await Promise.resolve(); });
  const old = streams[0]; hook.unmount();
  await act(async () => { old.onopen?.(); old.onerror?.(); old.onmessage?.(new MessageEvent('message', { data: '{"requests_total":99}' })); });
  expect(mocks.health).not.toHaveBeenCalled(); expect(mocks.stats).not.toHaveBeenCalled(); expect(vi.getTimerCount()).toBe(0);

});
it.each(['success', 'failure'])('stale polling %s cannot publish after restart', async (outcome) => {
  vi.useFakeTimers(); mocks.url.mockRejectedValue(new Error('no SSE'));
  const old = deferred<{ requests_total: number }>();
  mocks.stats.mockReturnValueOnce(old.promise).mockResolvedValue({ requests_total: 20 });
  mocks.health.mockResolvedValue({ status: 'healthy', uptime: 'new' });
  const hook = renderHook(({ interval }) => useStats(interval), { initialProps: { interval: 1000 } });
  await act(async () => { await Promise.resolve(); });
  hook.rerender({ interval: 2000 });
  await act(async () => { await Promise.resolve(); });
  expect(hook.result.current.stats?.requests_total).toBe(20);
  await act(async () => {
    if (outcome === 'success') old.resolve({ requests_total: 10 }); else old.reject(new Error('stale error'));
    await old.promise.catch(() => {});
  });
  expect(hook.result.current.stats?.requests_total).toBe(20); expect(hook.result.current.error).toBeNull();

});
