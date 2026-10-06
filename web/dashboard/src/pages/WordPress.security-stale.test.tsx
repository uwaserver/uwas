import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, act, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import WordPress from './WordPress';

// Real production component; only the @/lib/api boundary is mocked.
// wpSecurityStatus is host-tagged so site A's stale toggle continuation can
// be ordered after site B's guarded load deterministically.

const m = vi.hoisted(() => ({
  fetchWPSites: vi.fn(),
  fetchWPSiteDetail: vi.fn(),
  wpSecurityStatus: vi.fn(),
  wpListUsers: vi.fn(),
  wpHarden: vi.fn(),
  fetchDomains: vi.fn(),
  fetchDBStatus: vi.fn(),
  fetchDockerDBs: vi.fn(),
  installWordPress: vi.fn(),
  fetchWPInstallStatus: vi.fn(),
  wpUpdateCore: vi.fn(),
  wpUpdatePlugins: vi.fn(),
  wpPluginAction: vi.fn(),
  wpFixPermissions: vi.fn(),
  wpToggleDebug: vi.fn(),
  wpErrorLog: vi.fn(),
  wpChangePassword: vi.fn(),
  wpOptimizeDB: vi.fn(),
}));

vi.mock('@/lib/api', () => m);

const SEC_A = {
  xmlrpc_disabled: true, file_edit_disabled: true, ssl_forced: true,
  debug_enabled: false, directory_listing_blocked: true, table_prefix: 'wpA_',
  php_version: '8.1.31',
};
const SEC_B = {
  xmlrpc_disabled: false, file_edit_disabled: false, ssl_forced: false,
  debug_enabled: false, directory_listing_blocked: false, table_prefix: 'wpB_',
  php_version: '8.2.28',
};

function site(domain: string) {
  return {
    domain,
    version: '6.7.1',
    db_name: `wp_${domain.split('.')[0]}`,
    health: { plugin_updates: 0, core_update: false, ssl: true, debug: false, php_version: '8.1.31' },
    permissions: { wp_config: '640', wp_content: '755', uploads: '755', htaccess: '644', owner: 'www-data', writable: true },
    plugins: [], themes: [],
  };
}

async function flush() { await act(async () => { await Promise.resolve(); }); }

function xmlrpcToggle(): HTMLElement {
  const label = screen.getByText('XML-RPC');
  const row = label.closest('div.flex.items-center.justify-between');
  expect(row).toBeTruthy();
  const btn = row!.querySelector('button');
  expect(btn).toBeTruthy();
  return btn!;
}

describe('WordPress security panel stale-response race', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    m.fetchDomains.mockResolvedValue([]);
    m.fetchDBStatus.mockResolvedValue({ installed: true, running: true });
    m.fetchDockerDBs.mockResolvedValue({ containers: [] });
    m.wpListUsers.mockResolvedValue([]);
    m.fetchWPSiteDetail.mockImplementation(async (d: string) => site(d));
  });
  afterEach(() => { vi.restoreAllMocks(); });

  it('control: expanding B shows B’s own security status', async () => {
    m.fetchWPSites.mockResolvedValue([site('a.example'), site('b.example')]);
    m.wpSecurityStatus.mockImplementation(async (d: string) =>
      d === 'a.example' ? { ...SEC_A } : { ...SEC_B });

    render(<MemoryRouter><WordPress /></MemoryRouter>);
    await waitFor(() => expect(screen.getByText('a.example')).toBeTruthy());
    await act(async () => { fireEvent.click(screen.getByText('b.example')); });
    await act(async () => { fireEvent.click(screen.getByText('Security')); });
    await flush();
    await waitFor(() => expect(screen.getByText('XML-RPC enabled')).toBeTruthy());
    expect(screen.queryByText('XML-RPC disabled')).toBeNull();
  });

  it('keeps B’s security status when A’s toggle continuation lands last', async () => {
    m.fetchWPSites.mockResolvedValue([site('a.example'), site('b.example')]);
    m.wpSecurityStatus.mockImplementation(async (d: string) =>
      d === 'a.example' ? { ...SEC_A } : { ...SEC_B });
    // Park wpHarden for A so its continuation resolves after the switch to B.
    let releaseHarden: () => void = () => {};
    const hardenGate = new Promise<void>(r => { releaseHarden = r; });
    m.wpHarden.mockImplementation(async () => { await hardenGate; return { ok: true }; });

    render(<MemoryRouter><WordPress /></MemoryRouter>);
    await waitFor(() => expect(screen.getByText('a.example')).toBeTruthy());

    // Expand A; its security status loads (wpSecurityStatus call 1 → SEC_A).
    await act(async () => { fireEvent.click(screen.getByText('a.example')); });
    await act(async () => { fireEvent.click(screen.getByText('Security')); });
    await flush();
    await waitFor(() => expect(screen.getByText('XML-RPC')).toBeTruthy());
    expect(screen.getByText('XML-RPC disabled')).toBeTruthy();

    // Click A's XML-RPC toggle → wpHarden('a.example') parks.
    await act(async () => { fireEvent.click(xmlrpcToggle()); });
    expect(m.wpHarden).toHaveBeenCalledWith('a.example', { disable_xmlrpc: false });

    // Switch: collapse A, expand B (activeSiteRef becomes 'b.example');
    // B's guarded load (wpSecurityStatus call 2 → SEC_B) commits.
    await act(async () => { fireEvent.click(screen.getByText('a.example')); });
    await act(async () => { fireEvent.click(screen.getByText('b.example')); });
    await act(async () => { fireEvent.click(screen.getByText('Security')); });
    await flush();
    await waitFor(() => expect(screen.getByText('XML-RPC enabled')).toBeTruthy());

    // A's parked toggle continuation lands LAST: wpHarden resolves, the
    // unguarded continuation re-reads wpSecurityStatus('a.example') (call 3)
    // and calls setSecurity — pre-fix this overwrites B's panel.
    await act(async () => { releaseHarden(); });
    await flush();

    // CONTRACT: B's panel still shows B's status; the stale A commit must not render.
    expect(screen.getByText('XML-RPC enabled')).toBeTruthy();
    expect(screen.queryByText('XML-RPC disabled')).toBeNull();
  });
});
