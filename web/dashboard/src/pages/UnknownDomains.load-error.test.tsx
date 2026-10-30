import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import UnknownDomains from './UnknownDomains';

// F53601: a failed unknown-domain poll was swallowed and the page said
// "No unknown domains detected".

const apiMocks = vi.hoisted(() => ({
  fetchUnknownDomains: vi.fn(), fetchFeatures: vi.fn(), fetchDomains: vi.fn(),
  blockUnknownDomain: vi.fn(), unblockUnknownDomain: vi.fn(),
  dismissUnknownDomain: vi.fn(), redirectUnknownDomain: vi.fn(),
}));
vi.mock('@/lib/api', () => apiMocks);

const flush = () => act(async () => {});
const renderPage = () => render(<MemoryRouter><UnknownDomains /></MemoryRouter>);

describe('UnknownDomains load failure', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.fetchFeatures.mockResolvedValue({});
    apiMocks.fetchDomains.mockResolvedValue([]);
  });

  it('shows the error instead of the empty state', async () => {
    apiMocks.fetchUnknownDomains.mockRejectedValue(new Error('upstream 502'));
    renderPage(); await flush();
    console.log('EXPECTED: upstream 502 shown, no empty state');
    console.log('ACTUAL: empty=' + !!screen.queryByText(/No unknown domains detected/) + ' err=' + !!screen.queryByText(/upstream 502/));
    expect(screen.getByText(/upstream 502/)).toBeTruthy();
    expect(screen.queryByText(/No unknown domains detected/)).toBeNull();
  });

  it('control: empty list says so without an error', async () => {
    apiMocks.fetchUnknownDomains.mockResolvedValue([]);
    renderPage(); await flush();
    expect(screen.getByText(/No unknown domains detected/)).toBeTruthy();
    expect(screen.queryByText(/Could not load/)).toBeNull();
  });
});
