import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import PHP from './PHP';
import { ConfirmContext } from '@/components/useConfirm';

// ── Mocks ───────────────────────────────────────────────────────────────────
// One vi.fn per value imported by PHP.tsx; mount-time fetchers get resolved
// defaults in beforeEach, the rest stay inert unless invoked.
const apiMocks = vi.hoisted(() => ({
  fetchPHP: vi.fn(),
  fetchPHPInstallInfo: vi.fn(),
  installPHP: vi.fn(),
  updatePHP: vi.fn(),
  uninstallPHP: vi.fn(),
  fetchPHPInstallStatus: vi.fn(),
  fetchDomains: vi.fn(),
  fetchDomainPHPInstances: vi.fn(),
  assignDomainPHP: vi.fn(),
  unassignDomainPHP: vi.fn(),
  startDomainPHP: vi.fn(),
  stopDomainPHP: vi.fn(),
  fetchDomainPHPConfig: vi.fn(),
  updateDomainPHPConfig: vi.fn(),
  enablePHP: vi.fn(),
  disablePHP: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

const confirmValue = {
  confirmAction: vi.fn().mockResolvedValue(true),
  promptText: vi.fn().mockResolvedValue(null),
};

function renderPage() {
  return render(
    <ConfirmContext.Provider value={confirmValue}>
      <PHP />
    </ConfirmContext.Provider>,
  );
}

// Flush mount effects + in-flight promise chains without advancing the clock.
const flush = () => act(async () => {});

// The install panel only offers "Install PHP" when no PHP is detected.
async function openInstallPanelAndClickInstall() {
  fireEvent.click(screen.getByRole('button', { name: 'Install PHP' }));
  await flush();
  fireEvent.click(screen.getByRole('button', { name: 'Install PHP 8.4' }));
}

describe('PHP page install polling lifecycle', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => {
      if (vi.isMockFunction(fn)) fn.mockReset();
    });
    apiMocks.fetchPHP.mockResolvedValue([]);
    apiMocks.fetchDomains.mockResolvedValue([]);
    apiMocks.fetchDomainPHPInstances.mockResolvedValue([]);
    apiMocks.fetchPHPInstallInfo.mockResolvedValue({
      distro: 'ubuntu',
      version: '8.4',
      commands: ['apt-get install -y php8.4-fpm'],
      packages: ['php8.4-fpm'],
      notes: '',
    });
    // Mount-time resume check: not running → arms nothing.
    apiMocks.fetchPHPInstallStatus.mockResolvedValue({ status: 'idle' });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  // Control: while mounted, the install arms the 2s status poll, polling runs
  // while the task is 'running', and the interval disarms itself when the task
  // finishes.
  it('polls install status while mounted and stops itself when the install finishes', async () => {
    vi.useFakeTimers();
    apiMocks.installPHP.mockResolvedValue({ status: 'ok' });
    renderPage();
    await flush();

    await openInstallPanelAndClickInstall();
    await flush(); // install resolves while mounted; poll interval is armed

    const during = apiMocks.fetchPHPInstallStatus.mock.calls.length;
    expect(during).toBe(1); // only the mount-time resume check so far

    apiMocks.fetchPHPInstallStatus.mockResolvedValue({ status: 'running', version: '8.4' });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(6_100); // ~3 poll ticks
    });
    expect(apiMocks.fetchPHPInstallStatus.mock.calls.length).toBeGreaterThanOrEqual(during + 3);

    apiMocks.fetchPHPInstallStatus.mockResolvedValue({ status: 'done', version: '8.4' });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2_100); // tick sees 'done' → clears interval, loadAll()
    });
    expect(apiMocks.fetchPHP.mock.calls.length).toBeGreaterThanOrEqual(2); // refetch after done

    const afterDone = apiMocks.fetchPHPInstallStatus.mock.calls.length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(apiMocks.fetchPHPInstallStatus.mock.calls.length).toBe(afterDone);
  });

  it('does not arm install polling after unmounting mid-install', async () => {
    vi.useFakeTimers();
    let resolveInstall!: (v: unknown) => void;
    apiMocks.installPHP.mockImplementation(
      () => new Promise((resolve) => { resolveInstall = resolve; }),
    );
    const { unmount } = renderPage();
    await flush();

    await openInstallPanelAndClickInstall();
    unmount(); // navigate away while the install POST is still in flight

    await act(async () => {
      resolveInstall({ status: 'ok' }); // POST resolves after unmount
    });
    // The install task is still executing server-side while the page is gone.
    apiMocks.fetchPHPInstallStatus.mockResolvedValue({ status: 'running', version: '8.4' });
    const before = apiMocks.fetchPHPInstallStatus.mock.calls.length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    // The post-await continuation must not re-arm the 2s fetchPHPInstallStatus
    // poll loop that the already-run cleanup can never clear.
    expect(apiMocks.fetchPHPInstallStatus.mock.calls.length).toBe(before);
  });

  // Control: proves the existing unmount cleanup stops polling when the
  // interval already exists at unmount time.
  it('stops install polling when the unmount happens after the interval exists', async () => {
    vi.useFakeTimers();
    apiMocks.installPHP.mockResolvedValue({ status: 'ok' });
    const { unmount } = renderPage();
    await flush();

    await openInstallPanelAndClickInstall();
    await flush(); // interval armed while mounted

    apiMocks.fetchPHPInstallStatus.mockResolvedValue({ status: 'running', version: '8.4' });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5_000);
    });
    expect(apiMocks.fetchPHPInstallStatus.mock.calls.length).toBeGreaterThan(1);

    unmount();
    const before = apiMocks.fetchPHPInstallStatus.mock.calls.length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(apiMocks.fetchPHPInstallStatus.mock.calls.length).toBe(before);
  });

  describe('update and uninstall', () => {
    const inst = (version: string, extra: Record<string, unknown> = {}) => ({
      version, binary: `/usr/bin/php-fpm${version.slice(0, 3)}`, config_file: '', extensions: [],
      sapi: 'fpm-fcgi', running: false, listen_addr: '', domain_count: 0, ...extra,
    });

    it('shows a security banner and updates all vulnerable versions from it', async () => {
      apiMocks.fetchPHP.mockResolvedValue([
        inst('8.3.6', { advisory: 'PHP 8.3.6 is vulnerable to CVE-2026-109618/CVE-2026-109619 (RCE); update to 8.3.36 or newer' }),
      ]);
      apiMocks.updatePHP.mockResolvedValue({ status: 'started', versions: ['8.3'] });
      renderPage();
      await flush();

      expect(screen.getByText(/known security vulnerabilities/i)).toBeTruthy();
      fireEvent.click(screen.getByRole('button', { name: /update all/i }));
      await flush();
      expect(apiMocks.updatePHP).toHaveBeenCalledWith('all');
    });

    it('updates one branch from its card', async () => {
      apiMocks.fetchPHP.mockResolvedValue([inst('8.4.27')]);
      apiMocks.updatePHP.mockResolvedValue({ status: 'started', versions: ['8.4'] });
      renderPage();
      await flush();

      fireEvent.click(screen.getByRole('button', { name: /^update$/i }));
      await flush();
      expect(apiMocks.updatePHP).toHaveBeenCalledWith('8.4');
    });

    it('hides Update for end-of-life branches but still allows uninstall', async () => {
      apiMocks.fetchPHP.mockResolvedValue([inst('8.1.30', { eol: true, advisory: 'PHP 8.1 is end-of-life' })]);
      apiMocks.uninstallPHP.mockResolvedValue({ status: 'started', version: '8.1' });
      confirmValue.confirmAction.mockResolvedValueOnce(true).mockResolvedValueOnce(true);
      renderPage();
      await flush();

      expect(screen.queryByRole('button', { name: /^update$/i })).toBeNull();
      fireEvent.click(screen.getByRole('button', { name: /uninstall/i }));
      await flush();
      expect(apiMocks.uninstallPHP).toHaveBeenCalledWith('8.1', true);
    });

    it('disables uninstall while domains use the version', async () => {
      apiMocks.fetchPHP.mockResolvedValue([inst('8.3.36', { domain_count: 2, domains: ['a.test', 'b.test'] })]);
      renderPage();
      await flush();

      const btn = screen.getByRole('button', { name: /uninstall/i }) as HTMLButtonElement;
      expect(btn.disabled).toBe(true);
    });
  });
});
