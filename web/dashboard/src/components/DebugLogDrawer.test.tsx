import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import DebugLogDrawer from './DebugLogDrawer';

// ── Mocks ──────────────────────────────────────────────────────────────────

// Define types first so mock signatures are sound
interface MockEntry {
  id: number;
  time: string;
  level: string;
  scope: string;
  message: string;
  detail?: string;
  duration_ms?: number;
}

const defaultSnapshot = { enabled: false, entries: [] as MockEntry[] };

const mockSubscribe = vi.fn((_listener: () => void) => () => {});
const mockGetSnapshot = vi.fn(() => defaultSnapshot);
const mockSetEnabled = vi.fn((_value: boolean) => {});
const mockClearLog = vi.fn(() => {});
const mockAddLog = vi.fn((_entry: unknown) => {});
const mockCopyText = vi.fn(async (_text: string) => true);

vi.mock('@/lib/debugLog', () => ({
  subscribeDebugLog: (listener: () => void) => {
    mockSubscribe(listener);
    return () => {};
  },
  getDebugLogSnapshot: () => mockGetSnapshot(),
  setDebugLogEnabled: (value: boolean) => mockSetEnabled(value),
  clearDebugLog: () => mockClearLog(),
  addDebugLog: (entry: unknown) => mockAddLog(entry),
  formatDebugDetail: (v: unknown) => (v ? String(v) : undefined),
}));

vi.mock('@/lib/clipboard', () => ({
  copyText: (text: string) => mockCopyText(text),
}));

function setSnapshot(enabled: boolean, entries: MockEntry[]) {
  mockGetSnapshot.mockReturnValue({ enabled, entries });
}

const sampleEntry = {
  id: 1,
  time: '2026-07-19T00:00:00.000Z',
  level: 'info',
  scope: 'api',
  message: 'GET /api/v1/domains',
};

// ── Tests ───────────────────────────────────────────────────────────────────

