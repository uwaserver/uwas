import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { fetchStats, fetchMetrics, setToken, setTOTPCode } from './api';

// F37600: with 2FA enabled the dashboard keeps the code typed at login and
// sends it as X-TOTP-Code on every request. The server accepts a code for
// about 90 s, after which every request is answered 403 "invalid 2FA code" and
// counted as a failed authentication (10 per minute locks the IP out for 5
// minutes). api() only recognised "2fa_required", so the stale code kept being
// sent by every poller until the operator's address was locked out.

function forbidden(error: string) {
  return new Response(JSON.stringify({ error }), {
    status: 403,
    headers: { 'Content-Type': 'application/json' },
  });
}

function totpHeader(call: unknown[]): string | undefined {
  const init = call[1] as { headers?: Record<string, string> } | undefined;
  return init?.headers?.['X-TOTP-Code'];
}

describe('expired 2FA code', () => {
  let assigned: string[];
  const realLocation = window.location;

  beforeEach(() => {
    vi.restoreAllMocks();
    assigned = [];
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: {
        pathname: '/_uwas/dashboard/domains',
        get href() { return 'http://localhost/_uwas/dashboard/domains'; },
        set href(v: string) { assigned.push(v); },
      },
    });
    setToken('tok', 'api_key');
    setTOTPCode('123456');
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    Object.defineProperty(window, 'location', { configurable: true, value: realLocation });
    setToken('');
  });

  it('control: "2fa_required" drops the code and goes to the 2FA prompt', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => forbidden('2fa_required')));
    await expect(fetchStats()).rejects.toThrow('2FA required');
    expect(assigned).toEqual(['/_uwas/dashboard/login?2fa=required']);
  });

  it('api(): "invalid 2FA code" drops the stale code and goes to the 2FA prompt', async () => {
    const f = vi.fn(async () => forbidden('invalid 2FA code'));
    vi.stubGlobal('fetch', f);
    await expect(fetchStats()).rejects.toThrow();
    expect(assigned).toEqual(['/_uwas/dashboard/login?2fa=required']);
    // The next request must not present the rejected code again.
    f.mockImplementation(async () => forbidden('2fa_required'));
    await expect(fetchStats()).rejects.toThrow();
    expect(totpHeader(f.mock.calls[1] as unknown[])).toBeUndefined();
  });

  it('fetchMetrics(): "invalid 2FA code" drops the stale code too', async () => {
    const f = vi.fn(async () => forbidden('invalid 2FA code'));
    vi.stubGlobal('fetch', f);
    await expect(fetchMetrics()).rejects.toThrow();
    expect(assigned).toEqual(['/_uwas/dashboard/login?2fa=required']);
    f.mockImplementation(async () => forbidden('2fa_required'));
    await expect(fetchMetrics()).rejects.toThrow();
    expect(totpHeader(f.mock.calls[1] as unknown[])).toBeUndefined();
  });

  it('on the login page a wrong code only reports the error (no reload loop)', async () => {
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: {
        pathname: '/_uwas/dashboard/login',
        get href() { return ''; },
        set href(v: string) { assigned.push(v); },
      },
    });
    vi.stubGlobal('fetch', vi.fn(async () => forbidden('invalid 2FA code')));
    await expect(fetchStats()).rejects.toThrow();
    expect(assigned).toEqual([]);
  });
});
