import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import Settings from './Settings';

// F30300: Reload re-fetches the form from the server but kept dirtyKeys, so the
// next Save wrote the server's values back as if they were the user's edits
// while the edits themselves were gone. After a reload the form and the dirty
// set must agree: nothing is dirty and nothing stale is saved.

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

describe('Settings Reload keeps form and dirty set consistent', () => {
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

  it('does not leave a dirty (Save-able) state after Reload overwrote the form', async () => {
    render(<Settings />);
    await flush();
    const input = screen.getByDisplayValue(':80') as HTMLInputElement;
    fireEvent.change(input, { target: { value: ':8081' } });
    await flush();
    expect(screen.getByText('Unsaved changes')).toBeTruthy();
    expect((screen.getByRole('button', { name: /Save Settings/ }) as HTMLButtonElement).disabled).toBe(false);

    fireEvent.click(screen.getByRole('button', { name: /^Reload$/ }));
    await flush();

    // The form shows the server value again...
    expect((screen.getByDisplayValue(':80') as HTMLInputElement).value).toBe(':80');
    // ...so there must be nothing left to save.
    expect(screen.queryByText('Unsaved changes')).toBeNull();
    expect((screen.getByRole('button', { name: /Save Settings/ }) as HTMLButtonElement).disabled).toBe(true);
    expect(apiMocks.saveSettings).not.toHaveBeenCalled();
  });

  it('control: without edits Reload shows no save buttons', async () => {
    render(<Settings />);
    await flush();
    fireEvent.click(screen.getByRole('button', { name: /^Reload$/ }));
    await flush();
    expect(screen.queryByText('Unsaved changes')).toBeNull();
    expect((screen.getByRole('button', { name: /Save Settings/ }) as HTMLButtonElement).disabled).toBe(true);
  });
});
