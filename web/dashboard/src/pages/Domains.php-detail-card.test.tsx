import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import Domains from './Domains';
import { ConfirmContext } from '@/components/useConfirm';

// F46300: the domain detail endpoint returns the stored config.Domain, so a
// PHP domain's php block carries `timeout` as a Go duration string ("30s",
// config.Duration marshals to JSON that way) and `max_upload` as a byte count
// (config.ByteSize marshals to a number). The detail card read
// Number("30s") (NaN, so Timeout was always "--") and a non-existent
// `upload_max_size` key (Upload Max was always "--"), so an operator could
// not see the limits they had configured.

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

const flush = () => act(async () => {});

const DOMAIN = {
  host: 'shop.example.com',
  type: 'php',
  aliases: null,
  ssl: 'auto',
  root: '/var/www/shop/public_html',
};

function detailWith(php: Record<string, unknown>) {
  return {
    host: 'shop.example.com',
    type: 'php',
    aliases: null,
    ssl: { mode: 'auto', force_ssl: false, cert: '', key: '', min_version: '' },
    root: '/var/www/shop/public_html',
    php,
  };
}

async function openDetail(php: Record<string, unknown>) {
  apiMocks.fetchDomainDetail.mockResolvedValue(detailWith(php));
  renderPage();
  await flush();
  fireEvent.click(screen.getAllByText('shop.example.com')[0]);
  await flush();
}

// The value cell of a DetailRow is the sibling of its label.
function valueOf(label: string): string {
  const el = screen.getByText(label);
  return el.parentElement?.textContent?.replace(label, '').trim() ?? '';
}

describe('Domains PHP detail card shows the configured limits', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => {
      if (vi.isMockFunction(fn)) fn.mockReset();
    });
    apiMocks.fetchDomains.mockResolvedValue([DOMAIN]);
    apiMocks.fetchCerts.mockResolvedValue([]);
    apiMocks.fetchPHP.mockResolvedValue([]);
    apiMocks.fetchApps.mockResolvedValue([]);
    apiMocks.fetchServerIPs.mockResolvedValue({ ips: [], public_ip: '' });
    apiMocks.fetchDomainHealth.mockResolvedValue([]);
  });

  it('shows the Go duration timeout and the byte-count upload limit', async () => {
    await openDetail({ fpm_address: '127.0.0.1:9000', timeout: '30s', max_upload: 67108864 });
    const timeout = valueOf('Timeout');
    const upload = valueOf('Upload Max');
    console.log(`EXPECTED: Timeout "30s", Upload Max "64 MB"\nACTUAL: Timeout "${timeout}", Upload Max "${upload}"`);
    expect(timeout).toBe('30s');
    expect(upload).toBe('64 MB');
  });

  it('formats a multi-unit duration and a sub-MB upload limit', async () => {
    await openDetail({ fpm_address: '127.0.0.1:9000', timeout: '1m30s', max_upload: 512 * 1024 });
    expect(valueOf('Timeout')).toBe('1m30s');
    expect(valueOf('Upload Max')).toBe('512 KB');
  });

  // Control: a plain number of seconds (what a hand-written JSON config
  // yields) keeps working, and nothing configured shows the placeholder.
  it('control: numeric seconds still show, and unset limits show "--"', async () => {
    await openDetail({ fpm_address: '127.0.0.1:9000', timeout: 45 });
    expect(valueOf('Timeout')).toBe('45s');
    expect(valueOf('Upload Max')).toBe('--');
  });

  it('control: a zero timeout (unset) shows "--"', async () => {
    await openDetail({ fpm_address: '127.0.0.1:9000', timeout: 0 });
    expect(valueOf('Timeout')).toBe('--');
  });
});
