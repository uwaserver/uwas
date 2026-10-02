import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import Webhooks from './Webhooks';

// ── Mocks ───────────────────────────────────────────────────────────────────
const apiMocks = vi.hoisted(() => ({
  fetchWebhooks: vi.fn(),
  createWebhook: vi.fn(),
  deleteWebhook: vi.fn(),
  testWebhook: vi.fn(),
  fetchFeatures: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

function renderPage() {
  return render(<Webhooks />);
}

// Flush mount effects + in-flight promise chains without advancing the clock.
const flush = () => act(async () => {});

const WH_A = {
  url: 'https://a.example/hook', events: [] as string[], headers: {},
  secret: '****1234', retry: 3, timeout: 30, enabled: true,
};
const WH_B = {
  url: 'https://b.example/hook', events: ['backup.completed'] as string[], headers: {},
  secret: '****5678', retry: 5, timeout: 30, enabled: true,
};

// Click Delete on the FIRST row (rows render in list order), then confirm.
async function deleteFirstRow() {
  fireEvent.click(screen.getAllByRole('button', { name: 'Delete' })[0]);
  fireEvent.click(screen.getByRole('button', { name: 'Yes' }));
  await flush();
}

describe('Webhooks delete resolves the target against the fresh list', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => {
      if (vi.isMockFunction(fn)) fn.mockReset();
    });
    apiMocks.fetchFeatures.mockResolvedValue({});
    apiMocks.fetchWebhooks.mockResolvedValue([WH_A, WH_B]);
    apiMocks.deleteWebhook.mockResolvedValue({ success: true });
    apiMocks.createWebhook.mockResolvedValue({ success: true });
    apiMocks.testWebhook.mockResolvedValue({ success: true, message: 'ok' });
  });

  // Regression: the server deletes by list POSITION (webhook_handlers.go
  // parses the {id} path segment with strconv.Atoi and removes
  // Global.Webhooks[idx]), and this page loads the list once and never
  // polls — so the render-time index goes stale whenever the list changes
  // (second tab, another admin, API scripts; the PIN prompt widens the
  // window). Deleting row A must re-resolve A's CURRENT position, not send
  // the stale render index and remove whatever moved into its slot.
  it('deletes the fresh index when the list reordered', async () => {
    apiMocks.fetchWebhooks
      .mockResolvedValueOnce([WH_A, WH_B]) // mount load
      .mockResolvedValueOnce([WH_B, WH_A]); // fresh list at delete time: A moved to index 1
    renderPage();
    await flush();
    await deleteFirstRow();

    expect(apiMocks.deleteWebhook).toHaveBeenCalledTimes(1);
    expect(apiMocks.deleteWebhook).toHaveBeenCalledWith(1);
  });

  // Regression: when the target webhook is already gone, nothing may be
  // deleted — the stale index would remove an innocent webhook.
  it('deletes nothing when the target webhook no longer exists', async () => {
    apiMocks.fetchWebhooks
      .mockResolvedValueOnce([WH_A, WH_B]) // mount load
      .mockResolvedValueOnce([WH_B]) // fresh list at delete time: A already removed elsewhere
      .mockResolvedValue([WH_B]); // the refresh after the no-op
    renderPage();
    await flush();
    await deleteFirstRow();

    expect(apiMocks.deleteWebhook).not.toHaveBeenCalled();
  });

  // Control: an unchanged list deletes at the same index.
  it('deletes the render index when the list is unchanged', async () => {
    apiMocks.fetchWebhooks
      .mockResolvedValueOnce([WH_A, WH_B]) // mount load
      .mockResolvedValueOnce([WH_A, WH_B]); // fresh list identical
    renderPage();
    await flush();
    await deleteFirstRow();

    expect(apiMocks.deleteWebhook).toHaveBeenCalledWith(0);
  });
});
