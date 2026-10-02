import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import Backups from './Backups';

// ── Mocks ───────────────────────────────────────────────────────────────────

const mockFetchBackups = vi.fn();
const mockCreateBackup = vi.fn();
const mockRestoreBackup = vi.fn();
const mockDeleteBackup = vi.fn();
const mockFetchBackupSchedule = vi.fn();
const mockUpdateBackupSchedule = vi.fn();
const mockFetchFeatures = vi.fn();

vi.mock('@/lib/api', () => ({
  fetchBackups: (...args: unknown[]) => mockFetchBackups(...args),
  createBackup: (...args: unknown[]) => mockCreateBackup(...args),
  restoreBackup: (...args: unknown[]) => mockRestoreBackup(...args),
  deleteBackup: (...args: unknown[]) => mockDeleteBackup(...args),
  fetchBackupSchedule: (...args: unknown[]) => mockFetchBackupSchedule(...args),
  updateBackupSchedule: (...args: unknown[]) => mockUpdateBackupSchedule(...args),
  fetchFeatures: (...args: unknown[]) => mockFetchFeatures(...args),
}));

// Drain the async load chain inside act.
const settle = () => act(async () => {
  for (let i = 0; i < 10; i++) await Promise.resolve();
});

function intervalSelect(): HTMLSelectElement {
  return screen.getByRole('combobox') as HTMLSelectElement;
}

describe('Backups schedule form', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockFetchFeatures.mockResolvedValue({});
    mockFetchBackups.mockResolvedValue([]);
    mockUpdateBackupSchedule.mockResolvedValue({ status: 'ok' });
  });

  it('represents a stored custom interval as Custom... with the value prefilled', async () => {
    mockFetchBackupSchedule.mockResolvedValue({
      enabled: true, interval: '90m', keep: 7, last_backup: '', next_backup: '',
    });
    render(<Backups />);
    await settle();

    // "90m" matches no preset option, so the select must fall back to
    // Custom... and show the stored value in the input. Unmatched, the
    // dropdown renders blank and the configured interval is invisible.
    expect(intervalSelect().value).toBe('__custom__');
    const input = screen.getByPlaceholderText('e.g. 3h, 90m, 2d, 240h') as HTMLInputElement;
    expect(input.value).toBe('90m');
  });

  it('represents a day-shorthand schedule stored as 48h as Custom... too', async () => {
    mockFetchBackupSchedule.mockResolvedValue({
      enabled: true, interval: '48h', keep: 7, last_backup: '', next_backup: '',
    });
    render(<Backups />);
    await settle();

    // "2d" is stored as "48h" after conversion — also not a preset.
    expect(intervalSelect().value).toBe('__custom__');
    const input = screen.getByPlaceholderText('e.g. 3h, 90m, 2d, 240h') as HTMLInputElement;
    expect(input.value).toBe('48h');
  });

  // Control: the select works unchanged for stored preset intervals.
  it('still selects the matching option for stored preset intervals', async () => {
    mockFetchBackupSchedule.mockResolvedValue({
      enabled: true, interval: '24h', keep: 7, last_backup: '', next_backup: '',
    });
    render(<Backups />);
    await settle();

    expect(intervalSelect().value).toBe('24h');
  });

  it('round-trips a custom day interval through the backend duration format', async () => {
    mockFetchBackupSchedule.mockResolvedValue({
      enabled: false, interval: '24h', keep: 7, last_backup: '', next_backup: '',
    });
    render(<Backups />);
    await settle();

    // Enable auto-backup so the interval controls render. The toggle button
    // has no accessible name of its own (its label is a sibling span), so
    // target it via that label's container.
    const toggle = screen
      .getByText(/auto-backup disabled/i)
      .parentElement!.querySelector('button') as HTMLButtonElement;
    fireEvent.click(toggle);
    fireEvent.change(intervalSelect(), { target: { value: '__custom__' } });
    const input = screen.getByPlaceholderText('e.g. 3h, 90m, 2d, 240h');
    fireEvent.change(input, { target: { value: '2d' } });
    fireEvent.click(screen.getByRole('button', { name: /save schedule/i }));
    await settle();

    expect(mockUpdateBackupSchedule).toHaveBeenCalledWith(
      expect.objectContaining({ enabled: true, interval: '48h' }),
    );
  });
});
