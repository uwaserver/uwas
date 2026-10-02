import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import Setup from './Setup';

// ── Mocks ───────────────────────────────────────────────────────────────────
// One vi.fn per value imported by Setup.tsx; mount-time fetchers get resolved
// defaults in beforeEach, the rest stay inert unless invoked.
const apiMocks = vi.hoisted(() => ({
  fetchPackages: vi.fn(),
  fetchPHP: vi.fn(),
  fetchTasks: vi.fn(),
  startSetupInstall: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

const PKG = {
  id: 'mariadb',
  name: 'MariaDB',
  description: 'MariaDB server',
  category: 'Database',
  installed: false,
  version: '10.11',
  required: true,
  recommended: true,
};

const TASK = { id: 't1', status: 'running' as const };

function renderPage() {
  return render(<Setup />);
}

// Flush mount effects + in-flight promise chains without advancing the clock.
const flush = () => act(async () => {});

// Step 1 → step 2 → "Install N" (beginInstall). The mount pre-selects every
// recommended, not-installed item (PHP 8.3 + required packages), so the exact
// count depends on fixtures — match the button by pattern.
async function startInstall() {
  fireEvent.click(screen.getByRole('button', { name: 'Next' }));
  await flush();
  fireEvent.click(screen.getByRole('button', { name: /^Install \d+$/ }));
}

describe('Setup wizard install polling lifecycle', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => {
      if (vi.isMockFunction(fn)) fn.mockReset();
    });
    apiMocks.fetchPackages.mockResolvedValue([PKG]);
    apiMocks.fetchPHP.mockResolvedValue([]);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  // Control: while mounted, the wizard polls the 1.5s task poll while the
  // tasks run, and the interval disarms itself once every task is done/error.
  it('polls task status while mounted and stops itself when all tasks finish', async () => {
    vi.useFakeTimers();
    apiMocks.startSetupInstall.mockResolvedValue({
      items: [{ type: 'package', id: 'mariadb', name: 'MariaDB', task_id: 't1' }],
    });
    apiMocks.fetchTasks.mockResolvedValue([TASK]);
    renderPage();
    await flush();

    await startInstall();
    await flush(); // startSetupInstall resolves while mounted; poll armed + first tick

    const during = apiMocks.fetchTasks.mock.calls.length;
    expect(during).toBe(1); // the immediate poll() at arm time

    await act(async () => {
      await vi.advanceTimersByTimeAsync(4_600); // ~3 interval ticks
    });
    expect(apiMocks.fetchTasks.mock.calls.length).toBeGreaterThanOrEqual(during + 3);

    apiMocks.fetchTasks.mockResolvedValue([{ ...TASK, status: 'done' as const }]);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_600); // tick sees allDone → clears interval
    });
    const afterDone = apiMocks.fetchTasks.mock.calls.length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(apiMocks.fetchTasks.mock.calls.length).toBe(afterDone);
  });

  it('does not arm the task poll after unmounting mid-install-start', async () => {
    vi.useFakeTimers();
    let resolveStart!: (v: unknown) => void;
    apiMocks.startSetupInstall.mockImplementation(
      () => new Promise((resolve) => { resolveStart = resolve; }),
    );
    const { unmount } = renderPage();
    await flush();

    await startInstall();
    unmount(); // navigate away while the start-install POST is still in flight

    await act(async () => {
      resolveStart({
        items: [{ type: 'package', id: 'mariadb', name: 'MariaDB', task_id: 't1' }],
      }); // POST resolves after unmount — the server keeps installing
    });
    // The install tasks keep running server-side while the page is gone.
    apiMocks.fetchTasks.mockResolvedValue([TASK]);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    // The post-await continuation must not re-arm the 1.5s fetchTasks poll
    // loop that the already-run cleanup can never clear. fetchTasks is ONLY
    // called from the poll loop, so any call here is the re-armed interval
    // polling an unmounted page.
    expect(apiMocks.fetchTasks.mock.calls.length).toBe(0);
  });

  // Control: proves the existing unmount cleanup stops polling when the
  // interval already exists at unmount time.
  it('stops task polling when the unmount happens after the interval exists', async () => {
    vi.useFakeTimers();
    apiMocks.startSetupInstall.mockResolvedValue({
      items: [{ type: 'package', id: 'mariadb', name: 'MariaDB', task_id: 't1' }],
    });
    apiMocks.fetchTasks.mockResolvedValue([TASK]);
    const { unmount } = renderPage();
    await flush();

    await startInstall();
    await flush(); // poll armed while mounted (immediate poll() already ran)

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3_100); // ~2 interval ticks
    });
    expect(apiMocks.fetchTasks.mock.calls.length).toBeGreaterThan(1);

    unmount();
    const before = apiMocks.fetchTasks.mock.calls.length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(apiMocks.fetchTasks.mock.calls.length).toBe(before);
  });
});
