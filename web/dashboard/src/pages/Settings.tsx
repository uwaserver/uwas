import { useState, useEffect, useCallback, useRef } from 'react';
import {
  RefreshCw,
  CheckCircle,
  XCircle,
  Server,
  Clock,
  Shield,
  Lock,
  Database,
  Archive,
  FileText,
  Save,
  Download,
  Eye,
  EyeOff,
  Copy,
  Activity,
  Cpu,
  AlertTriangle,
} from 'lucide-react';
import {
  fetchConfigRaw,
  saveConfigRaw,
  triggerReload,
  fetchConfigExport,
  fetchHealth,
  fetchSystem,
  type HealthData,
  type SystemInfo,
} from '@/lib/api';

// ---------------------------------------------------------------------------
// YAML helpers — lightweight line-level get/set (no library needed)
// ---------------------------------------------------------------------------

/** Read a simple scalar value from YAML given a dot-separated key path.
 *  Supports paths like "global.http_listen" or "global.timeouts.read".
 *  Returns the raw string value (without quotes) or '' if not found. */
function yamlGet(yaml: string, path: string): string {
  const parts = path.split('.');
  const lines = yaml.split('\n');
  let depth = 0;
  let partIdx = 0;

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const trimmed = line.trimStart();
    if (trimmed === '' || trimmed.startsWith('#')) continue;

    const indent = line.length - trimmed.length;

    // Reset depth when we encounter a line at or before current depth
    while (depth > 0 && indent <= (depth - 1) * 2 && partIdx > 0) {
      partIdx--;
      depth--;
    }

    const key = parts[partIdx];
    const regex = new RegExp(`^${key}\\s*:`);
    if (indent === partIdx * 2 && regex.test(trimmed)) {
      if (partIdx === parts.length - 1) {
        // Found the leaf key — extract value
        const colonIdx = trimmed.indexOf(':');
        let val = trimmed.slice(colonIdx + 1).trim();
        // Strip inline comments
        if (val.includes(' #')) val = val.split(' #')[0].trim();
        // Strip quotes
        if ((val.startsWith('"') && val.endsWith('"')) || (val.startsWith("'") && val.endsWith("'"))) {
          val = val.slice(1, -1);
        }
        return val;
      }
      partIdx++;
      depth = partIdx;
    }
  }
  return '';
}

/** Set a simple scalar value in YAML given a dot-separated key path.
 *  If the key exists, its value is replaced in-place. If not, the key line
 *  is inserted under the parent (creating parent sections as needed). */
function yamlSet(yaml: string, path: string, value: string): string {
  const parts = path.split('.');
  const lines = yaml.split('\n');
  let depth = 0;
  let partIdx = 0;
  let insertAfter = -1; // Track last matched parent line for insertion

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const trimmed = line.trimStart();
    if (trimmed === '' || trimmed.startsWith('#')) continue;

    const indent = line.length - trimmed.length;

    while (depth > 0 && indent <= (depth - 1) * 2 && partIdx > 0) {
      partIdx--;
      depth--;
    }

    const key = parts[partIdx];
    const regex = new RegExp(`^${key}\\s*:`);
    if (indent === partIdx * 2 && regex.test(trimmed)) {
      if (partIdx === parts.length - 1) {
        // Replace value
        const colonIdx = line.indexOf(':');
        const prefix = line.slice(0, colonIdx + 1);
        // Determine if we need to quote the value
        const formatted = formatYamlValue(value);
        lines[i] = `${prefix} ${formatted}`;
        return lines.join('\n');
      }
      insertAfter = i;
      partIdx++;
      depth = partIdx;
    }
  }

  // Key not found — insert it
  if (insertAfter >= 0 && partIdx === parts.length - 1) {
    const indent = '  '.repeat(parts.length - 1);
    const formatted = formatYamlValue(value);
    const newLine = `${indent}${parts[parts.length - 1]}: ${formatted}`;
    lines.splice(insertAfter + 1, 0, newLine);
    return lines.join('\n');
  }

  // Need to create parent sections too
  let insertAt = lines.length;
  let currentIndent = 0;
  for (let p = partIdx; p < parts.length; p++) {
    const prefix = '  '.repeat(currentIndent);
    if (p === parts.length - 1) {
      const formatted = formatYamlValue(value);
      lines.splice(insertAt, 0, `${prefix}${parts[p]}: ${formatted}`);
    } else {
      lines.splice(insertAt, 0, `${prefix}${parts[p]}:`);
    }
    insertAt++;
    currentIndent++;
  }
  return lines.join('\n');
}

