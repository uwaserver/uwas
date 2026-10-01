import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, act, within } from '@testing-library/react';
import Packages from './Packages';
import { ConfirmContext } from '@/components/useConfirm';
import type { PackageInfo } from '@/lib/api';

// ── Mocks ───────────────────────────────────────────────────────────────────

const mockFetchPackages = vi.fn();
const mockInstallPackage = vi.fn();
const mockRemovePackage = vi.fn();
const mockFetchTasks = vi.fn();

vi.mock('@/lib/api', () => ({
  fetchPackages: (...args: unknown[]) => mockFetchPackages(...args),
  installPackage: (...args: unknown[]) => mockInstallPackage(...args),
  removePackage: (...args: unknown[]) => mockRemovePackage(...args),
  fetchTasks: (...args: unknown[]) => mockFetchTasks(...args),
}));

const confirmValue = {
  confirmAction: vi.fn().mockResolvedValue(true),
  promptText: vi.fn().mockResolvedValue(null),
};

// ── Fixtures ────────────────────────────────────────────────────────────────

function pkg(overrides: Partial<PackageInfo> = {}): PackageInfo {
  return {
    id: 'nginx',
    name: 'Nginx',
    description: 'web server',
    installed: false,
    category: 'Required',
    required: false,
    can_remove: true,
    ...overrides,
  };
}

function renderPage() {
  return render(
    <ConfirmContext.Provider value={confirmValue}>
      <Packages />
    </ConfirmContext.Provider>,
  );
}

// Flush mount effects + in-flight promise chains without advancing the clock.
const flush = () => act(async () => {});

describe('Packages page polling lifecycle', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockFetchPackages.mockResolvedValue([pkg()]);
    mockFetchTasks.mockResolvedValue([]);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  // Control: proves the harness actually polls while mounted and that the
  // existing cleanup stops polling for the normal unmount ordering.
  it('stops install polling when the unmount happens after the interval exists', async () => {
    vi.useFakeTimers();
    mockInstallPackage.mockResolvedValue({ status: 'ok' });
    const { unmount } = renderPage();
    await flush();

    fireEvent.click(screen.getByRole('button', { name: /install/i }));
    await flush(); // install resolves while mounted; poll interval is armed

    const whileMounted = mockFetchPackages.mock.calls.length;
    await act(async () => {
      vi.advanceTimersByTime(7_000);
    });
    expect(mockFetchPackages.mock.calls.length).toBeGreaterThan(whileMounted);

    unmount();
    const before = mockFetchPackages.mock.calls.length;
    await act(async () => {
      vi.advanceTimersByTime(10_000);
    });
    expect(mockFetchPackages.mock.calls.length).toBe(before);
  });

  it('does not arm install polling after unmounting mid-install', async () => {
    vi.useFakeTimers();
    let resolveInstall!: (v: unknown) => void;
    mockInstallPackage.mockImplementation(
      () => new Promise((resolve) => { resolveInstall = resolve; }),
    );
    const { unmount } = renderPage();
    await flush();

    fireEvent.click(screen.getByRole('button', { name: /install/i }));
    unmount(); // navigate away while the install POST is still in flight

    await act(async () => {
      resolveInstall({ status: 'ok' }); // POST resolves after unmount
    });
    const before = mockFetchPackages.mock.calls.length;
    await act(async () => {
      vi.advanceTimersByTime(10_000);
    });
    // The post-await continuation must not re-arm the 3s poll loop that the
    // already-run cleanup can never clear.
    expect(mockFetchPackages.mock.calls.length).toBe(before);
  });

  it('does not arm task-resume polling after unmounting before the task list loads', async () => {
    vi.useFakeTimers();
    let resolveTasks!: (v: unknown) => void;
    mockFetchTasks.mockImplementation(
      () => new Promise((resolve) => { resolveTasks = resolve; }),
    );
    const { unmount } = renderPage();
    await flush();

    unmount(); // navigate away while the mount-time fetchTasks() is pending
    const before = mockFetchTasks.mock.calls.length;
    await act(async () => {
      resolveTasks([
        { id: 't1', type: 'package', name: 'nginx', action: 'install', status: 'running' },
      ]);
    });
    await act(async () => {
      vi.advanceTimersByTime(10_000);
    });
    // The resume continuation must not re-arm its 3s poll loop post-unmount.
    expect(mockFetchTasks.mock.calls.length).toBe(before);
  });
});

