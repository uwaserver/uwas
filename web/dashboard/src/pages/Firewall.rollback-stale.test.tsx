import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import Firewall from './Firewall';
import { ConfirmContext } from '@/components/useConfirm';

// F31150: while the enable-rollback banner is up the page polls every second.
// A poll started before "keep it on" was confirmed could land after the
// post-confirm refresh and bring the banner (and its countdown) back.

const apiMocks = vi.hoisted(() => ({
  fetchFirewall: vi.fn(),
  firewallAllow: vi.fn(),
  firewallDeny: vi.fn(),
  firewallDeleteRule: vi.fn(),
  firewallMoveRule: vi.fn(),
  firewallEnable: vi.fn(),
  firewallDisable: vi.fn(),
  firewallConfirm: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);
vi.mock('@/components/AutoBlockPanel', () => ({ default: () => null }));

const confirmValue = {
  confirmAction: vi.fn().mockResolvedValue(true),
  promptText: vi.fn().mockResolvedValue(null),
};

const flush = () => act(async () => {});
const pending = { active: true, rules: [], rollback_pending: true, rollback_seconds: 30 };
const settled = { active: true, rules: [], rollback_pending: false };

describe('Firewall rollback banner', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
  });
  afterEach(() => { vi.useRealTimers(); });

  it('a poll in flight during confirm cannot resurrect the banner', async () => {
    let releaseStale!: (v: unknown) => void;
    apiMocks.fetchFirewall
      .mockResolvedValueOnce(pending)                                       // mount
      .mockImplementationOnce(() => new Promise((r) => { releaseStale = r; })) // 1s poll, slow
      .mockResolvedValue(settled);                                          // post-confirm refresh
    apiMocks.firewallConfirm.mockResolvedValue({});
    render(
      <ConfirmContext.Provider value={confirmValue}><Firewall /></ConfirmContext.Provider>,
    );
    await flush();
    await act(async () => { vi.advanceTimersByTime(1000); });
    fireEvent.click(screen.getByRole('button', { name: /keep it on/i }));
    await flush();
    expect(screen.queryByText(/auto-disable in/)).toBeNull();

    await act(async () => { releaseStale(pending); });
    console.log('EXPECTED: banner stays hidden after the stale poll lands');
    console.log('ACTUAL: banner shown=' + !!screen.queryByText(/auto-disable in/));
    expect(screen.queryByText(/auto-disable in/)).toBeNull();
  });

  it('control: banner shows while rollback is pending', async () => {
    apiMocks.fetchFirewall.mockResolvedValue(pending);
    render(
      <ConfirmContext.Provider value={confirmValue}><Firewall /></ConfirmContext.Provider>,
    );
    await flush();
    expect(screen.getByText(/auto-disable in/)).toBeTruthy();
  });
});
