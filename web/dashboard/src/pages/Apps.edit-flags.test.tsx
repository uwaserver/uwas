import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import Apps from './Apps';
import { ConfirmContext } from '@/components/useConfirm';

// ── Mocks ───────────────────────────────────────────────────────────────────
// One vi.fn per value imported by Apps.tsx; mount-time fetchers get
// resolved defaults in beforeEach, the rest stay inert unless invoked.
const apiMocks = vi.hoisted(() => ({
  fetchApps: vi.fn(),
  fetchApp: vi.fn(),
  createApp: vi.fn(),
  updateApp: vi.fn(),
  deleteApp: vi.fn(),
  startApp: vi.fn(),
  stopApp: vi.fn(),
  restartApp: vi.fn(),
  fetchAppLogs: vi.fn(),
  deployApp: vi.fn(),
  deployAppLive: vi.fn(),
  fetchAppStats: vi.fn(),
  fetchAppDeployPreflight: vi.fn(),
  generateAppDeployKey: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

const confirmValue = {
  confirmAction: vi.fn().mockResolvedValue(true),
  promptText: vi.fn().mockResolvedValue(null),
};

function renderPage() {
  return render(
    <ConfirmContext.Provider value={confirmValue}>
      <Apps />
    </ConfirmContext.Provider>,
  );
}

// Flush mount effects + in-flight promise chains without advancing the clock.
const flush = () => act(async () => {});

// An app the operator configured outside the dashboard: auto-restart on and
// the app disabled. The Apps UI has no controls for either flag, so the edit
// save must round-trip them — the apps PUT handler assigns both fields
// unconditionally from the patch body (internal/admin/apps/handler.go), so a
// body that omits them silently re-enables the app and drops its
// crash-restart protection.
const APP = {
  name: 'myapp',
  runtime: 'node',
  command: 'node server.js',
  port: 3000,
  env: { FOO: 'bar' },
  auto_restart: true,
  disabled: true,
};

describe('Apps edit save round-trips auto_restart and disabled', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => {
      if (vi.isMockFunction(fn)) fn.mockReset();
    });
    apiMocks.fetchApps.mockResolvedValue([APP]);
    apiMocks.fetchApp.mockResolvedValue({ app: APP });
    apiMocks.updateApp.mockResolvedValue({ app: APP, started: false });
    apiMocks.createApp.mockResolvedValue({ app: APP, started: false });
  });

  async function openEditAndSave() {
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    await flush(); // openEdit fetches the app and prefills the form

    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await flush();
  }

  // Regression: the handler does `existing.AutoRestart = patch.AutoRestart;
  // existing.Disabled = patch.Disabled` with no zero-value gate, so a body
  // without the two fields resets them to false — re-enabling a disabled
  // app (which then gets started) and dropping auto-restart.
  it('round-trips auto_restart and disabled on edit save', async () => {
    renderPage();
    await flush();
    await openEditAndSave();

    expect(apiMocks.updateApp).toHaveBeenCalledTimes(1);
    const [name, body] = apiMocks.updateApp.mock.calls[0] as [string, Record<string, any>];
    expect(name).toBe('myapp');
    expect(body.auto_restart).toBe(true);
    expect(body.disabled).toBe(true);
  });

  // Control: the form-managed operational fields follow the prefill —
  // proves the flow reaches updateApp with a real body.
  it('carries the form-managed fields on edit save', async () => {
    renderPage();
    await flush();
    await openEditAndSave();

    const body = apiMocks.updateApp.mock.calls[0][1] as Record<string, any>;
    expect(body.name).toBe('myapp');
    expect(body.command).toBe('node server.js');
    expect(body.port).toBe(3000);
  });
});
