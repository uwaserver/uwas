import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act, fireEvent } from '@testing-library/react';
import Migration from './Migration';

// F43603: the cPanel import has loaded the dumps since F42003 and reports a
// per-database `imported` flag, but the result page only said "Databases
// found" with a count, so the operator could not tell whether a database was
// loaded, skipped (it already had tables) or failed.

const apiMocks = vi.hoisted(() => ({
  fetchDomains: vi.fn(),
  migrateSite: vi.fn(),
  migrateCPanel: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

const flush = () => act(async () => {});

async function importBackup(untickImport = false) {
  render(<Migration />);
  await flush();
  fireEvent.click(screen.getByText(/cPanel Import/));
  if (untickImport) {
    fireEvent.click(screen.getByLabelText(/Import databases/));
  }
  const input = document.querySelector('input[type="file"]') as HTMLInputElement;
  fireEvent.change(input, { target: { files: [new File(['x'], 'cpmove-u.tar.gz')] } });
  fireEvent.click(screen.getByText('Import Backup'));
  await flush();
}

const result = {
  status: 'imported', user: 'u',
  domains: [{ domain: 'a.test', doc_root: 'public_html', type: 'main', ssl: false }],
  databases: [
    { name: 'u_loaded', user: 'u', size_mb: 1, imported: true },
    { name: 'u_skipped', user: 'u', size_mb: 2, imported: false },
  ],
  ssl_certs: 0, files_count: 3, domains_added: ['a.test'], errors: ['database u_skipped not imported: it already has tables'],
};

describe('cPanel import database status', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.fetchDomains.mockResolvedValue([]);
    apiMocks.migrateCPanel.mockResolvedValue(result);
  });

  it('marks each database imported or not imported when import was requested', async () => {
    await importBackup();
    console.log('EXPECTED: "imported" and "not imported, see warnings" badges');
    console.log('ACTUAL: imported=' + !!screen.queryByText('imported') + ' skipped=' + !!screen.queryByText('not imported, see warnings'));
    expect(screen.getByText('imported')).toBeTruthy();
    expect(screen.getByText('not imported, see warnings')).toBeTruthy();
  });

  it('control: without the import option, databases are plainly not imported', async () => {
    apiMocks.migrateCPanel.mockResolvedValue({
      ...result, databases: [{ name: 'u_loaded', user: 'u', size_mb: 1, imported: false }], errors: [],
    });
    await importBackup(true);
    expect(apiMocks.migrateCPanel).toHaveBeenCalledWith(expect.anything(), false);
    expect(screen.getByText('not imported')).toBeTruthy();
    expect(screen.queryByText('not imported, see warnings')).toBeNull();
  });
});
