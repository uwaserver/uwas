import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { importDatabase, fetchDockerDBs, setToken, getToken } from './api';

// importDatabase must honor the module's 401 session-expiry contract, which
// every other hand-rolled fetch in api.ts implements (uploadFile, migrateCPanel,
// fetchConfigExport) and api() implements centrally (clearToken + redirect to
// login + throw 'Unauthorized'). On session expiry mid-import, the stale token
// must not strand the admin on a dead Database page with endless identical
// retries. The control exercises that contract through the public api()
// surface (fetchDockerDBs -> api()).

const DUMP = new File([new TextEncoder().encode('SELECT 1;\n')], 'dump.sql');

function unauthorizedResponse() {
  return new Response(JSON.stringify({ error: 'admin required' }), {
    status: 401,
    headers: { 'Content-Type': 'application/json' },
  });
}

describe('importDatabase 401 session-expiry contract', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    setToken('tok', 'api_key');
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    setToken('');
  });

  it('control: the public api() path clears the token and throws Unauthorized on 401', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => unauthorizedResponse()));
    await expect(fetchDockerDBs()).rejects.toThrow('Unauthorized');
    expect(getToken()).toBe('');
  });

  it('control: a healthy import resolves', async () => {
    vi.stubGlobal('fetch', vi.fn(async () =>
      new Response(JSON.stringify({ status: 'imported' }), { status: 200, headers: { 'Content-Type': 'application/json' } })));
    const res = await importDatabase('testdb', DUMP);
    expect(res.status).toBe('imported');
    expect(getToken()).toBe('tok');
  });

  it('throws Unauthorized on 401', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => unauthorizedResponse()));
    await expect(importDatabase('testdb', DUMP)).rejects.toThrow('Unauthorized');
  });

  it('clears the stale token on 401', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => unauthorizedResponse()));
    await importDatabase('testdb', DUMP).catch(() => {});
    // CONTRACT: the stale session must not survive a 401 — the admin has a
    // path back to login instead of endless identical retries.
    expect(getToken()).toBe('');
  });
});
