import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act, fireEvent } from '@testing-library/react';
import AdminUsers from './AdminUsers';

// F62601: the change-password / API-key inline forms keep their typed values
// when the operator opens the same form on another user's row. The inputs are
// masked, so the password typed for alice was silently submitted for bob.

const api = vi.hoisted(() => ({
  fetchAdminUsers: vi.fn(), createAdminUser: vi.fn(), updateAdminUser: vi.fn(),
  deleteAdminUser: vi.fn(), regenAdminApiKey: vi.fn(), changeAdminPassword: vi.fn(),
}));
vi.mock('@/lib/api', () => api);
vi.mock('@/components/useConfirm', () => ({ useConfirm: () => ({ confirmAction: vi.fn() }) }));

const flush = () => act(async () => { for (let i = 0; i < 10; i++) await Promise.resolve(); });
const users = [
  { username: 'alice', role: 'admin', email: '', domains: [], enabled: true },
  { username: 'bob', role: 'user', email: '', domains: [], enabled: true },
];

describe('AdminUsers inline password form', () => {
  beforeEach(() => { vi.clearAllMocks(); api.fetchAdminUsers.mockResolvedValue(users); });

  it('does not carry typed passwords over to another user row', async () => {
    render(<AdminUsers />); await flush();
    fireEvent.click(screen.getAllByTitle('Change password')[0]);
    fireEvent.change(screen.getByPlaceholderText('New password'), { target: { value: 'for-alice-only' } });
    fireEvent.change(screen.getByPlaceholderText('Current (own account)'), { target: { value: 'my-current' } });
    // alice's row switched to the form; bob's row still shows the lock button.
    fireEvent.click(screen.getAllByTitle('Change password')[0]);
    const next = screen.getByPlaceholderText('New password') as HTMLInputElement;
    const cur = screen.getByPlaceholderText('Current (own account)') as HTMLInputElement;
    console.log('EXPECTED: empty fields for the newly opened row');
    console.log(`ACTUAL: new="${next.value}" current="${cur.value}"`);
    expect(next.value).toBe('');
    expect(cur.value).toBe('');
  });

  it('control: the same row keeps what was typed', async () => {
    render(<AdminUsers />); await flush();
    fireEvent.click(screen.getAllByTitle('Change password')[0]);
    fireEvent.change(screen.getByPlaceholderText('New password'), { target: { value: 'typed' } });
    expect((screen.getByPlaceholderText('New password') as HTMLInputElement).value).toBe('typed');
  });
});
