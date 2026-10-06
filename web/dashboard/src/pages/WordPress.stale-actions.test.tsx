import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, act, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import WordPress from './WordPress';

// Real production component; only the @/lib/api boundary is mocked.
// Site-scoped mocks are host-tagged so a parked action for site A can be
// released after the user has switched to site B (deterministic ordering).

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

const USER_A = { id: 1, login: 'admina', email: 'a@example.com', role: 'administrator' };
const USER_B = { id: 2, login: 'adminb', email: 'b@example.com', role: 'editor' };

async function flush() { await act(async () => { await Promise.resolve(); }); }

function park<T>(): { promise: Promise<T>; release: (v: T) => void } {
  let release!: (v: T) => void;
  const promise = new Promise<T>(r => { release = r; });
  return { promise, release };
}

async function expandSite(host: string) {
  await act(async () => { fireEvent.click(screen.getByText(host)); });
  await flush();
}

describe('WordPress site-scoped action continuations (stale commits)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    m.fetchWPSites.mockResolvedValue([site('a.example'), site('b.example')]);
    m.fetchWPSiteDetail.mockImplementation(async (d: string) => site(d));
    m.fetchDomains.mockResolvedValue([]);
    m.fetchDBStatus.mockResolvedValue({ installed: true, running: true });
    m.fetchDockerDBs.mockResolvedValue({ containers: [] });
    m.wpSecurityStatus.mockResolvedValue({
      xmlrpc_disabled: false, file_edit_disabled: false, ssl_forced: false,
      debug_enabled: false, directory_listing_blocked: false, table_prefix: 'wp_',
      php_version: '8.1.31',
    });
    m.wpListUsers.mockImplementation(async (d: string) => (d === 'a.example' ? [USER_A] : [USER_B]));
  });
  afterEach(() => { vi.restoreAllMocks(); });

  it('control: Error Log output renders for the site it was fetched on', async () => {
    m.wpErrorLog.mockResolvedValue({ log: 'A-LOG-CONTENT', message: '' });
    render(<MemoryRouter><WordPress /></MemoryRouter>);
    await waitFor(() => expect(screen.getByText('a.example')).toBeTruthy());
    await expandSite('a.example');
    await act(async () => { fireEvent.click(screen.getByText('Error Log')); });
    await flush();
    expect(await screen.findByText('A-LOG-CONTENT')).toBeTruthy();
  });

  it('keeps A’s error log out of B’s panel when the fetch lands after a site switch', async () => {
    const gate = park<{ log: string; message: string }>();
    m.wpErrorLog.mockImplementation(() => gate.promise);

    render(<MemoryRouter><WordPress /></MemoryRouter>);
    await waitFor(() => expect(screen.getByText('a.example')).toBeTruthy());

    // Expand A (overview tab) and click Error Log — the fetch parks.
    await expandSite('a.example');
    await act(async () => { fireEvent.click(screen.getByText('Error Log')); });
    expect(m.wpErrorLog).toHaveBeenCalledWith('a.example');

    // Switch to B, then release A's continuation.
    await expandSite('b.example');
    await act(async () => { gate.release({ log: 'A-LOG-CONTENT', message: '' }); });
    await flush();

    // CONTRACT: B's panel must not render A's log.
    expect(screen.queryByText('A-LOG-CONTENT')).toBeNull();
  });

  it('keeps A’s optimize output out of B’s panel when it lands after a site switch', async () => {
    const gate = park<{ output: string }>();
    m.wpOptimizeDB.mockImplementation(() => gate.promise);

    render(<MemoryRouter><WordPress /></MemoryRouter>);
    await waitFor(() => expect(screen.getByText('a.example')).toBeTruthy());

    // Expand A → Optimize sub-tab → Optimize (parks).
    await expandSite('a.example');
    await act(async () => { fireEvent.click(screen.getByText('DB Optimize')); });
    await act(async () => { fireEvent.click(screen.getByText('Optimize Database')); });
    expect(m.wpOptimizeDB).toHaveBeenCalledWith('a.example');

    // Switch to B → Optimize sub-tab → release A's continuation.
    await expandSite('b.example');
    await act(async () => { fireEvent.click(screen.getByText('DB Optimize')); });
    await act(async () => { gate.release({ output: 'A-OPT-OUTPUT' }); });
    await flush();

    // CONTRACT: B's Optimize tab must not render A's output.
    expect(screen.queryByText('A-OPT-OUTPUT')).toBeNull();
  });

  it('does not close B’s open password form when A’s password save lands late', async () => {
    const gate = park<Record<string, never>>();
    m.wpChangePassword.mockImplementation(() => gate.promise);

    render(<MemoryRouter><WordPress /></MemoryRouter>);
    await waitFor(() => expect(screen.getByText('a.example')).toBeTruthy());

    // Expand A → Users sub-tab → open the password form for A's user → save (parks).
    await expandSite('a.example');
    await act(async () => { fireEvent.click(screen.getByText('Users')); });
    await flush();
    await act(async () => { fireEvent.click(screen.getByText('Change Password')); });
    const inputA = screen.getByPlaceholderText('New password (min 8 chars)');
    await act(async () => { fireEvent.change(inputA, { target: { value: 'A-pass-123' } }); });
    await act(async () => { fireEvent.click(screen.getByText('Save')); });
    expect(m.wpChangePassword).toHaveBeenCalledWith('a.example', 'admina', 'A-pass-123');

    // Switch to B → Users sub-tab → open B's form and type.
    await expandSite('b.example');
    await act(async () => { fireEvent.click(screen.getByText('Users')); });
    await flush();
    await act(async () => { fireEvent.click(screen.getByText('Change Password')); });
    const inputB = screen.getByPlaceholderText('New password (min 8 chars)');
    await act(async () => { fireEvent.change(inputB, { target: { value: 'X' } }); });

    // A's save lands late.
    await act(async () => { gate.release({}); });
    await flush();

    // CONTRACT: B's open form must survive the stale continuation.
    expect(screen.getByPlaceholderText('New password (min 8 chars)')).toBeTruthy();
  });
});
