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
});