/** Format a value for YAML: quote strings that could be misinterpreted. */
function formatYamlValue(value: string): string {
  if (value === '') return '""';
  if (value === 'true' || value === 'false') return value;
  if (/^\d+$/.test(value)) return value;
  // If it looks like a duration or path or URL, quote it
  if (value.includes(':') || value.includes('/') || value.includes('@') || value.includes(' ')) {
    return `"${value}"`;
  }
  return value;
}

function formatBytes(b: number): string {
  if (b >= 1 << 30) return `${(b / (1 << 30)).toFixed(1)} GB`;
  if (b >= 1 << 20) return `${(b / (1 << 20)).toFixed(1)} MB`;
  if (b >= 1 << 10) return `${(b / (1 << 10)).toFixed(1)} KB`;
  return `${b} B`;
}

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface FieldDef {
  key: string;       // dot-path in YAML, e.g. "global.http_listen"
  label: string;
  type: 'text' | 'number' | 'toggle' | 'select' | 'secret';
  placeholder?: string;
  options?: { value: string; label: string }[];
  help?: string;
}

interface SectionDef {
  id: string;
  title: string;
  icon: React.ReactNode;
  iconColor: string;
  fields: FieldDef[];
}

// ---------------------------------------------------------------------------
// Section definitions
// ---------------------------------------------------------------------------

