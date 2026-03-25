import { useState, useEffect, useCallback } from 'react';
import {
  Save, RotateCcw, CheckCircle, XCircle, FileCode, ChevronRight, AlertTriangle,
} from 'lucide-react';
import {
  fetchConfigRaw, saveConfigRaw,
  fetchDomainConfigRaw, saveDomainConfigRaw,
  fetchDomains, type DomainData,
} from '@/lib/api';

interface ConfigFile {
  id: string;
  label: string;
  host?: string;
}

export default function ConfigEditor() {
  const [files, setFiles] = useState<ConfigFile[]>([{ id: '__main__', label: 'Main Config' }]);
  const [activeFile, setActiveFile] = useState('__main__');
  const [content, setContent] = useState('');
  const [originalContent, setOriginalContent] = useState('');
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [status, setStatus] = useState<{ ok: boolean; message: string } | null>(null);
  const [validationError, setValidationError] = useState('');

  // Load domain list for sidebar
  useEffect(() => {
    fetchDomains()
      .then((domains: DomainData[]) => {
        const domainFiles: ConfigFile[] = domains.map((d) => ({
          id: `domain:${d.host}`,
          label: d.host,
          host: d.host,
        }));
        setFiles([{ id: '__main__', label: 'Main Config' }, ...domainFiles]);
      })
      .catch(() => {});
  }, []);

  // Load content for active file
  const loadContent = useCallback(async () => {
    setLoading(true);
    setStatus(null);
    setValidationError('');
    try {
      let result: { content: string };
      if (activeFile === '__main__') {
        result = await fetchConfigRaw();
      } else {
        const host = activeFile.replace('domain:', '');
        result = await fetchDomainConfigRaw(host);
      }
      setContent(result.content);
      setOriginalContent(result.content);
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
      setContent('');
      setOriginalContent('');
    } finally {
      setLoading(false);
    }
  }, [activeFile]);

  useEffect(() => {
    loadContent();
  }, [loadContent]);

  // Simple YAML validation
  const validateYaml = (text: string): string => {
    const lines = text.split('\n');
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i];
      // Check for tabs (YAML uses spaces)
      if (line.includes('\t')) {
        return `Line ${i + 1}: Tab characters not allowed in YAML, use spaces`;
      }
      // Check for very basic indentation consistency
      if (line.match(/^\s+/) && line.trimStart().startsWith('- ')) {
        continue; // list items are fine
      }
    }
    return '';
  };

  const handleContentChange = (value: string) => {
    setContent(value);
    const err = validateYaml(value);
    setValidationError(err);
  };

  const handleSave = async () => {
    if (validationError) return;
    setSaving(true);
    setStatus(null);
    try {
      if (activeFile === '__main__') {
        await saveConfigRaw(content);
      } else {
        const host = activeFile.replace('domain:', '');
        await saveDomainConfigRaw(host, content);
      }
      setOriginalContent(content);
      setStatus({ ok: true, message: 'Configuration saved successfully' });
    } catch (e) {
      setStatus({ ok: false, message: (e as Error).message });
    } finally {
      setSaving(false);
    }
  };

  const handleReload = () => {
    loadContent();
  };

  const isDirty = content !== originalContent;
  const activeLabel = files.find((f) => f.id === activeFile)?.label ?? '';

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <h1 className="text-xl font-bold sm:text-2xl text-foreground">Config Editor</h1>
        <p className="text-sm text-muted-foreground">Edit YAML configuration files</p>
      </div>

      <div className="flex gap-4" style={{ minHeight: 'calc(100vh - 220px)' }}>
        {/* File sidebar */}
        <div className="w-56 shrink-0 rounded-lg border border-border bg-card">
          <div className="border-b border-border px-4 py-3">
            <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              Config Files
            </h3>
          </div>
          <div className="space-y-0.5 p-2">
            {files.map((f) => (
              <button
                key={f.id}
                onClick={() => setActiveFile(f.id)}
                className={`flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm transition-colors ${
                  activeFile === f.id
                    ? 'bg-blue-600/20 text-blue-400'
                    : 'text-muted-foreground hover:bg-accent hover:text-foreground'
                }`}
              >
                <FileCode size={14} className="shrink-0" />
                <span className="truncate">{f.label}</span>
                {activeFile === f.id && (
                  <ChevronRight size={12} className="ml-auto shrink-0" />
                )}
              </button>
            ))}
          </div>
        </div>

        {/* Editor area */}
        <div className="flex flex-1 flex-col rounded-lg border border-border bg-card">
          {/* Toolbar */}
          <div className="flex items-center justify-between border-b border-border px-4 py-3">
            <div className="flex items-center gap-3">
              <span className="text-sm font-medium text-card-foreground">{activeLabel}</span>
              {isDirty && (
                <span className="rounded-full bg-amber-500/15 px-2 py-0.5 text-xs font-medium text-amber-400">
                  Modified
                </span>
              )}
              {validationError ? (
                <span className="flex items-center gap-1 text-xs text-red-400">
                  <XCircle size={12} /> Invalid
                </span>
              ) : content ? (
                <span className="flex items-center gap-1 text-xs text-emerald-400">
                  <CheckCircle size={12} /> Valid
                </span>
              ) : null}
            </div>
            <div className="flex items-center gap-2">
              <button
                onClick={handleReload}
                disabled={loading}
                className="flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-xs text-card-foreground hover:bg-[#475569] disabled:opacity-50"
              >
                <RotateCcw size={12} /> Reload
              </button>
              <button
                onClick={handleSave}
                disabled={saving || !isDirty || !!validationError}
                className="flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
              >
                <Save size={12} /> {saving ? 'Saving...' : 'Save'}
              </button>
            </div>
          </div>

          {/* Status messages */}
          {status && (
            <div
              className={`flex items-center gap-2 border-b border-border px-4 py-2 text-sm ${
                status.ok ? 'bg-emerald-500/10 text-emerald-400' : 'bg-red-500/10 text-red-400'
              }`}
            >
              {status.ok ? <CheckCircle size={14} /> : <XCircle size={14} />}
              {status.message}
            </div>
          )}

          {/* Validation error */}
          {validationError && (
            <div className="flex items-center gap-2 border-b border-border bg-amber-500/10 px-4 py-2 text-sm text-amber-400">
              <AlertTriangle size={14} />
              {validationError}
            </div>
          )}

          {/* Text editor */}
          <div className="relative flex-1">
            {loading ? (
              <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
                Loading configuration...
              </div>
            ) : (
              <textarea
                value={content}
                onChange={(e) => handleContentChange(e.target.value)}
                spellCheck={false}
                className="h-full w-full resize-none rounded-b-lg bg-background p-4 font-mono text-sm leading-relaxed text-foreground outline-none placeholder:text-muted-foreground"
                placeholder="# YAML configuration..."
                style={{ tabSize: 2 }}
              />
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
