import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import DNS from './DNS';

// ── API boundary mock (only the boundary — the real component runs) ─────────
const apiMocks = vi.hoisted(() => ({
  fetchDomains: vi.fn(),
  checkDNS: vi.fn(),
  fetchDNSRecords: vi.fn(),
  createDNSRecord: vi.fn(),
  updateDNSRecord: vi.fn(),
  deleteDNSRecord: vi.fn(),
  syncDNS: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

// Flush mount effects + in-flight promise chains.
const flush = () => act(async () => {});

const DOMAINS = [
  { id: 'd1', host: 'x.com', type: 'static', ssl: 'off' },
  { id: 'd2', host: 'other.example', type: 'static', ssl: 'off' },
];

const RECORD = {
  id: 'r1', type: 'A', name: '@', content: '203.0.113.7', ttl: 1, proxied: false,
};

describe('DNS deep-link auto-load must not retry in an unbounded loop', () => {
  let calls: number;
  let gates: Array<{ reject: (e: unknown) => void; resolve: (v: unknown) => void }>;

  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => {
      if (vi.isMockFunction(fn)) fn.mockReset();
    });
    calls = 0;
    gates = [];
    apiMocks.fetchDomains.mockResolvedValue(DOMAINS);
    // Counting wrapper stays in place for every test so `calls` is always the
    // true number of fetchDNSRecords invocations. Each attempt parks on a
    // hand-controlled gate unless the test resolves it directly.
    apiMocks.fetchDNSRecords.mockImplementation(
      () =>
        new Promise((resolve, reject) => {
          calls += 1;
          gates.push({ resolve, reject });
        }),
    );
  });

  afterEach(() => {
    window.history.replaceState({}, '', '/');
  });

  it('issues ONE auto-attempt per selection even when the fetch fails', async () => {
    window.history.replaceState({}, '', '/dns?domain=x.com');
    render(
      <MemoryRouter initialEntries={['/dns?domain=x.com']}>
        <DNS />
      </MemoryRouter>,
    );
    await flush();
    // Attempt 1 fired by the deep-link auto-load and is parked.
    expect(calls).toBe(1);
    expect(screen.getByText('Load Records')).toBeTruthy();

    // Attempt 1 fails with the benign "provider not configured"
    // classification — no user input whatsoever. (Pre-fix, each retry also
    // WIPES the not-configured guidance at attempt start, so the panel
    // flickers in and out forever — user-visible loop damage; the stable
    // observable is the request count.)
    await act(async () => {
      gates[0].reject(new Error('501: no dns provider configured'));
    });
    await flush();

    // Attempt 2 must not fire on its own. When the bug is present it fires
    // and parks in gates[1]; fail it too (still no user input) so the loop is
    // demonstrated across two unattended retries. Post-fix no gate appears.
    if (gates[1]) {
      await act(async () => {
        gates[1].reject(new Error('501: no dns provider configured'));
      });
      await flush();
    }

    // Unattended failures later, the contract is still: one auto-attempt
    // per selection. RED pre-fix: actual is 3 — an unbounded fetch loop.
    expect(calls).toBe(1);
  });

  it('control: a successful auto-load runs exactly once and stays once', async () => {
    window.history.replaceState({}, '', '/dns?domain=x.com');
    render(
      <MemoryRouter initialEntries={['/dns?domain=x.com']}>
        <DNS />
      </MemoryRouter>,
    );
    await flush();
    // Resolve the single parked attempt with a record.
    await act(async () => {
      gates[0].resolve({ records: [RECORD] });
    });
    await flush();
    expect(calls).toBe(1);
    expect(screen.getByText('203.0.113.7')).toBeTruthy();
    await flush();
    expect(calls).toBe(1);
  });

  it('control: no auto-load at all without a ?domain= URL param', async () => {
    window.history.replaceState({}, '', '/dns');
    render(
      <MemoryRouter initialEntries={['/dns']}>
        <DNS />
      </MemoryRouter>,
    );
    await flush();
    await flush();
    expect(calls).toBe(0);
  });

  it('manual retry via the Load Records button keeps working', async () => {
    window.history.replaceState({}, '', '/dns?domain=x.com');
    render(
      <MemoryRouter initialEntries={['/dns?domain=x.com']}>
        <DNS />
      </MemoryRouter>,
    );
    await flush();
    await act(async () => {
      gates[0].reject(new Error('501: no dns provider configured'));
    });
    await flush();

    const before = calls;
    fireEvent.click(screen.getByText('Load Records'));
    await flush();
    // The parked manual attempt fired exactly once more.
    expect(calls).toBe(before + 1);
  });
});
