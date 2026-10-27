import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  fetchStats, importDatabase, fetchConfigExport, deployAppLive, logout,
  getAuthHeaders, setToken, setTOTPCode,
} from './api';

// F38600: with 2FA enabled the server's gate asks every admin request (any
// path except /auth/2fa/) for X-TOTP-Code and answers 403 "2fa_required"
// otherwise. api(), uploadFile, migrateCPanel and fetchMetrics add the code;
// the hand-rolled fetches (database import, config export, live deploy,
// logout, and the pages that spread getAuthHeaders() for file/DB downloads)
// only sent the auth token, so for a 2FA admin they always failed with
// "2fa_required" (logout's server-side session revocation silently with it).

function totpHeader(call: unknown[]): string | undefined {
  const init = call[1] as { headers?: Record<string, string> } | undefined;
  return init?.headers?.['X-TOTP-Code'];
}

describe('2FA code on hand-rolled fetches', () => {
  let f: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    vi.restoreAllMocks();
    setToken('tok', 'api_key');
    setTOTPCode('123456');
    f = vi.fn(async () => new Response(JSON.stringify({ status: 'ok', log: '' }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }));
    vi.stubGlobal('fetch', f);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    setToken('');
  });

  it('control: api() sends the stored code', async () => {
    await fetchStats();
    expect(totpHeader(f.mock.calls[0])).toBe('123456');
  });

  it('getAuthHeaders() carries the second factor with the token', () => {
    expect(getAuthHeaders()['X-TOTP-Code']).toBe('123456');
    expect(getAuthHeaders().Authorization).toBe('Bearer tok');
  });

  it('control: no token means no headers at all', () => {
    setToken('');
    expect(Object.keys(getAuthHeaders())).toHaveLength(0);
  });

  it('importDatabase sends the stored code', async () => {
    await importDatabase('db1', new File(['SELECT 1;'], 'd.sql'));
    expect(totpHeader(f.mock.calls[0])).toBe('123456');
  });

  it('fetchConfigExport sends the stored code', async () => {
    f.mockImplementation(async () => new Response('a: 1\n', { status: 200 }));
    const RealURL = URL;
    vi.stubGlobal('URL', class extends RealURL {
      static createObjectURL = vi.fn(() => 'blob:x');
      static revokeObjectURL = vi.fn();
    });
    await fetchConfigExport();
    expect(totpHeader(f.mock.calls[0])).toBe('123456');
  });

  it('deployAppLive sends the stored code', async () => {
    await deployAppLive('app', {} as never, () => {});
    expect(totpHeader(f.mock.calls[0])).toBe('123456');
  });

  it('logout sends the stored code so the server can revoke the session', async () => {
    await logout();
    expect(totpHeader(f.mock.calls[0])).toBe('123456');
  });
});
