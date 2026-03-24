import { useState, useEffect } from 'react';
import { Settings, Save, RefreshCw, Code, Sliders } from 'lucide-react';
import {
  fetchPHP, fetchPHPConfig, updatePHPConfigKey, fetchPHPConfigRaw, savePHPConfigRaw,
  type PHPInstall,
} from '@/lib/api';

const popularSettings = [
  { key: 'memory_limit', label: 'Memory Limit', placeholder: '256M', hint: 'Max memory per PHP process' },
  { key: 'max_execution_time', label: 'Max Execution Time', placeholder: '300', hint: 'Seconds before timeout' },
  { key: 'upload_max_filesize', label: 'Upload Max Size', placeholder: '64M', hint: 'Max file upload size' },
  { key: 'post_max_size', label: 'Post Max Size', placeholder: '64M', hint: 'Max POST body size (>= upload)' },
  { key: 'max_input_vars', label: 'Max Input Vars', placeholder: '3000', hint: 'Max form variables' },
  { key: 'display_errors', label: 'Display Errors', placeholder: 'Off', hint: 'Show errors in browser (Off for production)' },
  { key: 'error_reporting', label: 'Error Reporting', placeholder: 'E_ALL & ~E_DEPRECATED', hint: 'Error level' },
  { key: 'opcache.enable', label: 'OPcache', placeholder: '1', hint: '1=enabled (recommended)' },
  { key: 'opcache.memory_consumption', label: 'OPcache Memory', placeholder: '128', hint: 'MB for opcode cache' },
  { key: 'date.timezone', label: 'Timezone', placeholder: 'UTC', hint: 'e.g. Europe/Istanbul' },
  { key: 'session.gc_maxlifetime', label: 'Session Lifetime', placeholder: '1440', hint: 'Seconds' },
  { key: 'max_file_uploads', label: 'Max File Uploads', placeholder: '20', hint: 'Simultaneous uploads' },
];

type Tab = 'form' | 'raw';

