import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import AutoBlockPanel from './AutoBlockPanel';
import { ConfirmContext } from './useConfirm';
import type { AutoBlockStatus } from '@/lib/api';

// ── Mocks ──────────────────────────────────────────────────────────────────
const mockFetch = vi.fn();
const mockAdd = vi.fn();
const mockRemove = vi.fn();

vi.mock('@/lib/api', () => ({
  fetchAutoBlock: () => mockFetch(),
  autoBlockAdd: (ip: string, reason?: string, duration?: string) => mockAdd(ip, reason, duration),
  autoBlockRemove: (ip: string) => mockRemove(ip),
}));

// Always-confirm dialog so unblock proceeds without a real modal.
const confirmValue = {
  confirmAction: vi.fn().mockResolvedValue(true),
  promptText: vi.fn().mockResolvedValue(null),
};

function renderPanel() {
  return render(
    <ConfirmContext.Provider value={confirmValue}>
      <AutoBlockPanel />
    </ConfirmContext.Provider>,
  );
}

const enabledStatus: AutoBlockStatus = {
  enabled: true,
  dry_run: false,
  firewall_sync: true,
  active_blocks: 1,
  total_detected: 7,
  window: '1m0s',
  thresholds: { aborts: 60 },
  blocks: [
    {
      ip: '203.0.113.55',
      reason: 'tls_abort',
      hits: 82,
      level: 1,
      blocked_at: new Date().toISOString(),
      expires_at: new Date(Date.now() + 15 * 60 * 1000).toISOString(),
      firewall: true,
    },
  ],
};

beforeEach(() => {
  vi.clearAllMocks();
  confirmValue.confirmAction.mockResolvedValue(true);
});

describe('AutoBlockPanel', () => {
  it('shows a disabled state that points at the setting, not an error', async () => {
    mockFetch.mockResolvedValue({ enabled: false, active_blocks: 0 });
    renderPanel();
    await waitFor(() => expect(screen.getByText(/auto-block is disabled/i)).toBeInTheDocument());
    expect(screen.getByText(/Disabled/)).toBeInTheDocument();
    // The disabled copy must route the operator to the real control.
    expect(screen.getByText(/Settings → Security → Auto-Block/)).toBeInTheDocument();
  });

  it('renders enforcing status, stats and an active block', async () => {
    mockFetch.mockResolvedValue(enabledStatus);
    renderPanel();
    await waitFor(() => expect(screen.getByText('Enforcing')).toBeInTheDocument());
    expect(screen.getByText('203.0.113.55')).toBeInTheDocument();
    expect(screen.getByText('tls_abort')).toBeInTheDocument();
    // firewall:true renders as a kernel-level block, not memory-only.
    expect(screen.getByText('kernel')).toBeInTheDocument();
  });

  it('marks a dry-run deployment distinctly from enforcing', async () => {
    mockFetch.mockResolvedValue({ ...enabledStatus, dry_run: true, blocks: [] });
    renderPanel();
    await waitFor(() => expect(screen.getByText('Dry run')).toBeInTheDocument());
    expect(screen.getByText(/no active blocks/i)).toBeInTheDocument();
  });

  it('blocks an IP through the manual form', async () => {
    mockFetch.mockResolvedValue(enabledStatus);
    mockAdd.mockResolvedValue({ blocked: '198.51.100.9' });
    renderPanel();
    await waitFor(() => expect(screen.getByText('Enforcing')).toBeInTheDocument());

    fireEvent.change(screen.getByPlaceholderText('203.0.113.10'), { target: { value: '198.51.100.9' } });
    fireEvent.click(screen.getByText(/Block IP/));

    await waitFor(() => expect(mockAdd).toHaveBeenCalledWith('198.51.100.9', 'manual', undefined));
  });

  it('confirms before unblocking and calls the API with the IP', async () => {
    mockFetch.mockResolvedValue(enabledStatus);
    mockRemove.mockResolvedValue({ unblocked: '203.0.113.55' });
    renderPanel();
    await waitFor(() => expect(screen.getByText('203.0.113.55')).toBeInTheDocument());

    fireEvent.click(screen.getByText('Unblock'));

    await waitFor(() => expect(confirmValue.confirmAction).toHaveBeenCalled());
    await waitFor(() => expect(mockRemove).toHaveBeenCalledWith('203.0.113.55'));
  });

  it('does not unblock when the confirmation is declined', async () => {
    mockFetch.mockResolvedValue(enabledStatus);
    confirmValue.confirmAction.mockResolvedValue(false);
    renderPanel();
    await waitFor(() => expect(screen.getByText('203.0.113.55')).toBeInTheDocument());

    fireEvent.click(screen.getByText('Unblock'));
    await waitFor(() => expect(confirmValue.confirmAction).toHaveBeenCalled());
    expect(mockRemove).not.toHaveBeenCalled();
  });
});
