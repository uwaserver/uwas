import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import Database from './Database';
import { ConfirmContext } from '@/components/useConfirm';

// F62602: the "Change Password" row form kept the password typed for one
// database user when the operator opened the form on another user's row (the
// input is masked), so the wrong user's password could be set.

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
vi.mock('@/lib/clipboard', () => ({ copyText: vi.fn().mockResolvedValue(true) }));

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

const flush = () => act(async () => { for (let i = 0; i < 10; i++) await Promise.resolve(); });

describe('Database user password form', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => { if (vi.isMockFunction(fn)) fn.mockReset(); });
    apiMocks.getAuthHeaders.mockImplementation(() => ({}));
    apiMocks.fetchDBStatus.mockResolvedValue({ backend: 'mariadb', installed: true, running: true, version: '10' });
    apiMocks.fetchDatabases.mockResolvedValue([]);
    apiMocks.fetchDBUsers.mockResolvedValue([
      { user: 'alice', host: 'localhost' },
      { user: 'bob', host: 'localhost' },
    ]);
    apiMocks.fetchDockerDBs.mockResolvedValue({ docker: false, version: '', containers: [] });
  });

  it('does not carry a typed password over to another user row', async () => {
    renderPage(); await flush();
    fireEvent.click(screen.getAllByText('Change Password')[0]);
    fireEvent.change(screen.getByPlaceholderText('New password'), { target: { value: 'for-alice-only' } });
    fireEvent.click(screen.getAllByText('Change Password')[0]); // bob's row now
    const input = screen.getByPlaceholderText('New password') as HTMLInputElement;
    console.log('EXPECTED: empty field for the newly opened row');
    console.log(`ACTUAL: "${input.value}"`);
    expect(input.value).toBe('');
  });

  it('control: the same row keeps what was typed', async () => {
    renderPage(); await flush();
    fireEvent.click(screen.getAllByText('Change Password')[0]);
    fireEvent.change(screen.getByPlaceholderText('New password'), { target: { value: 'typed' } });
    expect((screen.getByPlaceholderText('New password') as HTMLInputElement).value).toBe('typed');
  });
});
