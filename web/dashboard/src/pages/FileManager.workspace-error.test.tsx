import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import FileManager from './FileManager';
import { ConfirmContext } from '@/components/useConfirm';

// F54601: a failed workspace list load was swallowed, so the page claimed
// "No domain or application workspaces are available."

const fetchFileWorkspaces = vi.fn();

vi.mock('@/lib/api', () => ({
  fetchFileWorkspaces: (...a: unknown[]) => fetchFileWorkspaces(...a),
  fetchFiles: vi.fn().mockResolvedValue({ files: [], total: 0 }),
  fetchDiskUsage: vi.fn().mockResolvedValue(null),
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

const flush = () => act(async () => {});

function renderPage() {
  return render(
    <ConfirmContext.Provider value={confirmValue}>
      <FileManager />
    </ConfirmContext.Provider>,
  );
}

describe('FileManager workspace load failure', () => {
  beforeEach(() => { fetchFileWorkspaces.mockReset(); });

  it('control: an empty list says there are no workspaces', async () => {
    fetchFileWorkspaces.mockResolvedValue([]);
    renderPage();
    await flush();
    expect(screen.getByText(/No domain or application workspaces are available/)).toBeInTheDocument();
  });

  it('a failed load is reported instead of claiming there are no workspaces', async () => {
    fetchFileWorkspaces.mockRejectedValue(new Error("workspaces: boom"));
    renderPage();
    await flush();
    expect(screen.getByText(/workspaces: boom/)).toBeInTheDocument();
    expect(screen.queryByText(/No domain or application workspaces are available/)).toBeNull();
  });
});
