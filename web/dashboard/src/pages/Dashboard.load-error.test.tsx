import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import Dashboard from './Dashboard';

// F34300: the first-run wizard is documented as "does NOT appear if the API
// call fails", but a failed domain list request still flipped domainsLoaded,
// so a transient 502 showed "Welcome to UWAS" and "Domains 0" on a server
// that has sites.

const apiMocks = vi.hoisted(() => ({
  fetchDomains: vi.fn(),
  fetchCerts: vi.fn(),
  fetchSecurityStats: vi.fn(),
  fetchPHP: vi.fn(),
  fetchSystem: vi.fn(),
  fetchDomainHealth: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);
vi.mock('@/hooks/useStats', () => ({
  useStats: () => ({ stats: null, health: null, history: [], error: null }),
}));

function renderPage() {
  return render(
    <MemoryRouter>
      <Dashboard />
    </MemoryRouter>,
  );
}

const flush = () => act(async () => {});

describe('Dashboard domain list load failure', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.fetchCerts.mockResolvedValue([]);
    apiMocks.fetchSecurityStats.mockResolvedValue(null);
    apiMocks.fetchPHP.mockResolvedValue([]);
    apiMocks.fetchSystem.mockResolvedValue(null);
    apiMocks.fetchDomainHealth.mockResolvedValue([]);
  });

  it('shows the error and not the first-run wizard', async () => {
    apiMocks.fetchDomains.mockRejectedValue(new Error('upstream 502'));
    renderPage();
    await flush();
    console.log('EXPECTED: no "Welcome to UWAS", error mentioning "upstream 502"');
    console.log('ACTUAL: welcome=' + !!screen.queryByText(/Welcome to UWAS/) + ' error=' + !!screen.queryByText(/upstream 502/));
    expect(screen.queryByText(/Welcome to UWAS/)).toBeNull();
    expect(screen.getByText(/upstream 502/)).toBeTruthy();
  });

  it('control: a successful empty list still shows the wizard', async () => {
    apiMocks.fetchDomains.mockResolvedValue([]);
    renderPage();
    await flush();
    expect(screen.getByText(/Welcome to UWAS/)).toBeTruthy();
    expect(screen.queryByText(/could not load/i)).toBeNull();
  });
});
