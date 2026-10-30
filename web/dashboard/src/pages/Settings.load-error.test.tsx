import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import Settings from './Settings';

// F45603: the Settings load swallowed every failure ("catch { // ignore }"):
// the page rendered all-empty settings with no explanation, and Reload even
// reported "Configuration reloaded successfully" after a failed re-fetch.

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

const flush = () => act(async () => {});

describe('Settings load failures', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => fn.mockReset());
    apiMocks.fetchSettings.mockResolvedValue({ 'global.http_listen': ':80' });
    apiMocks.fetchConfigRaw.mockResolvedValue({ content: 'global:\n  http_listen: ":80"\n' });
    apiMocks.fetchHealth.mockResolvedValue({});
    apiMocks.fetchSystem.mockResolvedValue({});
    apiMocks.fetch2FAStatus.mockResolvedValue({ enabled: false });
    apiMocks.triggerReload.mockResolvedValue({});
    apiMocks.saveSettings.mockResolvedValue({});
  });

  it('control: a successful load shows the values and no error', async () => {
    render(<Settings />);
    await flush();
    expect(screen.getByDisplayValue(':80')).toBeTruthy();
    expect(screen.queryByText(/Could not load settings/)).toBeNull();
  });

  it('a failed load is reported with a Retry that reloads the page data', async () => {
    apiMocks.fetchConfigRaw.mockRejectedValueOnce(new Error('read config: permission denied'));
    render(<Settings />);
    await flush();
    expect(screen.getByText(/Could not load settings: read config: permission denied/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await flush();
    expect(screen.queryByText(/Could not load settings/)).toBeNull();
    expect(screen.getByDisplayValue(':80')).toBeTruthy();
  });

  it('Reload does not claim success when the re-fetch fails', async () => {
    render(<Settings />);
    await flush();
    apiMocks.fetchHealth.mockRejectedValue(new Error('health down'));
    fireEvent.click(screen.getByRole('button', { name: /^Reload$/ }));
    await flush();
    expect(screen.getByText(/Could not load settings: health down/)).toBeTruthy();
    expect(screen.queryByText('Configuration reloaded successfully')).toBeNull();
  });
});
