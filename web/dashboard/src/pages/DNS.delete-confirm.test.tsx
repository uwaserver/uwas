import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import DNS from './DNS';
import { ConfirmContext } from '@/components/useConfirm';

// F33701: the DNS record Delete button called the provider API on a single
// click. Every other destructive dashboard action (domain delete, package
// remove, firewall delete) asks first; a misclick here removes a live MX/A
// record at Cloudflare/Hetzner/Route53 with no undo.

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

const flush = () => act(async () => {});

const RECORD = { id: 'r1', type: 'MX', name: '@', content: 'mail.x.com', ttl: 300, proxied: false };

async function renderWithRecords(confirmAnswer: boolean) {
  const confirmAction = vi.fn().mockResolvedValue(confirmAnswer);
  window.history.replaceState({}, '', '/dns?domain=x.com');
  render(
    <MemoryRouter initialEntries={['/dns?domain=x.com']}>
      <ConfirmContext.Provider value={{ confirmAction, promptText: vi.fn().mockResolvedValue(null) }}>
        <DNS />
      </ConfirmContext.Provider>
    </MemoryRouter>,
  );
  await flush();
  await flush();
  return confirmAction;
}

describe('DNS record delete confirmation', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => fn.mockReset());
    apiMocks.fetchDomains.mockResolvedValue([{ id: 'd1', host: 'x.com', type: 'static', ssl: 'off' }]);
    apiMocks.fetchDNSRecords.mockResolvedValue({ records: [RECORD], provider: 'cloudflare' });
    apiMocks.deleteDNSRecord.mockResolvedValue({ status: 'deleted' });
  });

  afterEach(() => {
    window.history.replaceState({}, '', '/');
  });

  it('does not delete when the confirmation is declined', async () => {
    const confirmAction = await renderWithRecords(false);
    fireEvent.click(screen.getByRole('button', { name: /^delete$/i }));
    await flush();
    console.log('EXPECTED: confirm asked, deleteDNSRecord not called');
    console.log(`ACTUAL: confirm calls=${confirmAction.mock.calls.length}, delete calls=${apiMocks.deleteDNSRecord.mock.calls.length}`);
    expect(confirmAction).toHaveBeenCalledTimes(1);
    expect(apiMocks.deleteDNSRecord).not.toHaveBeenCalled();
    expect(screen.getByText('mail.x.com')).toBeTruthy();
  });

  it('control: deletes after the confirmation is accepted', async () => {
    const confirmAction = await renderWithRecords(true);
    fireEvent.click(screen.getByRole('button', { name: /^delete$/i }));
    await flush();
    expect(confirmAction).toHaveBeenCalledTimes(1);
    expect(apiMocks.deleteDNSRecord).toHaveBeenCalledWith('x.com', 'r1');
    expect(screen.queryByText('mail.x.com')).toBeNull();
  });
});
