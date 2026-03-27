import { useState, useEffect } from 'react';
import { Save, RefreshCw, Code, Sliders, CheckCircle, Zap } from 'lucide-react';
import {
  fetchPHP, fetchPHPConfig, updatePHPConfigKey, fetchPHPConfigRaw, savePHPConfigRaw,
} from '@/lib/api';

const popularSettings = [
  { key: 'memory_limit', label: 'Memory Limit', placeholder: '256M', hint: 'Max memory per PHP process', validate: /^\d+[KMG]$/i },
  { key: 'max_execution_time', label: 'Max Execution Time', placeholder: '300', hint: 'Seconds (0 = unlimited)', validate: /^\d+$/ },
  { key: 'upload_max_filesize', label: 'Upload Max Size', placeholder: '64M', hint: 'Max file upload size', validate: /^\d+[KMG]$/i },
  { key: 'post_max_size', label: 'Post Max Size', placeholder: '64M', hint: 'Must be >= upload_max_filesize', validate: /^\d+[KMG]$/i },
  { key: 'max_input_vars', label: 'Max Input Vars', placeholder: '3000', hint: 'Max form variables', validate: /^\d+$/ },
  { key: 'display_errors', label: 'Display Errors', placeholder: 'Off', hint: 'Off for production', validate: /^(On|Off|0|1)$/i },
  { key: 'error_reporting', label: 'Error Reporting', placeholder: 'E_ALL & ~E_DEPRECATED', hint: 'Error level bitmask' },
  { key: 'opcache.enable', label: 'OPcache', placeholder: '1', hint: '1 = enabled (recommended)', validate: /^[01]$/ },
  { key: 'opcache.memory_consumption', label: 'OPcache Memory', placeholder: '128', hint: 'MB for opcode cache', validate: /^\d+$/ },
  { key: 'date.timezone', label: 'Timezone', placeholder: 'UTC', hint: 'e.g. Europe/Istanbul, America/New_York' },
  { key: 'session.gc_maxlifetime', label: 'Session Lifetime', placeholder: '1440', hint: 'Seconds', validate: /^\d+$/ },
  { key: 'max_file_uploads', label: 'Max File Uploads', placeholder: '20', hint: 'Simultaneous uploads', validate: /^\d+$/ },
];

const presets: { name: string; desc: string; values: Record<string, string> }[] = [
  { name: 'WordPress', desc: '256M mem, 64M upload, OPcache on', values: { memory_limit: '256M', max_execution_time: '300', upload_max_filesize: '64M', post_max_size: '64M', max_input_vars: '3000', 'opcache.enable': '1', 'opcache.memory_consumption': '128' }},
  { name: 'Laravel', desc: '512M mem, 32M upload, OPcache on', values: { memory_limit: '512M', max_execution_time: '120', upload_max_filesize: '32M', post_max_size: '32M', 'opcache.enable': '1' }},
  { name: 'Development', desc: 'Errors on, no timeout, OPcache off', values: { memory_limit: '512M', max_execution_time: '0', display_errors: 'On', error_reporting: 'E_ALL', 'opcache.enable': '0' }},
  { name: 'Production', desc: 'Errors off, 30s timeout, OPcache on', values: { memory_limit: '256M', max_execution_time: '30', display_errors: 'Off', error_reporting: 'E_ALL & ~E_DEPRECATED & ~E_STRICT', 'opcache.enable': '1', 'opcache.memory_consumption': '128' }},
];

type Tab = 'form' | 'raw';

