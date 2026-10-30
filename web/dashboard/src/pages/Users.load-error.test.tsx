import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import Users from './Users';

// F53600: a failed users/domains/apps request was swallowed, so the page
// said "No SFTP users configured"; and one failing request (apps) blanked
// the users that had loaded fine.

const apiMocks = vi.hoisted(() => ({
  fetchUsers: vi.fn(), createUser: vi.fn(), deleteUser: vi.fn(),
  fetchDomains: vi.fn(), fetchApps: vi.fn(),
  fetchSSHKeys: vi.fn(), addSSHKey: vi.fn(), deleteSSHKey: vi.fn(),
}));
vi.mock('@/lib/api', () => apiMocks);
vi.mock('@/components/useConfirm', () => ({ useConfirm: () => ({ confirmAction: vi.fn() }) }));

const flush = () => act(async () => {});
const renderPage = () => render(<MemoryRouter><Users /></MemoryRouter>);
const user = { domain: 'a.example.com', username: 'u1', home_dir: '/h', web_dir: '/w' };

describe('Users load failure', () => {
  beforeEach(() => { vi.clearAllMocks(); });

  it('shows the error instead of the empty state', async () => {
    apiMocks.fetchUsers.mockRejectedValue(new Error('upstream 502'));
    apiMocks.fetchDomains.mockResolvedValue([]);
    apiMocks.fetchApps.mockResolvedValue([]);
    renderPage(); await flush();
    console.log('EXPECTED: upstream 502 shown, no empty state');
    console.log('ACTUAL: empty=' + !!screen.queryByText(/No SFTP users configured/) + ' err=' + !!screen.queryByText(/upstream 502/));
    expect(screen.getByText(/upstream 502/)).toBeTruthy();
    expect(screen.queryByText(/No SFTP users configured/)).toBeNull();
  });

  it('keeps loaded users when only apps fails', async () => {
    apiMocks.fetchUsers.mockResolvedValue([user]);
    apiMocks.fetchDomains.mockResolvedValue([]);
    apiMocks.fetchApps.mockRejectedValue(new Error('apps down'));
    renderPage(); await flush();
    expect(screen.getByText('a.example.com')).toBeTruthy();
    expect(screen.getByText(/apps down/)).toBeTruthy();
  });

  it('control: empty list says so without an error', async () => {
    apiMocks.fetchUsers.mockResolvedValue(null);
    apiMocks.fetchDomains.mockResolvedValue(null);
    apiMocks.fetchApps.mockResolvedValue(null);
    renderPage(); await flush();
    expect(screen.getByText(/No SFTP users configured/)).toBeTruthy();
    expect(screen.queryByText(/Could not load/)).toBeNull();
  });
});
