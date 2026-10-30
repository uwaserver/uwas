import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import Domains from './Domains';
import { ConfirmContext } from '@/components/useConfirm';

// F46303: the "Add Upstream" button pushed an empty entry and then rebuilt the
// stored value with filter(Boolean), so the new row vanished immediately and
// the button did nothing. An operator could only add a second upstream by
// typing a comma into the first input.

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

type Body = Record<string, { upstreams?: { address: string }[] } & Record<string, unknown>>;

const flush = () => act(async () => {});

const DETAIL = {
  host: 'api.example.com',
  type: 'proxy',
  aliases: null,
  ssl: { mode: 'auto', force_ssl: false, cert: '', key: '', min_version: '' },
  root: '',
  canonical_host: 'apex',
  proxy: { upstreams: [{ address: '10.0.0.1:8080', weight: 1 }], algorithm: 'round-robin' },
};

async function openEdit() {
  apiMocks.fetchDomains.mockResolvedValue([{ host: 'api.example.com', type: 'proxy', aliases: null, ssl: 'auto', root: '' }]);
  apiMocks.fetchDomainDetail.mockResolvedValue(DETAIL);
  apiMocks.updateDomain.mockResolvedValue({});
  render(
    <MemoryRouter>
      <ConfirmContext.Provider value={confirmValue}>
        <Domains />
      </ConfirmContext.Provider>
    </MemoryRouter>,
  );
  await flush();
  fireEvent.click(screen.getByTitle('Edit domain'));
  await flush();
}

const upstreamInputs = () => screen.getAllByPlaceholderText('http://127.0.0.1:3000');

describe('Add Upstream button', () => {
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

  it('adds an empty row that the operator can fill in', async () => {
    await openEdit();
    const before = upstreamInputs().length;
    fireEvent.click(screen.getByRole('button', { name: /Add Upstream/ }));
    const after = upstreamInputs().length;
    console.log(`EXPECTED: ${before + 1} upstream inputs after the click\nACTUAL: ${after}`);
    expect(after).toBe(before + 1);
  });

  it('sends the new address together with the existing one', async () => {
    await openEdit();
    fireEvent.click(screen.getByRole('button', { name: /Add Upstream/ }));
    const inputs = upstreamInputs();
    fireEvent.change(inputs[inputs.length - 1], { target: { value: '10.0.0.9:8080' } });
    fireEvent.click(screen.getByRole('button', { name: 'Update Domain' }));
    await flush();
    const body = apiMocks.updateDomain.mock.calls[0][1] as Body;
    expect((body.proxy.upstreams ?? []).map(u => u.address)).toEqual(['10.0.0.1:8080', '10.0.0.9:8080']);
  });

  // Edge: an empty row the operator never fills in is not sent.
  it('does not send an empty upstream row', async () => {
    await openEdit();
    fireEvent.click(screen.getByRole('button', { name: /Add Upstream/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Update Domain' }));
    await flush();
    const body = apiMocks.updateDomain.mock.calls[0][1] as Body;
    expect(body.proxy.upstreams).toEqual([{ address: '10.0.0.1:8080', weight: 1 }]);
  });

  // Control (independent of the Add button): removing a saved row still works.
  it('control: the remove button deletes a row', async () => {
    apiMocks.fetchDomains.mockResolvedValue([{ host: 'api.example.com', type: 'proxy', aliases: null, ssl: 'auto', root: '' }]);
    apiMocks.fetchDomainDetail.mockResolvedValue({
      ...DETAIL,
      proxy: {
        upstreams: [{ address: '10.0.0.1:8080', weight: 1 }, { address: '10.0.0.2:8080', weight: 1 }],
        algorithm: 'round-robin',
      },
    });
    render(
      <MemoryRouter>
        <ConfirmContext.Provider value={confirmValue}>
          <Domains />
        </ConfirmContext.Provider>
      </MemoryRouter>,
    );
    await flush();
    fireEvent.click(screen.getByTitle('Edit domain'));
    await flush();
    expect(upstreamInputs().length).toBe(2);
    const row = upstreamInputs()[0].parentElement as HTMLElement;
    fireEvent.click(row.querySelector('button') as HTMLButtonElement);
    expect(upstreamInputs().length).toBe(1);
  });
});
