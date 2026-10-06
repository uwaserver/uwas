import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { importDatabase } from './api';

// importDatabase must transfer the dump's BYTES faithfully. The server
// (internal/admin/database/handler.go Import) reads the raw body and stores
// it as received. SQL dumps are byte streams and are NOT guaranteed UTF-8 —
// latin1-encoded dumps are common for legacy MySQL databases. File.text()
// always decodes as UTF-8 (invalid bytes become U+FFFD), so sending the
// decoded string corrupts every non-UTF-8 byte before the request is sent.

const LATIN1_SQL = "INSERT INTO t VALUES ('café');\nINSERT INTO u VALUES ('naïve');\n";
const UTF8_SQL = "INSERT INTO v VALUES ('žluťoučký');\n";

// latin1 is code points 0-255 by definition, so charCodeAt yields the exact byte.
const latin1Bytes = new Uint8Array([...LATIN1_SQL].map(ch => ch.charCodeAt(0)));
const utf8Bytes = new TextEncoder().encode(UTF8_SQL);

async function bodyBytes(body: BodyInit | null | undefined): Promise<Uint8Array> {
  if (typeof body === 'string') return new TextEncoder().encode(body);
  if (body instanceof ArrayBuffer) return new Uint8Array(body);
  if (ArrayBuffer.isView(body)) return new Uint8Array(body.buffer, body.byteOffset, body.byteLength);
  // Blob / File fallback
  return new Uint8Array(await (body as Blob).arrayBuffer());
}

describe('importDatabase byte fidelity', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('control: a UTF-8 dump round-trips byte-identically', async () => {
    let captured: BodyInit | null | undefined;
    vi.stubGlobal('fetch', vi.fn(async (_url: string | URL, init?: RequestInit) => {
      captured = init?.body;
      return { ok: true, json: async () => ({ status: 'imported' }) };
    }));
    const file = new File([utf8Bytes], 'dump.sql');
    const res = await importDatabase('testdb', file);
    expect(res.status).toBe('imported');
    const sent = await bodyBytes(captured);
    expect(Array.from(sent)).toEqual(Array.from(utf8Bytes));
    expect(mockedUrl()).toContain('/api/v1/database/testdb/import');
  });

  it('transfers a latin1 dump byte-faithfully (no UTF-8 re-decoding)', async () => {
    let captured: BodyInit | null | undefined;
    vi.stubGlobal('fetch', vi.fn(async (_url: string | URL, init?: RequestInit) => {
      captured = init?.body;
      return { ok: true, json: async () => ({ status: 'imported' }) };
    }));
    const file = new File([latin1Bytes], 'dump.sql');
    await importDatabase('testdb', file);
    const sent = await bodyBytes(captured);
    // CONTRACT: the bytes on the wire are exactly the file's bytes.
    expect(Array.from(sent)).toEqual(Array.from(latin1Bytes));
  });
});

function mockedUrl(): string {
  const f = (globalThis.fetch as ReturnType<typeof vi.fn>);
  const call = f.mock.calls[0];
  return typeof call?.[0] === 'string' ? call[0] : String(call?.[0] ?? '');
}
