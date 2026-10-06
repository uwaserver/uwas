import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import PHPConfig from './PHPConfig';
import { ConfirmContext } from '@/components/useConfirm';

// ── API boundary mock (only the boundary — the real component runs) ─────────
const apiMocks = vi.hoisted(() => ({
  fetchPHP: vi.fn(),
  fetchPHPConfig: vi.fn(),
  updatePHPConfigKey: vi.fn(),
  fetchPHPConfigRaw: vi.fn(),
  savePHPConfigRaw: vi.fn(),
  restartPHP: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

const confirmStub = {
  confirmAction: vi.fn(async () => true),
  promptText: vi.fn(async () => null),
};

const flush = () => act(async () => {});

const PHP_VERSIONS = [
  { version: '8.3.2', sapi: 'fpm', disabled: false },
  { version: '8.2.1', sapi: 'fpm', disabled: false },
];

const CFG_83 = { memory_limit: '512M' };
const CFG_82 = { memory_limit: '256M' };

function renderPage() {
  return render(
    <ConfirmContext.Provider value={confirmStub}>
      <PHPConfig />
    </ConfirmContext.Provider>,
  );
}

describe('PHPConfig: a superseded version\u2019s config must not commit over the selected one', () => {
  let gates: Array<{ ver: string; kind: 'cfg' | 'raw'; resolve: (v: unknown) => void; reject: (e: unknown) => void }>;

  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => {
      if (vi.isMockFunction(fn)) fn.mockReset();
    });
    confirmStub.confirmAction.mockClear();
    gates = [];
    apiMocks.fetchPHP.mockResolvedValue(PHP_VERSIONS);
    apiMocks.fetchPHPConfig.mockImplementation((ver: string) => {
      const p = new Promise((resolve, reject) => { gates.push({ ver, kind: 'cfg', resolve, reject }); });
      return p;
    });
    apiMocks.fetchPHPConfigRaw.mockImplementation((ver: string) => {
      const p = new Promise((resolve, reject) => { gates.push({ ver, kind: 'raw', resolve, reject }); });
      return p;
    });
  });

  function settle(ver: string, data: { cfg?: unknown; raw?: string } | { error: Error }) {
    return act(async () => {
      for (const g of gates.filter(x => x.ver === ver)) {
        if ('error' in data) g.reject(data.error);
        else if (g.kind === 'cfg') g.resolve(data.cfg);
        else g.resolve({ content: data.raw });
      }
    });
  }

  it('control: mounting and resolving one version shows its values', async () => {
    renderPage();
    await flush(); // versions load, selectedVer = 8.3.2, fetches parked
    await settle('8.3.2', { cfg: CFG_83, raw: 'INI-8.3' });

    expect(screen.getByDisplayValue('512M')).toBeTruthy();
  });

  it('keeps 8.2.1\u2019s values when 8.3.2\u2019s slower fetches land last', async () => {
    renderPage();
    await flush(); // selectedVer = 8.3.2; cfg+raw for 8.3.2 parked

    // Switch to 8.2.1 while 8.3.2's fetches are still in flight.
    fireEvent.change(screen.getByRole('combobox'), { target: { value: '8.2.1' } });
    await flush();
    expect(gates.filter(g => g.ver === '8.2.1').length).toBe(2);

    // 8.2.1's fetches resolve first — its values are correct and current.
    await settle('8.2.1', { cfg: CFG_82, raw: 'INI-8.2' });
    expect(screen.getByDisplayValue('256M')).toBeTruthy();

    // 8.3.2's stale fetches land LAST and must be discarded.
    await settle('8.3.2', { cfg: CFG_83, raw: 'INI-8.3' });

    // RED pre-fix: the form shows 8.3.2's memory_limit (512M) and the raw
    // editor carries 8.3.2's ini while PHP 8.2.1 is the selected version —
    // saving either tab writes 8.3's configuration into 8.2.
    expect(screen.getByDisplayValue('256M')).toBeTruthy();
    expect(screen.queryByDisplayValue('512M')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: /Raw php.ini/ }));
    await flush();
    expect(screen.getByDisplayValue('INI-8.2')).toBeTruthy();
    expect(screen.queryByDisplayValue('INI-8.3')).toBeNull();
  });

  it('a stale failure does not blank the selected version\u2019s settings', async () => {
    renderPage();
    await flush();

    fireEvent.change(screen.getByRole('combobox'), { target: { value: '8.2.1' } });
    await flush();
    await settle('8.2.1', { cfg: CFG_82, raw: 'INI-8.2' });

    await settle('8.3.2', { error: new Error('8.3 config read failed') });

    // RED pre-fix: the stale rejection ran setFormValues({}) /
    // setSavedValues({}) — blanking every field under PHP 8.2.1.
    expect(screen.getByDisplayValue('256M')).toBeTruthy();
  });
});
