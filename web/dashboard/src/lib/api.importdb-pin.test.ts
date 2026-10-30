import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { importDatabase, onPinRequired, setToken } from './api';

// F39000: POST /database/{name}/import runs the uploaded SQL as the database
// administrator (it can DROP DATABASE), so the server now asks for the PIN like
// it does for DELETE /database/{name}. importDatabase is a hand-rolled fetch, so
// it has to run the same PIN prompt-and-retry that api() does; without it every
// import on a PIN-protected panel would fail with a raw "pin_required".

const SQL = 'DROP TABLE t;\nSELECT 1;\n';
const DUMP = new File([new TextEncoder().encode(SQL)], 'dump.sql');

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

describe('importDatabase PIN contract', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    setToken('tok', 'api_key');
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    onPinRequired(null);
    vi.restoreAllMocks();
    setToken('');
  });

  it('prompts for the PIN and retries once with the same bytes', async () => {
    const seen: { pin: string | null; len: number }[] = [];
    vi.stubGlobal('fetch', vi.fn(async (_url: string, init: RequestInit) => {
      const headers = init.headers as Record<string, string>;
      seen.push({ pin: headers['X-Pin-Code'] ?? null, len: (init.body as ArrayBuffer).byteLength });
      return headers['X-Pin-Code'] === '123456'
        ? json(200, { status: 'imported' })
        : json(403, { error: 'pin_required' });
    }));
    onPinRequired((resolve) => resolve('123456'));

    const res = await importDatabase('testdb', DUMP);

    expect(res.status).toBe('imported');
    expect(seen).toEqual([
      { pin: null, len: SQL.length },
      { pin: '123456', len: SQL.length },
    ]);
  });

  it('does not keep the PIN for the next request', async () => {
    const pins: (string | null)[] = [];
    vi.stubGlobal('fetch', vi.fn(async (_url: string, init: RequestInit) => {
      const headers = init.headers as Record<string, string>;
      pins.push(headers['X-Pin-Code'] ?? null);
      return headers['X-Pin-Code'] ? json(200, { status: 'imported' }) : json(403, { error: 'pin_required' });
    }));
    onPinRequired((resolve) => resolve('123456'));
    await importDatabase('testdb', DUMP);
    pins.length = 0;
    await importDatabase('testdb', DUMP);
    expect(pins[0]).toBeNull();
  });

  it('control: no PIN configured, the import goes through on the first request', async () => {
    const fetchMock = vi.fn(async () => json(200, { status: 'imported' }));
    vi.stubGlobal('fetch', fetchMock);
    const res = await importDatabase('testdb', DUMP);
    expect(res.status).toBe('imported');
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('a rejected PIN prompt surfaces as an error without a second request', async () => {
    const fetchMock = vi.fn(async () => json(403, { error: 'pin_required' }));
    vi.stubGlobal('fetch', fetchMock);
    onPinRequired((_resolve, reject) => reject());
    const outcome = await importDatabase('testdb', DUMP).then(() => 'resolved', () => 'rejected');
    expect(outcome).toBe('rejected');
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});
