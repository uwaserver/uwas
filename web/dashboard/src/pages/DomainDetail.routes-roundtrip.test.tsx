import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router';
import DomainDetail from './DomainDetail';

// F46306: the Routes tab rebuilt every location from its form fields on save
// (match, one target, strip_prefix, redirect_code, basic_auth) and sent the
// list, which config.MergeDomain stores as a whole replacement. A location's
// response `headers`, `request_timeout` and per-path `rate_limit`, and the
// `cache_control` of a proxy/static/redirect route, are not editable here and
// were silently deleted by saving the page.

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

const LOCATIONS = [
  {
    match: '/api/',
    proxy_pass: 'http://127.0.0.1:4000',
    strip_prefix: true,
    headers: { 'X-Frame-Options': 'DENY' },
    cache_control: 'no-store',
    request_timeout: '45s',
    rate_limit: { requests: 30, window: '1m0s', by: 'ip' },
  },
  {
    match: '/static/',
    root: '/var/www/assets',
    cache_control: 'public, max-age=60',
  },
  {
    match: '/old/',
    redirect: 'https://example.org/new/',
    redirect_code: 308,
    headers: { 'X-Moved': '1' },
  },
  // A headers-only route: cache_control IS its editable target.
  { match: '/health', cache_control: 'no-cache', headers: { 'X-Probe': 'yes' } },
];

async function openRoutesAndSave(locations: unknown[]) {
  apiMocks.fetchDomainDetail.mockResolvedValue({
    host: 'a.example',
    type: 'static',
    aliases: null,
    ssl: { mode: 'auto' },
    root: '/var/www/a',
    locations,
  });
  render(
    <MemoryRouter initialEntries={['/domains/a.example']}>
      <Routes>
        <Route path="/domains/:host" element={<DomainDetail />} />
      </Routes>
    </MemoryRouter>,
  );
  await settle();
  fireEvent.click(screen.getByRole('button', { name: /Routes/ }));
  await settle();
  fireEvent.click(screen.getByRole('button', { name: /Save Routes/ }));
  await settle();
  expect(apiMocks.updateDomain).toHaveBeenCalledTimes(1);
  return (apiMocks.updateDomain.mock.calls[0][1] as { locations: Record<string, unknown>[] }).locations;
}

describe('Routes tab save round-trips what it does not edit', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => fn.mockReset());
    apiMocks.fetchDomainStats.mockResolvedValue({});
    apiMocks.fetchDomainAnalytics.mockResolvedValue(null);
    apiMocks.fetchDiskUsage.mockResolvedValue({ bytes: 0, human: '—' });
    apiMocks.fetchWPSites.mockResolvedValue([]);
    apiMocks.updateDomain.mockResolvedValue({ status: 'ok' });
  });

  it('keeps headers, request_timeout, rate_limit and cache_control', async () => {
    const saved = await openRoutesAndSave(LOCATIONS);
    console.log('EXPECTED: every location unchanged\nACTUAL: ' + JSON.stringify(saved));
    expect(saved).toEqual(LOCATIONS);
  });

  // Edge: editing the managed target still wins over the saved value.
  it('applies an edited target on top of the preserved fields', async () => {
    apiMocks.fetchDomainDetail.mockResolvedValue({
      host: 'a.example', type: 'static', aliases: null, ssl: { mode: 'auto' }, root: '/var/www/a',
      locations: [LOCATIONS[0]],
    });
    render(
      <MemoryRouter initialEntries={['/domains/a.example']}>
        <Routes><Route path="/domains/:host" element={<DomainDetail />} /></Routes>
      </MemoryRouter>,
    );
    await settle();
    fireEvent.click(screen.getByRole('button', { name: /Routes/ }));
    await settle();
    fireEvent.change(screen.getByDisplayValue('http://127.0.0.1:4000'), { target: { value: 'http://127.0.0.1:5000' } });
    fireEvent.click(screen.getByRole('button', { name: /Save Routes/ }));
    await settle();
    const saved = (apiMocks.updateDomain.mock.calls[0][1] as { locations: Record<string, unknown>[] }).locations;
    expect(saved).toEqual([{ ...LOCATIONS[0], proxy_pass: 'http://127.0.0.1:5000' }]);
  });

  // Control: a location with nothing beyond the managed fields round-trips.
  it('control: a plain proxy route is saved unchanged', async () => {
    const plain = [{ match: '/api/', proxy_pass: 'http://127.0.0.1:4000', strip_prefix: false }];
    const saved = await openRoutesAndSave(plain);
    expect(saved).toEqual(plain);
  });
});
