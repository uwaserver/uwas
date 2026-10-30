import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act, fireEvent } from '@testing-library/react';
import AdminUsers from './AdminUsers';

// F58603: the load-error check and the action-error banner shared one
// `error` state, so a failed "create" on a successfully loaded, empty list
// hid the "No admin users" hint (the list is known to be empty).

const api = vi.hoisted(() => ({
  fetchAdminUsers: vi.fn(), createAdminUser: vi.fn(), updateAdminUser: vi.fn(),
  deleteAdminUser: vi.fn(), regenAdminApiKey: vi.fn(),
}));
vi.mock('@/lib/api', () => api);
vi.mock('@/components/useConfirm', () => ({ useConfirm: () => ({ confirmAction: vi.fn() }) }));

const flush = () => act(async () => { for (let i = 0; i < 10; i++) await Promise.resolve(); });

async function submitCreate(container: HTMLElement) {
  fireEvent.click(screen.getByText('Add User'));
  const inputs = container.querySelectorAll('input');
  fireEvent.change(inputs[0], { target: { value: 'bob' } });
  fireEvent.change(inputs[1], { target: { value: 'pw' } });
  fireEvent.click(screen.getByText('Create'));
  await flush();
}

describe('AdminUsers action errors', () => {
  beforeEach(() => { vi.clearAllMocks(); });

  it('failed create on a loaded empty list keeps the empty hint', async () => {
    api.fetchAdminUsers.mockResolvedValue([]);
    api.createAdminUser.mockRejectedValue(new Error('password too weak'));
    const { container } = render(<AdminUsers />); await flush();
    await submitCreate(container);
    console.log('ACTUAL: hint=' + !!screen.queryByText(/No admin users/));
    expect(screen.getByText(/password too weak/)).toBeTruthy();
    expect(screen.getByText(/No admin users/)).toBeTruthy();
  });

  it('control: failed load still hides the hint', async () => {
    api.fetchAdminUsers.mockRejectedValue(new Error('upstream 502'));
    render(<AdminUsers />); await flush();
    expect(screen.getByText(/upstream 502/)).toBeTruthy();
    expect(screen.queryByText(/No admin users/)).toBeNull();
  });

  it('control: failed load, then failed create, still does not claim an empty list', async () => {
    api.fetchAdminUsers.mockRejectedValue(new Error('upstream 502'));
    api.createAdminUser.mockRejectedValue(new Error('password too weak'));
    const { container } = render(<AdminUsers />); await flush();
    await submitCreate(container);
    expect(screen.getByText(/password too weak/)).toBeTruthy();
    expect(screen.queryByText(/No admin users/)).toBeNull();
  });
});
