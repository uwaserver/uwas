import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import Settings from './Settings';

// ── Mocks ───────────────────────────────────────────────────────────────────
const apiMocks = vi.hoisted(() => ({
  fetchConfigRaw: vi.fn(),
  triggerReload: vi.fn(),
  fetchConfigExport: vi.fn(),
  fetchHealth: vi.fn(),
  fetchSystem: vi.fn(),
  fetchSettings: vi.fn(),
  saveSettings: vi.fn(),
  fetch2FAStatus: vi.fn(),
  setup2FA: vi.fn(),
  verify2FA: vi.fn(),
  disable2FA: vi.fn(),
  generateRecoveryCodes: vi.fn(),
  sendNotifyTest: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

// The clipboard is the boundary under test: copyText resolves FALSE when
// every copy strategy failed (lib/clipboard contract).
const clipboardMocks = vi.hoisted(() => ({ copyText: vi.fn() }));
vi.mock('@/lib/clipboard', () => clipboardMocks);

function renderPage() {
  return render(<Settings />);
}

const flush = () => act(async () => {});

const BASE_SETTINGS = { 'global.admin.api_key': 'test-key-123' };

// Mount, then open the Security tab where the admin API Key (type: secret)
// field renders with its copy button. Several secret fields share the
// title="Copy" button — scope to the API Key field's container.
async function clickApiKeyCopy() {
  fireEvent.click(screen.getByRole('button', { name: 'Security' }));
  await flush();
  const copyBtn = screen
    .getByText('API Key')
    .closest('div')
    ?.querySelector('button[title="Copy"]') as HTMLButtonElement;
  fireEvent.click(copyBtn);
  await flush();
}

describe('Settings copy reports clipboard failure instead of false success', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => {
      if (vi.isMockFunction(fn)) fn.mockReset();
    });
    clipboardMocks.copyText.mockReset();
    apiMocks.fetchSettings.mockResolvedValue(BASE_SETTINGS);
    apiMocks.fetchConfigRaw.mockResolvedValue({ content: 'global:\n  admin:\n    api_key: test-key-123\n' });
    apiMocks.fetchHealth.mockResolvedValue({});
    apiMocks.fetchSystem.mockResolvedValue({});
    apiMocks.fetch2FAStatus.mockResolvedValue({ enabled: false });
  });

  // Regression: copyToClipboard treated a RESOLVED copyText as success —
  // but copyText resolves false when every strategy failed, and the .then
  // showed "Copied to clipboard" with success styling anyway. Settings
  // fields include real credentials (admin API key, OAuth secrets), so the
  // operator must see the failure.
  it('shows a copy-failed message when the clipboard write fails', async () => {
    clipboardMocks.copyText.mockResolvedValue(false);
    renderPage();
    await flush();
    await clickApiKeyCopy();

    expect(clipboardMocks.copyText).toHaveBeenCalledWith('test-key-123');
    expect(screen.getByText(/Copy failed/i)).toBeTruthy();
  });

  // Control: a successful copy keeps the existing success status.
  it('keeps the success status when the clipboard write succeeds', async () => {
    clipboardMocks.copyText.mockResolvedValue(true);
    renderPage();
    await flush();
    await clickApiKeyCopy();

    expect(clipboardMocks.copyText).toHaveBeenCalledWith('test-key-123');
    expect(screen.getByText('Copied to clipboard')).toBeTruthy();
    expect(screen.queryByText(/Copy failed/i)).toBeNull();
  });
});
