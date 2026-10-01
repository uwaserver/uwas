import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import Database from './Database';
import { ConfirmContext } from '@/components/useConfirm';

// ── Mocks ───────────────────────────────────────────────────────────────────

// One vi.fn per value imported by Database.tsx; the four mount-time fetchers
// get resolved defaults in beforeEach, the rest stay inert unless invoked.
// vi.hoisted: vi.mock factories are hoisted above module consts, so the mock
// object must be created in the hoisted phase.
const apiMocks = vi.hoisted(() => ({
  fetchDBStatus: vi.fn(),
  fetchDatabases: vi.fn(),
  createDatabase: vi.fn(),
  dropDatabase: vi.fn(),
  installDatabase: vi.fn(),
  uninstallDatabase: vi.fn(),
  diagnoseDatabase: vi.fn(),
  fetchDBUsers: vi.fn(),
  changeDBPassword: vi.fn(),
  dropDBUser: vi.fn(),
  configureDBRemoteAccess: vi.fn(),
  exportDatabase: vi.fn(),
  importDatabase: vi.fn(),
  getAuthHeaders: vi.fn(() => ({})),
  startDB: vi.fn(),
  stopDB: vi.fn(),
  restartDB: vi.fn(),
  fetchDockerDBs: vi.fn(),
  createDockerDB: vi.fn(),
  startDockerDB: vi.fn(),
  stopDockerDB: vi.fn(),
  removeDockerDB: vi.fn(),
  fetchDockerDBDatabases: vi.fn(),
  createDockerDBDatabase: vi.fn(),
  dropDockerDBDatabase: vi.fn(),
  fetchFirewall: vi.fn(),
  firewallAllow: vi.fn(),
  fetchDBTables: vi.fn(),
  fetchDBColumns: vi.fn(),
  runDBQuery: vi.fn(),
  fetchTask: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

vi.mock('@/lib/clipboard', () => ({
  copyText: vi.fn().mockResolvedValue(true),
}));

const confirmValue = {
  confirmAction: vi.fn().mockResolvedValue(true),
  promptText: vi.fn().mockResolvedValue(null),
};

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/database']}>
      <ConfirmContext.Provider value={confirmValue}>
        <Database />
      </ConfirmContext.Provider>
    </MemoryRouter>,
  );
}

// Flush mount effects + in-flight promise chains without advancing the clock.
const flush = () => act(async () => {});

describe('Database page install polling lifecycle', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => {
      if (vi.isMockFunction(fn)) fn.mockReset();
    });
    apiMocks.getAuthHeaders.mockImplementation(() => ({}));
    apiMocks.fetchDBStatus.mockResolvedValue({
      backend: 'mariadb',
      installed: false,
      running: false,
      version: '',
    });
    apiMocks.fetchDatabases.mockResolvedValue([]);
    apiMocks.fetchDBUsers.mockResolvedValue([]);
    apiMocks.fetchDockerDBs.mockResolvedValue({ docker: false, version: '', containers: [] });
    apiMocks.fetchTask.mockResolvedValue({ id: 't1', status: 'running' });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  // Control: proves the harness polls while mounted and that the existing
  // cleanup stops polling when the interval already exists at unmount.
  it('stops install polling when the unmount happens after the interval exists', async () => {
    vi.useFakeTimers();
    apiMocks.installDatabase.mockResolvedValue({ task_id: 't1' });
    const { unmount } = renderPage();
    await flush();

    fireEvent.click(screen.getByRole('button', { name: 'Install MariaDB' }));
    await flush(); // install resolves while mounted; poll interval is armed

    const whileMounted = apiMocks.fetchTask.mock.calls.length;
    await act(async () => {
      vi.advanceTimersByTime(7_000);
    });
    expect(apiMocks.fetchTask.mock.calls.length).toBeGreaterThan(whileMounted);

    unmount();
    const before = apiMocks.fetchTask.mock.calls.length;
    await act(async () => {
      vi.advanceTimersByTime(10_000);
    });
    expect(apiMocks.fetchTask.mock.calls.length).toBe(before);
  });

  it('does not arm install polling after unmounting mid-install', async () => {
    vi.useFakeTimers();
    let resolveInstall!: (v: unknown) => void;
    apiMocks.installDatabase.mockImplementation(
      () => new Promise((resolve) => { resolveInstall = resolve; }),
    );
    const { unmount } = renderPage();
    await flush();

    fireEvent.click(screen.getByRole('button', { name: 'Install MariaDB' }));
    unmount(); // navigate away while the install POST is still in flight

    await act(async () => {
      resolveInstall({ task_id: 't1' }); // POST resolves after unmount
    });
    const before = apiMocks.fetchTask.mock.calls.length;
    await act(async () => {
      vi.advanceTimersByTime(10_000);
    });
    // The post-await continuation must not re-arm the 3s fetchTask poll loop
    // that the already-run cleanup can never clear.
    expect(apiMocks.fetchTask.mock.calls.length).toBe(before);
  });
});
