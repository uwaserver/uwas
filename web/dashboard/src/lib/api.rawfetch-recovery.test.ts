import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  fetchConfigExport, deployAppLive, fetchStats, getAuthHeaders, getToken, setToken, setTOTPCode,
} from './api';

// F45600: api(), uploadFile, importDatabase, migrateCPanel and fetchMetrics
// recover from an expired session (401: clear the token, return to login) and
// from a rejected 2FA code (403 "2fa_required"/"invalid 2FA code": drop the
// stored code, return to the 2FA prompt). The remaining hand-rolled fetches —
// the config export, the live deploy stream and the pages' file/DB downloads —
// only turned the response into an error message: the stale token and the
// rejected code stayed in place, so every poller kept presenting them (each
// rejected code counts as a failed authentication) and the operator was left
// on the page with a bare "Unauthorized" / "invalid 2FA code".

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

describe('session and 2FA recovery on hand-rolled fetches', () => {
  let assigned: string[];
  const realLocation = window.location;

  beforeEach(() => {
    vi.restoreAllMocks();
    assigned = [];
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: {
        pathname: '/_uwas/dashboard/settings',
        get href() { return 'http://localhost/_uwas/dashboard/settings'; },
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

  it('control: api() clears the token on 401 and returns to login', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json(401, { error: 'unauthorized' })));
    await expect(fetchStats()).rejects.toThrow('Unauthorized');
    expect(getToken()).toBe('');
    expect(assigned).toEqual(['/_uwas/dashboard/login']);
  });

  it('fetchConfigExport: 401 clears the token and returns to login', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json(401, { error: 'unauthorized' })));
    await expect(fetchConfigExport()).rejects.toThrow('Unauthorized');
    expect(getToken()).toBe('');
    expect(assigned).toEqual(['/_uwas/dashboard/login']);
  });

  it('fetchConfigExport: a rejected 2FA code is dropped and the 2FA prompt opens', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json(403, { error: 'invalid 2FA code' })));
    await expect(fetchConfigExport()).rejects.toThrow('invalid 2FA code');
    expect(assigned).toEqual(['/_uwas/dashboard/login?2fa=required']);
    expect(getAuthHeaders()['X-TOTP-Code']).toBeUndefined();
  });

  it('deployAppLive: 401 clears the token and returns to login', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json(401, { error: 'unauthorized' })));
    await expect(deployAppLive('demo', {}, () => {})).rejects.toThrow();
    expect(getToken()).toBe('');
    expect(assigned).toEqual(['/_uwas/dashboard/login']);
  });

  it('deployAppLive: a rejected 2FA code is dropped and the 2FA prompt opens', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json(403, { error: '2fa_required' })));
    await expect(deployAppLive('demo', {}, () => {})).rejects.toThrow();
    expect(assigned).toEqual(['/_uwas/dashboard/login?2fa=required']);
    expect(getAuthHeaders()['X-TOTP-Code']).toBeUndefined();
  });

  it('control: an ordinary error still surfaces its own message', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json(500, { error: 'boom' })));
    await expect(fetchConfigExport()).rejects.toThrow('boom');
    await expect(deployAppLive('demo', {}, () => {})).rejects.toThrow('boom');
    expect(assigned).toEqual([]);
    expect(getToken()).toBe('tok');
  });
});
