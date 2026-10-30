import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import AdminUsers from './AdminUsers';
import * as api from '@/lib/api';

// F39100: replacing your OWN API key now proves the current password (the
// server answers 400 "current_password required" without it). The page has to
// ask for the password in the row and retry with it, instead of showing a dead
// error; replacing another user's key keeps working without a password.

vi.mock('@/components/useConfirm', () => ({
  useConfirm: () => ({ confirmAction: vi.fn(async () => true) }),
}));

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>();
  return {
    ...actual,
    fetchAdminUsers: vi.fn(),
    regenAdminApiKey: vi.fn(),
  };
});

const USERS = [
  { username: 'alice', role: 'admin', email: '', domains: [], created_at: '' },
  { username: 'bob', role: 'user', email: '', domains: [], created_at: '' },
];

describe('AdminUsers API key regeneration', () => {
  beforeEach(() => {
    vi.mocked(api.fetchAdminUsers).mockResolvedValue(USERS);
    vi.mocked(api.regenAdminApiKey).mockReset();
  });

  it('asks for the current password when the server requires it, then retries with it', async () => {
    vi.mocked(api.regenAdminApiKey)
      .mockRejectedValueOnce(new Error('current_password required'))
      .mockResolvedValueOnce({ api_key: 'newkey-12345678' });
    render(<AdminUsers />);
    await screen.findByText('alice');

    fireEvent.click(screen.getAllByTitle('Regenerate API key')[0]);
    const input = await screen.findByPlaceholderText('Current password');
    expect(screen.queryByText(/current_password required/)).toBeNull();

    fireEvent.change(input, { target: { value: 'hunter2-hunter2' } });
    fireEvent.click(screen.getByText('Regenerate'));

    await waitFor(() => expect(api.regenAdminApiKey).toHaveBeenLastCalledWith('alice', 'hunter2-hunter2'));
    await waitFor(() => expect(screen.queryByPlaceholderText('Current password')).toBeNull());
  });

  it('control: another user’s key is replaced without any password prompt', async () => {
    vi.mocked(api.regenAdminApiKey).mockResolvedValueOnce({ api_key: 'newkey-12345678' });
    render(<AdminUsers />);
    await screen.findByText('bob');

    fireEvent.click(screen.getAllByTitle('Regenerate API key')[1]);

    await waitFor(() => expect(api.regenAdminApiKey).toHaveBeenCalledWith('bob', undefined));
    expect(screen.queryByPlaceholderText('Current password')).toBeNull();
  });

  it('a wrong password is shown as an error and the prompt stays', async () => {
    vi.mocked(api.regenAdminApiKey)
      .mockRejectedValueOnce(new Error('current_password required'))
      .mockRejectedValueOnce(new Error('invalid current password'));
    render(<AdminUsers />);
    await screen.findByText('alice');

    fireEvent.click(screen.getAllByTitle('Regenerate API key')[0]);
    const input = await screen.findByPlaceholderText('Current password');
    fireEvent.change(input, { target: { value: 'wrong-password-x' } });
    fireEvent.click(screen.getByText('Regenerate'));

    await screen.findByText(/invalid current password/);
    expect(screen.getByPlaceholderText('Current password')).toBeTruthy();
  });
});