describe('DebugLogDrawer', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setSnapshot(false, []);
  });

  // ── Floating toggle ─────────────────────────────────────────────────────

  it('shows the floating toggle button', () => {
    render(<DebugLogDrawer />);

    expect(screen.getByLabelText('Toggle debug log')).toBeInTheDocument();
  });

  it('shows entry count badge on floating button', () => {
    setSnapshot(true, [
      { ...sampleEntry, id: 1 },
      { ...sampleEntry, id: 2 },
    ]);
    render(<DebugLogDrawer />);

    expect(screen.getByText('2')).toBeInTheDocument();
  });

  it('shows count 0 when no entries', () => {
    render(<DebugLogDrawer />);

    expect(screen.getByText('0')).toBeInTheDocument();
  });

  // ── Toggle switch ───────────────────────────────────────────────────────

  it('toggles debug on when clicked', () => {
    render(<DebugLogDrawer />);
    fireEvent.click(screen.getByLabelText('Toggle debug log'));

    expect(mockSetEnabled).toHaveBeenCalledWith(true);
  });

  it('toggles debug off when already on', () => {
    setSnapshot(true, []);
    render(<DebugLogDrawer />);
    fireEvent.click(screen.getByLabelText('Toggle debug log'));

    expect(mockSetEnabled).toHaveBeenCalledWith(false);
  });

  it('shows enabled indicator icon color when debug is on', () => {
    setSnapshot(true, []);
    render(<DebugLogDrawer />);

    // The enabled indicator button should have emerald background
    const toggleBtn = screen.getByLabelText('Toggle debug log');
    expect(toggleBtn.className).toContain('bg-emerald-500');
  });

  it('shows muted icon color when debug is off', () => {
    setSnapshot(false, []);
    render(<DebugLogDrawer />);

    const toggleBtn = screen.getByLabelText('Toggle debug log');
    expect(toggleBtn.className).toContain('bg-muted');
  });

  // ── Drawer open/close ───────────────────────────────────────────────────

  it('opens the drawer when the activity button is clicked', () => {
    render(<DebugLogDrawer />);

    // Click on the open-debug button (the one that shows "0")
    fireEvent.click(screen.getByText('0'));

    expect(screen.getByText('Debug Log')).toBeInTheDocument();
  });

  it('closes the drawer when the Close button is clicked', () => {
    render(<DebugLogDrawer />);

    fireEvent.click(screen.getByText('0'));
    expect(screen.getByText('Debug Log')).toBeInTheDocument();

    fireEvent.click(screen.getByText('Close'));
    expect(screen.queryByText('Debug Log')).not.toBeInTheDocument();
  });

  // ── Empty state ─────────────────────────────────────────────────────────

  it('shows empty state message when there are no entries', () => {
    render(<DebugLogDrawer />);
    fireEvent.click(screen.getByText('0'));

    expect(
      screen.getByText('Turn debug on and run an action to see events here.'),
    ).toBeInTheDocument();
  });

  it('shows empty state message when debug is on but no entries yet', () => {
    setSnapshot(true, []);
    render(<DebugLogDrawer />);
    fireEvent.click(screen.getByText('0'));

    expect(
      screen.getByText('Turn debug on and run an action to see events here.'),
    ).toBeInTheDocument();
  });

  // ── Entry rendering ─────────────────────────────────────────────────────

  it('renders entries when drawer is open', () => {
    setSnapshot(true, [
      { ...sampleEntry, id: 1, message: 'GET /api/v1/domains' },
      { ...sampleEntry, id: 2, message: 'GET /api/v1/health' },
    ]);
    render(<DebugLogDrawer />);
    fireEvent.click(screen.getByText('2'));

    expect(screen.getByText('GET /api/v1/domains')).toBeInTheDocument();
    expect(screen.getByText('GET /api/v1/health')).toBeInTheDocument();
  });

  it('renders entry with level badge', () => {
    setSnapshot(true, [{ ...sampleEntry, level: 'error', message: 'fail' }]);
    render(<DebugLogDrawer />);
    fireEvent.click(screen.getByText('1'));

    expect(screen.getByText('error')).toBeInTheDocument();
  });

  it('renders entry with duration when present', () => {
    setSnapshot(true, [
      { ...sampleEntry, duration_ms: 150, message: 'slow request' },
    ]);
    render(<DebugLogDrawer />);
    fireEvent.click(screen.getByText('1'));

    expect(screen.getByText('150ms')).toBeInTheDocument();
  });

  it('renders entry detail in a pre block when present', () => {
    setSnapshot(true, [
      { ...sampleEntry, detail: 'stack trace here', message: 'error detail' },
    ]);
    render(<DebugLogDrawer />);
    fireEvent.click(screen.getByText('1'));

    const pre = document.querySelector('pre');
    expect(pre).toBeInTheDocument();
    expect(pre?.textContent).toBe('stack trace here');
  });

  it('shows the live capture status line', () => {
    setSnapshot(true, [{ ...sampleEntry, id: 1 }]);
    render(<DebugLogDrawer />);
    fireEvent.click(screen.getByText('1'));

    expect(screen.getByText(/Live capture is on/)).toBeInTheDocument();
  });

  it('shows capture off status when disabled', () => {
    render(<DebugLogDrawer />);
    fireEvent.click(screen.getByText('0'));

    expect(screen.getByText(/Live capture is off/)).toBeInTheDocument();
  });

  // ── Buttons: Copy and Clear ─────────────────────────────────────────────

  it('disables Copy and Clear buttons when there are no entries', () => {
    render(<DebugLogDrawer />);
    fireEvent.click(screen.getByText('0'));

    const copyBtn = screen.getByText('Copy').closest('button');
    const clearBtn = screen.getByText('Clear').closest('button');
    expect(copyBtn).toBeDisabled();
    expect(clearBtn).toBeDisabled();
  });

  it('enables Copy and Clear buttons when there are entries', () => {
    setSnapshot(true, [{ ...sampleEntry, id: 1 }]);
    render(<DebugLogDrawer />);
    fireEvent.click(screen.getByText('1'));

    const copyBtn = screen.getByText('Copy').closest('button');
    const clearBtn = screen.getByText('Clear').closest('button');
    expect(copyBtn).not.toBeDisabled();
    expect(clearBtn).not.toBeDisabled();
  });

  it('calls clearDebugLog when Clear is clicked', () => {
    setSnapshot(true, [{ ...sampleEntry, id: 1 }]);
    render(<DebugLogDrawer />);
    fireEvent.click(screen.getByText('1'));

    fireEvent.click(screen.getByText('Clear'));
    expect(mockClearLog).toHaveBeenCalledTimes(1);
  });

  it('calls copyText when Copy is clicked', async () => {
    setSnapshot(true, [
      { ...sampleEntry, id: 1, message: 'test log', level: 'info' },
    ]);
    render(<DebugLogDrawer />);
    fireEvent.click(screen.getByText('1'));

    fireEvent.click(screen.getByText('Copy'));
    expect(mockCopyText).toHaveBeenCalledTimes(1);
    const [textArg] = mockCopyText.mock.calls[0];
    expect(textArg).toContain('test log');
    expect(textArg).toContain('INFO');
  });

  // ── Level styling ───────────────────────────────────────────────────────

  it.each([
    ['info', 'text-blue-300'] as const,
    ['success', 'text-emerald-300'] as const,
    ['warn', 'text-amber-300'] as const,
    ['error', 'text-red-300'] as const,
  ])('applies %s level styling to badge', (level, expectedClass) => {
    setSnapshot(true, [{ ...sampleEntry, id: 1, level }]);
    render(<DebugLogDrawer />);
    fireEvent.click(screen.getByText('1'));

    const badge = screen.getByText(level);
    expect(badge.className).toContain(expectedClass);
  });
});
