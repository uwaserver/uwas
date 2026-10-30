import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router';
import DomainDetail from './DomainDetail';

// F47000: the Security tab sends `basic_auth` on every save, and the server
// stores a patch that carries the key as a whole replacement
// (config.MergeDomain). With Basic Auth switched off the form sent
// `{ enabled: false }`, so saving ANY unrelated setting on the page (WAF, rate
// limit, resources) silently deleted the stored users and realm of a domain
// whose Basic Auth was merely disabled. The config validator accepts a
// disabled block that still lists users, so they can be sent back.

const apiMocks = vi.hoisted(() => ({
  fetchDomainDetail: vi.fn(),
  updateDomain: vi.fn(),
  fetchDomainStats: vi.fn(),
  fetchDiskUsage: vi.fn(),
  fetchDomainAnalytics: vi.fn(),
  fetchWPSites: vi.fn(),
  wpSecurityStatus: vi.fn(),
  wpHarden: vi.fn(),
  wpListUsers: vi.fn(),
  wpChangePassword: vi.fn(),
  wpUpdateCore: vi.fn(),
  wpUpdatePlugins: vi.fn(),
  wpFixPermissions: vi.fn(),
  wpToggleDebug: vi.fn(),
  wpErrorLog: vi.fn(),
  wpOptimizeDB: vi.fn(),
  wpPluginAction: vi.fn(),
  previewCacheControl: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

const settle = () => act(async () => {
  for (let i = 0; i < 20; i++) await Promise.resolve();
});

type Body = { basic_auth?: { enabled?: boolean; realm?: string; users?: Record<string, string> } };

async function saveSecurity(basicAuth: Record<string, unknown> | undefined): Promise<Body> {
  apiMocks.fetchDomainDetail.mockResolvedValue({
    host: 'a.example',
    type: 'static',
    aliases: null,
    ssl: { mode: 'auto' },
    root: '/var/www/a',
    basic_auth: basicAuth,
  });
  render(
    <MemoryRouter initialEntries={['/domains/a.example']}>
      <Routes>
        <Route path="/domains/:host" element={<DomainDetail />} />
      </Routes>
    </MemoryRouter>,
  );
  await settle();
  fireEvent.click(screen.getByRole('button', { name: /Security/ }));
  await settle();
  fireEvent.click(screen.getByRole('button', { name: /Save Security Settings/ }));
  await settle();
  expect(apiMocks.updateDomain).toHaveBeenCalledTimes(1);
  return apiMocks.updateDomain.mock.calls[0][1] as Body;
}

describe('Security tab save keeps a disabled Basic Auth block', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => fn.mockReset());
    apiMocks.fetchDomainStats.mockResolvedValue({});
    apiMocks.fetchDomainAnalytics.mockResolvedValue(null);
    apiMocks.fetchDiskUsage.mockResolvedValue({ bytes: 0, human: '—' });
    apiMocks.fetchWPSites.mockResolvedValue([]);
    apiMocks.updateDomain.mockResolvedValue({ status: 'ok' });
  });

  it('keeps the users and realm of a disabled block', async () => {
    const saved = { enabled: false, realm: 'Staff', users: { alice: 'pw-a', bob: 'pw-b' } };
    const body = await saveSecurity(saved);
    console.log('EXPECTED: ' + JSON.stringify(saved) + '\nACTUAL: ' + JSON.stringify(body.basic_auth));
    expect(body.basic_auth).toEqual(saved);
  });

  // Control: an enabled block still round-trips.
  it('control: an enabled block is saved unchanged', async () => {
    const saved = { enabled: true, realm: 'Staff', users: { alice: 'pw-a' } };
    const body = await saveSecurity(saved);
    expect(body.basic_auth).toEqual(saved);
  });

  // Edge: no Basic Auth configured at all still sends a disabled, empty block.
  it('a domain without Basic Auth sends a plain disabled block', async () => {
    const body = await saveSecurity(undefined);
    expect(body.basic_auth).toEqual({ enabled: false });
  });
});
