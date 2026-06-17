import { useState, useEffect, useCallback, useMemo } from 'react';
import {
  FolderOpen,
  File,
  Folder,
  ChevronRight,
  Trash2,
  Plus,
  Save,
  X,
  RefreshCw,
  HardDrive,
  Upload,
  ArrowLeft,
  Home,
  Search,
} from 'lucide-react';
import {
  fetchFileWorkspaces,
  fetchFiles,
  readFile,
  writeFile,
  deleteFile,
  createDir,
  uploadFile,
  fetchDiskUsage,
  getToken,
  type FileEntry,
  type FileWorkspace,
} from '@/lib/api';
import { useConfirm } from '@/components/useConfirm';

function formatSize(bytes: number): string {
  if (bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`;
}

function formatDate(dateStr: string): string {
  if (!dateStr) return '--';
  try {
    return new Date(dateStr).toLocaleString('en-US', {
      month: 'short',
      day: 'numeric',
      year: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    });
  } catch {
    return dateStr;
  }
}

export default function FileManager() {
  const { confirmAction } = useConfirm();
  const [workspaces, setWorkspaces] = useState<FileWorkspace[]>([]);
  const [selectedWorkspaceID, setSelectedWorkspaceID] = useState('');
  const [files, setFiles] = useState<FileEntry[]>([]);
  const [fileFilter, setFileFilter] = useState('');
  const [currentPath, setCurrentPath] = useState('.');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [diskUsage, setDiskUsage] = useState<{ bytes: number; human: string; root: string } | null>(null);

  // Editor state
  const [editingFile, setEditingFile] = useState<string | null>(null);
  const [editContent, setEditContent] = useState('');
  const [saving, setSaving] = useState(false);
  const [editDirty, setEditDirty] = useState(false);
  const [saveState, setSaveState] = useState<'idle' | 'saved' | 'error'>('idle');

  // Delete confirmation
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);

  // Create folder
  const [showNewFolder, setShowNewFolder] = useState(false);
  const [newFolderName, setNewFolderName] = useState('');
  const [creatingDir, setCreatingDir] = useState(false);

  // Upload
  const [uploading, setUploading] = useState(false);

  useEffect(() => {
    fetchFileWorkspaces()
      .then(items => {
        const next = items ?? [];
        setWorkspaces(next);
        setSelectedWorkspaceID(current => current || next[0]?.id || '');
      })
      .catch(() => {});
  }, []);

  const selectedWorkspace = useMemo(
    () => workspaces.find(w => w.id === selectedWorkspaceID) ?? null,
    [selectedWorkspaceID, workspaces],
  );
  const domainWorkspaces = useMemo(() => workspaces.filter(w => w.kind === 'domain'), [workspaces]);
  const applicationWorkspaces = useMemo(() => workspaces.filter(w => w.kind === 'application'), [workspaces]);

  const loadFiles = useCallback(async () => {
    if (!selectedWorkspaceID) return;
    setLoading(true);
    setError('');
    // Disk usage is a sidebar metric — when its endpoint fails (e.g.
    // permission denied for the underlying du command) we still want to
    // render the file list. Use allSettled so one failure doesn't blank
    // the whole page.
    const [filesRes, duRes] = await Promise.allSettled([
      fetchFiles(selectedWorkspaceID, currentPath),
      fetchDiskUsage(selectedWorkspaceID),
    ]);
    if (filesRes.status === 'fulfilled') {
      const sorted = (filesRes.value ?? []).sort((a, b) => {
        if (a.is_dir && !b.is_dir) return -1;
        if (!a.is_dir && b.is_dir) return 1;
        return a.name.localeCompare(b.name);
      });
      setFiles(sorted);
    } else {
      setError((filesRes.reason as Error).message);
      setFiles([]);
    }
    setDiskUsage(duRes.status === 'fulfilled' ? duRes.value : null);
    setLoading(false);
  }, [selectedWorkspaceID, currentPath]);

  // Helper: confirm before discarding unsaved edits.
  const confirmDiscardEdits = async (): Promise<boolean> => {
    if (!editDirty) return true;
    return confirmAction({
      title: 'Discard unsaved changes?',
      message: 'You have unsaved changes. They will be lost if you continue.',
      confirmLabel: 'Discard',
      variant: 'warning',
    });
  };

  useEffect(() => {
    loadFiles();
  }, [loadFiles]);

  const navigateTo = async (path: string) => {
    if (!await confirmDiscardEdits()) return;
    setEditingFile(null);
    setEditDirty(false);
    setSaveState('idle');
    setFileFilter('');
    setCurrentPath(path);
  };

  const navigateUp = () => {
    if (currentPath === '.' || currentPath === '/') return;
    const parts = currentPath.split('/');
    parts.pop();
    navigateTo(parts.length === 0 ? '.' : parts.join('/'));
  };

  const breadcrumbs = currentPath === '.'
    ? ['.']
    : ['.', ...currentPath.split('/').filter(Boolean)];

  const visibleFiles = useMemo(() => {
    const q = fileFilter.trim().toLowerCase();
    if (!q) return files;
    return files.filter(entry =>
      entry.name.toLowerCase().includes(q) ||
      entry.path.toLowerCase().includes(q),
    );
  }, [fileFilter, files]);

  const handleOpenFile = async (entry: FileEntry) => {
    if (entry.is_dir) {
      await navigateTo(entry.path);
      return;
    }

    // Check if file is an image
    const lowerName = entry.name.toLowerCase();
    if (lowerName.endsWith('.png') || lowerName.endsWith('.jpg') ||
        lowerName.endsWith('.jpeg') || lowerName.endsWith('.gif') ||
        lowerName.endsWith('.webp') || lowerName.endsWith('.svg') ||
        lowerName.endsWith('.ico')) {
      // The /read endpoint is auth-gated by Bearer token, so a plain
      // window.open() lands on a 401. Fetch with auth, turn the response
      // into a blob URL, then open that in a new tab.
      setError('');
      try {
        const tok = getToken();
        const res = await fetch(
          `/api/v1/files/${encodeURIComponent(selectedWorkspaceID)}/read?path=${encodeURIComponent(entry.path)}`,
          { headers: tok ? { Authorization: `Bearer ${tok}` } : {} },
        );
        if (!res.ok) {
          const body = await res.json().catch(() => ({ error: res.statusText }));
          throw new Error(body.error || res.statusText);
        }
        const blob = await res.blob();
        const url = URL.createObjectURL(blob);
        const win = window.open(url, '_blank');
        // Revoke after the new tab has had a moment to load — Firefox keeps
        // the URL alive for the new document's lifetime, Chrome wants ~1s.
        setTimeout(() => URL.revokeObjectURL(url), 60_000);
        if (!win) setError('Browser blocked the image preview pop-up. Allow pop-ups for this site.');
      } catch (e) {
        setError((e as Error).message);
      }
      return;
    }

    // Open text editor — confirm if user has unsaved changes in another file.
    if (!await confirmDiscardEdits()) return;
    setError('');
    try {
      const result = await readFile(selectedWorkspaceID, entry.path);
      setEditingFile(entry.path);
      setEditContent(result.content);
      setEditDirty(false);
      setSaveState('idle');
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const handleSaveFile = useCallback(async () => {
    if (!editingFile || saving) return;
    setSaving(true);
    setError('');
    setSaveState('idle');
    try {
      await writeFile(selectedWorkspaceID, editingFile, editContent);
      setEditDirty(false);
      setSaveState('saved');
    } catch (e) {
      setSaveState('error');
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  }, [editContent, editingFile, saving, selectedWorkspaceID]);

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (!editingFile || !(e.ctrlKey || e.metaKey) || e.key.toLowerCase() !== 's') return;
      e.preventDefault();
      if (editDirty && !saving) void handleSaveFile();
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [editDirty, editingFile, handleSaveFile, saving]);

  useEffect(() => {
    if (!editDirty) return;
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = '';
    };
    window.addEventListener('beforeunload', onBeforeUnload);
    return () => window.removeEventListener('beforeunload', onBeforeUnload);
  }, [editDirty]);

  const handleDelete = async (path: string) => {
    setDeleting(true);
    setError('');
    try {
      await deleteFile(selectedWorkspaceID, path);
      setConfirmDelete(null);
      if (editingFile === path) {
        setEditingFile(null);
        setEditDirty(false);
        setSaveState('idle');
      }
      await loadFiles();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setDeleting(false);
    }
  };

  const handleCreateDir = async () => {
    if (!newFolderName.trim()) return;
    setCreatingDir(true);
    setError('');
    const path = currentPath === '.' ? newFolderName.trim() : `${currentPath}/${newFolderName.trim()}`;
    try {
      await createDir(selectedWorkspaceID, path);
      setShowNewFolder(false);
      setNewFolderName('');
      await loadFiles();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setCreatingDir(false);
    }
  };

  const handleUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    // Check if file already exists
    const exists = files.some(f => f.name === file.name && !f.is_dir);
    if (exists && !await confirmAction({
      title: `Overwrite "${file.name}"?`,
      message: 'A file with this name already exists in the current directory.',
      confirmLabel: 'Overwrite',
      variant: 'warning',
    })) {
      e.target.value = '';
      return;
    }
    setUploading(true);
    setError('');
    try {
      const path = currentPath === '.' ? file.name : `${currentPath}/${file.name}`;
      await uploadFile(selectedWorkspaceID, path, file);
      await loadFiles();
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setUploading(false);
      e.target.value = '';
    }
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">File Manager</h1>
          <p className="mt-1 text-sm text-muted-foreground">Browse and edit domain or application files.</p>
        </div>
        <button
          onClick={loadFiles}
          className="flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-xs text-card-foreground hover:bg-[#475569]"
        >
          <RefreshCw size={12} /> Refresh
        </button>
      </div>

      {error && (
        <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">{error}</div>
      )}

      {/* Workspace selector + disk usage */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-end">
        <div className="flex-1">
          <label className="mb-1.5 block text-xs font-medium uppercase text-muted-foreground">Workspace</label>
          <select
            value={selectedWorkspaceID}
            onChange={async e => {
              if (!await confirmDiscardEdits()) {
                // Snap the select element back to the previously selected
                // workspace so the UI matches state after the user cancels.
                e.target.value = selectedWorkspaceID;
                return;
              }
              setSelectedWorkspaceID(e.target.value);
              setCurrentPath('.');
              setFileFilter('');
              setEditingFile(null);
              setEditDirty(false);
              setSaveState('idle');
            }}
            className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none focus:border-blue-500"
          >
            {domainWorkspaces.length > 0 && (
              <optgroup label="Domains">
                {domainWorkspaces.map(target => (
                  <option key={target.id} value={target.id}>{target.label}</option>
                ))}
              </optgroup>
            )}
            {applicationWorkspaces.length > 0 && (
              <optgroup label="Applications">
                {applicationWorkspaces.map(target => (
                  <option key={target.id} value={target.id}>
                    {target.label}
                    {target.domains?.length ? ` (${target.domains.join(', ')})` : ''}
                  </option>
                ))}
              </optgroup>
            )}
          </select>
          {workspaces.length === 0 && (
            <p className="mt-1 text-xs text-muted-foreground">No domain or application workspaces are available.</p>
          )}
          {selectedWorkspace?.root && (
            <p className="mt-1 truncate font-mono text-[11px] text-muted-foreground" title={selectedWorkspace.root}>
              {selectedWorkspace.root}
            </p>
          )}
        </div>
        {diskUsage && (
          <div className="flex max-w-full items-center gap-2 rounded-md border border-border bg-card px-4 py-2.5 sm:max-w-[52%]">
            <HardDrive size={16} className="shrink-0 text-muted-foreground" />
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                <span className="text-sm text-card-foreground">Disk Usage:</span>
                <span className="text-sm font-semibold text-foreground">{diskUsage.human}</span>
              </div>
              {diskUsage.root && (
                <p className="mt-0.5 truncate font-mono text-[11px] text-muted-foreground" title={diskUsage.root}>
                  {diskUsage.root}
                </p>
              )}
            </div>
          </div>
        )}
      </div>

      {/* Breadcrumb + actions */}
      <div className="flex flex-col gap-3 rounded-lg border border-border bg-card px-4 py-3 lg:flex-row lg:items-center lg:justify-between">
        <div className="flex min-w-0 items-center gap-1 overflow-x-auto text-sm text-muted-foreground">
          <button onClick={() => navigateTo('.')} className="hover:text-foreground" title="Home">
            <Home size={14} />
          </button>
          {breadcrumbs.slice(1).map((part, i) => {
            const path = breadcrumbs.slice(1, i + 2).join('/');
            return (
              <span key={i} className="flex items-center gap-1">
                <ChevronRight size={12} />
                <button
                  onClick={() => navigateTo(path)}
                  className="hover:text-foreground whitespace-nowrap"
                >
                  {part}
                </button>
              </span>
            );
          })}
        </div>
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center lg:shrink-0">
          <div className="relative min-w-48 sm:w-56">
            <Search size={14} className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-muted-foreground" />
            <input
              value={fileFilter}
              onChange={e => setFileFilter(e.target.value)}
              placeholder="Filter files"
              className="h-8 w-full rounded-md border border-border bg-background pl-8 pr-3 text-xs text-foreground outline-none focus:border-blue-500"
            />
          </div>
          {currentPath !== '.' && (
            <button
              onClick={navigateUp}
              className="flex items-center gap-1 rounded-md bg-accent px-2.5 py-1.5 text-xs text-card-foreground hover:bg-[#475569]"
            >
              <ArrowLeft size={12} /> Up
            </button>
          )}
          <button
            onClick={() => setShowNewFolder(true)}
            className="flex items-center gap-1 rounded-md bg-accent px-2.5 py-1.5 text-xs text-card-foreground hover:bg-[#475569]"
          >
            <Plus size={12} /> New Folder
          </button>
          <label className="flex cursor-pointer items-center gap-1 rounded-md bg-blue-600 px-2.5 py-1.5 text-xs font-medium text-white hover:bg-blue-700">
            <Upload size={12} />
            {uploading ? 'Uploading...' : 'Upload'}
            <input type="file" className="hidden" onChange={handleUpload} disabled={uploading} />
          </label>
        </div>
      </div>

      {/* New folder inline form */}
      {showNewFolder && (
        <div className="flex items-center gap-3 rounded-lg border border-blue-500/30 bg-blue-500/5 px-4 py-3">
          <Folder size={16} className="text-blue-400" />
          <input
            type="text"
            placeholder="Folder name..."
            value={newFolderName}
            onChange={e => setNewFolderName(e.target.value)}
            onKeyDown={e => e.key === 'Enter' && handleCreateDir()}
            className="flex-1 rounded-md border border-border bg-background px-3 py-1.5 text-sm text-foreground outline-none focus:border-blue-500"
            autoFocus
          />
          <button
            onClick={handleCreateDir}
            disabled={creatingDir || !newFolderName.trim()}
            className="rounded-md bg-blue-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          >
            {creatingDir ? 'Creating...' : 'Create'}
          </button>
          <button
            onClick={() => { setShowNewFolder(false); setNewFolderName(''); }}
            className="text-muted-foreground hover:text-foreground"
          >
            <X size={16} />
          </button>
        </div>
      )}

      {/* File editor */}
      {editingFile && (
        <div className="rounded-lg border border-border bg-card shadow-md">
          <div className="flex items-center justify-between border-b border-border px-5 py-3">
            <div className="flex items-center gap-2">
              <File size={16} className="text-blue-400" />
              <span className="font-mono text-sm text-foreground">{editingFile}</span>
              {editDirty && <span className="text-xs text-amber-400">(modified)</span>}
              {saveState === 'saved' && !editDirty && <span className="text-xs text-emerald-400">Saved</span>}
              {saveState === 'error' && <span className="text-xs text-red-400">Save failed</span>}
            </div>
            <div className="flex items-center gap-2">
              <button
                onClick={handleSaveFile}
                disabled={saving || !editDirty}
                title="Save file"
                aria-label="Save file"
                className="flex min-w-20 items-center justify-center gap-1 rounded-md bg-blue-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-blue-700 disabled:opacity-50"
              >
                {saving ? <RefreshCw size={12} className="animate-spin" /> : <Save size={12} />}
                {saving ? 'Saving' : 'Save'}
              </button>
              <button
                onClick={async () => {
                  if (!await confirmDiscardEdits()) return;
                  setEditingFile(null);
                  setEditDirty(false);
                  setSaveState('idle');
                }}
                className="text-muted-foreground hover:text-foreground"
              >
                <X size={16} />
              </button>
            </div>
          </div>
          <textarea
            value={editContent}
            onChange={e => {
              setEditContent(e.target.value);
              setEditDirty(true);
              setSaveState('idle');
            }}
            className="h-96 w-full resize-y bg-background p-4 font-mono text-sm leading-6 text-foreground outline-none focus:ring-1 focus:ring-blue-500/40"
            spellCheck={false}
          />
        </div>
      )}

      {/* File list */}
      {loading ? (
        <div className="flex h-48 items-center justify-center text-muted-foreground">Loading files...</div>
      ) : files.length === 0 && !editingFile ? (
        <div className="rounded-lg border border-border bg-card px-6 py-12 text-center">
          <FolderOpen size={40} className="mx-auto mb-3 text-muted-foreground" />
          <p className="text-card-foreground font-medium">Empty directory</p>
          <p className="text-sm text-muted-foreground mt-1">Upload a file or create a folder to get started.</p>
        </div>
      ) : visibleFiles.length === 0 && !editingFile ? (
        <div className="rounded-lg border border-border bg-card px-6 py-12 text-center">
          <Search size={36} className="mx-auto mb-3 text-muted-foreground" />
          <p className="font-medium text-card-foreground">No matches</p>
          <p className="mt-1 text-sm text-muted-foreground">Try a different file name.</p>
        </div>
      ) : !editingFile && (
        <div className="overflow-hidden rounded-lg border border-border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border bg-card/50 text-left text-xs uppercase tracking-wider text-muted-foreground">
                <th className="px-4 py-3">Name</th>
                <th className="px-4 py-3">Size</th>
                <th className="px-4 py-3">Modified</th>
                <th className="px-4 py-3">Permissions</th>
                <th className="px-4 py-3 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {visibleFiles.map(entry => (
                <tr key={entry.path} className="bg-background hover:bg-card/50">
                  <td className="px-4 py-3">
                    <button
                      onClick={() => handleOpenFile(entry)}
                      className="flex items-center gap-2 text-foreground hover:text-blue-400"
                    >
                      {entry.is_dir ? (
                        <Folder size={16} className="text-amber-400" />
                      ) : (
                        <File size={16} className="text-muted-foreground" />
                      )}
                      <span className="font-mono text-sm">{entry.name}</span>
                    </button>
                  </td>
                  <td className="px-4 py-3 text-muted-foreground text-xs">
                    {entry.is_dir ? '--' : formatSize(entry.size)}
                  </td>
                  <td className="px-4 py-3 text-muted-foreground text-xs whitespace-nowrap">
                    {formatDate(entry.mod_time)}
                  </td>
                  <td className="px-4 py-3 font-mono text-xs text-muted-foreground">
                    {entry.mode}
                  </td>
                  <td className="px-4 py-3 text-right">
                    {confirmDelete === entry.path ? (
                      <span className="flex items-center justify-end gap-2">
                        <span className="text-xs text-red-400">Delete?</span>
                        <button
                          onClick={() => handleDelete(entry.path)}
                          disabled={deleting}
                          className="rounded bg-red-600 px-2 py-1 text-xs text-white hover:bg-red-700 disabled:opacity-50"
                        >
                          {deleting ? '...' : 'Yes'}
                        </button>
                        <button
                          onClick={() => setConfirmDelete(null)}
                          className="rounded bg-accent px-2 py-1 text-xs text-card-foreground"
                        >
                          No
                        </button>
                      </span>
                    ) : (
                      <button
                        onClick={() => setConfirmDelete(entry.path)}
                        className="flex items-center gap-1 rounded-md bg-red-500/15 px-2.5 py-1.5 text-xs font-medium text-red-400 hover:bg-red-500/25"
                      >
                        <Trash2 size={13} /> Delete
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
