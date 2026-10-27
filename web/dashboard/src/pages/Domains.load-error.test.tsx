import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import Domains from './Domains';
import { ConfirmContext } from '@/components/useConfirm';

// F30900: a failed domain list request used to render the same screen as a
// server with no domains ("0 domains configured" + the getting-started
// wizard), so an operator could believe their sites had been lost.

const apiMocks = vi.hoisted(() => ({
  fetchDomains: vi.fn(),
  addDomain: vi.fn(),
  updateDomain: vi.fn(),
  deleteDomain: vi.fn(),
  fetchDomainDetail: vi.fn(),
  fetchCerts: vi.fn(),
  purgeCacheForHost: vi.fn(),
  fetchPHP: vi.fn(),
  fetchServerIPs: vi.fn(),
  fetchDomainHealth: vi.fn(),
  fetchApps: vi.fn(),
  setPinCode: vi.fn(),
  clearPinCode: vi.fn(),
  bulkImportDomains: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

const confirmValue = {
  confirmAction: vi.fn().mockResolvedValue(true),
  promptText: vi.fn().mockResolvedValue(null),
};

function renderPage() {
  return render(
    <MemoryRouter>
      <ConfirmContext.Provider value={confirmValue}>
        <Domains />
      </ConfirmContext.Provider>
    </MemoryRouter>,
  );
}

const flush = () => act(async () => {});

describe('Domains list load failure', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.fetchCerts.mockResolvedValue([]);
    apiMocks.fetchPHP.mockResolvedValue([]);
    apiMocks.fetchServerIPs.mockResolvedValue({ ips: [] });
    apiMocks.fetchDomainHealth.mockResolvedValue([]);
    apiMocks.fetchApps.mockResolvedValue([]);
  });

  it('shows the error instead of an empty-looking server', async () => {
    apiMocks.fetchDomains.mockRejectedValue(new Error('upstream 502'));
    renderPage();
    await flush();
    console.log('EXPECTED: an error banner mentioning "upstream 502"');
    console.log('ACTUAL: body=' + document.body.textContent?.slice(0, 120));
    expect(screen.getByText(/upstream 502/)).toBeTruthy();
  });

  it('control: a successful empty list shows no error banner', async () => {
    apiMocks.fetchDomains.mockResolvedValue([]);
    renderPage();
    await flush();
    expect(screen.queryByText(/could not load/i)).toBeNull();
  });
});
