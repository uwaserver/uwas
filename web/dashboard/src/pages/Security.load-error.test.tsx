import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import Security from './Security';

// F33703: a failed security stats/blocked-requests request rendered "No
// blocked requests yet / All traffic is clean." — a security page reporting a
// clean bill of health because the request failed. A failed per-domain detail
// request also left the row expanded with nothing in it and no message.

const apiMocks = vi.hoisted(() => ({
  fetchSecurityStats: vi.fn(),
  fetchSecurityBlocked: vi.fn(),
  fetchDomains: vi.fn(),
  fetchDomainDetail: vi.fn(),
  updateDomain: vi.fn(),
  fetchFeatures: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

const flush = () => act(async () => {});

const STATS = { waf_blocked: 0, bot_blocked: 0, rate_blocked: 0, hotlink_blocked: 0, total_blocked: 0 };

describe('Security page load failures', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => fn.mockReset());
    apiMocks.fetchDomains.mockResolvedValue([{ id: 'd1', host: 'x.com', type: 'static', ssl: 'off' }]);
    apiMocks.fetchFeatures.mockResolvedValue({});
  });

  it('does not report "All traffic is clean" when the request failed', async () => {
    apiMocks.fetchSecurityStats.mockRejectedValue(new Error('upstream 502'));
    apiMocks.fetchSecurityBlocked.mockRejectedValue(new Error('upstream 502'));
    render(<Security />);
    await flush();
    console.log('EXPECTED: error message, no "All traffic is clean"');
    console.log('ACTUAL: clean shown=' + !!screen.queryByText(/all traffic is clean/i));
    expect(screen.queryByText(/all traffic is clean/i)).toBeNull();
    expect(screen.getByText(/upstream 502/)).toBeTruthy();
  });

  it('control: a successful empty response still says traffic is clean', async () => {
    apiMocks.fetchSecurityStats.mockResolvedValue(STATS);
    apiMocks.fetchSecurityBlocked.mockResolvedValue([]);
    render(<Security />);
    await flush();
    expect(screen.getByText(/all traffic is clean/i)).toBeTruthy();
  });

  it('reports a failed domain detail request', async () => {
    apiMocks.fetchSecurityStats.mockResolvedValue(STATS);
    apiMocks.fetchSecurityBlocked.mockResolvedValue([]);
    apiMocks.fetchDomainDetail.mockRejectedValue(new Error('detail 500'));
    render(<Security />);
    await flush();
    fireEvent.click(screen.getByRole('button', { name: /per-domain/i }));
    await flush();
    fireEvent.click(screen.getByText('x.com'));
    await flush();
    console.log('ACTUAL: detail error shown=' + !!screen.queryByText(/detail 500/));
    expect(screen.getByText(/detail 500/)).toBeTruthy();
  });
});
