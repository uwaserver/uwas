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
  Globe,
  ShieldCheck,
} from 'lucide-react';
import {
  fetchConfigRaw,
  saveConfigRaw,
  triggerReload,
  fetchConfigExport,
  fetchHealth,
  fetchSystem,
  fetch2FAStatus,
  setup2FA,
  verify2FA,
  disable2FA,
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

/** Read a YAML array (block sequence) at a given dot-separated key path.
 *  Returns items joined by newlines. Handles both block ("- item") and
 *  flow ("[item1, item2]") formats. */
function yamlGetArray(yaml: string, path: string): string {
  const parts = path.split('.');
  const lines = yaml.split('\n');
  let depth = 0;
  let partIdx = 0;

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
        const colonIdx = trimmed.indexOf(':');
        const afterColon = trimmed.slice(colonIdx + 1).trim();
        // Flow format: [a, b, c]
        if (afterColon.startsWith('[')) {
          const inner = afterColon.slice(1, afterColon.lastIndexOf(']'));
          return inner.split(',').map(s => {
            s = s.trim();
            if ((s.startsWith('"') && s.endsWith('"')) || (s.startsWith("'") && s.endsWith("'"))) {
              s = s.slice(1, -1);
            }
            return s;
          }).filter(Boolean).join('\n');
        }
        // Block format: lines starting with "- "
        const items: string[] = [];
        const expectedIndent = indent + 2;
        for (let j = i + 1; j < lines.length; j++) {
          const jLine = lines[j];
          const jTrimmed = jLine.trimStart();
          if (jTrimmed === '' || jTrimmed.startsWith('#')) continue;
          const jIndent = jLine.length - jTrimmed.length;
          if (jIndent < expectedIndent) break;
          if (jIndent === expectedIndent && jTrimmed.startsWith('- ')) {
            let val = jTrimmed.slice(2).trim();
            if ((val.startsWith('"') && val.endsWith('"')) || (val.startsWith("'") && val.endsWith("'"))) {
              val = val.slice(1, -1);
            }
            items.push(val);
          }
        }
        return items.join('\n');
      }
      partIdx++;
      depth = partIdx;
    }
  }
  return '';
}

