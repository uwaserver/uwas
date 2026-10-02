import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import FileManager from './FileManager';
import { ConfirmContext } from '@/components/useConfirm';

const fetchFileWorkspaces = vi.fn();
const fetchFiles = vi.fn();
const fetchDiskUsage = vi.fn();
const uploadFile = vi.fn();

vi.mock('@/lib/api', () => ({
  fetchFileWorkspaces: (...args: unknown[]) => fetchFileWorkspaces(...args),
  fetchFiles: (...args: unknown[]) => fetchFiles(...args),
  fetchDiskUsage: (...args: unknown[]) => fetchDiskUsage(...args),
  readFile: vi.fn(),
  writeFile: vi.fn(),
  deleteFile: vi.fn(),
  createDir: vi.fn(),
  uploadFile: (...args: unknown[]) => uploadFile(...args),
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

// Regression (round 23): FileManager's upload collision check must cover the
// WHOLE directory, not just the visible page. The server (filemanager
// SaveUpload) opens uploads with O_TRUNC — silent overwrite — so a same-named
// file that lives beyond the visible 50-row page would be destroyed without
// the promised confirmation dialog.
describe('FileManager upload warns on collisions outside the visible page', () => {
  beforeEach(() => {
    fetchFileWorkspaces.mockReset();
    fetchFiles.mockReset();
    fetchDiskUsage.mockReset();
    uploadFile.mockReset();
    confirmValue.confirmAction.mockClear();
    confirmValue.promptText.mockClear();
    fetchFileWorkspaces.mockResolvedValue([{ id: 'example.com', label: 'example.com', kind: 'domain', root: '/var/www' }]);
    fetchDiskUsage.mockResolvedValue({ bytes: 10, human: '10 B', root: '/var/www' });
    // 61 entries: the visible page 1 holds file-00..file-49; collision.txt
    // lives on page 2 (never visible during these tests).
    fetchFiles.mockImplementation(async (_id: string, _path: string, opts?: { limit?: number; offset?: number; q?: string }) => {
      const limit = opts?.limit ?? 50;
      const offset = opts?.offset ?? 0;
      const q = (opts?.q ?? '').trim().toLowerCase();
      const listing = [...allFiles, entry('collision.txt')];
      const matched = q ? listing.filter(file => file.name.toLowerCase().includes(q)) : listing;
      return { items: matched.slice(offset, offset + limit), total: matched.length, limit, offset };
    });
    uploadFile.mockResolvedValue({});
  });

  it('warns before overwriting a file that exists beyond the visible page', async () => {
    render(
      <ConfirmContext.Provider value={confirmValue}>
        <FileManager />
      </ConfirmContext.Provider>,
    );
    expect(await screen.findByText('file-00.txt')).toBeInTheDocument();
    expect(screen.queryByText('collision.txt')).not.toBeInTheDocument();

    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    await userEvent.upload(input, new File(['hello'], 'collision.txt', { type: 'text/plain' }));

    await waitFor(() => {
      expect(confirmValue.confirmAction).toHaveBeenCalledWith(
        expect.objectContaining({ title: expect.stringContaining('collision.txt') }),
      );
    });
  });

  it('uploads a fresh name without an overwrite dialog', async () => {
    render(
      <ConfirmContext.Provider value={confirmValue}>
        <FileManager />
      </ConfirmContext.Provider>,
    );
    expect(await screen.findByText('file-00.txt')).toBeInTheDocument();

    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    await userEvent.upload(input, new File(['x'], 'brand-new.txt', { type: 'text/plain' }));

    await waitFor(() => expect(uploadFile).toHaveBeenCalled());
    expect(confirmValue.confirmAction).not.toHaveBeenCalled();
  });
});

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
