import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import CloneStaging from './CloneStaging';

const apiMocks = vi.hoisted(() => ({
  fetchDomains: vi.fn(),
  cloneSite: vi.fn(),
}));
vi.mock('@/lib/api', () => apiMocks);
const clipboardMocks = vi.hoisted(() => ({ copyText: vi.fn() }));
vi.mock('@/lib/clipboard', () => clipboardMocks);

const DOMAINS = [{ host: 'a.com', type: 'static', aliases: null }, { host: 'b.com', type: 'static', aliases: null }];

// Regression (round 24): after cloning a site, the form keeps the previous
// target ('staging.a.com') when the operator picks a new source — the
// auto-suggest only fires for an EMPTY target, and handleClone never
// refreshes `domains`, so the exists-disable can't catch the session-created
// staging host either. The server (internal/migrate Clone + handleClone) has
// no target-exists check and overwrites the existing staging (MkdirAll +
// copy; deterministic target DB name) — silently destroying the previous
// staging environment.
describe('CloneStaging re-suggests the staging target for a new source', () => {
  beforeEach(() => {
    apiMocks.fetchDomains.mockReset();
    apiMocks.cloneSite.mockReset();
    clipboardMocks.copyText.mockReset();
    apiMocks.fetchDomains.mockResolvedValue(DOMAINS);
    apiMocks.cloneSite.mockResolvedValue({
      status: 'done',
      source_domain: 'a.com',
      target_domain: 'staging.a.com',
      target_root: '/var/www/staging.a.com/public_html',
    });
  });

  const selectSource = async (host: string) => {
    fireEvent.change(screen.getByRole('combobox'), { target: { value: host } });
    await act(async () => {});
  };

  const targetInput = () => screen.getByPlaceholderText('staging.example.com') as HTMLInputElement;

  it('updates the suggested target when the source changes after a clone', async () => {
    render(<CloneStaging />);
    await act(async () => {});

    await selectSource('a.com');
    expect(targetInput().value).toBe('staging.a.com');

    fireEvent.click(screen.getByRole('button', { name: /clone site/i }));
    await act(async () => {});
    expect(apiMocks.cloneSite).toHaveBeenCalledWith({ source_domain: 'a.com', target_domain: 'staging.a.com' });

    // Pick a different source: the stale 'staging.a.com' must not survive —
    // cloning b.com into a.com's staging silently overwrites it (server has
    // no target-exists check).
    await selectSource('b.com');
    expect(targetInput().value).toBe('staging.b.com');

    fireEvent.click(screen.getByRole('button', { name: /clone site/i }));
    await act(async () => {});
    expect(apiMocks.cloneSite).toHaveBeenLastCalledWith({ source_domain: 'b.com', target_domain: 'staging.b.com' });
  });

  it('control: suggests staging.<source> on first selection', async () => {
    render(<CloneStaging />);
    await act(async () => {});

    await selectSource('b.com');
    expect(targetInput().value).toBe('staging.b.com');
    expect(apiMocks.cloneSite).not.toHaveBeenCalled();
  });
});
