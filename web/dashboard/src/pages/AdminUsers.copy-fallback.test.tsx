import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import AdminUsers from './AdminUsers';
import { ConfirmContext } from '@/components/useConfirm';

// ── Mocks ───────────────────────────────────────────────────────────────────
const apiMocks = vi.hoisted(() => ({
  fetchAdminUsers: vi.fn(),
  createAdminUser: vi.fn(),
  deleteAdminUser: vi.fn(),
  changeAdminPassword: vi.fn(),
  regenAdminApiKey: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

// The clipboard is the boundary under test: copyText resolves FALSE when
// every copy strategy failed (lib/clipboard contract).
const clipboardMocks = vi.hoisted(() => ({ copyText: vi.fn() }));
vi.mock('@/lib/clipboard', () => clipboardMocks);

const confirmValue = {
  confirmAction: vi.fn().mockResolvedValue(true),
  promptText: vi.fn().mockResolvedValue(null),
};

function renderPage() {
  return render(
    <ConfirmContext.Provider value={confirmValue}>
      <AdminUsers />
    </ConfirmContext.Provider>,
  );
}

const flush = () => act(async () => {});

// Open the create form, fill it, submit — the one-time credentials panel
// ("Save these credentials now.") appears with a copy button per field.
async function createUser() {
  fireEvent.click(screen.getByRole('button', { name: 'Add User' }));
  const text = document.querySelector('input[type="text"]') as HTMLInputElement;
  fireEvent.change(text, { target: { value: 'ops' } });
  const pw = document.querySelector('input[type="password"]') as HTMLInputElement;
  fireEvent.change(pw, { target: { value: 's3cret-pw' } });
  fireEvent.click(screen.getByRole('button', { name: 'Create' }));
  await flush();
}

describe('AdminUsers copy reports clipboard failure instead of false success', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => {
      if (vi.isMockFunction(fn)) fn.mockReset();
    });
    clipboardMocks.copyText.mockReset();
    apiMocks.fetchAdminUsers.mockResolvedValue([]);
    apiMocks.createAdminUser.mockResolvedValue({ username: 'ops', api_key: 'key-xyz' });
  });

  // Regression: the created panel holds a ONE-TIME password + API key
  // ("Save these credentials now."). copy() awaited copyText but ignored its
  // boolean, so a failed copy still swapped in the check icon — the operator
  // believes the secret is on the clipboard, dismisses the panel, and the
  // credential is unrecoverable.
  it('shows a copy-failed message when the clipboard write fails', async () => {
    clipboardMocks.copyText.mockResolvedValue(false);
    renderPage();
    await flush();
    await createUser();

    fireEvent.click(screen.getByTitle('Copy Password'));
    await flush();

    expect(clipboardMocks.copyText).toHaveBeenCalledWith('s3cret-pw');
    expect(screen.getByText(/Copy failed/i)).toBeTruthy();
  });

  // Control: a successful copy keeps the existing feedback (check icon).
  it('keeps the success signal when the clipboard write succeeds', async () => {
    clipboardMocks.copyText.mockResolvedValue(true);
    renderPage();
    await flush();
    await createUser();

    fireEvent.click(screen.getByTitle('Copy Password'));
    await flush();

    expect(clipboardMocks.copyText).toHaveBeenCalledWith('s3cret-pw');
    expect(screen.queryByText(/Copy failed/i)).toBeNull();
  });
});
