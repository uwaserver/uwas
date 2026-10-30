import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import Domains from './Domains';
import { ConfirmContext } from '@/components/useConfirm';

// F47001: the edit form only sent a `compression` block when the operator had
// moved away from the defaults ("an untouched form sends no block"). On edit
// that made the reverse impossible: a domain saved with min_size 4096 (or a
// types list) could not be set back to the defaults, because a form showing
// the defaults sends nothing and the server keeps the stored block. The edit
// reported success and changed nothing.

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

const flush = () => act(async () => {});

type Body = { compression?: { enabled?: boolean; min_size?: number; algorithms?: string[]; types?: string[] } };

async function editCompression(saved: Record<string, unknown> | undefined, change: () => void): Promise<Body> {
  apiMocks.fetchDomains.mockResolvedValue([{ host: 'a.example', type: 'static', aliases: null, ssl: 'auto', root: '' }]);
  apiMocks.fetchDomainDetail.mockResolvedValue({
    host: 'a.example',
    type: 'static',
    aliases: null,
    ssl: { mode: 'auto', force_ssl: false, cert: '', key: '', min_version: '' },
    root: '/var/www/a',
    canonical_host: 'apex',
    compression: saved,
  });
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
  change();
  fireEvent.click(screen.getByRole('button', { name: 'Update Domain' }));
  await flush();
  expect(apiMocks.updateDomain).toHaveBeenCalledTimes(1);
  return apiMocks.updateDomain.mock.calls[0][1] as Body;
}

describe('Domains edit can reset compression to the defaults', () => {
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

  it('sends the block when a saved custom min size is set back to 1024', async () => {
    const body = await editCompression({ enabled: true, min_size: 4096 }, () => {
      fireEvent.change(screen.getByLabelText('Min size (bytes)'), { target: { value: '1024' } });
    });
    console.log('EXPECTED: compression block with min_size 1024\nACTUAL: ' + JSON.stringify(body.compression));
    expect(body.compression).toMatchObject({ enabled: true, min_size: 1024 });
  });

  // Control: a domain with no saved compression block still sends none when the
  // form is untouched (the server reads that as "on, built-in defaults").
  it('control: an untouched form for a domain without a block sends none', async () => {
    const body = await editCompression(undefined, () => {
      fireEvent.change(screen.getByLabelText('SSL Mode'), { target: { value: 'manual' } });
    });
    expect(body.compression).toBeUndefined();
  });

  // Control: a saved custom block that the operator did not touch round-trips.
  it('control: a saved block is sent back unchanged when only SSL changes', async () => {
    const body = await editCompression({ enabled: true, min_size: 4096, types: ['text/html'] }, () => {
      fireEvent.change(screen.getByLabelText('SSL Mode'), { target: { value: 'manual' } });
    });
    expect(body.compression).toMatchObject({ enabled: true, min_size: 4096, types: ['text/html'] });
  });
});
