import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import WordPress from './WordPress';

// ── Mocks ───────────────────────────────────────────────────────────────────

const mockFetchDomains = vi.fn();
const mockInstallWordPress = vi.fn();
const mockFetchWPInstallStatus = vi.fn();
const mockFetchDBStatus = vi.fn();
const mockFetchDockerDBs = vi.fn();
const mockFetchWPSites = vi.fn();
const mockFetchWPSiteDetail = vi.fn();
const mockWpUpdateCore = vi.fn();
const mockWpUpdatePlugins = vi.fn();
const mockWpPluginAction = vi.fn();
const mockWpFixPermissions = vi.fn();
const mockWpToggleDebug = vi.fn();
const mockWpErrorLog = vi.fn();
const mockWpListUsers = vi.fn();
const mockWpChangePassword = vi.fn();
const mockWpSecurityStatus = vi.fn();
const mockWpHarden = vi.fn();
const mockWpOptimizeDB = vi.fn();

vi.mock('@/lib/api', () => ({
  fetchDomains: (...args: unknown[]) => mockFetchDomains(...args),
  installWordPress: (...args: unknown[]) => mockInstallWordPress(...args),
  fetchWPInstallStatus: (...args: unknown[]) => mockFetchWPInstallStatus(...args),
  fetchDBStatus: (...args: unknown[]) => mockFetchDBStatus(...args),
  fetchDockerDBs: (...args: unknown[]) => mockFetchDockerDBs(...args),
  fetchWPSites: (...args: unknown[]) => mockFetchWPSites(...args),
  fetchWPSiteDetail: (...args: unknown[]) => mockFetchWPSiteDetail(...args),
  wpUpdateCore: (...args: unknown[]) => mockWpUpdateCore(...args),
  wpUpdatePlugins: (...args: unknown[]) => mockWpUpdatePlugins(...args),
  wpPluginAction: (...args: unknown[]) => mockWpPluginAction(...args),
  wpFixPermissions: (...args: unknown[]) => mockWpFixPermissions(...args),
  wpToggleDebug: (...args: unknown[]) => mockWpToggleDebug(...args),
  wpErrorLog: (...args: unknown[]) => mockWpErrorLog(...args),
  wpListUsers: (...args: unknown[]) => mockWpListUsers(...args),
  wpChangePassword: (...args: unknown[]) => mockWpChangePassword(...args),
  wpSecurityStatus: (...args: unknown[]) => mockWpSecurityStatus(...args),
  wpHarden: (...args: unknown[]) => mockWpHarden(...args),
  wpOptimizeDB: (...args: unknown[]) => mockWpOptimizeDB(...args),
}));

// ── Fixtures ────────────────────────────────────────────────────────────────

const SITE = {
  domain: 'wp.example',
  version: '6.5',
  db_name: 'wp_db',
  db_user: 'wp_user',
  db_host: 'localhost',
  admin_url: 'https://wp.example/wp-admin',
  health: { plugin_updates: 0, core_update: false, ssl: true, debug: false, php_version: '8.2' },
  plugins: [],
  themes: [],
  permissions: { wp_config: '640', wp_content: '755', uploads: '755', htaccess: '644', owner: 'www-data', writable: true },
};

const DONE_STATUS = {
  status: 'done' as const,
  domain: 'wp.example',
  db_name: 'wp_db',
  db_user: 'wp_user',
  db_pass: 'pw-secret-one-time',
  admin_url: 'https://wp.example/wp-admin',
};

// Drain the async chains inside act (microtasks are unaffected by fake timers).
const settle = () => act(async () => {
  for (let i = 0; i < 20; i++) await Promise.resolve();
});

function renderPage() {
  return render(
    <MemoryRouter>
      <WordPress />
    </MemoryRouter>,
  );
}

describe('WordPress install flow', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
    mockFetchWPSites.mockResolvedValue([]);
    mockFetchDomains.mockResolvedValue([{ host: 'wp.example', type: 'php', aliases: null }]);
    mockFetchDBStatus.mockResolvedValue({ installed: true, running: true });
    mockFetchDockerDBs.mockResolvedValue({ containers: [] });
    mockInstallWordPress.mockResolvedValue({ status: 'running' });
    mockFetchWPSiteDetail.mockResolvedValue(SITE);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  // Control: with no sites installed, the page opens on the Install tab.
  it('opens on the Install tab when no sites exist', async () => {
    renderPage();
    await settle();
    expect(screen.getByRole('button', { name: /install wordpress/i })).toBeInTheDocument();
    expect(screen.getByText(/ready for WordPress/i)).toBeInTheDocument();
  });

  it('keeps the one-time credentials panel visible after the install completes', async () => {
    renderPage();
    await settle(); // initial load: no sites → Install tab

    fireEvent.click(screen.getByRole('button', { name: /install wordpress/i }));
    await settle(); // installWordPress resolved; 2s status poll armed

    // One poll tick reports success; the done branch refreshes the site list.
    mockFetchWPSites.mockResolvedValue([SITE]);
    mockFetchWPInstallStatus.mockResolvedValue(DONE_STATUS);
    await act(async () => {
      vi.advanceTimersByTime(2_000);
    });
    await settle();

    // Mechanism (must hold in every run): the poll tick fired and the done
    // branch refreshed the site list.
    expect(mockFetchWPInstallStatus).toHaveBeenCalledTimes(1);
    expect(mockFetchWPSites).toHaveBeenCalledTimes(2); // mount + done-branch

    // The done panel carries one-time credentials (DB password + admin URL).
    // Refreshing the site list must not navigate away from it before the
    // operator can copy them.
    expect(screen.queryByText('WordPress installed!')).not.toBeNull();
    expect(screen.queryByText('pw-secret-one-time')).not.toBeNull();
  });
});
