import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor, act } from '@testing-library/react';
import SystemStatsBar from './SystemStatsBar';

const mockFetchSystem = vi.fn();

vi.mock('@/lib/api', () => ({
  fetchSystem: () => mockFetchSystem(),
}));

function makeSys(overrides: Record<string, unknown> = {}) {
  return {
    version: 'v0.8.9',
    cpus: 8,
    load_1m: '1.5',
    load_5m: '2.0',
    ram_total_bytes: 16_000_000_000,
    ram_total_human: '16 GB',
    ram_available_bytes: 8_000_000_000,
    disk_total_bytes: 500_000_000_000,
    disk_free_bytes: 250_000_000_000,
    disk_total_human: '500 GB',
    disk_free_human: '250 GB',
    disk_used_human: '250 GB',
    uptime: '3d 12h',
    ...overrides,
  };
}

/** Render SystemStatsBar inside act so async state updates are captured. */
function renderBar() {
  let result!: ReturnType<typeof render>;
  act(() => {
    result = render(<SystemStatsBar />);
  });
  return result;
}

describe('SystemStatsBar', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockFetchSystem.mockResolvedValue(makeSys());
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('renders nothing when system data has not loaded yet', () => {
    mockFetchSystem.mockReturnValue(new Promise(() => {}));
    const { container } = render(<SystemStatsBar />);
    expect(container.firstChild).toBeNull();
  });

  it('shows CPU count and load', async () => {
    renderBar();

    await waitFor(() => {
      expect(screen.getByText('8 CPU')).toBeInTheDocument();
      expect(screen.getByText('1.5')).toBeInTheDocument();
    });
  });

  it('shows RAM and disk percentages (both 50%)', async () => {
    renderBar();

    await waitFor(() => {
      // Both RAM and disk are at 50% — two elements match
      const pcts = screen.getAllByText('50%');
      expect(pcts).toHaveLength(2);
    });
  });

  it('shows RAM percentage in emerald when ≤ 60%', async () => {
    mockFetchSystem.mockResolvedValue(makeSys({
      ram_total_bytes: 16_000_000_000,
      ram_available_bytes: 8_000_000_000,
      disk_free_bytes: 400_000_000_000, // 20% disk — different from RAM
    }));
    renderBar();

    await waitFor(() => {
      const pcts = screen.getAllByText(/%/);
      // At least one percentage should be emerald
      const emeraldPct = pcts.find(el => el.className.includes('text-emerald-400'));
      expect(emeraldPct).toBeTruthy();
    });
  });

  it('shows RAM in amber when > 60% and ≤ 80%', async () => {
    mockFetchSystem.mockResolvedValue(makeSys({
      ram_total_bytes: 10_000_000_000,
      ram_available_bytes: 3_000_000_000, // 70%
      disk_free_bytes: 400_000_000_000,    // 20% — not amber
    }));
    renderBar();

    await waitFor(() => {
      const pct = screen.getByText('70%');
      expect(pct.className).toContain('text-amber-400');
    });
  });

  it('shows RAM in red when > 80%', async () => {
    mockFetchSystem.mockResolvedValue(makeSys({
      ram_total_bytes: 10_000_000_000,
      ram_available_bytes: 1_000_000_000, // 90%
      disk_free_bytes: 400_000_000_000,    // 20% — not red
    }));
    renderBar();

    await waitFor(() => {
      const pct = screen.getByText('90%');
      expect(pct.className).toContain('text-red-400');
    });
  });

  it('shows disk percentage', async () => {
    mockFetchSystem.mockResolvedValue(makeSys({
      disk_total_bytes: 100_000_000_000,
      disk_free_bytes: 50_000_000_000, // 50%
    }));
    renderBar();

    await waitFor(() => {
      const pcts = screen.getAllByText('50%');
      expect(pcts.length).toBeGreaterThanOrEqual(1);
    });
  });

  it('shows disk in emerald when ≤ 60%', async () => {
    mockFetchSystem.mockResolvedValue(makeSys({
      disk_total_bytes: 100_000_000_000,
      disk_free_bytes: 50_000_000_000, // 50%
    }));
    renderBar();

    await waitFor(() => {
      const diskPcts = screen.getAllByText('50%');
      expect(diskPcts.length).toBeGreaterThanOrEqual(1);
      expect(diskPcts[0].className).toContain('text-emerald-400');
    });
  });

  it('shows disk in amber when > 60% and ≤ 80%', async () => {
    mockFetchSystem.mockResolvedValue(makeSys({
      disk_total_bytes: 100_000_000_000,
      disk_free_bytes: 30_000_000_000, // 70%
      ram_available_bytes: 0,           // 100% RAM but hide with 0
    }));
    renderBar();

    await waitFor(() => {
      const pct = screen.getByText('70%');
      expect(pct.className).toContain('text-amber-400');
    });
  });

  it('shows disk in red when > 80%', async () => {
    mockFetchSystem.mockResolvedValue(makeSys({
      disk_total_bytes: 100_000_000_000,
      disk_free_bytes: 10_000_000_000, // 90%
      ram_available_bytes: 0,           // hide RAM percentage
    }));
    renderBar();

    await waitFor(() => {
      const pct = screen.getByText('90%');
      expect(pct.className).toContain('text-red-400');
    });
  });

  it('shows uptime', async () => {
    renderBar();

    await waitFor(() => {
      expect(screen.getByText('3d 12h')).toBeInTheDocument();
    });
  });

  it('shows version', async () => {
    renderBar();

    await waitFor(() => {
      expect(screen.getByText('v0.8.9')).toBeInTheDocument();
    });
  });

  it('does not show RAM percentage when total bytes is missing', async () => {
    mockFetchSystem.mockResolvedValue(makeSys({
      ram_total_bytes: undefined,
      ram_available_bytes: 8_000_000_000,
      disk_total_bytes: 100_000_000_000,
      disk_free_bytes: 50_000_000_000,
    }));
    renderBar();

    await waitFor(() => {
      expect(screen.getByText('RAM')).toBeInTheDocument();
      expect(screen.getByText('16 GB')).toBeInTheDocument();
    });

    // Only the disk percentage should appear (50%)
    const percentages = screen.getAllByText('50%');
    expect(percentages).toHaveLength(1);
  });

  it('does not show RAM percentage when available bytes is missing', async () => {
    mockFetchSystem.mockResolvedValue(makeSys({
      ram_total_bytes: 16_000_000_000,
      ram_available_bytes: undefined,
      disk_total_bytes: 100_000_000_000,
      disk_free_bytes: 50_000_000_000,
    }));
    renderBar();

    await waitFor(() => {
      expect(screen.getByText('16 GB')).toBeInTheDocument();
    });
  });

  it('refetches system info on interval', async () => {
    vi.useFakeTimers();
    mockFetchSystem.mockResolvedValue(makeSys({ load_1m: '1.0' }));
    renderBar();

    // Flush promises and trigger the initial load()
    await vi.advanceTimersByTimeAsync(10);
    expect(mockFetchSystem).toHaveBeenCalledTimes(1);

    // After 2 seconds, the interval should fire again
    mockFetchSystem.mockResolvedValue(makeSys({ load_1m: '2.5' }));
    await vi.advanceTimersByTimeAsync(2000);
    expect(mockFetchSystem).toHaveBeenCalledTimes(2);

    vi.useRealTimers();
  });
});
