import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import Domains from './Domains';
import { ConfirmContext } from '@/components/useConfirm';

// ── Mocks ───────────────────────────────────────────────────────────────────
// One vi.fn per value imported by Domains.tsx; mount-time fetchers get
// resolved defaults in beforeEach, the rest stay inert unless invoked.
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

// Flush mount effects + in-flight promise chains without advancing the clock.
const flush = () => act(async () => {});

const DOMAIN = {
  host: 'example.com',
  type: 'static',
  aliases: null,
  ssl: 'auto',
  root: '/var/www/example.com/public_html',
};

// The detail endpoint returns the FULL security block the server stores.
// The Domains edit form only manages three fields of it (WAF toggle,
// Cloudflare-only, blocked paths) — everything else must round-trip.
const DETAIL_SECURITY = {
  waf: { enabled: true, bypass_paths: ['/api/'] },
  cloudflare_only: false,
  rate_limit: { requests: 100, window: '1m0s', by: 'ip' },
  ip_whitelist: ['192.168.1.1'],
  ip_blacklist: ['10.0.0.1'],
  hotlink_protection: { enabled: true },
  geo_block_countries: ['US'],
  geo_allow_countries: ['TR'],
  blocked_paths: [],
};

const DETAIL = {
  host: 'example.com',
  type: 'static',
  aliases: null,
  ssl: { mode: 'auto', force_ssl: false, cert: '', key: '', min_version: '' },
  root: '/var/www/example.com/public_html',
  security: DETAIL_SECURITY,
  canonical_host: 'apex',
};

async function openEditAndSubmitWithSslMode(mode: string) {
  fireEvent.click(screen.getByTitle('Edit domain'));
  await flush(); // startEdit fetches the detail and prefills the form

  fireEvent.change(screen.getByLabelText('SSL Mode'), { target: { value: mode } });
  fireEvent.click(screen.getByRole('button', { name: 'Update Domain' }));
  await flush();
}

describe('Domains edit round-trips the full security block', () => {
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
    apiMocks.fetchDomainDetail.mockResolvedValue(DETAIL);
    apiMocks.updateDomain.mockResolvedValue({ ...DOMAIN });
    apiMocks.addDomain.mockResolvedValue({ ...DOMAIN });
  });

  // Control: the unrelated field the operator changed reaches the payload.
  it('sends the edited SSL mode on update', async () => {
    renderPage();
    await flush();
    await openEditAndSubmitWithSslMode('manual');

    expect(apiMocks.updateDomain).toHaveBeenCalledTimes(1);
    const body = apiMocks.updateDomain.mock.calls[0][1] as Record<string, unknown>;
    expect(body.ssl).toMatchObject({ mode: 'manual' });
  });

  // Regression: the server replaces the WHOLE security block whenever the
  // patch body carries a `security` key (config.MergeDomain: presence-keyed
  // whole-block replace, no sub-field merge). The edit form only manages
  // WAF / Cloudflare-only / blocked paths, so it must round-trip every other
  // section from the fetched detail — otherwise editing an unrelated field
  // silently wipes the domain's rate limit, IP black/whitelist, hotlink
  // protection, and geo rules.
  it('round-trips security sections the edit form does not manage', async () => {
    renderPage();
    await flush();
    await openEditAndSubmitWithSslMode('manual');

    const body = apiMocks.updateDomain.mock.calls[0][1] as { security: Record<string, any> };
    expect(body.security.rate_limit).toEqual({ requests: 100, window: '1m0s', by: 'ip' });
    expect(body.security.ip_blacklist).toEqual(['10.0.0.1']);
    expect(body.security.ip_whitelist).toEqual(['192.168.1.1']);
    expect(body.security.hotlink_protection).toEqual({ enabled: true });
    expect(body.security.geo_block_countries).toEqual(['US']);
    expect(body.security.geo_allow_countries).toEqual(['TR']);
    // Nested WAF fields the form does not manage must survive too.
    expect(body.security.waf.bypass_paths).toEqual(['/api/']);
    // The one list the form DOES manage wins over the saved value.
    expect(body.security.blocked_paths).toEqual([]);
  });

  // Control: the three form-managed security fields follow the form state
  // (prefilled from the detail), independent of the round-trip fix.
  it('applies the form-managed security fields on update', async () => {
    renderPage();
    await flush();
    await openEditAndSubmitWithSslMode('manual');

    const body = apiMocks.updateDomain.mock.calls[0][1] as { security: Record<string, any> };
    expect(body.security.waf.enabled).toBe(true);
    expect(body.security.cloudflare_only).toBe(false);
  });
});

