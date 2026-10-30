import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { changeAdminPassword, regenAdminApiKey, fetchDockerDBs, setToken, getToken } from './api';

// F57601: the server answers a WRONG current password with 401 (like a failed
// login), but api() treats every 401 as an expired session: it cleared the
// token and bounced the admin to the login page, so a typo while changing a
// password or replacing an API key logged the admin out. A credential-check
// 401 must surface its message and keep the session; a real session-expiry 401
// on the same routes (and every other route) must still log out.

function r401(error: string) {
  return new Response(JSON.stringify({ error }), { status: 401, headers: { 'Content-Type': 'application/json' } });
}

describe('wrong current password is not a session expiry', () => {
  beforeEach(() => { vi.restoreAllMocks(); setToken('tok', 'api_key'); });
  afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); setToken(''); });

  it('control: an ordinary 401 still clears the token', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => r401('admin required')));
    await expect(fetchDockerDBs()).rejects.toThrow('Unauthorized');
    expect(getToken()).toBe('');
  });

  it('control: a 401 "unauthorized" on the password route is still session expiry', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => r401('unauthorized')));
    await expect(changeAdminPassword('a', 'newpassword1!', 'x')).rejects.toThrow('Unauthorized');
    expect(getToken()).toBe('');
  });

  it('changeAdminPassword: wrong current password keeps the session and shows the reason', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => r401('invalid current password')));
    await expect(changeAdminPassword('a', 'newpassword1!', 'wrong')).rejects.toThrow('invalid current password');
    expect(getToken()).toBe('tok');
  });

  it('regenAdminApiKey: wrong current password keeps the session and shows the reason', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => r401('invalid current password')));
    await expect(regenAdminApiKey('a', 'wrong')).rejects.toThrow('invalid current password');
    expect(getToken()).toBe('tok');
  });

  it('lockout message on the password route also keeps the session', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => r401('too many failed attempts; try again later')));
    await expect(changeAdminPassword('a', 'newpassword1!', 'x')).rejects.toThrow('too many failed attempts');
    expect(getToken()).toBe('tok');
  });
});
