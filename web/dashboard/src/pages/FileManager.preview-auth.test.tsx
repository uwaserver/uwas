import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import FileManager from './FileManager';
import { ConfirmContext } from '@/components/useConfirm';
import { getToken, setToken, setTOTPCode, getAuthHeaders } from '@/lib/api';

// F45600: opening an image fetches it with a hand-rolled authenticated fetch.
// An expired session or a rejected 2FA code was only shown as an error line:
// the stale token / code stayed in place and the operator was not sent back to
// sign in.

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return {
    ...actual,
    fetchFileWorkspaces: vi.fn().mockResolvedValue([{ id: 'example.com', label: 'example.com', kind: 'domain', root: '/var/www' }]),
    fetchDiskUsage: vi.fn().mockResolvedValue({ bytes: 10, human: '10 B', root: '/var/www' }),
    fetchFiles: vi.fn().mockResolvedValue({
      items: [{ name: 'logo.png', path: 'logo.png', is_dir: false, size: 1, mod_time: '2026-01-01T00:00:00Z', mode: '-rw-r--r--' }],
      total: 1, limit: 50, offset: 0,
    }),
  };
});

const confirmValue = {
  confirmAction: vi.fn().mockResolvedValue(true),
  promptText: vi.fn().mockResolvedValue(null),
};

function stubRead(status: number, body: unknown) {
  vi.stubGlobal('fetch', vi.fn(async () =>
    new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })));
}

describe('FileManager image preview recovers from auth failures', () => {
  let assigned: string[];
  const realLocation = window.location;

  beforeEach(() => {
    assigned = [];
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: {
        pathname: '/_uwas/dashboard/files',
        get href() { return 'http://localhost/_uwas/dashboard/files'; },
        set href(v: string) { assigned.push(v); },
      },
    });
    setToken('tok', 'api_key');
    setTOTPCode('123456');
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    Object.defineProperty(window, 'location', { configurable: true, value: realLocation });
    setToken('');
  });

  async function openImage() {
    render(
      <ConfirmContext.Provider value={confirmValue}>
        <FileManager />
      </ConfirmContext.Provider>,
    );
    await userEvent.click(await screen.findByText('logo.png'));
  }

  it('an expired session clears the token and returns to login', async () => {
    stubRead(401, { error: 'unauthorized' });
    await openImage();
    await waitFor(() => expect(assigned).toEqual(['/_uwas/dashboard/login']));
    expect(getToken()).toBe('');
  });

  it('a rejected 2FA code is dropped and the 2FA prompt opens', async () => {
    stubRead(403, { error: 'invalid 2FA code' });
    await openImage();
    await waitFor(() => expect(assigned).toEqual(['/_uwas/dashboard/login?2fa=required']));
    expect(getAuthHeaders()['X-TOTP-Code']).toBeUndefined();
  });

  it('control: an ordinary failure is reported and keeps the session', async () => {
    stubRead(404, { error: 'file not found' });
    await openImage();
    expect(await screen.findByText('file not found')).toBeInTheDocument();
    expect(assigned).toEqual([]);
    expect(getToken()).toBe('tok');
  });
});
