import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import ConfigEditor from '@/pages/ConfigEditor';
import { ConfirmContext } from '@/components/useConfirm';

// ── API boundary mock (only the boundary — the real component runs) ─────────
const apiMocks = vi.hoisted(() => ({
  fetchConfigRaw: vi.fn(),
  saveConfigRaw: vi.fn(),
  fetchDomainConfigRaw: vi.fn(),
  saveDomainConfigRaw: vi.fn(),
  fetchDomains: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

type Deferred<T> = {
  promise: Promise<T>;
  resolve: (v: T) => void;
  reject: (e: unknown) => void;
};
function makeDeferred<T>(): Deferred<T> {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

const confirmStub = {
  confirmAction: vi.fn(async () => true),
  promptText: vi.fn(async () => null),
};

function renderEditor() {
  return render(
    <ConfirmContext.Provider value={confirmStub}>
      <ConfigEditor />
    </ConfirmContext.Provider>,
  );
}

// Flush mount effects + in-flight promise chains.
const flush = () => act(async () => {});
const textarea = () =>
  screen.getByPlaceholderText('# YAML configuration...') as HTMLTextAreaElement;

const DOMAINS = [
  { id: 'd1', host: 'a.example', type: 'static', ssl: 'off' },
  { id: 'd2', host: 'b.example', type: 'static', ssl: 'off' },
];

describe('ConfigEditor: superseded file-switch load must not commit state', () => {
  let pending: Record<string, Deferred<{ content: string }>>;

  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => {
      if (vi.isMockFunction(fn)) fn.mockReset();
    });
    confirmStub.confirmAction.mockClear();
    pending = {};
    apiMocks.fetchDomains.mockResolvedValue(DOMAINS);
    apiMocks.fetchConfigRaw.mockResolvedValue({ content: 'MAIN-CONTENT\n' });
    // Every domain-config fetch is hand-controlled so the test fixes the
    // response ORDER (the thing the production code must be robust to).
    apiMocks.fetchDomainConfigRaw.mockImplementation((host: string) => {
      pending[host] = makeDeferred<{ content: string }>();
      return pending[host].promise;
    });
  });

  it('shows the selected file after a slower superseded response lands last', async () => {
    renderEditor();
    await flush();
    expect(textarea().value).toBe('MAIN-CONTENT\n'); // baseline sane

    // Select a.example — its fetch hangs (slow server/link).
    fireEvent.click(screen.getByRole('button', { name: 'a.example' }));
    await flush();
    // While a.example is still in flight, select b.example — fast response.
    fireEvent.click(screen.getByRole('button', { name: 'b.example' }));
    await flush();

    await act(async () => {
      pending['b.example'].resolve({ content: 'B-CONTENT\n' });
    });
    await flush();

    await act(async () => {
      pending['a.example'].resolve({ content: 'A-CONTENT\n' });
    });
    await flush();

    // The editor must reflect the SELECTED file (b.example), not whichever
    // stale response happened to land last.
    expect(textarea().value).toBe('B-CONTENT\n');
  });

  it('persists the selected file when saving after the overlap settles', async () => {
    renderEditor();
    await flush();

    fireEvent.click(screen.getByRole('button', { name: 'a.example' }));
    await flush();
    fireEvent.click(screen.getByRole('button', { name: 'b.example' }));
    await flush();
    await act(async () => {
      pending['b.example'].resolve({ content: 'B-CONTENT\n' });
    });
    await act(async () => {
      pending['a.example'].resolve({ content: 'A-CONTENT\n' });
    });
    await flush();

    // Edit relative to whatever is displayed, then save.
    fireEvent.change(textarea(), {
      target: { value: textarea().value + '# tuned\n' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await flush();

    // The write must target b.example with b.example's content — never the
    // superseded file's YAML under b.example's host.
    expect(apiMocks.saveDomainConfigRaw).toHaveBeenCalledWith(
      'b.example',
      'B-CONTENT\n# tuned\n',
    );
  });

  it('ignores an error from a superseded load; newest selection stays intact', async () => {
    renderEditor();
    await flush();

    fireEvent.click(screen.getByRole('button', { name: 'a.example' }));
    await flush();
    fireEvent.click(screen.getByRole('button', { name: 'b.example' }));
    await flush();
    await act(async () => {
      pending['b.example'].resolve({ content: 'B-CONTENT\n' });
    });
    await flush();

    await act(async () => {
      pending['a.example'].reject(new Error('a.example load failed'));
    });
    await flush();

    // The stale failure must neither blank the editor nor surface as an
    // error for the file the user is actually looking at.
    expect(textarea().value).toBe('B-CONTENT\n');
    expect(screen.queryByText(/a\.example load failed/)).toBeNull();
  });

  it('control: sequential switches show each file without any overlap', async () => {
    renderEditor();
    await flush();

    fireEvent.click(screen.getByRole('button', { name: 'a.example' }));
    await flush();
    await act(async () => {
      pending['a.example'].resolve({ content: 'A-CONTENT\n' });
    });
    await flush();
    expect(textarea().value).toBe('A-CONTENT\n');

    fireEvent.click(screen.getByRole('button', { name: 'b.example' }));
    await flush();
    await act(async () => {
      pending['b.example'].resolve({ content: 'B-CONTENT\n' });
    });
    await flush();
    expect(textarea().value).toBe('B-CONTENT\n');
  });
});