// ── .htaccess toggle persistence ────────────────────────────────────────────
// The server validates htaccess.mode as exactly "import"|"off"
// (internal/config/validate.go) and replaces the whole block when the patch
// carries the key (internal/config/merge.go). The edit form prefills
// htaccessEnabled from the saved mode and renders the toggle for php
// (checkbox) and every non-redirect type (Security-section button) — but the
// old submit path never read it: php always sent "import", every other type
// sent no block at all. A saved "off" was silently re-enabled on any php
// edit, and toggling on a static/proxy domain did nothing.
const PHP_DOMAIN = {
  host: 'phpsite.com',
  type: 'php',
  aliases: null,
  ssl: 'auto',
  root: '/var/www/phpsite.com/public_html',
};

const PHP_DETAIL = {
  host: 'phpsite.com',
  type: 'php',
  aliases: null,
  ssl: { mode: 'auto', force_ssl: false, cert: '', key: '', min_version: '' },
  root: '/var/www/phpsite.com/public_html',
  security: undefined,
  htaccess: { mode: 'off' },
};

// The submit button is disabled for php domains while no non-CLI PHP
// install exists (Domains.tsx submit disabled condition), so the edit
// flow needs at least one install from fetchPHP to be submittable.
const PHP_INSTALL = { version: '8.3', sapi: 'fpm-fcgi', listen_addr: '/run/php/php8.3-fpm.sock', status: 'active' };

describe('Domains edit persists the .htaccess toggle', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => {
      if (vi.isMockFunction(fn)) fn.mockReset();
    });
    apiMocks.fetchDomains.mockResolvedValue([DOMAIN]);
    apiMocks.fetchCerts.mockResolvedValue([]);
    apiMocks.fetchPHP.mockResolvedValue([PHP_INSTALL]);
    apiMocks.fetchApps.mockResolvedValue([]);
    apiMocks.fetchServerIPs.mockResolvedValue({ ips: [], public_ip: '' });
    apiMocks.fetchDomainHealth.mockResolvedValue([]);
    apiMocks.fetchDomainDetail.mockResolvedValue(DETAIL);
    apiMocks.updateDomain.mockResolvedValue({ ...DOMAIN });
    apiMocks.addDomain.mockResolvedValue({ ...DOMAIN });
  });

  async function openEditAndSubmit(toggleHtaccess?: 'checkbox' | 'button') {
    fireEvent.click(screen.getByTitle('Edit domain'));
    await flush(); // startEdit fetches the detail and prefills the form

    if (toggleHtaccess === 'checkbox') {
      fireEvent.click(screen.getByLabelText('Import .htaccess rules'));
    } else if (toggleHtaccess === 'button') {
      // Accessible name concatenates the button's two text lines.
      fireEvent.click(screen.getByRole('button', { name: /\.htaccess Import/ }));
    }
    fireEvent.click(screen.getByRole('button', { name: 'Update Domain' }));
    await flush();
  }

  // Regression: a php domain saved with htaccess "off" must stay off when
  // the operator edits anything else — the old code force-sent "import".
  it('keeps htaccess off for a php domain saved with mode off', async () => {
    apiMocks.fetchDomains.mockResolvedValue([PHP_DOMAIN]);
    apiMocks.fetchDomainDetail.mockResolvedValue(PHP_DETAIL);
    renderPage();
    await flush();
    await openEditAndSubmit();

    expect(apiMocks.updateDomain).toHaveBeenCalledTimes(1);
    const body = apiMocks.updateDomain.mock.calls[0][1] as { htaccess: { mode: string } };
    expect(body.htaccess.mode).toBe('off');
  });

  // Control: with the checkbox ticked (toggled from the saved off state),
  // "import" reaches the payload.
  it('sends import when the php checkbox is enabled', async () => {
    apiMocks.fetchDomains.mockResolvedValue([PHP_DOMAIN]);
    apiMocks.fetchDomainDetail.mockResolvedValue(PHP_DETAIL);
    renderPage();
    await flush();
    await openEditAndSubmit('checkbox');

    const body = apiMocks.updateDomain.mock.calls[0][1] as { htaccess: { mode: string } };
    expect(body.htaccess.mode).toBe('import');
  });

  // Regression: the Security-section toggle on a non-php domain must reach
  // the payload — the old code sent no htaccess block for static domains,
  // so the toggle did nothing.
  it('sends import when a static domain toggles htaccess on', async () => {
    renderPage();
    await flush();
    await openEditAndSubmit('button');

    const body = apiMocks.updateDomain.mock.calls[0][1] as { htaccess: { mode: string } };
    expect(body.htaccess.mode).toBe('import');
  });
});

