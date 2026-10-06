import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import Cloudflare from './Cloudflare';
import { ConfirmContext } from '@/components/useConfirm';

// ── API boundary mock (only the boundary — the real component runs) ─────────
const apiMocks = vi.hoisted(() => ({
  fetchCloudflareStatus: vi.fn(),
  connectCloudflare: vi.fn(),
  disconnectCloudflare: vi.fn(),
  purgeCloudflareCache: vi.fn(),
  fetchCloudflareZones: vi.fn(),
  importCloudflareZone: vi.fn(),
  fetchCloudflareTunnels: vi.fn(),
  createCloudflareTunnel: vi.fn(),
  deleteCloudflareTunnel: vi.fn(),
  startCloudflareTunnel: vi.fn(),
  stopCloudflareTunnel: vi.fn(),
  fetchCloudflareTunnelLogs: vi.fn(),
  installCloudflared: vi.fn(),
  fetchCloudflareIPs: vi.fn(),
  updateCloudflareIPs: vi.fn(),
  syncCloudflareIPs: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

const confirmStub = {
  confirmAction: vi.fn(async () => true),
  promptText: vi.fn(async () => null),
};

const flush = () => act(async () => {});

const STATUS = {
  connected: true,
  email: 'admin@example.com',
  cloudflared_installed: true,
  cloudflared_version: '2024.1.0',
};

const ZONES = [
  { id: 'zA', name: 'a.example', status: 'active', plan: 'free' },
  { id: 'zB', name: 'b.example', status: 'active', plan: 'free' },
];

// Render helper
function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/cloudflare']}>
      <ConfirmContext.Provider value={confirmStub}>
        <Cloudflare />
      </ConfirmContext.Provider>
    </MemoryRouter>,
  );
}

const importButtons = () =>
  screen.getAllByRole('button', { name: /Import to UWAS/ });

describe('Cloudflare import drawer: a superseded zone preview must not evict the open one', () => {
  let calls: number;
  let gates: Array<{
    zoneId: string;
    resolve: (v: unknown) => void;
    reject: (e: unknown) => void;
  }>;

  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => {
      if (vi.isMockFunction(fn)) fn.mockReset();
    });
    confirmStub.confirmAction.mockClear();
    calls = 0;
    gates = [];
    apiMocks.fetchCloudflareStatus.mockResolvedValue(STATUS);
    apiMocks.fetchCloudflareZones.mockResolvedValue(ZONES);
    apiMocks.fetchCloudflareTunnels.mockResolvedValue([]);
    apiMocks.fetchCloudflareIPs.mockResolvedValue({ ip_ranges: [], count: 0 });
    // Each dry-run parks on a hand-controlled gate; every invocation counted.
    apiMocks.importCloudflareZone.mockImplementation((zoneId: string) => {
      calls += 1;
      return new Promise((resolve, reject) => {
        gates.push({ zoneId, resolve, reject });
      });
    });
  });

  it('control: a single-zone preview commits against that zone', async () => {
    renderPage();
    await flush();

    fireEvent.click(importButtons()[0]); // a.example
    await flush();
    expect(calls).toBe(1);
    // Dry-run in flight: the drawer shows its loading state.
    expect(screen.getByText('Reading DNS records from Cloudflare...')).toBeTruthy();

    await act(async () => {
      gates[0].resolve({ added: ['shop.a.example'], skipped: [] });
    });
    await flush();
    expect(screen.getByText('shop.a.example')).toBeTruthy();

    const commit = screen.getByRole('button', { name: /Add 1 site/ });
    fireEvent.click(commit);
    await flush();
    expect(apiMocks.importCloudflareZone).toHaveBeenLastCalledWith(
      'zA',
      'static',
      '/var/www/{host}/public_html',
      { hostnames: ['shop.a.example'] },
    );
  });

  it('keeps zone B\u2019s preview when zone A\u2019s slower dry-run lands last', async () => {
    renderPage();
    await flush();

    // Open the import drawer for a.example — its dry-run hangs (slow API).
    fireEvent.click(importButtons()[0]);
    await flush();
    expect(calls).toBe(1);

    // While it is in flight, open the drawer for b.example instead.
    fireEvent.click(importButtons()[1]);
    await flush();
    expect(calls).toBe(2);
    expect(gates[1].zoneId).toBe('zB');

    // b.example's dry-run resolves first — the drawer shows its preview.
    await act(async () => {
      gates[1].resolve({ added: ['shop.b.example'], skipped: [] });
    });
    await flush();
    expect(screen.getByText('shop.b.example')).toBeTruthy();
    expect(screen.getByRole('button', { name: /Add 1 site/ })).toBeTruthy();

    // a.example's stale dry-run lands LAST. It belongs to a closed drawer
    // and must be discarded — not overwrite the open drawer's preview.
    await act(async () => {
      gates[0].resolve({ added: ['stale.a.example'], skipped: [] });
    });
    await flush();

    // RED pre-fix: A's response replaced importPreview (zoneId: zA), so the
    // open zB drawer hides its whole body — no hostnames, no commit button,
    // no spinner, no error. The drawer wedges until the user double-toggles.
    expect(screen.getByText('shop.b.example')).toBeTruthy();
    expect(screen.getByRole('button', { name: /Add 1 site/ })).toBeTruthy();
  });

  it('commit still targets the open zone after a superseded preview lands', async () => {
    renderPage();
    await flush();

    fireEvent.click(importButtons()[0]);
    await flush();
    fireEvent.click(importButtons()[1]);
    await flush();
    await act(async () => {
      gates[1].resolve({ added: ['shop.b.example'], skipped: [] });
    });
    await flush();
    await act(async () => {
      gates[0].resolve({ added: ['stale.a.example'], skipped: [] });
    });
    await flush();

    fireEvent.click(screen.getByRole('button', { name: /Add 1 site/ }));
    await flush();

    // The commit must carry zone B's id and B's selected hostnames — the
    // stale preview belonged to zone A and must not leak into the write.
    expect(apiMocks.importCloudflareZone).toHaveBeenLastCalledWith(
      'zB',
      'static',
      '/var/www/{host}/public_html',
      { hostnames: ['shop.b.example'] },
    );
  });
});
