import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  fetchDomains, fetchAuditLog, fetchPHP, fetchBackups, fetchUsers, fetchFileWorkspaces,
  fetchCronJobs, fetchServices, fetchDatabases, fetchWebhooks, fetchWPSites, fetchPackages,
  fetchTasks, fetchApps, fetchSoftwareTemplates, fetchSoftwareInstances, setToken,
} from './api';

// F50600: the list endpoints are paginated server side (default page 50,
// max 500, oldest first: parsePagination/paginateSlice in internal/admin).
// None of the dashboard fetchers sent a limit, so every list silently stopped
// at its first 50 entries: a server with 120 domains showed 50, and the audit
// log (oldest first) never showed anything newer than the 50th entry. This
// fake server honours limit/offset exactly like the Go handlers do.

const TOTAL = 1203; // not a multiple of the 500 page size

function pagedServer(total: number) {
  const calls: string[] = [];
  const handler = vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), 'http://x');
    calls.push(url.pathname + url.search);
    let limit = 50;
    const l = Number(url.searchParams.get('limit'));
    if (Number.isInteger(l) && l > 0) limit = Math.min(l, 500);
    const offset = Math.max(0, Number(url.searchParams.get('offset')) || 0);
    const items: { id: number }[] = [];
    for (let i = offset; i < Math.min(offset + limit, total); i++) items.push({ id: i });
    return new Response(JSON.stringify({ items, total, limit, offset }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  });
  return { handler, calls };
}

const fetchers: [string, () => Promise<unknown[]>, string][] = [
  ['fetchDomains', fetchDomains, '/api/v1/domains'],
  ['fetchAuditLog', fetchAuditLog, '/api/v1/audit'],
  ['fetchPHP', fetchPHP, '/api/v1/php'],
  ['fetchBackups', fetchBackups, '/api/v1/backups'],
  ['fetchUsers', fetchUsers, '/api/v1/users'],
  ['fetchFileWorkspaces', fetchFileWorkspaces, '/api/v1/files/workspaces'],
  ['fetchCronJobs', fetchCronJobs, '/api/v1/cron'],
  ['fetchServices', fetchServices, '/api/v1/services'],
  ['fetchDatabases', fetchDatabases, '/api/v1/database/list'],
  ['fetchWebhooks', fetchWebhooks, '/api/v1/webhooks'],
  ['fetchWPSites', fetchWPSites, '/api/v1/wordpress/sites'],
  ['fetchPackages', fetchPackages, '/api/v1/packages'],
  ['fetchTasks', fetchTasks, '/api/v1/tasks'],
  ['fetchApps', fetchApps, '/api/v1/apps'],
  ['fetchSoftwareTemplates', fetchSoftwareTemplates, '/api/v1/software/templates'],
  ['fetchSoftwareInstances', fetchSoftwareInstances, '/api/v1/software/instances'],
];

describe('paginated list fetchers return every entry, not the first page', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    setToken('tok', 'api_key');
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    setToken('');
  });

  it.each(fetchers)('%s', async (_name, fetcher, path) => {
    const { handler, calls } = pagedServer(TOTAL);
    vi.stubGlobal('fetch', handler);
    const got = (await fetcher()) as { id: number }[];
    console.log(`EXPECTED: ${TOTAL}\nACTUAL: ${got.length} (${calls.join(', ')})`);
    expect(got).toHaveLength(TOTAL);
    expect(got[0].id).toBe(0);
    expect(got[TOTAL - 1].id).toBe(TOTAL - 1);
    expect(new Set(got.map((e) => e.id)).size).toBe(TOTAL); // no page repeated
    expect(calls.every((c) => c.startsWith(path + '?'))).toBe(true);
  });

  it('control: a short list takes one request', async () => {
    const { handler, calls } = pagedServer(7);
    vi.stubGlobal('fetch', handler);
    expect(await fetchDomains()).toHaveLength(7);
    expect(calls).toHaveLength(1);
  });

  it('control: an empty list is [] and takes one request', async () => {
    const { handler, calls } = pagedServer(0);
    vi.stubGlobal('fetch', handler);
    expect(await fetchApps()).toEqual([]);
    expect(calls).toHaveLength(1);
  });

  it('a server that ignores limit/offset does not loop forever', async () => {
    const f = vi.fn(async () => new Response(JSON.stringify({ items: [{ id: 1 }, { id: 2 }], total: 2 }), { status: 200 }));
    vi.stubGlobal('fetch', f);
    expect(await fetchServices()).toHaveLength(2);
    expect(f).toHaveBeenCalledTimes(1);
  });

  it('a missing or null items field is an empty list', async () => {
    const f = vi.fn(async () => new Response(JSON.stringify({ items: null, total: 0 }), { status: 200 }));
    vi.stubGlobal('fetch', f);
    expect(await fetchBackups()).toEqual([]);
  });
});
