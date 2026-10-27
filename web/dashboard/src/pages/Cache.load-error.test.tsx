import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import Cache from './Cache';

// F34301: a failed cache stats request was swallowed, so the page kept
// saying "Cache not enabled" with zero counters. A broken admin API looked
// like a server with the cache switched off.

const apiMocks = vi.hoisted(() => ({
  triggerPurge: vi.fn(),
  purgeCacheForHost: vi.fn(),
  fetchCacheStats: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);
vi.mock('@/hooks/useStats', () => ({
  useStats: () => ({ stats: null, health: null, history: [], error: null }),
}));

function renderPage() {
  return render(
    <MemoryRouter>
      <Cache />
    </MemoryRouter>,
  );
}

const flush = () => act(async () => {});

describe('Cache stats load failure', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('shows the error instead of "Cache not enabled"', async () => {
    apiMocks.fetchCacheStats.mockRejectedValue(new Error('upstream 502'));
    renderPage();
    await flush();
    console.log('EXPECTED: error mentioning "upstream 502", no "Cache not enabled"');
    console.log('ACTUAL: notEnabled=' + !!screen.queryByText(/Cache not enabled/) + ' error=' + !!screen.queryByText(/upstream 502/));
    expect(screen.getByText(/upstream 502/)).toBeTruthy();
    expect(screen.queryByText(/Cache not enabled/)).toBeNull();
  });

  it('control: a disabled cache still says so', async () => {
    apiMocks.fetchCacheStats.mockResolvedValue({
      enabled: false, entries: 0, used_bytes: 0, hits: 0, misses: 0, stales: 0, domains: [],
    });
    renderPage();
    await flush();
    expect(screen.getByText(/Cache not enabled/)).toBeTruthy();
    expect(screen.queryByText(/could not load/i)).toBeNull();
  });
});