// ── Stale 120s give-up timeout ──────────────────────────────────────────────
//
// The poll loops' catch blocks clear only the interval, not the 120s
// give-up timeout. After a failed poll the timeout survives; when a
// follow-up action succeeds, the stale timeout still fires and injects a
// bogus "did not complete within 2 minutes" error for the OLD action.

describe('Packages page stale give-up timeout', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockFetchTasks.mockResolvedValue([]);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  // The Install button's accessible name is just "Install" for every
  // package, so scope the query to the package's card.
  function installButtonFor(name: string): HTMLElement {
    const card = screen.getByText(name).closest('div.rounded-lg');
    if (!card) throw new Error(`card for ${name} not found`);
    return within(card as HTMLElement).getByRole('button', { name: /install/i });
  }

  it('does not fire the install give-up timeout after its poll failed and a later install succeeded', async () => {
    vi.useFakeTimers();
    // Initial load: nginx + redis present. Poll A (call 2) fails; poll B
    // (call 3) sees redis installed, completing the second install; call 4
    // is B's trailing load().
    mockFetchPackages
      .mockResolvedValueOnce([pkg(), pkg({ id: 'redis', name: 'Redis' })])
      .mockRejectedValueOnce(new Error('network down'))
      .mockResolvedValueOnce([pkg(), pkg({ id: 'redis', name: 'Redis', installed: true })])
      .mockResolvedValue([pkg(), pkg({ id: 'redis', name: 'Redis', installed: true })]);
    mockInstallPackage.mockResolvedValue({ status: 'ok' });
    const { unmount } = renderPage();
    await flush();

    // Install A (nginx) arms interval A + the 120s give-up timeout.
    fireEvent.click(installButtonFor('Nginx'));
    await flush();
    // First poll tick fails: interval cleared, but the give-up timeout survives.
    await act(async () => {
      vi.advanceTimersByTime(3_000);
    });

    // Install B (redis) within the stale timeout's window.
    fireEvent.click(installButtonFor('Redis'));
    await flush();
    // B's first poll succeeds: B completes cleanly and clears ITS OWN timeout.
    await act(async () => {
      vi.advanceTimersByTime(3_000);
    });
    expect(screen.getByText('Redis installed!')).toBeInTheDocument();

    // Cross the stale timeout's fire time (armed at install A, +120s).
    await act(async () => {
      vi.advanceTimersByTime(115_000);
    });
    // The stale timeout must never announce nginx as timed out — nginx's
    // poll merely hiccuped once, and redis finished successfully after it.
    expect(screen.queryByText(/Install of Nginx did not complete within 2 minutes/)).toBeNull();
    unmount();
  });

  it('does not fire the remove give-up timeout after its poll failed and a later install succeeded', async () => {
    vi.useFakeTimers();
    mockFetchPackages
      .mockResolvedValueOnce([pkg({ installed: true }), pkg({ id: 'redis', name: 'Redis' })]) // initial load
      .mockRejectedValueOnce(new Error('network down')) // remove poll A tick 1
      .mockResolvedValueOnce([pkg({ installed: true }), pkg({ id: 'redis', name: 'Redis', installed: true })]) // install B poll
      .mockResolvedValue([pkg({ installed: true }), pkg({ id: 'redis', name: 'Redis', installed: true })]); // B's load()
    mockRemovePackage.mockResolvedValue({ status: 'ok' });
    mockInstallPackage.mockResolvedValue({ status: 'ok' });
    const { unmount } = renderPage();
    await flush();

    // Remove A (nginx): confirm stub resolves true, arms poll + give-up timeout.
    fireEvent.click(screen.getByRole('button', { name: /remove/i }));
    await flush();
    await act(async () => {
      vi.advanceTimersByTime(3_000); // poll A fails; timeout survives
    });

    // Install B (redis) within the stale timeout's window; it succeeds.
    fireEvent.click(screen.getByRole('button', { name: /install/i }));
    await flush();
    await act(async () => {
      vi.advanceTimersByTime(3_000);
    });
    expect(screen.getByText('Redis installed!')).toBeInTheDocument();

    await act(async () => {
      vi.advanceTimersByTime(115_000);
    });
    expect(screen.queryByText(/Remove of Nginx did not complete within 2 minutes/)).toBeNull();
    unmount();
  });
});
