import { useState, useEffect, useCallback } from 'react';
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
} from 'lucide-react';
import {
  fetchDomains,
  fetchFiles,
  readFile,
  writeFile,
  deleteFile,
  createDir,
  fetchDiskUsage,
  type DomainData,
  type FileEntry,
} from '@/lib/api';

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
  const [domains, setDomains] = useState<DomainData[]>([]);
  const [selectedDomain, setSelectedDomain] = useState('');
  const [files, setFiles] = useState<FileEntry[]>([]);
  const [currentPath, setCurrentPath] = useState('.');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [diskUsage, setDiskUsage] = useState<{ bytes: number; human: string } | null>(null);

  // Editor state
  const [editingFile, setEditingFile] = useState<string | null>(null);
  const [editContent, setEditContent] = useState('');
  const [saving, setSaving] = useState(false);
  const [editDirty, setEditDirty] = useState(false);

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
    fetchDomains()
      .then(d => {
        setDomains(d ?? []);
        if (d && d.length > 0) setSelectedDomain(d[0].host);
      })
      .catch(() => {});
  }, []);

  const loadFiles = useCallback(async () => {
    if (!selectedDomain) return;
    setLoading(true);
    setError('');
    try {
      const [f, du] = await Promise.all([
        fetchFiles(selectedDomain, currentPath),
        fetchDiskUsage(selectedDomain),
      ]);
      // Sort: dirs first, then files alphabetically
      const sorted = (f ?? []).sort((a, b) => {
        if (a.is_dir && !b.is_dir) return -1;
        if (!a.is_dir && b.is_dir) return 1;
        return a.name.localeCompare(b.name);
      });
      setFiles(sorted);
      setDiskUsage(du);
    } catch (e) {
      setError((e as Error).message);
      setFiles([]);
    } finally {
      setLoading(false);
    }
  }, [selectedDomain, currentPath]);

  useEffect(() => {
    loadFiles();
  }, [loadFiles]);

  const navigateTo = (path: string) => {
    setEditingFile(null);
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

  const handleOpenFile = async (entry: FileEntry) => {
    if (entry.is_dir) {
      navigateTo(entry.path);
      return;
    }
    // Open text editor
    setError('');
    try {
      const result = await readFile(selectedDomain, entry.path);
      setEditingFile(entry.path);
      setEditContent(result.content);
      setEditDirty(false);
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const handleSaveFile = async () => {
    if (!editingFile) return;
    setSaving(true);
    setError('');
    try {
      await writeFile(selectedDomain, editingFile, editContent);
      setEditDirty(false);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async (path: string) => {
    setDeleting(true);
    setError('');
    try {
      await deleteFile(selectedDomain, path);
      setConfirmDelete(null);
      if (editingFile === path) {
        setEditingFile(null);
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
      await createDir(selectedDomain, path);
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
    setUploading(true);
    setError('');
    try {
      const text = await file.text();
      const path = currentPath === '.' ? file.name : `${currentPath}/${file.name}`;
      await writeFile(selectedDomain, path, text);
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
          <h1 className="text-2xl font-bold text-slate-100">File Manager</h1>
          <p className="mt-1 text-sm text-slate-400">Browse and edit domain files.</p>
        </div>
        <button
          onClick={loadFiles}
          className="flex items-center gap-1.5 rounded-md bg-[#334155] px-3 py-1.5 text-xs text-slate-300 hover:bg-[#475569]"
        >
          <RefreshCw size={12} /> Refresh
        </button>
      </div>

      {error && (
        <div className="rounded-md bg-red-500/10 px-4 py-3 text-sm text-red-400">{error}</div>
      )}

      {/* Domain selector + disk usage */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-end">
        <div className="flex-1">
          <label className="mb-1.5 block text-xs font-medium uppercase text-slate-500">Domain</label>
          <select
            value={selectedDomain}
            onChange={e => {
              setSelectedDomain(e.target.value);
              setCurrentPath('.');
              setEditingFile(null);
            }}
            className="w-full rounded-md border border-[#334155] bg-[#0f172a] px-3 py-2.5 text-sm text-slate-200 outline-none focus:border-blue-500"
          >
            {domains.map(d => (
              <option key={d.host} value={d.host}>{d.host}</option>
            ))}
          </select>
        </div>
        {diskUsage && (
          <div className="flex items-center gap-2 rounded-md border border-[#334155] bg-[#1e293b] px-4 py-2.5">
            <HardDrive size={16} className="text-slate-400" />
            <span className="text-sm text-slate-300">Disk Usage:</span>
            <span className="text-sm font-semibold text-slate-100">{diskUsage.human}</span>
          </div>
        )}
      </div>

      {/* Breadcrumb + actions */}
      <div className="flex items-center justify-between gap-3 rounded-lg border border-[#334155] bg-[#1e293b] px-4 py-3">
        <div className="flex items-center gap-1 text-sm text-slate-400 overflow-x-auto">
          <button onClick={() => navigateTo('.')} className="hover:text-slate-200" title="Home">
            <Home size={14} />
          </button>
          {breadcrumbs.slice(1).map((part, i) => {
            const path = breadcrumbs.slice(1, i + 2).join('/');
            return (
              <span key={i} className="flex items-center gap-1">
                <ChevronRight size={12} />
                <button
                  onClick={() => navigateTo(path)}
                  className="hover:text-slate-200 whitespace-nowrap"
                >
                  {part}
                </button>
              </span>
            );
          })}
        </div>
        <div className="flex items-center gap-2 shrink-0">
          {currentPath !== '.' && (
            <button
              onClick={navigateUp}
              className="flex items-center gap-1 rounded-md bg-[#334155] px-2.5 py-1.5 text-xs text-slate-300 hover:bg-[#475569]"
            >
              <ArrowLeft size={12} /> Up
            </button>
          )}
          <button
            onClick={() => setShowNewFolder(true)}
            className="flex items-center gap-1 rounded-md bg-[#334155] px-2.5 py-1.5 text-xs text-slate-300 hover:bg-[#475569]"
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
            className="flex-1 rounded-md border border-[#334155] bg-[#0f172a] px-3 py-1.5 text-sm text-slate-200 outline-none focus:border-blue-500"
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
            className="text-slate-400 hover:text-slate-200"
          >
            <X size={16} />
          </button>
        </div>
      )}

      {/* File editor */}
      {editingFile && (
        <div className="rounded-lg border border-[#334155] bg-[#1e293b] shadow-md">
          <div className="flex items-center justify-between border-b border-[#334155] px-5 py-3">
            <div className="flex items-center gap-2">
              <File size={16} className="text-blue-400" />
              <span className="font-mono text-sm text-slate-200">{editingFile}</span>
              {editDirty && <span className="text-xs text-amber-400">(modified)</span>}
            </div>
            <div className="flex items-center gap-2">
              <button
                onClick={handleSaveFile}
                disabled={saving || !editDirty}
                className="flex items-center gap-1 rounded-md bg-blue-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-blue-700 disabled:opacity-50"
              >
                {saving ? <RefreshCw size={12} className="animate-spin" /> : <Save size={12} />}
                Save
              </button>
              <button
                onClick={() => setEditingFile(null)}
                className="text-slate-400 hover:text-slate-200"
              >
                <X size={16} />
              </button>
            </div>
          </div>
          <textarea
            value={editContent}
            onChange={e => { setEditContent(e.target.value); setEditDirty(true); }}
            className="h-96 w-full resize-y bg-[#0f172a] p-4 font-mono text-sm text-slate-200 outline-none"
            spellCheck={false}
          />
        </div>
      )}

      {/* File list */}
      {loading ? (
        <div className="flex h-48 items-center justify-center text-slate-400">Loading files...</div>
      ) : files.length === 0 && !editingFile ? (
        <div className="rounded-lg border border-[#334155] bg-[#1e293b] px-6 py-12 text-center">
          <FolderOpen size={40} className="mx-auto mb-3 text-slate-500" />
          <p className="text-slate-300 font-medium">Empty directory</p>
          <p className="text-sm text-slate-500 mt-1">Upload a file or create a folder to get started.</p>
        </div>
      ) : !editingFile && (
        <div className="overflow-hidden rounded-lg border border-[#334155]">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[#334155] bg-[#1e293b]/50 text-left text-xs uppercase tracking-wider text-slate-500">
                <th className="px-4 py-3">Name</th>
                <th className="px-4 py-3">Size</th>
                <th className="px-4 py-3">Modified</th>
                <th className="px-4 py-3">Permissions</th>
                <th className="px-4 py-3 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[#334155]">
              {files.map(entry => (
                <tr key={entry.path} className="bg-[#0f172a] hover:bg-[#1e293b]/50">
                  <td className="px-4 py-3">
                    <button
                      onClick={() => handleOpenFile(entry)}
                      className="flex items-center gap-2 text-slate-200 hover:text-blue-400"
                    >
                      {entry.is_dir ? (
                        <Folder size={16} className="text-amber-400" />
                      ) : (
                        <File size={16} className="text-slate-400" />
                      )}
                      <span className="font-mono text-sm">{entry.name}</span>
                    </button>
                  </td>
                  <td className="px-4 py-3 text-slate-400 text-xs">
                    {entry.is_dir ? '--' : formatSize(entry.size)}
                  </td>
                  <td className="px-4 py-3 text-slate-400 text-xs whitespace-nowrap">
                    {formatDate(entry.mod_time)}
                  </td>
                  <td className="px-4 py-3 font-mono text-xs text-slate-500">
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
                          className="rounded bg-[#334155] px-2 py-1 text-xs text-slate-300"
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
