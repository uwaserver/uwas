import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { fetchConfigExport } from './api';

// fetchConfigExport must obey the module's bounded-request invariant
// (DEFAULT_REQUEST_TIMEOUT doc: every request is aborted at 30s; the only
// deliberate exceptions are long-lived SSE/WS paths and large-file transfers
// — a YAML config download is neither). Settings.handleExport wedges its
// Export button forever if the fetch never settles.

// A hung endpoint whose fetch honors init.signal (a real browser aborts the
// request when the signal fires). Without a signal it never settles.
function hungFetchHonoringSignal() {
  return vi.fn((_url: string | URL, init?: RequestInit) =>
    new Promise<Response>((_resolve, reject) => {
      if (init?.signal) {
        init.signal.addEventListener('abort', () => {
          reject(new DOMException('The operation was aborted.', 'AbortError'));
        });
      }
    }));
}

describe('fetchConfigExport request timeout', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('control: a healthy endpoint exports successfully', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('server:\n  name: uwas\n', { status: 200 })));
    const createObjectURL = vi.fn(() => 'blob:mock');
    const revokeObjectURL = vi.fn();
    const RealURL = URL;
    vi.stubGlobal('URL', class extends RealURL {
      static createObjectURL = createObjectURL;
      static revokeObjectURL = revokeObjectURL;
    });

    await expect(fetchConfigExport()).resolves.toBeUndefined();
    const url = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0][0];
    expect(String(url)).toContain('/api/v1/config/export');
    expect(createObjectURL).toHaveBeenCalled();
    expect(revokeObjectURL).toHaveBeenCalled();
  });

  it('aborts a hung endpoint at the 30s request timeout', async () => {
    const fetchMock = hungFetchHonoringSignal();
    vi.stubGlobal('fetch', fetchMock);

    const p = fetchConfigExport();
    let outcome = 'pending';
    let rejection: unknown;
    // Real-timer race: post-fix the module's 30s abort fires and the export
    // rejects; the sentinel at 31.5s bounds the wait in both worlds.
    const outcomePromise = Promise.race([
      p.then(
        () => 'resolved',
        (e: unknown) => { outcome = 'rejected'; rejection = e; return 'rejected'; },
      ),
      new Promise<string>(r => setTimeout(() => r('pending'), 31_500)),
    ]);

    // CONTRACT (the fix's essence): the request must carry the abort signal
    // the module's timeout relies on. Pre-fix this is undefined.
    expect(fetchMock.mock.calls[0][1]?.signal).toBeTruthy();
    expect(await outcomePromise).toBe('rejected');
    expect(outcome).toBe('rejected');
    expect(String(rejection)).toMatch(/timed out/i);
  }, 40_000);
});
