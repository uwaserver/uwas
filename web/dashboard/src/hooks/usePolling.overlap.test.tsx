import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render } from '@testing-library/react';
import { usePolling } from './usePolling';

// F39303: a poll whose request is still in flight when the next tick fires was
// started again, so a slow endpoint piled up requests and their answers came
// back out of order. A request that never settles must not stop polling
// forever, so a tick is only skipped for a bounded time.

function Harness({ fn, interval }: { fn: () => void | Promise<void>; interval: number }) {
  usePolling(fn, interval);
  return null;
}

describe('usePolling overlapping ticks', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    Object.defineProperty(document, 'hidden', { value: false, writable: true });
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('skips a tick while the previous call is still pending', async () => {
    let resolve!: () => void;
    const fn = vi.fn(() => new Promise<void>((r) => { resolve = r; }));
    render(<Harness fn={fn} interval={1000} />);
    expect(fn).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(3000);
    expect(fn).toHaveBeenCalledTimes(1); // EXPECTED: no overlapping request

    resolve();
    await vi.advanceTimersByTimeAsync(1000);
    expect(fn).toHaveBeenCalledTimes(2); // control: polling resumes once it settles
  });

  it('polls again after a call that never settles', async () => {
    const fn = vi.fn(() => new Promise<void>(() => {}));
    render(<Harness fn={fn} interval={1000} />);
    expect(fn).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(31_000);
    expect(fn.mock.calls.length).toBeGreaterThanOrEqual(2); // a hung request must not stop the poll for good
  });

  it('keeps polling synchronous callbacks every tick', async () => {
    const fn = vi.fn();
    render(<Harness fn={fn} interval={1000} />);
    await vi.advanceTimersByTimeAsync(3000);
    expect(fn).toHaveBeenCalledTimes(4);
  });

  it('keeps polling after a callback that rejects', async () => {
    const fn = vi.fn(() => Promise.reject(new Error('boom')));
    render(<Harness fn={fn} interval={1000} />);
    await vi.advanceTimersByTimeAsync(3000);
    expect(fn).toHaveBeenCalledTimes(4);
  });
});