export default function PHPConfig() {
  const [installs, setInstalls] = useState<PHPInstall[]>([]);
  const [selectedVer, setSelectedVer] = useState('');
  const [tab, setTab] = useState<Tab>('form');

  // Form state
  const [formValues, setFormValues] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState<string | null>(null);
  const [status, setStatus] = useState<{ ok: boolean; msg: string } | null>(null);

  // Raw editor state
  const [rawContent, setRawContent] = useState('');
  const [rawDirty, setRawDirty] = useState(false);
  const [rawSaving, setRawSaving] = useState(false);

  useEffect(() => {
    fetchPHP().then(p => {
      const list = (p ?? []).filter(i => !i.disabled);
      setInstalls(list);
      if (list.length > 0) setSelectedVer(list[0].version);
    }).catch(() => {});
  }, []);

  useEffect(() => {
    if (!selectedVer) return;
    fetchPHPConfig(selectedVer).then(cfg => setFormValues(cfg ?? {})).catch(() => setFormValues({}));
    fetchPHPConfigRaw(selectedVer).then(r => { setRawContent(r?.content ?? ''); setRawDirty(false); }).catch(() => setRawContent(''));
  }, [selectedVer]);

  const handleFormSave = async (key: string, value: string) => {
    setSaving(key);
    setStatus(null);
    try {
      await updatePHPConfigKey(selectedVer, key, value);
      setFormValues(prev => ({ ...prev, [key]: value }));
      setStatus({ ok: true, msg: `${key} = ${value} saved` });
    } catch (e) {
      setStatus({ ok: false, msg: (e as Error).message });
    } finally {
      setSaving(null);
    }
  };

  const handleRawSave = async () => {
    setRawSaving(true);
    setStatus(null);
    try {
      await savePHPConfigRaw(selectedVer, rawContent);
      setRawDirty(false);
      setStatus({ ok: true, msg: 'php.ini saved' });
      // Refresh form values
      fetchPHPConfig(selectedVer).then(cfg => setFormValues(cfg ?? {})).catch(() => {});
    } catch (e) {
      setStatus({ ok: false, msg: (e as Error).message });
    } finally {
      setRawSaving(false);
    }
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">PHP Configuration</h1>
          <p className="mt-1 text-sm text-slate-400">Edit php.ini settings — changes take effect after PHP restart.</p>
        </div>
      </div>

      {/* Version selector + tab */}
      <div className="flex items-center gap-4">
        <select value={selectedVer} onChange={e => setSelectedVer(e.target.value)}
          className="rounded-md border border-[#334155] bg-[#1e293b] px-3 py-2 text-sm text-slate-200 outline-none focus:border-blue-500">
          {installs.map(i => <option key={i.version} value={i.version}>PHP {i.version}</option>)}
        </select>
        <div className="flex rounded-md border border-[#334155] overflow-hidden">
          <button onClick={() => setTab('form')}
            className={`flex items-center gap-1.5 px-4 py-2 text-xs font-medium transition ${tab === 'form' ? 'bg-blue-600 text-white' : 'bg-[#1e293b] text-slate-400 hover:text-slate-200'}`}>
            <Sliders size={13} /> Popular Settings
          </button>
          <button onClick={() => setTab('raw')}
            className={`flex items-center gap-1.5 px-4 py-2 text-xs font-medium transition ${tab === 'raw' ? 'bg-blue-600 text-white' : 'bg-[#1e293b] text-slate-400 hover:text-slate-200'}`}>
            <Code size={13} /> Raw php.ini
          </button>
        </div>
      </div>

      {status && (
        <div className={`rounded-md px-4 py-2.5 text-sm ${status.ok ? 'bg-emerald-500/10 text-emerald-400' : 'bg-red-500/10 text-red-400'}`}>
          {status.msg}
        </div>
      )}

      {/* Form tab */}
      {tab === 'form' && (
        <div className="rounded-lg border border-[#334155] bg-[#1e293b] divide-y divide-[#334155]">
          {popularSettings.map(s => (
            <div key={s.key} className="flex items-center gap-4 px-5 py-3">
              <div className="flex-1 min-w-0">
                <p className="text-sm font-medium text-slate-300">{s.label}</p>
                <p className="text-[10px] text-slate-500">{s.key} — {s.hint}</p>
              </div>
              <input
                value={formValues[s.key] ?? ''}
                onChange={e => setFormValues(prev => ({ ...prev, [s.key]: e.target.value }))}
                placeholder={s.placeholder}
                className="w-40 rounded-md border border-[#334155] bg-[#0f172a] px-3 py-1.5 text-sm font-mono text-slate-200 outline-none focus:border-blue-500"
              />
              <button
                onClick={() => handleFormSave(s.key, formValues[s.key] ?? '')}
                disabled={saving === s.key}
                className="flex items-center gap-1 rounded-md bg-blue-600/15 px-3 py-1.5 text-xs font-medium text-blue-400 hover:bg-blue-600/25 disabled:opacity-50"
              >
                {saving === s.key ? <RefreshCw size={11} className="animate-spin" /> : <Save size={11} />}
                Save
              </button>
            </div>
          ))}
        </div>
      )}

      {/* Raw editor tab */}
      {tab === 'raw' && (
        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <p className="text-xs text-slate-500">
              Full php.ini file — edit with care. <span className="text-amber-400">Restart PHP after saving.</span>
            </p>
            <button
              onClick={handleRawSave}
              disabled={rawSaving || !rawDirty}
              className="flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
            >
              {rawSaving ? <RefreshCw size={13} className="animate-spin" /> : <Save size={13} />}
              {rawSaving ? 'Saving...' : 'Save php.ini'}
            </button>
          </div>
          <textarea
            value={rawContent}
            onChange={e => { setRawContent(e.target.value); setRawDirty(true); }}
            spellCheck={false}
            className="w-full h-[60vh] rounded-lg border border-[#334155] bg-[#0f172a] px-4 py-3 font-mono text-xs text-slate-200 outline-none focus:border-blue-500 resize-none leading-relaxed"
          />
        </div>
      )}

      {/* Quick presets */}
      {tab === 'form' && (
        <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5">
          <h2 className="text-sm font-semibold text-slate-300 mb-3 flex items-center gap-2">
            <Settings size={14} /> Quick Presets
          </h2>
          <div className="flex flex-wrap gap-2">
            {([
              { name: 'WordPress', values: { memory_limit: '256M', max_execution_time: '300', upload_max_filesize: '64M', post_max_size: '64M', 'opcache.enable': '1' }},
              { name: 'Laravel', values: { memory_limit: '512M', max_execution_time: '120', upload_max_filesize: '32M', post_max_size: '32M', 'opcache.enable': '1' }},
              { name: 'Development', values: { memory_limit: '512M', max_execution_time: '0', display_errors: 'On', error_reporting: 'E_ALL', 'opcache.enable': '0' }},
              { name: 'Production', values: { memory_limit: '256M', max_execution_time: '30', display_errors: 'Off', error_reporting: 'E_ALL & ~E_DEPRECATED & ~E_STRICT', 'opcache.enable': '1' }},
            ] as { name: string; values: Record<string, string> }[]).map(preset => (
              <button
                key={preset.name}
                onClick={async () => {
                  setStatus(null);
                  for (const [k, v] of Object.entries(preset.values)) {
                    await updatePHPConfigKey(selectedVer, k, v);
                  }
                  setFormValues(prev => ({ ...prev, ...preset.values }));
                  setStatus({ ok: true, msg: `${preset.name} preset applied` });
                }}
                className="rounded-md border border-[#334155] bg-[#0f172a] px-3 py-2 text-xs font-medium text-slate-300 hover:bg-[#334155] hover:text-white transition"
              >
                {preset.name}
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
