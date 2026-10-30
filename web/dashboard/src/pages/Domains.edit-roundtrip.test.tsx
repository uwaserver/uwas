import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import Domains from './Domains';
import { ConfirmContext } from '@/components/useConfirm';

// F46302: the server replaces the whole proxy block whenever a patch carries
// proxy.upstreams, and the whole redirect block whenever it carries
// redirect.target (config.MergeDomain). The edit form manages only the
// upstream addresses + algorithm (proxy) and target + status (redirect), and
// built those blocks from scratch, so saving ANY edit of a proxy domain reset
// health checks, sticky sessions, circuit breaker, websocket/gRPC flags,
// timeouts, retries, canary and every upstream weight, and saving a redirect
// domain dropped preserve_path. The security and cache blocks already
// round-trip the saved value; proxy and redirect must too.

const apiMocks = vi.hoisted(() => ({
  fetchDomains: vi.fn(),
  addDomain: vi.fn(),
  updateDomain: vi.fn(),
  deleteDomain: vi.fn(),
  fetchDomainDetail: vi.fn(),
  fetchCerts: vi.fn(),
  purgeCacheForHost: vi.fn(),
  fetchPHP: vi.fn(),
  fetchServerIPs: vi.fn(),
  fetchDomainHealth: vi.fn(),
  fetchApps: vi.fn(),
  setPinCode: vi.fn(),
  clearPinCode: vi.fn(),
  bulkImportDomains: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

const confirmValue = {
  confirmAction: vi.fn().mockResolvedValue(true),
  promptText: vi.fn().mockResolvedValue(null),
};

function renderPage() {
  return render(
    <MemoryRouter>
      <ConfirmContext.Provider value={confirmValue}>
        <Domains />
      </ConfirmContext.Provider>
    </MemoryRouter>,
  );
}

type Body = Record<string, { upstreams?: { address: string }[] } & Record<string, unknown>>;

const flush = () => act(async () => {});

const SAVED_PROXY = {
  upstreams: [
    { address: '10.0.0.1:8080', weight: 3 },
    { address: '10.0.0.2:8080', weight: 1 },
  ],
  algorithm: 'least-conn',
  health_check: { path: '/healthz', interval: '10s', timeout: '2s', threshold: 3, rise: 2 },
  sticky: { type: 'cookie', cookie_name: 'srv' },
  circuit_breaker: { threshold: 5, timeout: '30s' },
  websocket: true,
  grpc: true,
  timeouts: { connect: '3s', read: '60s' },
  max_retries: 2,
  canary: { enabled: true, weight: 10 },
};

const SAVED_REDIRECT = { target: 'https://new.example.com', status: 308, preserve_path: true };

function listRow(host: string, type: string) {
  return { host, type, aliases: null, ssl: 'auto', root: '' };
}

function detailFor(host: string, type: string, extra: Record<string, unknown>) {
  return {
    host,
    type,
    aliases: null,
    ssl: { mode: 'auto', force_ssl: false, cert: '', key: '', min_version: '' },
    root: '',
    canonical_host: 'apex',
    ...extra,
  };
}

async function editAndSubmit(host: string, type: string, extra: Record<string, unknown>) {
  apiMocks.fetchDomains.mockResolvedValue([listRow(host, type)]);
  apiMocks.fetchDomainDetail.mockResolvedValue(detailFor(host, type, extra));
  apiMocks.updateDomain.mockResolvedValue({});
  renderPage();
  await flush();
  fireEvent.click(screen.getByTitle('Edit domain'));
  await flush();
  // Change an unrelated field so the submit is a genuine edit.
  fireEvent.change(screen.getByLabelText('SSL Mode'), { target: { value: 'manual' } });
  fireEvent.click(screen.getByRole('button', { name: 'Update Domain' }));
  await flush();
  expect(apiMocks.updateDomain).toHaveBeenCalledTimes(1);
  return apiMocks.updateDomain.mock.calls[0][1] as Body;
}

describe('Domains edit round-trips the proxy and redirect blocks', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => {
      if (vi.isMockFunction(fn)) fn.mockReset();
    });
    apiMocks.fetchCerts.mockResolvedValue([]);
    apiMocks.fetchPHP.mockResolvedValue([]);
    apiMocks.fetchApps.mockResolvedValue([]);
    apiMocks.fetchServerIPs.mockResolvedValue({ ips: [], public_ip: '' });
    apiMocks.fetchDomainHealth.mockResolvedValue([]);
  });

  it('keeps every saved proxy setting and upstream weight', async () => {
    const body = await editAndSubmit('api.example.com', 'proxy', { proxy: SAVED_PROXY });
    console.log('EXPECTED: proxy block equals the saved block\nACTUAL: ' + JSON.stringify(body.proxy));
    expect(body.proxy).toEqual(SAVED_PROXY);
  });

  it('keeps preserve_path on a redirect domain', async () => {
    const body = await editAndSubmit('old.example.com', 'redirect', { redirect: SAVED_REDIRECT });
    console.log('EXPECTED: redirect block equals the saved block\nACTUAL: ' + JSON.stringify(body.redirect));
    expect(body.redirect).toEqual(SAVED_REDIRECT);
  });

  // Edge: editing the managed fields still wins over the saved values, and
  // saved weights follow their address (a new address starts at 1).
  it('applies an added upstream and a new algorithm on top of the saved block', async () => {
    apiMocks.fetchDomains.mockResolvedValue([listRow('api.example.com', 'proxy')]);
    apiMocks.fetchDomainDetail.mockResolvedValue(detailFor('api.example.com', 'proxy', { proxy: SAVED_PROXY }));
    apiMocks.updateDomain.mockResolvedValue({});
    renderPage();
    await flush();
    fireEvent.click(screen.getByTitle('Edit domain'));
    await flush();
    fireEvent.click(screen.getByRole('button', { name: /Add Upstream/ }));
    const inputs = screen.getAllByPlaceholderText('http://127.0.0.1:3000');
    fireEvent.change(inputs[inputs.length - 1], { target: { value: '10.0.0.3:8080' } });
    fireEvent.click(screen.getByText('Round Robin'));
    fireEvent.click(screen.getByRole('button', { name: 'Update Domain' }));
    await flush();
    const body = apiMocks.updateDomain.mock.calls[0][1] as Body;
    expect(body.proxy).toEqual({
      ...SAVED_PROXY,
      algorithm: 'round-robin',
      upstreams: [
        { address: '10.0.0.1:8080', weight: 3 },
        { address: '10.0.0.2:8080', weight: 1 },
        { address: '10.0.0.3:8080', weight: 1 },
      ],
    });
  });

  it('drops a removed upstream but keeps the other weight and every other setting', async () => {
    apiMocks.fetchDomains.mockResolvedValue([listRow('api.example.com', 'proxy')]);
    apiMocks.fetchDomainDetail.mockResolvedValue(detailFor('api.example.com', 'proxy', { proxy: SAVED_PROXY }));
    apiMocks.updateDomain.mockResolvedValue({});
    renderPage();
    await flush();
    fireEvent.click(screen.getByTitle('Edit domain'));
    await flush();
    const inputs = screen.getAllByPlaceholderText('http://127.0.0.1:3000');
    expect(inputs.length).toBe(2);
    // The remove button is the sibling of the first upstream input.
    const row = inputs[0].parentElement as HTMLElement;
    fireEvent.click(row.querySelector('button') as HTMLButtonElement);
    fireEvent.click(screen.getByRole('button', { name: 'Update Domain' }));
    await flush();
    const body = apiMocks.updateDomain.mock.calls[0][1] as Body;
    expect(body.proxy).toEqual({
      ...SAVED_PROXY,
      upstreams: [{ address: '10.0.0.2:8080', weight: 1 }],
    });
  });

  // Control: the unrelated field the operator changed reaches the payload.
  it('control: the edited SSL mode still reaches the payload', async () => {
    const body = await editAndSubmit('api.example.com', 'proxy', { proxy: SAVED_PROXY });
    expect(body.ssl).toMatchObject({ mode: 'manual' });
  });
});
