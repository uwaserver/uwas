import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render } from '@testing-library/react';
import { usePolling } from './usePolling';

// ── Test helpers ────────────────────────────────────────────────────────────

/**
 * Renders a component that calls usePolling and records calls in a ref so we
 * can assert on call counts across renders.
 */
function setupPolling(fn: () => void, intervalMs: number | null, deps: ReadonlyArray<unknown> = []) {
  const callCount = { current: 0 };
  const wrappedFn = () => {
    callCount.current++;
    fn();
  };
  render(<PollingHarness fn={wrappedFn} interval={intervalMs} deps={deps} />);
  return callCount;
}

function PollingHarness({
  fn,
  interval,
  deps,
}: { fn: () => void; interval: number | null; deps: ReadonlyArray<unknown> }) {
  usePolling(fn, interval, deps);
  return null;
}

describe('usePolling', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    // Ensure document is not hidden
    Object.defineProperty(document, 'hidden', { value: false, writable: true });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('calls fn immediately on mount', () => {
    const fn = vi.fn();
    setupPolling(fn, 1000);

    // Immediate call should happen on the first tick
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it('calls fn repeatedly at the given interval', async () => {
    const fn = vi.fn();
    setupPolling(fn, 1000);

    expect(fn).toHaveBeenCalledTimes(1); // immediate

    vi.advanceTimersByTime(1000);
    expect(fn).toHaveBeenCalledTimes(2);

    vi.advanceTimersByTime(1000);
    expect(fn).toHaveBeenCalledTimes(3);
  });

  it('does not call fn when interval is null', () => {
    const fn = vi.fn();
    setupPolling(fn, null);

    expect(fn).not.toHaveBeenCalled();

    vi.advanceTimersByTime(10000);
    expect(fn).not.toHaveBeenCalled();
  });

  it('stops polling when interval becomes null', () => {
    const fn = vi.fn();
    const { rerender } = render(<PollingHarness fn={fn} interval={1000} deps={[]} />);

    expect(fn).toHaveBeenCalledTimes(1);

    // Rerender with null interval
    rerender(<PollingHarness fn={fn} interval={null} deps={[]} />);

    vi.advanceTimersByTime(5000);
    // Should not have been called again after the interval was set to null
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it('stops polling when the document is hidden', () => {
    const fn = vi.fn();
    setupPolling(fn, 500);

    const immediateCall = 1;
    expect(fn).toHaveBeenCalledTimes(immediateCall);

    // Document becomes hidden
    Object.defineProperty(document, 'hidden', { value: true, writable: true });
    document.dispatchEvent(new Event('visibilitychange'));

    vi.advanceTimersByTime(5000);
    // No additional calls after hiding
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it('resumes polling with an immediate call when document becomes visible again', () => {
    const fn = vi.fn();
    setupPolling(fn, 1000);

    expect(fn).toHaveBeenCalledTimes(1);

    // Hide
    Object.defineProperty(document, 'hidden', { value: true, writable: true });
    document.dispatchEvent(new Event('visibilitychange'));

    vi.advanceTimersByTime(5000);
    expect(fn).toHaveBeenCalledTimes(1); // no calls while hidden

    // Show
    Object.defineProperty(document, 'hidden', { value: false, writable: true });
    document.dispatchEvent(new Event('visibilitychange'));

    // Immediate call on resume
    expect(fn).toHaveBeenCalledTimes(2);

    // Ticks continue
    vi.advanceTimersByTime(1000);
    expect(fn).toHaveBeenCalledTimes(3);
  });

  it('cleans up interval and event listener on unmount', () => {
    const fn = vi.fn();
    const { unmount } = render(<PollingHarness fn={fn} interval={1000} deps={[]} />);

    expect(fn).toHaveBeenCalledTimes(1);

    unmount();

    vi.advanceTimersByTime(5000);
    // No more calls after unmount
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it('restarts polling when deps change', () => {
    const fn = vi.fn();
    const { rerender } = render(<PollingHarness fn={fn} interval={1000} deps={['a']} />);

    expect(fn).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(1000);
    expect(fn).toHaveBeenCalledTimes(2);

    // Change deps — effect re-runs, immediate call
    rerender(<PollingHarness fn={fn} interval={1000} deps={['b']} />);
    // The immediate tick fires, plus we consumed one tick already pending
    vi.advanceTimersByTime(0); // flush pending microtask from effect re-run
    expect(fn).toHaveBeenCalledTimes(3);

    vi.advanceTimersByTime(1000);
    expect(fn).toHaveBeenCalledTimes(4);
  });
});
