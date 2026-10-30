import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import Database from './Database';
import { ConfirmContext } from '@/components/useConfirm';
import { getToken, setToken, setTOTPCode, getAuthHeaders } from '@/lib/api';

// F45600: the Export button downloads through a hand-rolled fetch. An expired
// session or a rejected 2FA code was only shown as "Export failed: …": the
// stale token / code stayed in place and the operator was not sent back to
// sign in.

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return {
    ...actual,
    fetchDBStatus: vi.fn().mockResolvedValue({ backend: 'mariadb', installed: true, running: true, version: '11' }),
    fetchDatabases: vi.fn().mockResolvedValue([{ name: 'shop', host: 'localhost', size: '1 MB', tables: 3 }]),
    fetchDBUsers: vi.fn().mockResolvedValue([]),
    fetchDockerDBs: vi.fn().mockResolvedValue({ docker: false, version: '', containers: [] }),
  };
});

const confirmValue = {
  confirmAction: vi.fn().mockResolvedValue(true),
  promptText: vi.fn().mockResolvedValue(null),
};

function stubExport(status: number, body: unknown) {
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    if (String(url).endsWith('/export')) {
      return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
    }
    return new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } });
  }));
}

describe('Database export recovers from auth failures', () => {
  let assigned: string[];
  const realLocation = window.location;

  beforeEach(() => {
    assigned = [];
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: {
        pathname: '/_uwas/dashboard/database',
        get href() { return 'http://localhost/_uwas/dashboard/database'; },
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

  async function clickExport() {
    render(
      <MemoryRouter initialEntries={['/database']}>
        <ConfirmContext.Provider value={confirmValue}>
          <Database />
        </ConfirmContext.Provider>
      </MemoryRouter>,
    );
    await act(async () => {});
    fireEvent.click(await screen.findByTitle('Export SQL dump'));
  }

  it('an expired session clears the token and returns to login', async () => {
    stubExport(401, { error: 'unauthorized' });
    await clickExport();
    await waitFor(() => expect(assigned).toEqual(['/_uwas/dashboard/login']));
    expect(getToken()).toBe('');
  });

  it('a rejected 2FA code is dropped and the 2FA prompt opens', async () => {
    stubExport(403, { error: 'invalid 2FA code' });
    await clickExport();
    await waitFor(() => expect(assigned).toEqual(['/_uwas/dashboard/login?2fa=required']));
    expect(getAuthHeaders()['X-TOTP-Code']).toBeUndefined();
  });

  it('control: an ordinary failure is reported and keeps the session', async () => {
    stubExport(500, { error: 'database export failed' });
    await clickExport();
    expect(await screen.findByText(/Export failed: database export failed/)).toBeInTheDocument();
    expect(assigned).toEqual([]);
    expect(getToken()).toBe('tok');
  });
});
