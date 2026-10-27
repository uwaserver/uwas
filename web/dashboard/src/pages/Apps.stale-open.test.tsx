import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import Apps from './Apps';
import { ConfirmContext } from '@/components/useConfirm';

// F34303: openDeploy / openEdit applied whatever fetchApp answered last, not
// what was opened last. A slow answer for app A landing after the operator
// opened app B put A's git URL and webhook secret into B's form, and
// "Save webhook config" then wrote them onto B.

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

const appA = { name: 'a', runtime: 'node', command: 'node a.js', port: 3001, deploy: { git_url: 'https://example.com/a.git', webhook_secret: 'secret-a' } };
const appB = { name: 'b', runtime: 'node', command: 'node b.js', port: 3002, deploy: { git_url: 'https://example.com/b.git', webhook_secret: 'secret-b' } };

const flush = () => act(async () => {});

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>(r => { resolve = r; });
  return { promise, resolve };
}

function renderPage() {
  return render(
    <ConfirmContext.Provider value={confirmValue}>
      <Apps />
    </ConfirmContext.Provider>,
  );
}

function cardButton(app: string, label: string) {
  const heading = screen.getByText(app, { selector: 'h3, h2, span, p, div' });
  void heading;
  const idx = app === 'a' ? 0 : 1;
  return screen.getAllByRole('button', { name: label })[idx];
}

describe('opening a second app while the first answer is still in flight', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => {
      if (vi.isMockFunction(fn)) fn.mockReset();
    });
    apiMocks.fetchApps.mockResolvedValue([appA, appB]);
    apiMocks.fetchAppDeployPreflight.mockResolvedValue(null);
    apiMocks.fetchAppWebhookStatus.mockResolvedValue(null);
  });

  it('keeps the Deploy form of the app opened last', async () => {
    const slowA = deferred<{ app: typeof appA }>();
    apiMocks.fetchApp.mockImplementation((name: string) =>
      name === 'a' ? slowA.promise : Promise.resolve({ app: appB }));
    renderPage();
    await flush();

    fireEvent.click(cardButton('a', 'Deploy'));
    await flush();
    // close the modal while A is still loading, then open B
    const heading = screen.getByRole('heading', { name: /Deploy a/ });
    fireEvent.click(heading.parentElement!.querySelector('button')!);
    fireEvent.click(cardButton('b', 'Deploy'));
    await flush();

    await act(async () => { slowA.resolve({ app: appA }); });
    const input = screen.getByPlaceholderText('https://github.com/user/repo.git') as HTMLInputElement;
    console.log('EXPECTED: ' + appB.deploy.git_url);
    console.log('ACTUAL: ' + input.value);
    expect(input.value).toBe(appB.deploy.git_url);
  });

  it('keeps the Edit form of the app opened last', async () => {
    const slowA = deferred<{ app: typeof appA }>();
    apiMocks.fetchApp.mockImplementation((name: string) =>
      name === 'a' ? slowA.promise : Promise.resolve({ app: appB }));
    renderPage();
    await flush();

    fireEvent.click(cardButton('a', 'Edit'));
    fireEvent.click(cardButton('b', 'Edit'));
    await flush();
    await act(async () => { slowA.resolve({ app: appA }); });

    const values = Array.from(document.querySelectorAll('input')).map(i => (i as HTMLInputElement).value);
    console.log('EXPECTED: form shows app b');
    console.log('ACTUAL: inputs=' + JSON.stringify(values));
    expect(values).toContain('b');
    expect(values).not.toContain('a');
  });

  it('control: a single open fills the form', async () => {
    apiMocks.fetchApp.mockResolvedValue({ app: appA });
    renderPage();
    await flush();
    fireEvent.click(cardButton('a', 'Deploy'));
    await flush();
    const input = screen.getByPlaceholderText('https://github.com/user/repo.git') as HTMLInputElement;
    expect(input.value).toBe(appA.deploy.git_url);
  });
});
