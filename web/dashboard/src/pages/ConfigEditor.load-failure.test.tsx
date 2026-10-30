import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import ConfigEditor from '@/pages/ConfigEditor';
import { ConfirmContext } from '@/components/useConfirm';

// F45602: a failed load blanked the editor but left it editable. Typing into
// the "empty" editor and pressing Save replaced the real configuration file
// with the typed text (the server validates the YAML, but a small valid
// document is accepted). While the load has failed the editor must be
// read-only and Save disabled until a Reload succeeds.

const apiMocks = vi.hoisted(() => ({
  fetchConfigRaw: vi.fn(),
  saveConfigRaw: vi.fn(),
  fetchDomainConfigRaw: vi.fn(),
  saveDomainConfigRaw: vi.fn(),
  fetchDomains: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

const confirmStub = {
  confirmAction: vi.fn(async () => true),
  promptText: vi.fn(async () => null),
};

const flush = () => act(async () => {});

function renderEditor() {
  return render(
    <ConfirmContext.Provider value={confirmStub}>
      <ConfigEditor />
    </ConfirmContext.Provider>,
  );
}

describe('ConfigEditor after a failed load', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => fn.mockReset());
    apiMocks.fetchDomains.mockResolvedValue([]);
    apiMocks.saveConfigRaw.mockResolvedValue({ status: 'ok' });
  });

  it('control: a successful load is editable and saves the edit', async () => {
    apiMocks.fetchConfigRaw.mockResolvedValue({ content: 'global:\n  log_level: info\n' });
    renderEditor();
    await flush();
    const ta = document.querySelector('textarea') as HTMLTextAreaElement;
    expect(ta.value).toContain('log_level: info');
    expect(ta.readOnly).toBe(false);
    fireEvent.change(ta, { target: { value: 'global:\n  log_level: debug\n' } });
    fireEvent.click(screen.getByRole('button', { name: /Save/ }));
    await flush();
    expect(apiMocks.saveConfigRaw).toHaveBeenCalledWith('global:\n  log_level: debug\n');
  });

  it('a failed load shows the error and cannot overwrite the real file', async () => {
    apiMocks.fetchConfigRaw.mockRejectedValue(new Error('read config: permission denied'));
    renderEditor();
    await flush();
    expect(await screen.findByText(/read config: permission denied/)).toBeInTheDocument();
    const ta = document.querySelector('textarea') as HTMLTextAreaElement;
    expect(ta.readOnly).toBe(true);
    fireEvent.change(ta, { target: { value: 'global:\n  log_level: debug\n' } });
    const save = screen.getByRole('button', { name: /Save/ }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    fireEvent.click(save);
    await flush();
    expect(apiMocks.saveConfigRaw).not.toHaveBeenCalled();
  });

  it('Reload after a failure restores an editable editor', async () => {
    apiMocks.fetchConfigRaw
      .mockRejectedValueOnce(new Error('read config: permission denied'))
      .mockResolvedValue({ content: 'global:\n  log_level: info\n' });
    renderEditor();
    await flush();
    fireEvent.click(screen.getByRole('button', { name: /Reload/ }));
    await flush();
    const ta = document.querySelector('textarea') as HTMLTextAreaElement;
    expect(ta.value).toContain('log_level: info');
    expect(ta.readOnly).toBe(false);
  });
});
