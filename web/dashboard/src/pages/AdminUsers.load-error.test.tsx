import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import AdminUsers from './AdminUsers';

// F55603: a failed admin-users request left the error banner but the page
// also said "No admin users — Multi-user auth may not be enabled".

const api = vi.hoisted(() => ({
  fetchAdminUsers: vi.fn(), createAdminUser: vi.fn(), updateAdminUser: vi.fn(),
  deleteAdminUser: vi.fn(), regenAdminApiKey: vi.fn(),
}));
vi.mock('@/lib/api', () => api);
vi.mock('@/components/useConfirm', () => ({ useConfirm: () => ({ confirmAction: vi.fn() }) }));

const flush = () => act(async () => { for (let i = 0; i < 10; i++) await Promise.resolve(); });

describe('AdminUsers load failure', () => {
  beforeEach(() => { vi.clearAllMocks(); });

  it('shows the error, not the "no admin users" hint', async () => {
    api.fetchAdminUsers.mockRejectedValue(new Error('upstream 502'));
    render(<AdminUsers />); await flush();
    console.log('ACTUAL: empty=' + !!screen.queryByText(/No admin users/));
    expect(screen.getByText(/upstream 502/)).toBeTruthy();
    expect(screen.queryByText(/No admin users/)).toBeNull();
  });

  it('control: empty list says so', async () => {
    api.fetchAdminUsers.mockResolvedValue([]);
    render(<AdminUsers />); await flush();
    expect(screen.getByText(/No admin users/)).toBeTruthy();
  });
});
