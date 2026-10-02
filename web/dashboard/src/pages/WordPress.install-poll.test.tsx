import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import WordPress from './WordPress';

// ── Mocks ───────────────────────────────────────────────────────────────────
// One vi.fn per value imported by WordPress.tsx; mount-time fetchers get
// resolved defaults in beforeEach, the rest stay inert unless invoked.
const apiMocks = vi.hoisted(() => ({
  fetchDomains: vi.fn(),
  installWordPress: vi.fn(),
  fetchWPInstallStatus: vi.fn(),
  fetchDBStatus: vi.fn(),
  fetchDockerDBs: vi.fn(),
  fetchWPSites: vi.fn(),
  fetchWPSiteDetail: vi.fn(),
  wpUpdateCore: vi.fn(),
  wpUpdatePlugins: vi.fn(),
  wpPluginAction: vi.fn(),
  wpFixPermissions: vi.fn(),
  wpToggleDebug: vi.fn(),
  wpErrorLog: vi.fn(),
  wpListUsers: vi.fn(),
  wpChangePassword: vi.fn(),
  wpSecurityStatus: vi.fn(),
  wpHarden: vi.fn(),
  wpOptimizeDB: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

// The clipboard boundary: copyText resolves FALSE when every copy strategy
// failed (lib/clipboard contract).
const clipboardMocks = vi.hoisted(() => ({ copyText: vi.fn() }));
vi.mock('@/lib/clipboard', () => clipboardMocks);

function renderPage() {
  return render(
    <MemoryRouter>
      <WordPress />
    </MemoryRouter>,
  );
}

// Flush mount effects + in-flight promise chains without advancing the clock.
const flush = () => act(async () => {});

// With no installed sites the page opens on the Install tab with a single
// "Install WordPress" action button; the mount flow auto-selects the first
// installable PHP domain.
async function clickInstall() {
  fireEvent.click(screen.getByRole('button', { name: /install wordpress/i }));
}

describe('WordPress page install polling lifecycle', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => {
      if (vi.isMockFunction(fn)) fn.mockReset();
    });
    apiMocks.fetchWPSites.mockResolvedValue([]);
    apiMocks.fetchDomains.mockResolvedValue([{ host: 'wp.example', type: 'php', aliases: null }]);
    apiMocks.fetchDBStatus.mockResolvedValue({ installed: true, running: true });
    apiMocks.fetchDockerDBs.mockResolvedValue({ containers: [] });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  // Control: while mounted, the install arms the 2s status poll, polling runs
  // while the task is 'running', and the interval disarms itself when the task
  // finishes (refreshing the site list).
  it('polls install status while mounted and stops itself when the install finishes', async () => {
    vi.useFakeTimers();
    apiMocks.installWordPress.mockResolvedValue({ status: 'running' });
    apiMocks.fetchWPInstallStatus.mockResolvedValue({ status: 'running', domain: 'wp.example' });
    renderPage();
    await flush();

    await clickInstall();
    await flush(); // install resolves while mounted; poll interval is armed
    expect(apiMocks.fetchWPInstallStatus.mock.calls.length).toBe(0); // not polled before the first tick

    await act(async () => {
      await vi.advanceTimersByTimeAsync(6_100); // ~3 poll ticks
    });
    expect(apiMocks.fetchWPInstallStatus.mock.calls.length).toBeGreaterThanOrEqual(3);

    apiMocks.fetchWPInstallStatus.mockResolvedValue({
      status: 'done',
      domain: 'wp.example',
      db_name: 'wp_db',
      db_user: 'wp_user',
      db_pass: 'pw-secret-one-time',
      admin_url: 'https://wp.example/wp-admin',
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2_100); // tick sees 'done' → clears interval, loadSites()
    });
    expect(apiMocks.fetchWPSites.mock.calls.length).toBe(2); // mount + done-branch refresh

    const afterDone = apiMocks.fetchWPInstallStatus.mock.calls.length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(apiMocks.fetchWPInstallStatus.mock.calls.length).toBe(afterDone);
  });

  it('does not arm install polling after unmounting mid-install', async () => {
    vi.useFakeTimers();
    let resolveInstall!: (v: unknown) => void;
    apiMocks.installWordPress.mockImplementation(
      () => new Promise((resolve) => { resolveInstall = resolve; }),
    );
    const { unmount } = renderPage();
    await flush();

    await clickInstall();
    unmount(); // navigate away while the install POST is still in flight

    await act(async () => {
      resolveInstall({ status: 'running' }); // POST resolves after unmount
    });
    // The install task keeps executing server-side while the page is gone.
    apiMocks.fetchWPInstallStatus.mockResolvedValue({ status: 'running', domain: 'wp.example' });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    // The post-await continuation must not re-arm the 2s fetchWPInstallStatus
    // poll loop that the already-run cleanup can never clear. This endpoint is
    // ONLY called from the poll interval, so any call here is the re-armed
    // interval polling an unmounted page.
    expect(apiMocks.fetchWPInstallStatus.mock.calls.length).toBe(0);
  });

  // Control: proves the existing unmount cleanup stops polling when the
  // interval already exists at unmount time.
  it('stops install polling when the unmount happens after the interval exists', async () => {
    vi.useFakeTimers();
    apiMocks.installWordPress.mockResolvedValue({ status: 'running' });
    apiMocks.fetchWPInstallStatus.mockResolvedValue({ status: 'running', domain: 'wp.example' });
    const { unmount } = renderPage();
    await flush();

    await clickInstall();
    await flush(); // interval armed while mounted

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5_000);
    });
    expect(apiMocks.fetchWPInstallStatus.mock.calls.length).toBeGreaterThan(0);

    unmount();
    const before = apiMocks.fetchWPInstallStatus.mock.calls.length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(apiMocks.fetchWPInstallStatus.mock.calls.length).toBe(before);
  });
});