const SECTIONS: SectionDef[] = [
  {
    id: 'server',
    title: 'Server',
    icon: <Server size={18} />,
    iconColor: 'text-blue-400',
    fields: [
      { key: 'global.http_listen', label: 'HTTP Listen', type: 'text', placeholder: ':80' },
      { key: 'global.https_listen', label: 'HTTPS Listen', type: 'text', placeholder: ':443' },
      { key: 'global.http3', label: 'HTTP/3', type: 'toggle' },
      { key: 'global.worker_count', label: 'Worker Count', type: 'text', placeholder: 'auto', help: '"auto" or a number' },
      { key: 'global.max_connections', label: 'Max Connections', type: 'number', placeholder: '10000' },
      { key: 'global.pid_file', label: 'PID File', type: 'text', placeholder: '/var/run/uwas.pid' },
      { key: 'global.web_root', label: 'Web Root', type: 'text', placeholder: '/var/www' },
    ],
  },
  {
    id: 'timeouts',
    title: 'Timeouts',
    icon: <Clock size={18} />,
    iconColor: 'text-amber-400',
    fields: [
      { key: 'global.timeouts.read', label: 'Read Timeout', type: 'text', placeholder: '30s' },
      { key: 'global.timeouts.read_header', label: 'Read Header Timeout', type: 'text', placeholder: '10s' },
      { key: 'global.timeouts.write', label: 'Write Timeout', type: 'text', placeholder: '60s' },
      { key: 'global.timeouts.idle', label: 'Idle Timeout', type: 'text', placeholder: '120s' },
      { key: 'global.timeouts.shutdown_grace', label: 'Shutdown Grace', type: 'text', placeholder: '15s' },
      { key: 'global.timeouts.max_header_bytes', label: 'Max Header Bytes', type: 'number', placeholder: '1048576' },
    ],
  },
  {
    id: 'admin',
    title: 'Admin',
    icon: <Shield size={18} />,
    iconColor: 'text-purple-400',
    fields: [
      { key: 'global.admin.enabled', label: 'Enabled', type: 'toggle' },
      { key: 'global.admin.listen', label: 'Listen Address', type: 'text', placeholder: ':9443' },
      { key: 'global.admin.api_key', label: 'API Key', type: 'secret' },
    ],
  },
  {
    id: 'acme',
    title: 'ACME / Let\'s Encrypt',
    icon: <Lock size={18} />,
    iconColor: 'text-emerald-400',
    fields: [
      { key: 'global.acme.email', label: 'Email', type: 'text', placeholder: 'admin@example.com' },
      { key: 'global.acme.ca_url', label: 'CA URL', type: 'text', placeholder: 'https://acme-v02.api.letsencrypt.org/directory' },
      { key: 'global.acme.storage', label: 'Storage Path', type: 'text', placeholder: '/etc/uwas/certs' },
      { key: 'global.acme.dns_provider', label: 'DNS Provider', type: 'text', placeholder: 'cloudflare' },
      { key: 'global.acme.on_demand', label: 'On Demand', type: 'toggle' },
      { key: 'global.acme.on_demand_ask', label: 'On Demand Ask URL', type: 'text', placeholder: 'https://example.com/check' },
    ],
  },
  {
    id: 'cache',
    title: 'Cache',
    icon: <Database size={18} />,
    iconColor: 'text-cyan-400',
    fields: [
      { key: 'global.cache.enabled', label: 'Enabled', type: 'toggle' },
      { key: 'global.cache.memory_limit', label: 'Memory Limit', type: 'text', placeholder: '256MB', help: 'e.g. 256MB, 1GB' },
      { key: 'global.cache.disk_path', label: 'Disk Path', type: 'text', placeholder: '/var/cache/uwas' },
      { key: 'global.cache.disk_limit', label: 'Disk Limit', type: 'text', placeholder: '1GB' },
      { key: 'global.cache.default_ttl', label: 'Default TTL (seconds)', type: 'number', placeholder: '300' },
      { key: 'global.cache.grace_ttl', label: 'Grace TTL (seconds)', type: 'number', placeholder: '60' },
      { key: 'global.cache.stale_while_revalidate', label: 'Stale While Revalidate', type: 'toggle' },
      { key: 'global.cache.purge_key', label: 'Purge Key', type: 'secret' },
    ],
  },
  {
    id: 'backup',
    title: 'Backup',
    icon: <Archive size={18} />,
    iconColor: 'text-orange-400',
    fields: [
      { key: 'global.backup.enabled', label: 'Enabled', type: 'toggle' },
      { key: 'global.backup.provider', label: 'Provider', type: 'select', options: [
        { value: 'local', label: 'Local' },
        { value: 's3', label: 'S3' },
        { value: 'sftp', label: 'SFTP' },
      ]},
      { key: 'global.backup.schedule', label: 'Schedule', type: 'text', placeholder: '24h' },
      { key: 'global.backup.keep', label: 'Keep Last N', type: 'number', placeholder: '7' },
    ],
  },
  {
    id: 'logging',
    title: 'Logging',
    icon: <FileText size={18} />,
    iconColor: 'text-rose-400',
    fields: [
      { key: 'global.log_level', label: 'Log Level', type: 'select', options: [
        { value: 'debug', label: 'Debug' },
        { value: 'info', label: 'Info' },
        { value: 'warn', label: 'Warn' },
        { value: 'error', label: 'Error' },
      ]},
      { key: 'global.log_format', label: 'Log Format', type: 'select', options: [
        { value: 'text', label: 'Text' },
        { value: 'json', label: 'JSON' },
      ]},
    ],
  },
  {
    id: 'alerting',
    title: 'Alerting',
    icon: <AlertTriangle size={18} />,
    iconColor: 'text-yellow-400',
    fields: [
      { key: 'global.alerting.enabled', label: 'Enabled', type: 'toggle' },
      { key: 'global.alerting.webhook_url', label: 'Webhook URL', type: 'text', placeholder: 'https://hooks.slack.com/...' },
    ],
  },
  {
    id: 'mcp',
    title: 'MCP',
    icon: <Cpu size={18} />,
    iconColor: 'text-indigo-400',
    fields: [
      { key: 'global.mcp.enabled', label: 'Enabled', type: 'toggle' },
      { key: 'global.mcp.listen', label: 'Listen Address', type: 'text', placeholder: ':9444' },
    ],
  },
];

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export default function Settings() {
  const [rawYaml, setRawYaml] = useState('');
  const [originalYaml, setOriginalYaml] = useState('');
  const [formValues, setFormValues] = useState<Record<string, string>>({});
  const [health, setHealth] = useState<HealthData | null>(null);
  const [system, setSystem] = useState<SystemInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [reloading, setReloading] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [status, setStatus] = useState<{ ok: boolean; message: string } | null>(null);
  const [revealedSecrets, setRevealedSecrets] = useState<Record<string, boolean>>({});
  const statusTimeout = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Gather all field keys
  const allFields = SECTIONS.flatMap(s => s.fields);

  /** Parse raw YAML into form values. */
  const parseYaml = useCallback((yaml: string) => {
    const values: Record<string, string> = {};
    for (const f of allFields) {
      values[f.key] = yamlGet(yaml, f.key);
    }
    setFormValues(values);
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  /** Load raw config + health + system info. */
  const load = useCallback(async () => {
    try {
      const [raw, h, s] = await Promise.all([
        fetchConfigRaw(),
        fetchHealth(),
        fetchSystem(),
      ]);
      setRawYaml(raw.content);
      setOriginalYaml(raw.content);
      parseYaml(raw.content);
      setHealth(h);
      setSystem(s);
    } catch {
      // ignore
    } finally {
      setLoading(false);
    }
  }, [parseYaml]);

  useEffect(() => { load(); }, [load]);

  /** Show a temporary status message. */
  const showStatus = (ok: boolean, message: string) => {
    setStatus({ ok, message });
    if (statusTimeout.current) clearTimeout(statusTimeout.current);
    statusTimeout.current = setTimeout(() => setStatus(null), 5000);
  };

  /** Update a single form value and synchronise back to raw YAML. */
  const updateField = (key: string, value: string) => {
    setFormValues(prev => ({ ...prev, [key]: value }));
    setRawYaml(prev => yamlSet(prev, key, value));
  };

  const isDirty = rawYaml !== originalYaml;

  /** Save all settings. */
  const handleSave = async () => {
    setSaving(true);
    try {
      await saveConfigRaw(rawYaml);
      setOriginalYaml(rawYaml);
      showStatus(true, 'Settings saved successfully');
    } catch (e) {
      showStatus(false, (e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  /** Save + reload. */
  const handleSaveAndReload = async () => {
    setSaving(true);
    setReloading(true);
    try {
      await saveConfigRaw(rawYaml);
      setOriginalYaml(rawYaml);
      await triggerReload();
      showStatus(true, 'Settings saved and configuration reloaded');
      await load();
    } catch (e) {
      showStatus(false, (e as Error).message);
    } finally {
      setSaving(false);
      setReloading(false);
    }
  };

  const handleReload = async () => {
    setReloading(true);
    try {
      await triggerReload();
      showStatus(true, 'Configuration reloaded successfully');
      await load();
    } catch (e) {
      showStatus(false, (e as Error).message);
    } finally {
      setReloading(false);
    }
  };

  const handleExport = async () => {
    setExporting(true);
    try {
      await fetchConfigExport();
      showStatus(true, 'Configuration exported successfully');
    } catch (e) {
      showStatus(false, (e as Error).message);
    } finally {
      setExporting(false);
    }
  };

  const handleDiscard = () => {
    setRawYaml(originalYaml);
    parseYaml(originalYaml);
    setStatus(null);
  };

  const toggleSecret = (key: string) => {
    setRevealedSecrets(prev => ({ ...prev, [key]: !prev[key] }));
  };

  const copyToClipboard = (value: string) => {
    navigator.clipboard.writeText(value).then(() => {
      showStatus(true, 'Copied to clipboard');
    });
  };

  if (loading) {
    return (
      <div className="flex h-96 items-center justify-center text-slate-400">
        Loading settings...
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">Settings</h1>
          <p className="text-sm text-slate-400">
            Global server configuration
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={handleExport}
            disabled={exporting}
            className="flex items-center gap-2 rounded-md border border-[#334155] bg-[#1e293b] px-4 py-2 text-sm font-medium text-slate-200 transition hover:bg-[#334155] disabled:opacity-50"
          >
            <Download size={14} />
            {exporting ? 'Exporting...' : 'Export'}
          </button>
          <button
            onClick={handleReload}
            disabled={reloading}
            className="flex items-center gap-2 rounded-md border border-[#334155] bg-[#1e293b] px-4 py-2 text-sm font-medium text-slate-200 transition hover:bg-[#334155] disabled:opacity-50"
          >
            <RefreshCw size={14} className={reloading ? 'animate-spin' : ''} />
            Reload
          </button>
        </div>
      </div>

      {/* Status message */}
      {status && (
        <div
          className={`flex items-center gap-2 rounded-md px-4 py-3 text-sm ${
            status.ok
              ? 'bg-emerald-500/10 text-emerald-400'
              : 'bg-red-500/10 text-red-400'
          }`}
        >
          {status.ok ? <CheckCircle size={14} /> : <XCircle size={14} />}
          {status.message}
        </div>
      )}

      {/* Dirty indicator + save bar */}
      {isDirty && (
        <div className="sticky top-0 z-10 flex items-center justify-between rounded-lg border border-amber-500/30 bg-amber-500/10 px-4 py-3">
          <div className="flex items-center gap-2 text-sm text-amber-400">
            <AlertTriangle size={14} />
            You have unsaved changes
          </div>
          <div className="flex items-center gap-2">
            <button
              onClick={handleDiscard}
              className="rounded-md border border-[#334155] bg-[#1e293b] px-3 py-1.5 text-sm text-slate-300 hover:bg-[#334155]"
            >
              Discard
            </button>
            <button
              onClick={handleSave}
              disabled={saving}
              className="flex items-center gap-2 rounded-md bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
            >
              <Save size={14} />
              {saving ? 'Saving...' : 'Save'}
            </button>
            <button
              onClick={handleSaveAndReload}
              disabled={saving || reloading}
              className="flex items-center gap-2 rounded-md bg-emerald-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-emerald-700 disabled:opacity-50"
            >
              <RefreshCw size={14} className={reloading ? 'animate-spin' : ''} />
              Save &amp; Reload
            </button>
          </div>
        </div>
      )}

      {/* Server Status card */}
      <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-5 shadow-md">
        <div className="mb-4 flex items-center gap-2">
          <Activity size={18} className="text-blue-400" />
          <h2 className="text-sm font-semibold text-slate-300">Server Status</h2>
        </div>
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-6">
          <StatusItem label="Status" value={
            <span className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium ${
              health?.status === 'ok'
                ? 'bg-emerald-500/15 text-emerald-400'
                : 'bg-amber-500/15 text-amber-400'
            }`}>
              {health?.status === 'ok' ? <CheckCircle size={12} /> : <XCircle size={12} />}
              {health?.status ?? 'unknown'}
            </span>
          } />
          <StatusItem label="Uptime" value={health?.uptime ?? '--'} />
          <StatusItem label="Version" value={system?.version || '--'} />
          <StatusItem label="Go" value={system?.go_version || '--'} />
          <StatusItem label="OS / Arch" value={system ? `${system.os}/${system.arch}` : '--'} />
          <StatusItem label="Memory" value={system ? formatBytes(system.memory_alloc) : '--'} />
        </div>
      </div>

      {/* Settings sections */}
      {SECTIONS.map(section => (
        <div
          key={section.id}
          className="rounded-lg border border-[#334155] bg-[#1e293b] p-5 shadow-md"
        >
          <div className="mb-5 flex items-center gap-2">
            <span className={section.iconColor}>{section.icon}</span>
            <h2 className="text-sm font-semibold text-slate-300">{section.title}</h2>
          </div>
          <div className="grid grid-cols-1 gap-x-6 gap-y-4 sm:grid-cols-2 lg:grid-cols-3">
            {section.fields.map(field => (
              <FieldInput
                key={field.key}
                field={field}
                value={formValues[field.key] ?? ''}
                onChange={(v) => updateField(field.key, v)}
                revealed={revealedSecrets[field.key] ?? false}
                onToggleReveal={() => toggleSecret(field.key)}
                onCopy={() => copyToClipboard(formValues[field.key] ?? '')}
              />
            ))}
          </div>
        </div>
      ))}

      {/* Bottom save bar */}
      <div className="flex items-center justify-end gap-3 rounded-lg border border-[#334155] bg-[#1e293b] px-5 py-4 shadow-md">
        {isDirty && (
          <span className="mr-auto text-sm text-amber-400">Unsaved changes</span>
        )}
        <button
          onClick={handleExport}
          disabled={exporting}
          className="flex items-center gap-2 rounded-md border border-[#334155] bg-[#0f172a] px-4 py-2 text-sm text-slate-300 hover:bg-[#334155] disabled:opacity-50"
        >
          <Download size={14} />
          Export YAML
        </button>
        <button
          onClick={handleSave}
          disabled={saving || !isDirty}
          className="flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
        >
          <Save size={14} />
          {saving ? 'Saving...' : 'Save Settings'}
        </button>
        <button
          onClick={handleSaveAndReload}
          disabled={saving || reloading || !isDirty}
          className="flex items-center gap-2 rounded-md bg-emerald-600 px-4 py-2 text-sm font-medium text-white hover:bg-emerald-700 disabled:cursor-not-allowed disabled:opacity-50"
        >
          <RefreshCw size={14} className={reloading ? 'animate-spin' : ''} />
          Save &amp; Reload
        </button>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function StatusItem({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div>
      <dt className="text-xs font-medium uppercase text-slate-500">{label}</dt>
      <dd className="mt-1 text-sm text-slate-200">{value}</dd>
    </div>
  );
}

interface FieldInputProps {
  field: FieldDef;
  value: string;
  onChange: (v: string) => void;
  revealed: boolean;
  onToggleReveal: () => void;
  onCopy: () => void;
}

function FieldInput({ field, value, onChange, revealed, onToggleReveal, onCopy }: FieldInputProps) {
  const inputClasses =
    'w-full rounded-md border border-[#334155] bg-[#0f172a] px-3 py-2 text-sm text-slate-200 outline-none transition placeholder:text-slate-600 focus:border-blue-500 focus:ring-1 focus:ring-blue-500/30';

  if (field.type === 'toggle') {
    const isOn = value === 'true';
    return (
      <div className="flex items-center justify-between sm:col-span-1">
        <label className="text-sm text-slate-300">{field.label}</label>
        <button
          type="button"
          role="switch"
          aria-checked={isOn}
          onClick={() => onChange(isOn ? 'false' : 'true')}
          className={`relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors ${
            isOn ? 'bg-blue-600' : 'bg-[#334155]'
          }`}
        >
          <span
            className={`pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow-sm ring-0 transition ${
              isOn ? 'translate-x-5' : 'translate-x-0'
            }`}
          />
        </button>
      </div>
    );
  }

  if (field.type === 'select') {
    return (
      <div>
        <label className="mb-1.5 block text-sm text-slate-300">{field.label}</label>
        <select
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className={inputClasses + ' cursor-pointer'}
        >
          {!value && <option value="">-- select --</option>}
          {field.options?.map(o => (
            <option key={o.value} value={o.value}>{o.label}</option>
          ))}
        </select>
      </div>
    );
  }

  if (field.type === 'secret') {
    return (
      <div>
        <label className="mb-1.5 block text-sm text-slate-300">{field.label}</label>
        <div className="flex gap-1">
          <input
            type={revealed ? 'text' : 'password'}
            value={value}
            onChange={(e) => onChange(e.target.value)}
            placeholder={field.placeholder}
            className={inputClasses + ' flex-1'}
          />
          <button
            type="button"
            onClick={onToggleReveal}
            className="rounded-md border border-[#334155] bg-[#0f172a] px-2 text-slate-400 hover:text-slate-200"
            title={revealed ? 'Hide' : 'Show'}
          >
            {revealed ? <EyeOff size={14} /> : <Eye size={14} />}
          </button>
          <button
            type="button"
            onClick={onCopy}
            className="rounded-md border border-[#334155] bg-[#0f172a] px-2 text-slate-400 hover:text-slate-200"
            title="Copy"
          >
            <Copy size={14} />
          </button>
        </div>
      </div>
    );
  }

  return (
    <div>
      <label className="mb-1.5 block text-sm text-slate-300">{field.label}</label>
      <input
        type={field.type === 'number' ? 'text' : 'text'}
        inputMode={field.type === 'number' ? 'numeric' : undefined}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={field.placeholder}
        className={inputClasses}
      />
      {field.help && (
        <p className="mt-1 text-xs text-slate-500">{field.help}</p>
      )}
    </div>
  );
}
