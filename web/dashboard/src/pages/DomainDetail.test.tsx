import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import { MemoryRouter, Routes, Route, useNavigate } from 'react-router';
import DomainDetail from './DomainDetail';

// ── Mocks ───────────────────────────────────────────────────────────────────

const mockFetchDomainDetail = vi.fn();
const mockUpdateDomain = vi.fn();
const mockFetchDomainStats = vi.fn();
const mockFetchDiskUsage = vi.fn();
const mockFetchDomainAnalytics = vi.fn();
const mockFetchWPSites = vi.fn();
const mockWpSecurityStatus = vi.fn();
const mockWpHarden = vi.fn();
const mockWpListUsers = vi.fn();
const mockWpChangePassword = vi.fn();
const mockWpUpdateCore = vi.fn();
const mockWpUpdatePlugins = vi.fn();
const mockWpFixPermissions = vi.fn();
const mockWpToggleDebug = vi.fn();
const mockWpErrorLog = vi.fn();
const mockWpOptimizeDB = vi.fn();
const mockWpPluginAction = vi.fn();
const mockPreviewCacheControl = vi.fn();

vi.mock('@/lib/api', () => ({
  fetchDomainDetail: (...args: unknown[]) => mockFetchDomainDetail(...args),
  updateDomain: (...args: unknown[]) => mockUpdateDomain(...args),
  fetchDomainStats: (...args: unknown[]) => mockFetchDomainStats(...args),
  fetchDiskUsage: (...args: unknown[]) => mockFetchDiskUsage(...args),
  fetchDomainAnalytics: (...args: unknown[]) => mockFetchDomainAnalytics(...args),
  fetchWPSites: (...args: unknown[]) => mockFetchWPSites(...args),
  wpSecurityStatus: (...args: unknown[]) => mockWpSecurityStatus(...args),
  wpHarden: (...args: unknown[]) => mockWpHarden(...args),
  wpListUsers: (...args: unknown[]) => mockWpListUsers(...args),
  wpChangePassword: (...args: unknown[]) => mockWpChangePassword(...args),
  wpUpdateCore: (...args: unknown[]) => mockWpUpdateCore(...args),
  wpUpdatePlugins: (...args: unknown[]) => mockWpUpdatePlugins(...args),
  wpFixPermissions: (...args: unknown[]) => mockWpFixPermissions(...args),
  wpToggleDebug: (...args: unknown[]) => mockWpToggleDebug(...args),
  wpErrorLog: (...args: unknown[]) => mockWpErrorLog(...args),
  wpOptimizeDB: (...args: unknown[]) => mockWpOptimizeDB(...args),
  wpPluginAction: (...args: unknown[]) => mockWpPluginAction(...args),
  previewCacheControl: (...args: unknown[]) => mockPreviewCacheControl(...args),
}));

// ── Fixtures ────────────────────────────────────────────────────────────────

// Distinct, response-derived markers: root path, main host, disk usage.
const A_DETAIL = {
  host: 'a.example',
  type: 'static',
  aliases: null,
  ssl: { mode: 'auto' },
  root: '/var/www/a',
  canonical_host: 'www', // mainHost renders as www.a.example
  security: { waf: { enabled: true }, rate_limit: { requests: 10, window: '1m0s', by: 'ip' } },
};
const B_DETAIL = {
  host: 'b.example',
  type: 'static',
  aliases: null,
  ssl: { mode: 'auto' },
  root: '/var/www/b',
  canonical_host: 'apex', // mainHost renders as b.example
  security: { waf: { enabled: false }, rate_limit: { requests: 0, window: '1m0s', by: 'ip' } },
};

// Navigate without remounting: /domains/:host keeps the same route element,
// so only the host param changes.
function NavProbe() {
  const navigate = useNavigate();
  return <button onClick={() => navigate('/domains/b.example')}>goto-b</button>;
}

function renderAt(initial: string) {
  return render(
    <MemoryRouter initialEntries={[initial]}>
      <NavProbe />
      <Routes>
        <Route path="/domains/:host" element={<DomainDetail />} />
      </Routes>
    </MemoryRouter>,
  );
}

// Drain the whole pending promise chain inside act, so every async
// continuation (fetch → .then(setState)) settles into the DOM
// deterministically and without act(...) warnings.
const settle = () => act(async () => {
  for (let i = 0; i < 20; i++) await Promise.resolve();
});

describe('DomainDetail stale-response race', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockFetchDomainStats.mockResolvedValue({});
    mockFetchDomainAnalytics.mockResolvedValue(null);
    mockFetchDiskUsage.mockResolvedValue({ bytes: 0, human: '—' });
    mockFetchWPSites.mockResolvedValue([]);
    mockUpdateDomain.mockResolvedValue({ status: 'ok' });
  });

  it('ignores a stale domain detail that resolves after navigation to another host', async () => {
    let resolveA!: (v: unknown) => void;
    mockFetchDomainDetail.mockImplementation((h: string) =>
      h === 'a.example'
        ? new Promise((resolve) => { resolveA = resolve; })
        : Promise.resolve(B_DETAIL),
    );
    renderAt('/domains/a.example');
    await settle(); // load(a) still pending

    fireEvent.click(screen.getByRole('button', { name: /goto-b/i }));
    await settle(); // host changed to b.example; B's detail applied

    await act(async () => {
      resolveA(A_DETAIL); // A's response lands late, after the host changed
    });
    await settle(); // drain the stale load's continuations
    // The stale response must not replace B's page content.
    expect(screen.getAllByText('/var/www/b').length).toBeGreaterThan(0);
    expect(screen.queryAllByText('/var/www/a')).toHaveLength(0);
    expect(screen.queryAllByText('www.a.example')).toHaveLength(0);
  });

  it('ignores stale fire-and-forget continuations (disk usage) after navigation', async () => {
    mockFetchDomainDetail.mockImplementation((h: string) =>
      Promise.resolve(h === 'a.example' ? A_DETAIL : B_DETAIL),
    );
    let resolveDiskA!: (v: unknown) => void;
    mockFetchDiskUsage.mockImplementation((h: string) =>
      h === 'a.example'
        ? new Promise((resolve) => { resolveDiskA = resolve; })
        : Promise.resolve({ bytes: 2, human: '2 B' }),
    );
    renderAt('/domains/a.example');
    await settle(); // A loaded; its disk-usage probe is still pending

    fireEvent.click(screen.getByRole('button', { name: /goto-b/i }));
    await settle(); // B loaded; B's disk usage applied

    await act(async () => {
      resolveDiskA({ bytes: 1, human: '9 KB' }); // A's probe lands late
    });
    await settle();
    expect(screen.getAllByText('2 B').length).toBeGreaterThan(0);
    expect(screen.queryAllByText('9 KB')).toHaveLength(0);
  });
});
