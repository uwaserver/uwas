import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import CloneStaging from './CloneStaging';

const apiMocks = vi.hoisted(() => ({
  fetchDomains: vi.fn(),
  cloneSite: vi.fn(),
}));
vi.mock('@/lib/api', () => apiMocks);
vi.mock('@/lib/clipboard', () => ({ copyText: vi.fn() }));

const DOMAINS = [{ host: 'a.com', type: 'php', aliases: null }];

// F62002: the clone endpoint answers status "done" with a non-empty error when
// the files were copied but the database clone failed. The page only showed
// the error for status "error", so the operator saw an unqualified "Clone
// Complete" with no database listed and no reason.
describe('CloneStaging surfaces a partial clone', () => {
  beforeEach(() => {
    apiMocks.fetchDomains.mockReset();
    apiMocks.cloneSite.mockReset();
    apiMocks.fetchDomains.mockResolvedValue(DOMAINS);
  });

  const clone = async () => {
    render(<CloneStaging />);
    await act(async () => {});
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'a.com' } });
    await act(async () => {});
    fireEvent.click(screen.getByRole('button', { name: /clone site/i }));
    await act(async () => {});
  };

  it('shows the database failure next to a done result', async () => {
    apiMocks.cloneSite.mockResolvedValue({
      status: 'done',
      source_domain: 'a.com',
      target_domain: 'staging.a.com',
      target_root: '/var/www/staging.a.com/public_html',
      error: 'database clone failed: mysqldump not found',
    });
    await clone();
    expect(screen.getByText(/database clone failed: mysqldump not found/)).toBeTruthy();
  });

  it('shows no warning for a clean clone', async () => {
    apiMocks.cloneSite.mockResolvedValue({
      status: 'done',
      source_domain: 'a.com',
      target_domain: 'staging.a.com',
      target_root: '/var/www/staging.a.com/public_html',
    });
    await clone();
    expect(screen.queryByText(/clone failed/i)).toBeNull();
    expect(screen.getByText('Clone Complete')).toBeTruthy();
  });
});
