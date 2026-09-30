import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import FileManager from './FileManager';
import { ConfirmContext } from '@/components/useConfirm';

const fetchFileWorkspaces = vi.fn();
const fetchFiles = vi.fn();
const fetchDiskUsage = vi.fn();

vi.mock('@/lib/api', () => ({
  fetchFileWorkspaces: (...args: unknown[]) => fetchFileWorkspaces(...args),
  fetchFiles: (...args: unknown[]) => fetchFiles(...args),
  fetchDiskUsage: (...args: unknown[]) => fetchDiskUsage(...args),
  readFile: vi.fn(),
  writeFile: vi.fn(),
  deleteFile: vi.fn(),
  createDir: vi.fn(),
  uploadFile: vi.fn(),
  getAuthHeaders: () => ({}),
  BASE: '',
}));

const confirmValue = {
  confirmAction: vi.fn().mockResolvedValue(true),
  promptText: vi.fn().mockResolvedValue(null),
};

function entry(name: string) {
  return { name, path: name, is_dir: false, size: 1, mod_time: '2026-01-01T00:00:00Z', mode: '-rw-r--r--' };
}

const allFiles = Array.from({ length: 60 }, (_, i) => entry(`file-${String(i).padStart(2, '0')}.txt`));

describe('FileManager listing', () => {
  beforeEach(() => {
    fetchFileWorkspaces.mockReset();
    fetchFiles.mockReset();
    fetchDiskUsage.mockReset();
    fetchFileWorkspaces.mockResolvedValue([{ id: 'example.com', label: 'example.com', kind: 'domain', root: '/var/www' }]);
    fetchDiskUsage.mockResolvedValue({ bytes: 10, human: '10 B', root: '/var/www' });
    fetchFiles.mockImplementation(async (_id: string, _path: string, opts?: { limit?: number; offset?: number; q?: string }) => {
      const limit = opts?.limit ?? 50;
      const offset = opts?.offset ?? 0;
      const q = (opts?.q ?? '').trim().toLowerCase();
      const matched = q ? allFiles.filter(file => file.name.toLowerCase().includes(q)) : allFiles;
      return {
        items: matched.slice(offset, offset + limit),
        total: matched.length,
        limit,
        offset,
      };
    });
  });

  it('pages through every file in the directory', async () => {
    const user = userEvent.setup();
    render(
      <ConfirmContext.Provider value={confirmValue}>
        <FileManager />
      </ConfirmContext.Provider>,
    );

    expect(await screen.findByText('file-00.txt')).toBeInTheDocument();
    expect(screen.getByText('file-49.txt')).toBeInTheDocument();
    expect(screen.queryByText('file-50.txt')).not.toBeInTheDocument();
    expect(screen.getByText('1–50 of 60')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Next' }));

    expect(await screen.findByText('file-50.txt')).toBeInTheDocument();
    expect(screen.getByText('file-59.txt')).toBeInTheDocument();
    expect(screen.queryByText('file-00.txt')).not.toBeInTheDocument();
    expect(screen.getByText('51–60 of 60')).toBeInTheDocument();
    await waitFor(() => {
      expect(fetchFiles).toHaveBeenLastCalledWith('example.com', '.', {
        limit: 50,
        offset: 50,
        q: '',
      });
    });
  });
});
