import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act, fireEvent } from '@testing-library/react';
import Migration from './Migration';

// F34302: the cPanel import endpoint answers `null` for domains, databases,
// errors and domains_added when the backup had none (Go nil slices; a clean
// import has no errors at all). Reading `.length` on null crashed the page
// right after a successful import, hiding the result.

const apiMocks = vi.hoisted(() => ({
  fetchDomains: vi.fn(),
  migrateSite: vi.fn(),
  migrateCPanel: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

const flush = () => act(async () => {});

async function importBackup() {
  render(<Migration />);
  await flush();
  fireEvent.click(screen.getByText(/cPanel Import/));
  const input = document.querySelector('input[type="file"]') as HTMLInputElement;
  const file = new File(['x'], 'cpmove-user.tar.gz');
  fireEvent.change(input, { target: { files: [file] } });
  fireEvent.click(screen.getByText('Import Backup'));
  await flush();
}

describe('cPanel import result with null lists', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.fetchDomains.mockResolvedValue([]);
  });

  it('renders the result when the lists are null', async () => {
    apiMocks.migrateCPanel.mockResolvedValue({
      status: 'imported', user: 'u', domains: null, databases: null,
      ssl_certs: 0, files_count: 12, domains_added: null, errors: null,
    });
    await importBackup();
    console.log('EXPECTED: "Import Complete" rendered');
    console.log('ACTUAL: complete=' + !!screen.queryByText('Import Complete'));
    expect(screen.getByText('Import Complete')).toBeTruthy();
  });

  it('control: populated lists render', async () => {
    apiMocks.migrateCPanel.mockResolvedValue({
      status: 'imported', user: 'u',
      domains: [{ domain: 'a.test', doc_root: '/x', type: 'main', ssl: false }],
      databases: [{ name: 'db1', user: 'u', size_mb: 1.5, imported: true }],
      ssl_certs: 1, files_count: 12, domains_added: ['a.test'], errors: ['warn one'],
    });
    await importBackup();
    expect(screen.getByText('Import Complete')).toBeTruthy();
    expect(screen.getByText(/warn one/)).toBeTruthy();
  });
});