/** Set a YAML array at a given dot-separated key path from newline-separated values. */
function yamlSetArray(yaml: string, path: string, value: string): string {
  const items = value.split('\n').map(s => s.trim()).filter(Boolean);
  const parts = path.split('.');
  const lines = yaml.split('\n');
  let depth = 0;
  let partIdx = 0;

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
        const colonIdx = trimmed.indexOf(':');
        const afterColon = trimmed.slice(colonIdx + 1).trim();

        // Remove existing array items (block or flow)
        let removeEnd = i;
        if (afterColon.startsWith('[')) {
          // Flow format — just this line
          removeEnd = i;
        } else if (afterColon === '' || afterColon.startsWith('#')) {
          // Block format — remove subsequent "- " lines
          const expectedIndent = indent + 2;
          removeEnd = i;
          for (let j = i + 1; j < lines.length; j++) {
            const jLine = lines[j];
            const jTrimmed = jLine.trimStart();
            if (jTrimmed === '' || jTrimmed.startsWith('#')) { removeEnd = j; continue; }
            const jIndent = jLine.length - jTrimmed.length;
            if (jIndent >= expectedIndent) {
              removeEnd = j;
            } else {
              break;
            }
          }
        }

        // Build replacement
        const prefix = '  '.repeat(parts.length - 1);
        const itemIndent = '  '.repeat(parts.length);
        const newLines: string[] = [];
        if (items.length === 0) {
          newLines.push(`${prefix}${parts[parts.length - 1]}: []`);
        } else {
          newLines.push(`${prefix}${parts[parts.length - 1]}:`);
          for (const item of items) {
            const formatted = item.includes(':') || item.includes('/') ? `"${item}"` : item;
            newLines.push(`${itemIndent}- ${formatted}`);
          }
        }
        lines.splice(i, removeEnd - i + 1, ...newLines);
        return lines.join('\n');
      }
      partIdx++;
      depth = partIdx;
    }
  }

  // Key not found — append
  const prefix = '  '.repeat(parts.length - 1);
  const itemIndent = '  '.repeat(parts.length);
  if (items.length === 0) return yaml;
  const newLines = [`${prefix}${parts[parts.length - 1]}:`];
  for (const item of items) {
    const formatted = item.includes(':') || item.includes('/') ? `"${item}"` : item;
    newLines.push(`${itemIndent}- ${formatted}`);
  }
  return [...lines, ...newLines].join('\n');
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
  type: 'text' | 'number' | 'toggle' | 'select' | 'secret' | 'textarea';
  placeholder?: string;
  options?: { value: string; label: string }[];
  help?: string;
  fullWidth?: boolean; // span all columns
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
    id: 'trusted_proxies',
    title: 'Trusted Proxies',
    icon: <Globe size={18} />,
    iconColor: 'text-teal-400',
    fields: [
      { key: 'global.trusted_proxies', label: 'Trusted Proxy CIDRs', type: 'textarea', placeholder: '10.0.0.0/8\n172.16.0.0/12\n192.168.0.0/16', help: 'One CIDR per line. Used to trust X-Forwarded-For headers from these sources.', fullWidth: true },
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
// Dynamic / conditional field definitions (not in SECTIONS — rendered inline)
// ---------------------------------------------------------------------------

const BACKUP_LOCAL_FIELDS: FieldDef[] = [
  { key: 'global.backup.local.path', label: 'Local Path', type: 'text', placeholder: '/var/backups/uwas' },
];

const BACKUP_S3_FIELDS: FieldDef[] = [
  { key: 'global.backup.s3.endpoint', label: 'Endpoint', type: 'text', placeholder: 'https://s3.amazonaws.com' },
  { key: 'global.backup.s3.bucket', label: 'Bucket', type: 'text', placeholder: 'my-uwas-backups' },
  { key: 'global.backup.s3.access_key', label: 'Access Key', type: 'secret' },
  { key: 'global.backup.s3.secret_key', label: 'Secret Key', type: 'secret' },
  { key: 'global.backup.s3.region', label: 'Region', type: 'text', placeholder: 'us-east-1' },
];

const BACKUP_SFTP_FIELDS: FieldDef[] = [
  { key: 'global.backup.sftp.host', label: 'Host', type: 'text', placeholder: 'backup.example.com' },
  { key: 'global.backup.sftp.port', label: 'Port', type: 'number', placeholder: '22' },
  { key: 'global.backup.sftp.user', label: 'User', type: 'text', placeholder: 'backup' },
  { key: 'global.backup.sftp.key_file', label: 'Key File', type: 'text', placeholder: '/root/.ssh/id_rsa' },
  { key: 'global.backup.sftp.password', label: 'Password', type: 'secret' },
  { key: 'global.backup.sftp.remote_path', label: 'Remote Path', type: 'text', placeholder: '/backups/uwas' },
];

/** DNS credential fields by provider name. */
const DNS_CREDENTIAL_FIELDS: Record<string, FieldDef[]> = {
  cloudflare: [
    { key: 'global.acme.dns_credentials.api_token', label: 'API Token', type: 'secret' },
  ],
  route53: [
    { key: 'global.acme.dns_credentials.access_key_id', label: 'Access Key ID', type: 'secret' },
    { key: 'global.acme.dns_credentials.secret_access_key', label: 'Secret Access Key', type: 'secret' },
  ],
  digitalocean: [
    { key: 'global.acme.dns_credentials.api_token', label: 'API Token', type: 'secret' },
  ],
};

/** Collect all dynamic field keys so parseYaml can read them. */
const ALL_DYNAMIC_FIELDS: FieldDef[] = [
  ...BACKUP_LOCAL_FIELDS,
  ...BACKUP_S3_FIELDS,
  ...BACKUP_SFTP_FIELDS,
  ...Object.values(DNS_CREDENTIAL_FIELDS).flat(),
  // Generic DNS credential fallback keys
  { key: 'global.acme.dns_credentials.api_key', label: 'API Key', type: 'secret' },
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

  // 2FA state
  const [twoFA, setTwoFA] = useState<{ enabled: boolean } | null>(null);
  const [twoFASetup, setTwoFASetup] = useState<{ secret: string; uri: string } | null>(null);
  const [twoFACode, setTwoFACode] = useState('');
  const [twoFALoading, setTwoFALoading] = useState(false);
  const [twoFAError, setTwoFAError] = useState('');

  // Gather all field keys
  const allFields = SECTIONS.flatMap(s => s.fields);

  /** Parse raw YAML into form values. */
  const parseYaml = useCallback((yaml: string) => {
    const values: Record<string, string> = {};
    for (const f of allFields) {
      if (f.type === 'textarea') {
        values[f.key] = yamlGetArray(yaml, f.key);
      } else {
        values[f.key] = yamlGet(yaml, f.key);
      }
    }
    // Also parse dynamic fields (backup sub-sections, DNS credentials)
    for (const f of ALL_DYNAMIC_FIELDS) {
      values[f.key] = yamlGet(yaml, f.key);
    }
    setFormValues(values);
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  /** Load raw config + health + system info. */
  const load = useCallback(async () => {
    try {
      const [raw, h, s, tfa] = await Promise.all([
        fetchConfigRaw(),
        fetchHealth(),
        fetchSystem(),
        fetch2FAStatus().catch(() => ({ enabled: false })),
      ]);
      setRawYaml(raw.content);
      setOriginalYaml(raw.content);
      parseYaml(raw.content);
      setHealth(h);
      setSystem(s);
      setTwoFA(tfa);
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
    // Check if this is an array-type field (textarea)
    const field = allFields.find(f => f.key === key);
    if (field?.type === 'textarea') {
      setRawYaml(prev => yamlSetArray(prev, key, value));
    } else {
      setRawYaml(prev => yamlSet(prev, key, value));
    }
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

  /** Fill all empty fields with their placeholder (default) values. */
  const applyDefaults = () => {
    const defaults: Record<string, string> = {
      'global.http_listen': ':80',
      'global.https_listen': ':443',
      'global.http3': 'true',
      'global.worker_count': 'auto',
      'global.max_connections': '65536',
      'global.pid_file': '/var/run/uwas.pid',
      'global.web_root': '/var/www',
      'global.log_level': 'info',
      'global.log_format': 'text',
      'global.timeouts.read': '30s',
      'global.timeouts.read_header': '10s',
      'global.timeouts.write': '60s',
      'global.timeouts.idle': '120s',
      'global.timeouts.shutdown_grace': '15s',
      'global.timeouts.max_header_bytes': '1048576',
      'global.admin.enabled': 'true',
      'global.admin.listen': '0.0.0.0:9443',
      'global.mcp.enabled': 'true',
      'global.cache.enabled': 'true',
      'global.cache.memory_limit': '256MB',
      'global.cache.default_ttl': '3600',
      'global.backup.enabled': 'true',
      'global.backup.provider': 'local',
      'global.backup.schedule': '24h',
      'global.backup.keep': '7',
    };

    let yaml = rawYaml;
    let count = 0;
    for (const [key, def] of Object.entries(defaults)) {
      if (!formValues[key] || formValues[key] === '') {
        yaml = yamlSet(yaml, key, def);
        count++;
      }
    }
    if (count > 0) {
      setRawYaml(yaml);
      parseYaml(yaml);
      showStatus(true, `Applied ${count} default values`);
    } else {
      showStatus(true, 'All fields already have values');
    }
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
            onClick={applyDefaults}
            className="flex items-center gap-2 rounded-md border border-emerald-500/30 bg-emerald-500/10 px-4 py-2 text-sm font-medium text-emerald-400 transition hover:bg-emerald-500/20"
          >
            <CheckCircle size={14} />
            Apply Defaults
          </button>
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

          {/* Admin: 2FA Section */}
          {section.id === 'admin' && twoFA && (
            <div className="mt-5 border-t border-[#334155] pt-5">
              <h3 className="mb-4 flex items-center gap-2 text-xs font-semibold uppercase tracking-wider text-slate-500">
                <ShieldCheck size={14} />
                Two-Factor Authentication (TOTP)
                {twoFA.enabled ? (
                  <span className="rounded bg-emerald-500/20 px-1.5 py-0.5 text-[10px] font-medium normal-case text-emerald-400">Enabled</span>
                ) : (
                  <span className="rounded bg-slate-500/20 px-1.5 py-0.5 text-[10px] font-medium normal-case text-slate-400">Disabled</span>
                )}
              </h3>

              {twoFAError && (
                <div className="mb-3 rounded bg-red-500/10 px-3 py-2 text-sm text-red-400">{twoFAError}</div>
              )}

              {!twoFA.enabled && !twoFASetup && (
                <button
                  onClick={async () => {
                    setTwoFALoading(true);
                    setTwoFAError('');
                    try {
                      const data = await setup2FA();
                      setTwoFASetup(data);
                    } catch (err: unknown) {
                      setTwoFAError(err instanceof Error ? err.message : 'Setup failed');
                    } finally {
                      setTwoFALoading(false);
                    }
                  }}
                  disabled={twoFALoading}
                  className="rounded bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
                >
                  {twoFALoading ? 'Setting up...' : 'Enable 2FA'}
                </button>
              )}

              {!twoFA.enabled && twoFASetup && (
                <div className="space-y-4">
                  <div className="rounded bg-[#0f172a] p-4">
                    <p className="mb-2 text-sm text-slate-300">
                      Add this secret to your authenticator app (Google Authenticator, Authy, etc.):
                    </p>
                    <div className="flex items-center gap-2">
                      <code className="flex-1 rounded bg-[#334155] px-3 py-2 text-xs font-mono text-emerald-400 break-all">
                        {twoFASetup.secret}
                      </code>
                      <button
                        onClick={() => navigator.clipboard.writeText(twoFASetup.secret)}
                        className="rounded bg-[#334155] p-2 text-slate-400 hover:text-white"
                        title="Copy secret"
                      >
                        <Copy size={14} />
                      </button>
                    </div>
                    <p className="mt-3 text-xs text-slate-500">
                      Or use this URI: <code className="break-all text-slate-400">{twoFASetup.uri}</code>
                    </p>
                  </div>

                  <div className="flex items-end gap-3">
                    <div>
                      <label className="mb-1 block text-sm font-medium text-slate-300">
                        Verify Code
                      </label>
                      <input
                        type="text"
                        inputMode="numeric"
                        maxLength={6}
                        value={twoFACode}
                        onChange={e => setTwoFACode(e.target.value.replace(/\D/g, ''))}
                        placeholder="000000"
                        className="w-32 rounded border border-[#334155] bg-[#0f172a] px-3 py-2 text-center font-mono text-lg tracking-widest text-slate-200 outline-none focus:border-blue-500"
                      />
                    </div>
                    <button
                      onClick={async () => {
                        setTwoFALoading(true);
                        setTwoFAError('');
                        try {
                          await verify2FA(twoFACode);
                          setTwoFA({ enabled: true });
                          setTwoFASetup(null);
                          setTwoFACode('');
                          showStatus(true, '2FA enabled successfully');
                        } catch (err: unknown) {
                          setTwoFAError(err instanceof Error ? err.message : 'Invalid code');
                        } finally {
                          setTwoFALoading(false);
                        }
                      }}
                      disabled={twoFALoading || twoFACode.length !== 6}
                      className="rounded bg-emerald-600 px-4 py-2 text-sm font-medium text-white hover:bg-emerald-700 disabled:opacity-50"
                    >
                      {twoFALoading ? 'Verifying...' : 'Confirm & Enable'}
                    </button>
                    <button
                      onClick={() => { setTwoFASetup(null); setTwoFACode(''); setTwoFAError(''); }}
                      className="rounded px-3 py-2 text-sm text-slate-400 hover:text-white"
                    >
                      Cancel
                    </button>
                  </div>
                </div>
              )}

              {twoFA.enabled && (
                <div className="flex items-end gap-3">
                  <div>
                    <label className="mb-1 block text-sm font-medium text-slate-300">
                      Current Code (to disable)
                    </label>
                    <input
                      type="text"
                      inputMode="numeric"
                      maxLength={6}
                      value={twoFACode}
                      onChange={e => setTwoFACode(e.target.value.replace(/\D/g, ''))}
                      placeholder="000000"
                      className="w-32 rounded border border-[#334155] bg-[#0f172a] px-3 py-2 text-center font-mono text-lg tracking-widest text-slate-200 outline-none focus:border-blue-500"
                    />
                  </div>
                  <button
                    onClick={async () => {
                      setTwoFALoading(true);
                      setTwoFAError('');
                      try {
                        await disable2FA(twoFACode);
                        setTwoFA({ enabled: false });
                        setTwoFACode('');
                        showStatus(true, '2FA disabled');
                      } catch (err: unknown) {
                        setTwoFAError(err instanceof Error ? err.message : 'Invalid code');
                      } finally {
                        setTwoFALoading(false);
                      }
                    }}
                    disabled={twoFALoading || twoFACode.length !== 6}
                    className="rounded bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700 disabled:opacity-50"
                  >
                    {twoFALoading ? 'Disabling...' : 'Disable 2FA'}
                  </button>
                </div>
              )}
            </div>
          )}

          {/* ACME: DNS Credentials conditional sub-section */}
          {section.id === 'acme' && formValues['global.acme.dns_provider'] && (
            <div className="mt-5 border-t border-[#334155] pt-5">
              <h3 className="mb-4 text-xs font-semibold uppercase tracking-wider text-slate-500">
                DNS Credentials
                <span className="ml-2 font-normal normal-case text-slate-600">
                  ({formValues['global.acme.dns_provider']})
                </span>
              </h3>
              <div className="grid grid-cols-1 gap-x-6 gap-y-4 sm:grid-cols-2 lg:grid-cols-3">
                {(DNS_CREDENTIAL_FIELDS[formValues['global.acme.dns_provider']] ?? [
                  // Generic fallback: show key/value pair inputs
                  { key: 'global.acme.dns_credentials.api_token', label: 'API Token', type: 'secret' as const },
                  { key: 'global.acme.dns_credentials.api_key', label: 'API Key', type: 'secret' as const },
                ]).map(field => (
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
          )}

          {/* Backup: Provider-specific sub-sections */}
          {section.id === 'backup' && formValues['global.backup.provider'] === 'local' && (
            <div className="mt-5 border-t border-[#334155] pt-5">
              <h3 className="mb-4 text-xs font-semibold uppercase tracking-wider text-slate-500">
                Local Storage
              </h3>
              <div className="grid grid-cols-1 gap-x-6 gap-y-4 sm:grid-cols-2 lg:grid-cols-3">
                {BACKUP_LOCAL_FIELDS.map(field => (
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
          )}

          {section.id === 'backup' && formValues['global.backup.provider'] === 's3' && (
            <div className="mt-5 border-t border-[#334155] pt-5">
              <h3 className="mb-4 text-xs font-semibold uppercase tracking-wider text-slate-500">
                S3 Configuration
              </h3>
              <div className="grid grid-cols-1 gap-x-6 gap-y-4 sm:grid-cols-2 lg:grid-cols-3">
                {BACKUP_S3_FIELDS.map(field => (
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
          )}

          {section.id === 'backup' && formValues['global.backup.provider'] === 'sftp' && (
            <div className="mt-5 border-t border-[#334155] pt-5">
              <h3 className="mb-4 text-xs font-semibold uppercase tracking-wider text-slate-500">
                SFTP Configuration
              </h3>
              <div className="grid grid-cols-1 gap-x-6 gap-y-4 sm:grid-cols-2 lg:grid-cols-3">
                {BACKUP_SFTP_FIELDS.map(field => (
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
          )}
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

  if (field.type === 'textarea') {
    return (
      <div className={field.fullWidth ? 'sm:col-span-2 lg:col-span-3' : ''}>
        <label className="mb-1.5 block text-sm text-slate-300">{field.label}</label>
        <textarea
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={field.placeholder}
          rows={5}
          className={inputClasses + ' resize-y font-mono text-xs'}
        />
        {field.help && (
          <p className="mt-1 text-xs text-slate-500">{field.help}</p>
        )}
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