export default function PHPConfig() {
  const [versions, setVersions] = useState<string[]>([]);
  const [selectedVer, setSelectedVer] = useState('');
  const [tab, setTab] = useState<Tab>('form');
  const [formValues, setFormValues] = useState<Record<string, string>>({});
  const [savedValues, setSavedValues] = useState<Record<string, string>>({});
  const [savingAll, setSavingAll] = useState(false);
  const [status, setStatus] = useState<{ ok: boolean; msg: string } | null>(null);
  const [rawContent, setRawContent] = useState('');
  const [rawDirty, setRawDirty] = useState(false);
  const [rawSaving, setRawSaving] = useState(false);
  const [applyingPreset, setApplyingPreset] = useState('');

  useEffect(() => {
    fetchPHP().then(p => {
      // Deduplicate versions (same version may appear as CGI + FPM + CLI)
      const seen = new Set<string>();
      const uniqueVers: string[] = [];
      for (const i of (p ?? []).filter(x => !x.disabled && x.sapi !== 'cli')) {
        const short = i.version.split('.').slice(0, 2).join('.');
        if (!seen.has(short)) {
          seen.add(short);
          uniqueVers.push(i.version);
        }
      }
      setVersions(uniqueVers);
      if (uniqueVers.length > 0) setSelectedVer(uniqueVers[0]);
    }).catch(() => {});
  }, []);

  useEffect(() => {
    if (!selectedVer) return;
    fetchPHPConfig(selectedVer).then(cfg => { const c = cfg ?? {}; setFormValues(c); setSavedValues(c); }).catch(() => { setFormValues({}); setSavedValues({}); });
    fetchPHPConfigRaw(selectedVer).then(r => { setRawContent(r?.content ?? ''); setRawDirty(false); }).catch(() => setRawContent(''));
  }, [selectedVer]);

  const showStatus = (ok: boolean, msg: string) => {
    setStatus({ ok, msg });
    setTimeout(() => setStatus(null), 4000);
  };

  const dirtyKeys = popularSettings.filter(s => (formValues[s.key] ?? '') !== (savedValues[s.key] ?? '')).map(s => s.key);
  const hasDirty = dirtyKeys.length > 0;

  const handleSaveAll = async () => {
    // Validate all dirty keys first
    for (const key of dirtyKeys) {
      const setting = popularSettings.find(s => s.key === key);
      const value = formValues[key] ?? '';
      if (setting?.validate && value && !setting.validate.test(value)) {
        showStatus(false, `Invalid value for ${key}: "${value}"`);
        return;
      }
    }
    setSavingAll(true);
    try {
      for (const key of dirtyKeys) {
        await updatePHPConfigKey(selectedVer, key, formValues[key] ?? '');
      }
      setSavedValues({ ...formValues });
      showStatus(true, `${dirtyKeys.length} setting${dirtyKeys.length > 1 ? 's' : ''} saved`);
    } catch (e) {
      showStatus(false, (e as Error).message);
    } finally {
      setSavingAll(false);
    }
  };

  const handlePreset = async (preset: typeof presets[0]) => {
    setApplyingPreset(preset.name);
    try {
      for (const [k, v] of Object.entries(preset.values)) {
        await updatePHPConfigKey(selectedVer, k, v);
      }
      setFormValues(prev => ({ ...prev, ...preset.values }));
      setSavedValues(prev => ({ ...prev, ...preset.values }));
      showStatus(true, `${preset.name} preset applied (${Object.keys(preset.values).length} settings)`);
    } catch (e) {
      showStatus(false, (e as Error).message);
    } finally {
      setApplyingPreset('');
    }
  };

  const handleRawSave = async () => {
    setRawSaving(true);
    try {
      await savePHPConfigRaw(selectedVer, rawContent);
      setRawDirty(false);
      showStatus(true, 'php.ini saved');
      fetchPHPConfig(selectedVer).then(cfg => setFormValues(cfg ?? {})).catch(() => {});
    } catch (e) {
      showStatus(false, (e as Error).message);
    } finally {
      setRawSaving(false);
    }
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">PHP Configuration</h1>
          <p className="mt-1 text-sm text-muted-foreground">Edit php.ini — changes take effect after PHP restart.</p>
        </div>
      </div>

      {/* Version + tab selector */}
      <div className="flex items-center gap-4">
        <select value={selectedVer} onChange={e => setSelectedVer(e.target.value)}
          className="rounded-md border border-border bg-card px-3 py-2 text-sm text-foreground outline-none focus:border-blue-500">
          {versions.map(v => <option key={v} value={v}>PHP {v}</option>)}
        </select>
        <div className="flex rounded-md border border-border overflow-hidden">
          <button onClick={() => setTab('form')}
            className={`flex items-center gap-1.5 px-4 py-2 text-xs font-medium transition ${tab === 'form' ? 'bg-blue-600 text-white' : 'bg-card text-muted-foreground hover:text-foreground'}`}>
            <Sliders size={13} /> Settings
          </button>
          <button onClick={() => setTab('raw')}
            className={`flex items-center gap-1.5 px-4 py-2 text-xs font-medium transition ${tab === 'raw' ? 'bg-blue-600 text-white' : 'bg-card text-muted-foreground hover:text-foreground'}`}>
            <Code size={13} /> Raw php.ini
          </button>
        </div>
      </div>

      {status && (
        <div className={`flex items-center gap-2 rounded-md px-4 py-2.5 text-sm ${status.ok ? 'bg-emerald-500/10 text-emerald-400' : 'bg-red-500/10 text-red-400'}`}>
          <CheckCircle size={14} /> {status.msg}
        </div>
      )}

      {/* Settings tab */}
      {tab === 'form' && (<>
        {/* Quick Presets */}
        <div className="rounded-lg border border-border bg-card p-4">
          <h2 className="text-xs font-semibold text-muted-foreground mb-3 flex items-center gap-1.5">
            <Zap size={12} /> Quick Presets
          </h2>
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
            {presets.map(p => (
              <button key={p.name} onClick={() => handlePreset(p)}
                disabled={!!applyingPreset}
                className="rounded-lg border border-border bg-background px-3 py-2.5 text-left hover:border-blue-500/50 hover:bg-blue-500/5 transition disabled:opacity-50">
                <p className="text-sm font-medium text-card-foreground flex items-center gap-1.5">
                  {applyingPreset === p.name ? <RefreshCw size={11} className="animate-spin" /> : null}
                  {p.name}
                </p>
                <p className="text-[10px] text-muted-foreground mt-0.5">{p.desc}</p>
              </button>
            ))}
          </div>
        </div>

        {/* Settings form */}
        <div className="rounded-lg border border-border bg-card divide-y divide-border">
          {popularSettings.map(s => {
            const value = formValues[s.key] ?? '';
            const dirty = value !== (savedValues[s.key] ?? '');
            const invalid = s.validate && value && !s.validate.test(value);
            return (
              <div key={s.key} className={`flex items-center gap-4 px-5 py-3 ${dirty ? 'bg-blue-500/5' : ''}`}>
                <div className="flex-1 min-w-0">
                  <p className="text-sm font-medium text-card-foreground">{s.label}</p>
                  <p className="text-[10px] text-muted-foreground"><code>{s.key}</code> — {s.hint}</p>
                </div>
                <div className="relative">
                  <input
                    value={value}
                    onChange={e => setFormValues(prev => ({ ...prev, [s.key]: e.target.value }))}
                    placeholder={s.placeholder}
                    className={`w-44 rounded-md border px-3 py-1.5 text-sm font-mono text-foreground outline-none ${
                      invalid ? 'border-red-500 bg-red-500/5' : dirty ? 'border-blue-500 bg-blue-500/5' : 'border-border bg-background focus:border-blue-500'
                    }`}
                  />
                  {invalid && <p className="absolute -bottom-3.5 left-0 text-[9px] text-red-400">Invalid format</p>}
                </div>
              </div>
            );
          })}
        </div>

        {/* Save All button */}
        <div className="flex justify-end">
          <button
            onClick={handleSaveAll}
            disabled={!hasDirty || savingAll}
            className="flex items-center gap-1.5 rounded-md bg-blue-600 px-5 py-2.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40 transition">
            {savingAll ? <RefreshCw size={14} className="animate-spin" /> : <Save size={14} />}
            {savingAll ? 'Saving...' : hasDirty ? `Save ${dirtyKeys.length} change${dirtyKeys.length > 1 ? 's' : ''}` : 'No changes'}
          </button>
        </div>
      </>)}

      {/* Raw editor tab */}
      {tab === 'raw' && (
        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <p className="text-xs text-muted-foreground">
              Full php.ini — <span className="text-amber-400">restart PHP after saving</span>
            </p>
            <button onClick={handleRawSave} disabled={rawSaving || !rawDirty}
              className="flex items-center gap-1.5 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50">
              {rawSaving ? <RefreshCw size={13} className="animate-spin" /> : <Save size={13} />}
              {rawSaving ? 'Saving...' : 'Save php.ini'}
            </button>
          </div>
          <textarea
            value={rawContent}
            onChange={e => { setRawContent(e.target.value); setRawDirty(true); }}
            spellCheck={false}
            className="w-full h-[60vh] rounded-lg border border-border bg-background px-4 py-3 font-mono text-xs text-foreground outline-none focus:border-blue-500 resize-none leading-relaxed"
          />
        </div>
      )}
    </div>
  );
}