// ── cache block round-trip ──────────────────────────────────────────────────
// DomainCache carries five fields (enabled, ttl, rules, tags, esi —
// internal/config/cache.go), and MergeDomain replaces the whole block when
// the patch carries a `cache` key. The edit form manages only enabled/ttl,
// so an edit must round-trip rules/tags/esi from the fetched detail —
// otherwise editing any field of a YAML-configured domain wipes its cache
// rules, tags, and ESI flag.
const CACHE_DETAIL = {
  ...DETAIL,
  cache: {
    enabled: true,
    ttl: 300,
    rules: [{ path: '/assets/*', ttl: 86400 }],
    tags: ['prod'],
    esi: true,
  },
};

describe('Domains edit round-trips the cache block', () => {
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
    apiMocks.fetchDomainDetail.mockResolvedValue(CACHE_DETAIL);
    apiMocks.updateDomain.mockResolvedValue({ ...DOMAIN });
    apiMocks.addDomain.mockResolvedValue({ ...DOMAIN });
  });

  // Regression: rules/tags/esi are not form-managed — they must survive.
  it('round-trips cache sections the edit form does not manage', async () => {
    renderPage();
    await flush();
    await openEditAndSubmitWithSslMode('manual');

    expect(apiMocks.updateDomain).toHaveBeenCalledTimes(1);
    const body = apiMocks.updateDomain.mock.calls[0][1] as { cache: Record<string, any> };
    expect(body.cache.rules).toEqual([{ path: '/assets/*', ttl: 86400 }]);
    expect(body.cache.tags).toEqual(['prod']);
    expect(body.cache.esi).toBe(true);
  });

  // Control: the form-managed cache fields follow the form state
  // (prefilled from the detail), independent of the round-trip fix.
  it('applies the form-managed cache fields on update', async () => {
    renderPage();
    await flush();
    await openEditAndSubmitWithSslMode('manual');

    const body = apiMocks.updateDomain.mock.calls[0][1] as { cache: Record<string, any> };
    expect(body.cache.enabled).toBe(true);
    expect(body.cache.ttl).toBe(300);
  });

  // Control: a domain without a saved cache block keeps the previous
  // behavior — the block still goes out with the form defaults.
  it('sends the default cache block when no cache config exists', async () => {
    apiMocks.fetchDomainDetail.mockResolvedValue(DETAIL);
    renderPage();
    await flush();
    await openEditAndSubmitWithSslMode('manual');

    const body = apiMocks.updateDomain.mock.calls[0][1] as { cache: Record<string, any> };
    expect(body.cache.enabled).toBe(false);
    expect(body.cache.rules).toBeUndefined();
  });
});
