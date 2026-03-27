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
  Users,
} from 'lucide-react';
import {
  fetchConfigRaw,
  triggerReload,
  fetchConfigExport,
  fetchHealth,
  fetchSystem,
  fetchSettings,
  saveSettings,
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

type SettingsTab = 'general' | 'security' | 'performance' | 'integrations';
const SETTINGS_TABS: { id: SettingsTab; label: string }[] = [
  { id: 'general', label: 'General' },
  { id: 'security', label: 'Security' },
  { id: 'performance', label: 'Performance' },
  { id: 'integrations', label: 'Integrations' },
];

const SECTION_GROUPS: Record<string, SettingsTab> = {
  server: 'general', timeouts: 'general', logging: 'general',
  admin: 'security', acme: 'security', users: 'security', mcp: 'security',
  cache: 'performance',
  backup: 'integrations', alerting: 'integrations', trusted_proxies: 'integrations',
};

const SECTIONS: SectionDef[] = [
  {
    id: 'server',
    title: 'Server',
    icon: <Server size={18} />,
    iconColor: 'text-blue-400',
    fields: [
      { key: 'global.http_listen', label: 'HTTP Listen', type: 'text', placeholder: ':80' },
      { key: 'global.https_listen', label: 'HTTPS Listen', type: 'text', placeholder: ':443' },
      { key: 'global.http3', label: 'HTTP/3 (QUIC)', type: 'toggle', help: 'Enable HTTP/3 via QUIC. Requires HTTPS. Advertised via Alt-Svc header.' },
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
      { key: 'global.admin.pin_code', label: 'Pin Code', type: 'secret', help: 'Required for destructive operations (delete, uninstall). Set in YAML config.' },
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
    title: 'Alerting & Notifications',
    icon: <AlertTriangle size={18} />,
    iconColor: 'text-yellow-400',
    fields: [
      { key: 'global.alerting.enabled', label: 'Enabled', type: 'toggle' },
      { key: 'global.alerting.webhook_url', label: 'Generic Webhook URL', type: 'text', placeholder: 'https://example.com/webhook', help: 'Receives JSON POST with alert data' },
      { key: 'global.alerting.slack_url', label: 'Slack Webhook URL', type: 'text', placeholder: 'https://hooks.slack.com/services/T.../B.../xxx', help: 'Slack > Apps > Incoming Webhooks > Add' },
      { key: 'global.alerting.telegram_token', label: 'Telegram Bot Token', type: 'secret', placeholder: '123456789:ABCdefGHIjklMNOpqrSTUvwxYZ', help: 'Message @BotFather on Telegram → /newbot' },
      { key: 'global.alerting.telegram_chat_id', label: 'Telegram Chat ID', type: 'text', placeholder: '-1001234567890', help: 'Use @userinfobot or @getidsbot to find your chat ID' },
      { key: 'global.alerting.email_smtp_host', label: 'SMTP Host', type: 'text', placeholder: 'smtp.gmail.com:587' },
      { key: 'global.alerting.email_from', label: 'Email From', type: 'text', placeholder: 'alerts@example.com' },
      { key: 'global.alerting.email_to', label: 'Email To', type: 'text', placeholder: 'admin@example.com' },
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
  {
    id: 'users',
    title: 'Multi-User Auth',
    icon: <Users size={18} />,
    iconColor: 'text-cyan-400',
    fields: [
      { key: 'global.users.enabled', label: 'Enable Multi-User Auth', type: 'toggle' },
      { key: 'global.users.allow_reseller', label: 'Allow Resellers', type: 'toggle' },
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
  { key: 'global.backup.s3.endpoint', label: 'S3 Endpoint', type: 'text', placeholder: 'https://s3.amazonaws.com', help: 'AWS: https://s3.amazonaws.com · Wasabi: https://s3.wasabisys.com · MinIO: http://localhost:9000' },
  { key: 'global.backup.s3.bucket', label: 'Bucket Name', type: 'text', placeholder: 'my-uwas-backups' },
  { key: 'global.backup.s3.region', label: 'Region', type: 'text', placeholder: 'us-east-1', help: 'AWS region (e.g. eu-west-1, ap-southeast-1)' },
  { key: 'global.backup.s3.access_key', label: 'Access Key ID', type: 'secret', help: 'AWS IAM > Users > Security Credentials > Access Keys' },
  { key: 'global.backup.s3.secret_key', label: 'Secret Access Key', type: 'secret' },
];

const BACKUP_SFTP_FIELDS: FieldDef[] = [
  { key: 'global.backup.sftp.host', label: 'SFTP Host', type: 'text', placeholder: 'backup.example.com', help: 'Hostname or IP of the backup server' },
  { key: 'global.backup.sftp.port', label: 'Port', type: 'number', placeholder: '22' },
  { key: 'global.backup.sftp.user', label: 'Username', type: 'text', placeholder: 'backup' },
  { key: 'global.backup.sftp.key_file', label: 'SSH Key File', type: 'text', placeholder: '/root/.ssh/id_rsa', help: 'Path to private key on this server' },
  { key: 'global.backup.sftp.password', label: 'Password', type: 'secret', help: 'Alternative to SSH key (key preferred)' },
  { key: 'global.backup.sftp.remote_path', label: 'Remote Path', type: 'text', placeholder: '/backups/uwas', help: 'Directory on the remote server' },
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
  const [rawYaml, setRawYaml] = useState(''); // raw YAML for dynamic fields (DNS creds)
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

  /** Load structured settings + raw config + health + system info. */
  const load = useCallback(async () => {
    try {
      const [settings, raw, h, s, tfa] = await Promise.all([
        fetchSettings().catch(() => ({})),
        fetchConfigRaw(),
        fetchHealth(),
        fetchSystem(),
        fetch2FAStatus().catch(() => ({ enabled: false })),
      ]);
      setRawYaml(raw.content);
      // Prefer structured API values over YAML parsing
      const values: Record<string, string> = {};
      for (const [k, v] of Object.entries(settings)) {
        values[k] = v === null || v === undefined ? '' : String(v);
      }
      // Also parse dynamic fields from YAML (DNS credentials etc.)
      for (const f of ALL_DYNAMIC_FIELDS) {
        if (!values[f.key]) values[f.key] = yamlGet(rawYaml || raw.content, f.key);
      }
      setFormValues(values);
      setHealth(h);
      setSystem(s);
      setTwoFA(tfa);
    } catch {
      // ignore
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  /** Show a temporary status message. */
  const showStatus = (ok: boolean, message: string) => {
    setStatus({ ok, message });
    if (statusTimeout.current) clearTimeout(statusTimeout.current);
    statusTimeout.current = setTimeout(() => setStatus(null), 5000);
  };

  // Track which fields were changed
  const [dirtyKeys, setDirtyKeys] = useState<Set<string>>(new Set());

  /** Update a single form value. */
  const updateField = (key: string, value: string) => {
    setFormValues(prev => ({ ...prev, [key]: value }));
    setDirtyKeys(prev => new Set(prev).add(key));
  };

  const isDirty = dirtyKeys.size > 0;

  /** Save changed settings via structured API. */
  const handleSave = async () => {
    setSaving(true);
    try {
      // Build updates from dirty keys
      const updates: Record<string, any> = {};
      for (const key of dirtyKeys) {
        const val = formValues[key] ?? '';
        // Detect field type for proper serialization
        const field = allFields.find(f => f.key === key);
        if (field?.type === 'toggle') {
          updates[key] = val === 'true';
        } else if (field?.type === 'number') {
          updates[key] = Number(val) || 0;
        } else {
          updates[key] = val;
        }
      }
      await saveSettings(updates);
      setDirtyKeys(new Set());
      showStatus(true, `Saved ${Object.keys(updates).length} settings`);
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
      // Save via structured API first
      if (dirtyKeys.size > 0) {
        const updates: Record<string, any> = {};
        for (const key of dirtyKeys) {
          const val = formValues[key] ?? '';
          const field = allFields.find(f => f.key === key);
          if (field?.type === 'toggle') updates[key] = val === 'true';
          else if (field?.type === 'number') updates[key] = Number(val) || 0;
          else updates[key] = val;
        }
        await saveSettings(updates);
        setDirtyKeys(new Set());
      }
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
    setDirtyKeys(new Set());
    load(); // re-fetch from server
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

    let count = 0;
    const newDirty = new Set(dirtyKeys);
    const newValues = { ...formValues };
    for (const [key, def] of Object.entries(defaults)) {
      if (!formValues[key] || formValues[key] === '' || formValues[key] === '0' || formValues[key] === '0s') {
        newValues[key] = def;
        newDirty.add(key);
        count++;
      }
    }
    if (count > 0) {
      setFormValues(newValues);
      setDirtyKeys(newDirty);
      showStatus(true, `Applied ${count} default values — click Save to persist`);
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

  const [settingsTab, setSettingsTab] = useState<SettingsTab>('general');

  if (loading) {
    return (
      <div className="flex h-96 items-center justify-center text-muted-foreground">
        Loading settings...
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">Settings</h1>
          <p className="text-sm text-muted-foreground">
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
            className="flex items-center gap-2 rounded-md border border-border bg-card px-4 py-2 text-sm font-medium text-foreground transition hover:bg-accent disabled:opacity-50"
          >
            <Download size={14} />
            {exporting ? 'Exporting...' : 'Export'}
          </button>
          <button
            onClick={handleReload}
            disabled={reloading}
            className="flex items-center gap-2 rounded-md border border-border bg-card px-4 py-2 text-sm font-medium text-foreground transition hover:bg-accent disabled:opacity-50"
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
              className="rounded-md border border-border bg-card px-3 py-1.5 text-sm text-card-foreground hover:bg-accent"
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

      {/* Tabs */}
      <div className="flex gap-1 rounded-lg bg-background p-1">
        {SETTINGS_TABS.map(t => (
          <button key={t.id} onClick={() => setSettingsTab(t.id)}
            className={`flex-1 rounded-md py-2 text-sm font-medium transition ${settingsTab === t.id ? 'bg-card text-foreground shadow' : 'text-muted-foreground hover:text-card-foreground'}`}>
            {t.label}
          </button>
        ))}
      </div>

      {/* Server Status card */}
      <div className="rounded-lg border border-border bg-card p-5 shadow-md">
        <div className="mb-4 flex items-center gap-2">
          <Activity size={18} className="text-blue-400" />
          <h2 className="text-sm font-semibold text-card-foreground">Server Status</h2>
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

      {/* Settings sections (filtered by tab) */}
      {SECTIONS.filter(s => SECTION_GROUPS[s.id] === settingsTab).map(section => (
        <div
          key={section.id}
          className="rounded-lg border border-border bg-card p-5 shadow-md"
        >
          <div className="mb-5 flex items-center gap-2">
            <span className={section.iconColor}>{section.icon}</span>
            <h2 className="text-sm font-semibold text-card-foreground">{section.title}</h2>
          </div>
          <div className="space-y-4">
            {/* Toggles row */}
            {section.fields.some(f => f.type === 'toggle') && (
              <div className="flex flex-wrap gap-x-8 gap-y-3 rounded-lg bg-background px-4 py-3">
                {section.fields.filter(f => f.type === 'toggle').map(field => (
                  <FieldInput
                    key={field.key}
                    field={field}
                    value={formValues[field.key] ?? ''}
                    onChange={(v) => updateField(field.key, v)}
                    revealed={false}
                    onToggleReveal={() => {}}
                    onCopy={() => {}}
                  />
                ))}
              </div>
            )}
            {/* Other fields in 2-column grid */}
            <div className="grid grid-cols-1 gap-x-6 gap-y-4 sm:grid-cols-2">
              {section.fields.filter(f => f.type !== 'toggle').map(field => (
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

          {/* Admin: 2FA Section */}
          {section.id === 'admin' && twoFA && (
            <div className="mt-5 border-t border-border pt-5">
              <h3 className="mb-4 flex items-center gap-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                <ShieldCheck size={14} />
                Two-Factor Authentication (TOTP)
                {twoFA.enabled ? (
                  <span className="rounded bg-emerald-500/20 px-1.5 py-0.5 text-[10px] font-medium normal-case text-emerald-400">Enabled</span>
                ) : (
                  <span className="rounded bg-slate-500/20 px-1.5 py-0.5 text-[10px] font-medium normal-case text-muted-foreground">Disabled</span>
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
                  <div className="rounded bg-background p-4">
                    <p className="mb-2 text-sm text-card-foreground">
                      Add this secret to your authenticator app (Google Authenticator, Authy, etc.):
                    </p>
                    <div className="flex items-center gap-2">
                      <code className="flex-1 rounded bg-accent px-3 py-2 text-xs font-mono text-emerald-400 break-all">
                        {twoFASetup.secret}
                      </code>
                      <button
                        onClick={() => navigator.clipboard.writeText(twoFASetup.secret)}
                        className="rounded bg-accent p-2 text-muted-foreground hover:text-white"
                        title="Copy secret"
                      >
                        <Copy size={14} />
                      </button>
                    </div>
                    <p className="mt-3 text-xs text-muted-foreground">
                      Or use this URI: <code className="break-all text-muted-foreground">{twoFASetup.uri}</code>
                    </p>
                  </div>

                  <div className="flex items-end gap-3">
                    <div>
                      <label className="mb-1 block text-sm font-medium text-card-foreground">
                        Verify Code
                      </label>
                      <input
                        type="text"
                        inputMode="numeric"
                        maxLength={6}
                        value={twoFACode}
                        onChange={e => setTwoFACode(e.target.value.replace(/\D/g, ''))}
                        placeholder="000000"
                        className="w-32 rounded border border-border bg-background px-3 py-2 text-center font-mono text-lg tracking-widest text-foreground outline-none focus:border-blue-500"
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
                      className="rounded px-3 py-2 text-sm text-muted-foreground hover:text-white"
                    >
                      Cancel
                    </button>
                  </div>
                </div>
              )}

              {twoFA.enabled && (
                <div className="flex items-end gap-3">
                  <div>
                    <label className="mb-1 block text-sm font-medium text-card-foreground">
                      Current Code (to disable)
                    </label>
                    <input
                      type="text"
                      inputMode="numeric"
                      maxLength={6}
                      value={twoFACode}
                      onChange={e => setTwoFACode(e.target.value.replace(/\D/g, ''))}
                      placeholder="000000"
                      className="w-32 rounded border border-border bg-background px-3 py-2 text-center font-mono text-lg tracking-widest text-foreground outline-none focus:border-blue-500"
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
            <div className="mt-5 border-t border-border pt-5">
              <h3 className="mb-4 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                DNS Credentials
                <span className="ml-2 font-normal normal-case text-muted-foreground">
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
            <div className="mt-5 border-t border-border pt-5">
              <h3 className="mb-4 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
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
            <div className="mt-5 border-t border-border pt-5">
              <h3 className="mb-4 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
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
            <div className="mt-5 border-t border-border pt-5">
              <h3 className="mb-4 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
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
      <div className="flex items-center justify-end gap-3 rounded-lg border border-border bg-card px-5 py-4 shadow-md">
        {isDirty && (
          <span className="mr-auto text-sm text-amber-400">Unsaved changes</span>
        )}
        <button
          onClick={handleExport}
          disabled={exporting}
          className="flex items-center gap-2 rounded-md border border-border bg-background px-4 py-2 text-sm text-card-foreground hover:bg-accent disabled:opacity-50"
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
      <dt className="text-xs font-medium uppercase text-muted-foreground">{label}</dt>
      <dd className="mt-1 text-sm text-foreground">{value}</dd>
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
    'w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground outline-none transition placeholder:text-muted-foreground focus:border-blue-500 focus:ring-1 focus:ring-blue-500/30';

  if (field.type === 'toggle') {
    const isOn = value === 'true';
    return (
      <div className="flex items-center justify-between sm:col-span-1">
        <label className="text-sm text-card-foreground">{field.label}</label>
        <button
          type="button"
          role="switch"
          aria-checked={isOn}
          onClick={() => onChange(isOn ? 'false' : 'true')}
          className={`relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors ${
            isOn ? 'bg-blue-600' : 'bg-accent'
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
        <label className="mb-1.5 block text-sm text-card-foreground">{field.label}</label>
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
        <label className="mb-1.5 block text-sm text-card-foreground">{field.label}</label>
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
            className="rounded-md border border-border bg-background px-2 text-muted-foreground hover:text-foreground"
            title={revealed ? 'Hide' : 'Show'}
          >
            {revealed ? <EyeOff size={14} /> : <Eye size={14} />}
          </button>
          <button
            type="button"
            onClick={onCopy}
            className="rounded-md border border-border bg-background px-2 text-muted-foreground hover:text-foreground"
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
      <div className={field.fullWidth ? 'sm:col-span-2' : ''}>
        <label className="mb-1.5 block text-sm text-card-foreground">{field.label}</label>
        <textarea
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={field.placeholder}
          rows={5}
          className={inputClasses + ' resize-y font-mono text-xs'}
        />
        {field.help && (
          <p className="mt-1 text-xs text-muted-foreground">{field.help}</p>
        )}
      </div>
    );
  }

  return (
    <div>
      <label className="mb-1.5 block text-sm text-card-foreground">{field.label}</label>
      <input
        type={field.type === 'number' ? 'text' : 'text'}
        inputMode={field.type === 'number' ? 'numeric' : undefined}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={field.placeholder}
        className={inputClasses}
      />
      {field.help && (
        <p className="mt-1 text-xs text-muted-foreground">{field.help}</p>
      )}
    </div>
  );
}
