import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import PHPConfig from './PHPConfig';
import { ConfirmContext } from '@/components/useConfirm';

// F45601: a failed php.ini / settings load was swallowed and rendered as an
// empty file / empty settings. Typing into that "empty" php.ini editor and
// saving replaced the real php.ini with the typed text, and a failed version
// list left a blank page with no explanation.

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

function renderPage() {
  return render(
    <ConfirmContext.Provider value={confirmStub}>
      <PHPConfig />
    </ConfirmContext.Provider>,
  );
}

describe('PHPConfig load failures', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => { if (vi.isMockFunction(fn)) fn.mockReset(); });
    apiMocks.fetchPHP.mockResolvedValue([{ version: '8.3.2', sapi: 'fpm', disabled: false }]);
    apiMocks.fetchPHPConfig.mockResolvedValue({ memory_limit: '512M' });
    apiMocks.fetchPHPConfigRaw.mockResolvedValue({ content: 'memory_limit = 512M\n' });
    apiMocks.savePHPConfigRaw.mockResolvedValue({ status: 'ok' });
  });

  it('control: a successful load shows the raw php.ini and allows saving edits', async () => {
    renderPage();
    await flush();
    fireEvent.click(screen.getByText('Raw php.ini'));
    await flush(); // the tab switch awaits the discard confirmation
    const ta = document.querySelector('textarea') as HTMLTextAreaElement;
    expect(ta.value).toBe('memory_limit = 512M\n');
    expect(ta.readOnly).toBe(false);
    fireEvent.change(ta, { target: { value: 'memory_limit = 1G\n' } });
    fireEvent.click(screen.getByRole('button', { name: /Save php.ini/ }));
    await flush();
    expect(apiMocks.savePHPConfigRaw).toHaveBeenCalledWith('8.3.2', 'memory_limit = 1G\n');
  });

  it('a failed php.ini load is reported and the editor cannot overwrite the real file', async () => {
    apiMocks.fetchPHPConfigRaw.mockRejectedValue(new Error('read php.ini: permission denied'));
    renderPage();
    await flush();
    expect(await screen.findByText(/read php\.ini: permission denied/)).toBeInTheDocument();
    fireEvent.click(screen.getByText('Raw php.ini'));
    await flush(); // the tab switch awaits the discard confirmation
    const ta = document.querySelector('textarea') as HTMLTextAreaElement;
    expect(ta.readOnly).toBe(true);
    fireEvent.change(ta, { target: { value: 'memory_limit = 1G\n' } });
    const save = screen.getByRole('button', { name: /Save php.ini/ }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    fireEvent.click(save);
    await flush();
    expect(apiMocks.savePHPConfigRaw).not.toHaveBeenCalled();
  });

  it('a failed version list is reported instead of a blank page, and Retry reloads it', async () => {
    apiMocks.fetchPHP.mockRejectedValueOnce(new Error('php manager unavailable'));
    renderPage();
    await flush();
    expect(await screen.findByText(/php manager unavailable/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await flush();
    expect(apiMocks.fetchPHP).toHaveBeenCalledTimes(2);
    expect(screen.queryByText(/php manager unavailable/)).not.toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'PHP 8.3.2' })).toBeInTheDocument();
  });
});
