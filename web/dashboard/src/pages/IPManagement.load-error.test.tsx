import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import IPManagement from './IPManagement';
import { ConfirmContext } from '@/components/useConfirm';

// F33700: a failed IP/domain request used to render the same screen as a
// server with no IPs and no domains ("No server IPs detected." / "No domains
// configured."), with no error and no way to retry except the header button.

const apiMocks = vi.hoisted(() => ({
  fetchServerIPs: vi.fn(),
  fetchDomains: vi.fn(),
  updateDomain: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

const confirmValue = {
  confirmAction: vi.fn().mockResolvedValue(true),
  promptText: vi.fn().mockResolvedValue(null),
};

function renderPage() {
  return render(
    <ConfirmContext.Provider value={confirmValue}>
      <IPManagement />
    </ConfirmContext.Provider>,
  );
}

const flush = () => act(async () => {});

describe('IPManagement load failure', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('shows the error instead of an empty-looking server', async () => {
    apiMocks.fetchServerIPs.mockRejectedValue(new Error('upstream 502'));
    apiMocks.fetchDomains.mockResolvedValue([]);
    renderPage();
    await flush();
    console.log('EXPECTED: an error banner mentioning "upstream 502"');
    console.log('ACTUAL: body=' + document.body.textContent?.slice(0, 160));
    expect(screen.getByText(/upstream 502/)).toBeTruthy();
  });

  it('keeps the last good data and reports a failed refresh', async () => {
    apiMocks.fetchServerIPs.mockResolvedValueOnce({
      public_ip: '203.0.113.9',
      ips: [{ ip: '10.0.0.5', version: 4, interface: 'eth0', primary: true }],
    });
    apiMocks.fetchDomains.mockResolvedValue([]);
    renderPage();
    await flush();
    expect(screen.getByText('10.0.0.5')).toBeTruthy();

    apiMocks.fetchServerIPs.mockRejectedValueOnce(new Error('refresh failed'));
    await act(async () => {
      screen.getByRole('button', { name: /refresh/i }).click();
    });
    expect(screen.getByText('10.0.0.5')).toBeTruthy();
    expect(screen.getByText(/refresh failed/)).toBeTruthy();
  });

  it('control: a successful empty response shows no error banner', async () => {
    apiMocks.fetchServerIPs.mockResolvedValue({ ips: [] });
    apiMocks.fetchDomains.mockResolvedValue([]);
    renderPage();
    await flush();
    expect(screen.queryByText(/could not load/i)).toBeNull();
    expect(screen.getByText(/No server IPs detected/)).toBeTruthy();
  });
});
