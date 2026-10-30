import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import AutoBlockPanel from './AutoBlockPanel';
import { ConfirmContext } from './useConfirm';
import type { AutoBlockStatus } from '@/lib/api';

// F53602: a poll started before an unblock finished after the post-unblock
// load and resurrected the removed block. Gated completion order.

const mockFetch = vi.fn();
const mockRemove = vi.fn();
vi.mock('@/lib/api', () => ({
  fetchAutoBlock: () => mockFetch(),
  autoBlockAdd: vi.fn(),
  autoBlockRemove: (ip: string) => mockRemove(ip),
}));
const confirmValue = { confirmAction: vi.fn().mockResolvedValue(true), promptText: vi.fn().mockResolvedValue(null) };

const base = { enabled: true, dry_run: false, firewall_sync: true, total_detected: 1, window: '1m', thresholds: {} };
const withBlock: AutoBlockStatus = {
  ...base, active_blocks: 1,
  blocks: [{ ip: '203.0.113.55', reason: 'tls_abort', hits: 9, level: 1, blocked_at: new Date().toISOString(), expires_at: new Date(Date.now() + 900000).toISOString(), firewall: true }],
} as AutoBlockStatus;
const empty = { ...base, active_blocks: 0, blocks: [] } as unknown as AutoBlockStatus;

describe('AutoBlockPanel stale poll', () => {
  beforeEach(() => { vi.clearAllMocks(); vi.useRealTimers(); });

  it('a load that completes after a newer one is dropped', async () => {
    let releaseStale!: (v: AutoBlockStatus) => void;
    const stale = new Promise<AutoBlockStatus>(r => { releaseStale = r; });
    mockFetch.mockResolvedValueOnce(withBlock);          // initial
    render(<ConfirmContext.Provider value={confirmValue}><AutoBlockPanel /></ConfirmContext.Provider>);
    await act(async () => {});
    expect(screen.getByText('203.0.113.55')).toBeTruthy();

    // Refresh button starts a load that hangs (stands in for the 15s poll).
    mockFetch.mockReturnValueOnce(stale);
    fireEvent.click(screen.getByRole('button', { name: /refresh/i }));
    // Unblock → its own load resolves with the block gone.
    mockRemove.mockResolvedValue(undefined);
    mockFetch.mockResolvedValueOnce(empty);
    fireEvent.click(screen.getByRole('button', { name: /unblock/i }));
    await act(async () => {});
    expect(screen.queryByText('203.0.113.55')).toBeNull();

    await act(async () => { releaseStale(withBlock); });
    console.log('EXPECTED: block stays gone');
    console.log('ACTUAL: present=' + !!screen.queryByText('203.0.113.55'));
    expect(screen.queryByText('203.0.113.55')).toBeNull();
  });
});
