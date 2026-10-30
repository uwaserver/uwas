import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import Apps from './Apps';
import { ConfirmContext } from '@/components/useConfirm';

// F46307: the deploy preflight endpoint returns checks shaped
// {name, label, ok, detail?, required?} (internal/admin/deploy
// AppPreflightCheck). The Deploy dialog read `check.message`, a field the
// server never sends, so the reason a check failed ("git not found in PATH",
// "<dir> not found (will be created)") never reached the operator, and it
// showed the machine name ("port") instead of the label ("Port 3001").

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
  fetchAppWebhookStatus: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

const confirmValue = {
  confirmAction: vi.fn().mockResolvedValue(true),
  promptText: vi.fn().mockResolvedValue(null),
};

const app = { name: 'a', runtime: 'node', command: 'node a.js', port: 3001, deploy: { git_url: 'https://example.com/a.git' } };

const flush = () => act(async () => {});

async function openDeploy(preflight: unknown) {
  apiMocks.fetchApps.mockResolvedValue([app]);
  apiMocks.fetchApp.mockResolvedValue({ app });
  apiMocks.fetchAppDeployPreflight.mockResolvedValue(preflight);
  apiMocks.fetchAppWebhookStatus.mockResolvedValue(null);
  render(
    <ConfirmContext.Provider value={confirmValue}>
      <Apps />
    </ConfirmContext.Provider>,
  );
  await flush();
  fireEvent.click(screen.getByRole('button', { name: 'Deploy' }));
  await flush();
}

describe('Deploy dialog preflight list', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => {
      if (vi.isMockFunction(fn)) fn.mockReset();
    });
  });

  it('shows the label and the detail the server sends', async () => {
    await openDeploy({
      ok: false,
      checks: [
        { name: 'port', label: 'Port 3001', ok: true },
        { name: 'git', label: 'Git', ok: false, detail: 'git not found in PATH', required: true },
      ],
    });
    const body = document.body.textContent ?? '';
    console.log('EXPECTED: "Port 3001", "Git" and "git not found in PATH" are shown\nACTUAL: ' + body.slice(body.indexOf('Port'), body.indexOf('Port') + 80));
    expect(body).toContain('Port 3001');
    expect(body).toContain('git not found in PATH');
  });

  // Edge: a check without detail shows just its label.
  it('shows a check that has no detail', async () => {
    await openDeploy({ ok: true, checks: [{ name: 'port', label: 'Port 3001', ok: true }] });
    expect(document.body.textContent).toContain('Port 3001');
  });

  // Control: the legacy shape (name + message) keeps rendering.
  it('control: a check carrying only name and message still renders', async () => {
    await openDeploy({ ok: true, checks: [{ name: 'node', ok: false, required: false, message: 'node missing' }] });
    expect(document.body.textContent).toContain('node missing');
  });
});
