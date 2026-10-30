import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import Backups from './Backups';
import Certificates from './Certificates';

// F55601/F55602: a failed list request left the red error banner but the page
// also said "No backups yet. Create your first backup." / "No certificates
// configured", presenting an unknown state as an empty one.

const api = vi.hoisted(() => ({
  fetchBackups: vi.fn(), createBackup: vi.fn(), restoreBackup: vi.fn(), deleteBackup: vi.fn(),
  fetchBackupSchedule: vi.fn(), updateBackupSchedule: vi.fn(), fetchFeatures: vi.fn(),
  fetchCerts: vi.fn(), renewCert: vi.fn(), uploadCert: vi.fn(),
}));
vi.mock('@/lib/api', () => api);
vi.mock('@/components/useConfirm', () => ({ useConfirm: () => ({ confirmAction: vi.fn() }) }));

const flush = () => act(async () => { for (let i = 0; i < 10; i++) await Promise.resolve(); });

describe('list load failures', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.fetchFeatures.mockResolvedValue({});
  });

  it('Backups: error without the empty message', async () => {
    api.fetchBackups.mockRejectedValue(new Error('upstream 502'));
    api.fetchBackupSchedule.mockResolvedValue({ enabled: false, interval: '24h', keep: 7 });
    render(<MemoryRouter><Backups /></MemoryRouter>); await flush();
    console.log('EXPECTED: error only');
    console.log('ACTUAL: empty=' + !!screen.queryByText(/No backups yet/));
    expect(screen.getByText(/upstream 502/)).toBeTruthy();
    expect(screen.queryByText(/No backups yet/)).toBeNull();
  });

  it('Backups control: empty list says so', async () => {
    api.fetchBackups.mockResolvedValue(null);
    api.fetchBackupSchedule.mockResolvedValue({ enabled: false, interval: '24h', keep: 7 });
    render(<MemoryRouter><Backups /></MemoryRouter>); await flush();
    expect(screen.getByText(/No backups yet/)).toBeTruthy();
  });

  it('Certificates: error without the empty message', async () => {
    api.fetchCerts.mockRejectedValue(new Error('upstream 502'));
    render(<MemoryRouter><Certificates /></MemoryRouter>); await flush();
    console.log('ACTUAL: empty=' + !!screen.queryByText(/No certificates configured/));
    expect(screen.getByText(/upstream 502/)).toBeTruthy();
    expect(screen.queryByText(/No certificates configured/)).toBeNull();
  });

  it('Certificates control: empty list says so', async () => {
    api.fetchCerts.mockResolvedValue([]);
    render(<MemoryRouter><Certificates /></MemoryRouter>); await flush();
    expect(screen.getByText(/No certificates configured/)).toBeTruthy();
  });
});