describe('WordPress copy reports clipboard failure instead of false success', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    Object.values(apiMocks).forEach(fn => {
      if (vi.isMockFunction(fn)) fn.mockReset();
    });
    clipboardMocks.copyText.mockReset();
    apiMocks.fetchWPSites.mockResolvedValue([]);
    apiMocks.fetchDomains.mockResolvedValue([{ host: 'wp.example', type: 'php', aliases: null }]);
    apiMocks.fetchDBStatus.mockResolvedValue({ installed: true, running: true });
    apiMocks.fetchDockerDBs.mockResolvedValue({ containers: [] });
    apiMocks.installWordPress.mockResolvedValue({ status: 'running' });
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  // Drive the install flow to the done panel, which holds the ONE-TIME
  // credentials (db_name, db_user, db_pass, admin_url) with icon-only copy
  // buttons (Copy/Check swap by `copied === label`).
  async function installToDone() {
    renderPage();
    await flush();
    // Under fake timers the mount can transiently render both the empty-sites
    // CTA and the install-form button — click the first match.
    fireEvent.click(screen.getAllByRole('button', { name: /install wordpress/i })[0]);
    await flush();
    apiMocks.fetchWPInstallStatus.mockResolvedValue({
      status: 'done',
      domain: 'wp.example',
      db_name: 'wp_wpdb',
      db_user: 'wp_user',
      db_pass: 'one-time-pw',
      admin_url: 'https://wp.example/wp-admin',
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2_100); // poll tick sees 'done'
    });
    await flush();
  }

  // Regression: the done panel holds ONE-TIME credentials — a check icon on
  // a failed copy claims the secret is on the clipboard when it is not.
  it('shows a copy-failed message when the clipboard write fails', async () => {
    clipboardMocks.copyText.mockResolvedValue(false);
    renderPage();
    await flush();
    await installToDone();

    const copyBtn = screen
      .getByText('DB Password')
      .closest('div')
      ?.parentElement?.querySelector('button') as HTMLButtonElement;
    fireEvent.click(copyBtn);
    await flush();

    expect(clipboardMocks.copyText).toHaveBeenCalledWith('one-time-pw');
    expect(screen.getByText(/Copy failed/i)).toBeTruthy();
  });

  // Control: a successful copy keeps the existing feedback (check icon).
  it('keeps the success signal when the clipboard write succeeds', async () => {
    clipboardMocks.copyText.mockResolvedValue(true);
    renderPage();
    await flush();
    await installToDone();

    const copyBtn = screen
      .getByText('DB Password')
      .closest('div')
      ?.parentElement?.querySelector('button') as HTMLButtonElement;
    fireEvent.click(copyBtn);
    await flush();

    expect(screen.queryByText(/Copy failed/i)).toBeNull();
  });
});
