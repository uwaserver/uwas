import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import Logs from './Logs';

// F31000: Pause froze the list only for requests started afterwards. A poll
// already in flight when Pause was clicked still replaced the list, so the
// rows the operator paused to read shifted under them.

const apiMocks = vi.hoisted(() => ({ fetchLogs: vi.fn() }));
vi.mock('@/lib/api', () => apiMocks);

const entry = (path: string) => ({
  time: '2026-10-27T10:00:00Z', host: 'example.com', method: 'GET', path,
  status: 200, bytes: 1, duration_ms: 1, remote: '1.2.3.4',
});

const flush = () => act(async () => {});

describe('Logs pause', () => {
  beforeEach(() => { vi.clearAllMocks(); });

  it('ignores a poll that was in flight when Pause was clicked', async () => {
    let release!: (v: unknown) => void;
    apiMocks.fetchLogs
      .mockResolvedValueOnce([entry('/before')])
      .mockImplementationOnce(() => new Promise((r) => { release = r; }))
      .mockResolvedValue([entry('/manual')]);
    render(<Logs />);
    await flush();
    expect(screen.getByText('/before')).toBeTruthy();

    // second request (manual Refresh) is now in flight; pause, then it lands
    fireEvent.click(screen.getByText('Refresh'));
    fireEvent.click(screen.getByText('Pause'));
    await act(async () => { release([entry('/stale-after-pause')]); });
    console.log('EXPECTED: /before still shown while paused');
    console.log('ACTUAL: stale=' + !!screen.queryByText('/stale-after-pause'));
    expect(screen.queryByText('/stale-after-pause')).toBeNull();
    expect(screen.getByText('/before')).toBeTruthy();
  });

  it('control: Refresh clicked while already paused still updates', async () => {
    apiMocks.fetchLogs
      .mockResolvedValueOnce([entry('/before')])
      .mockResolvedValue([entry('/manual')]);
    render(<Logs />);
    await flush();
    fireEvent.click(screen.getByText('Pause'));
    fireEvent.click(screen.getByText('Refresh'));
    await flush();
    expect(screen.getByText('/manual')).toBeTruthy();
  });
});
