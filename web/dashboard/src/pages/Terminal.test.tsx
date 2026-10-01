import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import TerminalPage from './Terminal';

// ── Mocks ───────────────────────────────────────────────────────────────────

const mockRequestPin = vi.fn();
const mockTerminalWSURL = vi.fn();

vi.mock('@/lib/api', () => ({
  requestPin: (...args: unknown[]) => mockRequestPin(...args),
  terminalWSURL: (...args: unknown[]) => mockTerminalWSURL(...args),
}));

class WSMock {
  static instances: WSMock[] = [];
  url: string;
  sent: string[] = [];
  close = vi.fn();
  onopen: (() => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onerror: (() => void) | null = null;
  onclose: ((e: { code: number; reason: string }) => void) | null = null;

  constructor(url: string) {
    this.url = url;
    WSMock.instances.push(this);
  }

  send(data: string) {
    this.sent.push(data);
  }
}

// Flush the async connect() chain (requestPin → terminalWSURL → socket) via
// microtasks; no timers are involved on this path.
const flush = () => act(async () => {});

describe('Terminal double-connect guard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    WSMock.instances = [];
    mockRequestPin.mockResolvedValue('111111');
    mockTerminalWSURL.mockResolvedValue('ws://127.0.0.1:9443/api/v1/terminal?ticket=tk-1');
    vi.stubGlobal('WebSocket', WSMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  // A second Connect click during the async window replaces wsRef.current,
  // so the first socket can never be closed again — not by the component,
  // not by the unmount cleanup.
  it('closes the superseded socket instead of leaking it', async () => {
    const { unmount } = render(<TerminalPage />);

    fireEvent.click(screen.getByRole('button', { name: /connect/i }));
    await flush(); // first connect resolves: socket #1 exists, wsRef = #1

    fireEvent.click(screen.getByRole('button', { name: /connect/i }));
    await flush(); // second connect resolves: socket #2 exists, wsRef = #2

    expect(WSMock.instances.length).toBe(2);
    act(() => {
      WSMock.instances[1].onopen?.(); // active session opens
    });

    unmount(); // cleanup closes wsRef.current (#2)
    expect(WSMock.instances[1].close).toHaveBeenCalled();
    // The superseded socket #1 must not stay open forever.
    expect(WSMock.instances[0].close).toHaveBeenCalled();
  });

  // The superseded socket's close event arrives asynchronously after the
  // replacement; it must not tear down the new session's state.
  it('ignores onclose from a superseded socket', async () => {
    render(<TerminalPage />);

    fireEvent.click(screen.getByRole('button', { name: /connect/i }));
    await flush();
    fireEvent.click(screen.getByRole('button', { name: /connect/i }));
    await flush();

    act(() => {
      WSMock.instances[1].onopen?.(); // session #2 is the active one
    });
    expect(screen.getByRole('button', { name: /disconnect/i })).toBeInTheDocument();

    act(() => {
      WSMock.instances[0].onclose?.({ code: 1000, reason: '' }); // stale event from #1
    });
    // The active session must survive the stale close event.
    expect(screen.getByRole('button', { name: /disconnect/i })).toBeInTheDocument();
  });
});
