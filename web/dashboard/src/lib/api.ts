const BASE = import.meta.env.DEV ? 'http://127.0.0.1:9443' : '';

let token = localStorage.getItem('uwas_token') || '';

export function setToken(t: string) {
  token = t;
  localStorage.setItem('uwas_token', t);
}

export function getToken() {
  return token;
}

export function clearToken() {
  token = '';
  localStorage.removeItem('uwas_token');
}

async function api<T>(path: string, options?: RequestInit): Promise<T> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  };
  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }

  const res = await fetch(`${BASE}${path}`, { ...options, headers });

  if (res.status === 401) {
    clearToken();
    window.location.href = '/_uwas/dashboard/login';
    throw new Error('Unauthorized');
  }

  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(body.error || res.statusText);
  }

  return res.json();
}

export interface HealthData {
  status: string;
  uptime: string;
}

export interface StatsData {
  requests_total: number;
  cache_hits: number;
  cache_misses: number;
  active_conns: number;
  bytes_sent: number;
  uptime: string;
  slow_requests: number;
  latency_p50_ms: number;
  latency_p95_ms: number;
  latency_p99_ms: number;
  latency_max_ms: number;
}

export interface DomainData {
  host: string;
  ip?: string;
  aliases: string[] | null;
  type: string;
  ssl: string;
  root: string;
}

export interface ConfigData {
  global: {
    worker_count: string;
    max_connections: number;
    log_level: string;
    log_format: string;
  };
  domain_count: number;
}

export interface LogEntry {
  time: string;
  method: string;
  host: string;
  path: string;
  status: number;
  bytes: number;
  duration_ms: number;
  remote: string;
  user_agent: string;
}

export interface SystemInfo {
  version: string;
  commit: string;
  go_version: string;
  os: string;
  arch: string;
  cpus: number;
  goroutines: number;
  memory_alloc: number;
  memory_sys: number;
  gc_cycles: number;
}

export const fetchHealth = () => api<HealthData>('/api/v1/health');
export const fetchSystem = () => api<SystemInfo>('/api/v1/system');
export const fetchStats = () => api<StatsData>('/api/v1/stats');
export const fetchDomains = () => api<DomainData[]>('/api/v1/domains');
export const fetchConfig = () => api<ConfigData>('/api/v1/config');
export const fetchMetrics = () => fetch(`${BASE}/api/v1/metrics`, {
  headers: token ? { Authorization: `Bearer ${token}` } : {},
}).then(r => r.text());

export const triggerReload = () => api<{ status: string }>('/api/v1/reload', { method: 'POST' });
export const triggerPurge = (tag?: string) => api<{ status: string }>('/api/v1/cache/purge', {
  method: 'POST',
  body: JSON.stringify(tag ? { tag } : {}),
});
export interface CacheStatsData {
  enabled: boolean;
  hits: number;
  misses: number;
  stales: number;
  entries: number;
  used_bytes: number;
  hit_rate: string;
  domains: {
    host: string;
    enabled: boolean;
    ttl: number;
    tags: string[] | null;
    rules?: { match: string; ttl: number; bypass: boolean }[];
  }[];
}
export const fetchCacheStats = () => api<CacheStatsData>('/api/v1/cache/stats');

export interface MonitorResult {
  host: string;
  status: string;
  latency_ms: number;
  checks: { timestamp: string; status: string; latency_ms: number }[];
  uptime_percent: number;
}
export const fetchMonitor = () => api<MonitorResult[]>('/api/v1/monitor');

export interface AlertData {
  time: string;
  level: string;
  type: string;
  host: string;
  message: string;
}
export const fetchAlerts = () => api<AlertData[]>('/api/v1/alerts');

export interface MCPTool {
  name: string;
  description: string;
  inputSchema: Record<string, unknown>;
}
export const fetchMCPTools = () => api<MCPTool[]>('/api/v1/mcp/tools');
export const callMCPTool = (name: string, input?: Record<string, unknown>) =>
  api<unknown>('/api/v1/mcp/call', { method: 'POST', body: JSON.stringify({ name, input: input ?? {} }) });

export const fetchLogs = () => api<LogEntry[]>('/api/v1/logs');
export const addDomain = (domain: Record<string, unknown>) => api<DomainData>('/api/v1/domains', { method: 'POST', body: JSON.stringify(domain) });
export const updateDomain = (host: string, domain: Record<string, unknown>) => api<DomainData>(`/api/v1/domains/${encodeURIComponent(host)}`, { method: 'PUT', body: JSON.stringify(domain) });
export const deleteDomain = (host: string) => api<{ status: string }>(`/api/v1/domains/${encodeURIComponent(host)}`, { method: 'DELETE' });

export interface DomainDetail {
  host: string;
  aliases: string[] | null;
  type: string;
  ssl: { mode: string; cert: string; key: string; min_version: string };
  root: string;
  cache?: { enabled: boolean; ttl: number; rules?: { match: string; ttl: number; bypass: boolean }[] };
  security?: { blocked_paths: string[] | null; waf: { enabled: boolean; rules: string[] | null }; rate_limit?: { requests: number; window: string } };
  php?: { fpm_address: string; index_files: string[] | null; timeout: number; upload_max_size: string };
  proxy?: { upstreams: string[] | null; algorithm: string; health_check?: { path: string; interval: string } };
  redirect?: { target: string; status: number; preserve_path: boolean };
  htaccess?: { mode: string };
}

export interface CertInfo {
  host: string;
  ssl_mode: string;
  status: string;
  issuer: string;
  expiry?: string;
  days_left?: number;
}

export const renewCert = (host: string) =>
  api<{ status: string }>(`/api/v1/certs/${encodeURIComponent(host)}/renew`, { method: 'POST' });

export interface DomainAnalytics {
  host: string;
  page_views: number;
  unique_ips: number;
  bytes_sent: number;
  status_codes: Record<string, number>;
  top_paths: Record<string, number>;
  hourly_views: number[];
  views_last_hour: number;
  views_last_24h: number;
  views_last_7d: number;
  top_referrers: Record<string, number>;
  user_agents: Record<string, number>;
}

export interface AuditEntry {
  time: string;
  action: string;
  detail: string;
  ip: string;
  success: boolean;
}

export const fetchAuditLog = () => api<AuditEntry[]>('/api/v1/audit');

export const fetchDomainDetail = (host: string) => api<DomainDetail>(`/api/v1/domains/${encodeURIComponent(host)}`);
export const fetchCerts = () => api<CertInfo[]>('/api/v1/certs');
export const fetchAnalytics = () => api<DomainAnalytics[]>('/api/v1/analytics');
export const fetchConfigRaw = () => api<{ content: string }>('/api/v1/config/raw');
export const saveConfigRaw = (content: string) => api<{ status: string }>('/api/v1/config/raw', { method: 'PUT', body: JSON.stringify({ content }) });
export const fetchDomainConfigRaw = (host: string) => api<{ content: string }>(`/api/v1/config/domains/${encodeURIComponent(host)}/raw`);
export const saveDomainConfigRaw = (host: string, content: string) => api<{ status: string }>(`/api/v1/config/domains/${encodeURIComponent(host)}/raw`, { method: 'PUT', body: JSON.stringify({ content }) });

export interface PHPInstall {
  version: string;
  binary: string;
  config_file: string;
  extensions: string[];
  sapi: string;
  running: boolean;
  listen_addr: string;
}

export const fetchPHP = () => api<PHPInstall[]>('/api/v1/php');

export interface PHPInstallInfo {
  distro: string;
  version: string;
  commands: string[];
  packages: string[];
  notes: string;
}
export const fetchPHPInstallInfo = (version?: string) =>
  api<PHPInstallInfo>(`/api/v1/php/install-info${version ? `?version=${version}` : ''}`);

export interface PHPInstallStatus {
  version?: string;
  status: string; // "idle", "running", "done", "error"
  output?: string;
  error?: string;
  distro?: string;
}

export const installPHP = (version: string) =>
  api<{ status: string; version: string }>('/api/v1/php/install', { method: 'POST', body: JSON.stringify({ version }) });

export const fetchPHPInstallStatus = () => api<PHPInstallStatus>('/api/v1/php/install/status');

export interface DomainPHP {
  domain: string;
  version: string;
  listen_addr: string;
  running: boolean;
  pid: number;
  config_overrides: Record<string, string>;
}

export const fetchDomainPHPInstances = () => api<DomainPHP[]>('/api/v1/php/domains');
export const assignDomainPHP = (domain: string, version: string) =>
  api<DomainPHP>('/api/v1/php/domains', { method: 'POST', body: JSON.stringify({domain, version}) });
export const unassignDomainPHP = (domain: string) =>
  api<{status:string}>(`/api/v1/php/domains/${encodeURIComponent(domain)}`, { method: 'DELETE' });
export const startDomainPHP = (domain: string) =>
  api<{status:string}>(`/api/v1/php/domains/${encodeURIComponent(domain)}/start`, { method: 'POST' });
export const stopDomainPHP = (domain: string) =>
  api<{status:string}>(`/api/v1/php/domains/${encodeURIComponent(domain)}/stop`, { method: 'POST' });
export const fetchDomainPHPConfig = (domain: string) =>
  api<Record<string,string>>(`/api/v1/php/domains/${encodeURIComponent(domain)}/config`);
export const updateDomainPHPConfig = (domain: string, key: string, value: string) =>
  api<{status:string}>(`/api/v1/php/domains/${encodeURIComponent(domain)}/config`,
    { method: 'PUT', body: JSON.stringify({key, value}) });

export interface BackupInfo {
  name: string;
  size: number;
  created: string;
  provider: string;
}

export interface BackupSchedule {
  enabled: boolean;
  interval: string;
  keep: number;
  last_backup: string;
  next_backup: string;
}

export const fetchBackups = () => api<BackupInfo[]>('/api/v1/backups');
export const createBackup = (provider?: string) =>
  api<BackupInfo>('/api/v1/backups', { method: 'POST', body: JSON.stringify({ provider: provider || 'local' }) });
export const restoreBackup = (name: string, provider: string) =>
  api<{ status: string }>('/api/v1/backups/restore', { method: 'POST', body: JSON.stringify({ name, provider }) });
export const deleteBackup = (name: string, provider?: string) =>
  api<{ status: string }>(`/api/v1/backups/${encodeURIComponent(name)}?provider=${provider || 'local'}`, { method: 'DELETE' });
export const fetchBackupSchedule = () => api<BackupSchedule>('/api/v1/backups/schedule');
export const updateBackupSchedule = (schedule: Partial<BackupSchedule>) =>
  api<{ status: string }>('/api/v1/backups/schedule', { method: 'PUT', body: JSON.stringify(schedule) });

export interface UnknownDomainEntry {
  host: string;
  hits: number;
  first_seen: string;
  last_seen: string;
  blocked: boolean;
}

export const fetchUnknownDomains = () => api<UnknownDomainEntry[]>('/api/v1/unknown-domains');
export const blockUnknownDomain = (host: string) =>
  api<{ status: string }>(`/api/v1/unknown-domains/${encodeURIComponent(host)}/block`, { method: 'POST' });
export const unblockUnknownDomain = (host: string) =>
  api<{ status: string }>(`/api/v1/unknown-domains/${encodeURIComponent(host)}/unblock`, { method: 'POST' });
export const dismissUnknownDomain = (host: string) =>
  api<{ status: string }>(`/api/v1/unknown-domains/${encodeURIComponent(host)}`, { method: 'DELETE' });

export interface SiteUser {
  username: string;
  domain: string;
  home_dir: string;
  web_dir: string;
}

export interface SiteUserCreated extends SiteUser {
  password: string;
  server_ip: string;
  port: string;
}

export const fetchUsers = () => api<SiteUser[]>('/api/v1/users');
export const createUser = (domain: string) =>
  api<SiteUserCreated>('/api/v1/users', { method: 'POST', body: JSON.stringify({ domain }) });
export const deleteUser = (domain: string) =>
  api<{ status: string }>(`/api/v1/users/${encodeURIComponent(domain)}`, { method: 'DELETE' });

// WordPress
export const installWordPress = (domain: string, dbHost?: string) =>
  api<{ status: string }>('/api/v1/wordpress/install', { method: 'POST', body: JSON.stringify({ domain, db_host: dbHost || 'localhost' }) });
export const fetchWPInstallStatus = () => api<{ status: string; domain?: string; admin_url?: string; db_name?: string; db_user?: string; db_pass?: string; output?: string; error?: string }>('/api/v1/wordpress/install/status');

// File manager
export interface FileEntry { name: string; path: string; is_dir: boolean; size: number; mod_time: string; mode: string; }
export const fetchFiles = (domain: string, path?: string) => api<FileEntry[]>(`/api/v1/files/${encodeURIComponent(domain)}/list?path=${encodeURIComponent(path || '.')}`);
export const readFile = (domain: string, path: string) => api<{ content: string }>(`/api/v1/files/${encodeURIComponent(domain)}/read?path=${encodeURIComponent(path)}`);
export const writeFile = (domain: string, path: string, content: string) => api<{ status: string }>(`/api/v1/files/${encodeURIComponent(domain)}/write`, { method: 'PUT', body: JSON.stringify({ path, content }) });
export const deleteFile = (domain: string, path: string) => api<{ status: string }>(`/api/v1/files/${encodeURIComponent(domain)}/delete?path=${encodeURIComponent(path)}`, { method: 'DELETE' });
export const createDir = (domain: string, path: string) => api<{ status: string }>(`/api/v1/files/${encodeURIComponent(domain)}/mkdir`, { method: 'POST', body: JSON.stringify({ path }) });
export const fetchDiskUsage = (domain: string) => api<{ domain: string; bytes: number; human: string }>(`/api/v1/files/${encodeURIComponent(domain)}/disk-usage`);

// Cron
export interface CronJob { schedule: string; command: string; domain: string; comment: string; }
export const fetchCronJobs = () => api<CronJob[]>('/api/v1/cron');
export const addCronJob = (job: { schedule: string; command: string; domain?: string; comment?: string }) => api<{ status: string }>('/api/v1/cron', { method: 'POST', body: JSON.stringify(job) });
export const deleteCronJob = (schedule: string, command: string) => api<{ status: string }>('/api/v1/cron', { method: 'DELETE', body: JSON.stringify({ schedule, command }) });

// Firewall
export interface FirewallRule { number: number; action: string; from: string; to: string; port: string; proto: string; }
export interface FirewallStatus { active: boolean; backend: string; rules: FirewallRule[]; }
export const fetchFirewall = () => api<FirewallStatus>('/api/v1/firewall');
export const firewallAllow = (port: string, proto?: string) => api<{ status: string }>('/api/v1/firewall/allow', { method: 'POST', body: JSON.stringify({ port, proto }) });
export const firewallDeny = (port: string, proto?: string) => api<{ status: string }>('/api/v1/firewall/deny', { method: 'POST', body: JSON.stringify({ port, proto }) });
export const firewallDeleteRule = (number: number) => api<{ status: string }>(`/api/v1/firewall/${number}`, { method: 'DELETE' });
export const firewallEnable = () => api<{ status: string }>('/api/v1/firewall/enable', { method: 'POST' });
export const firewallDisable = () => api<{ status: string }>('/api/v1/firewall/disable', { method: 'POST' });

// SSH Keys
export const fetchSSHKeys = (domain: string) => api<string[]>(`/api/v1/users/${encodeURIComponent(domain)}/ssh-keys`);
export const addSSHKey = (domain: string, publicKey: string) => api<{ status: string }>(`/api/v1/users/${encodeURIComponent(domain)}/ssh-keys`, { method: 'POST', body: JSON.stringify({ public_key: publicKey }) });
export const deleteSSHKey = (domain: string, fingerprint: string) => api<{ status: string }>(`/api/v1/users/${encodeURIComponent(domain)}/ssh-keys`, { method: 'DELETE', body: JSON.stringify({ fingerprint }) });

// Database
export interface DBStatus { installed: boolean; running: boolean; version: string; backend: string; }
export interface DBInfo { name: string; user: string; password?: string; host: string; size?: string; tables?: number; }
export const fetchDBStatus = () => api<DBStatus>('/api/v1/database/status');
export const fetchDatabases = () => api<DBInfo[]>('/api/v1/database/list');
export const createDatabase = (name: string, user?: string, password?: string) =>
  api<{ status: string; name: string; user: string }>('/api/v1/database/create', { method: 'POST', body: JSON.stringify({ name, user, password }) });
export const dropDatabase = (name: string) =>
  api<{ status: string }>(`/api/v1/database/${encodeURIComponent(name)}`, { method: 'DELETE' });
export const installDatabase = () => api<{ status: string }>('/api/v1/database/install', { method: 'POST' });

// DNS
export interface DNSResult { domain: string; a: string[]; aaaa: string[]; cname?: string; mx: string[]; ns: string[]; txt: string[]; points_here: boolean; server_ips: string[]; error?: string; }
export const checkDNS = (domain: string) => api<DNSResult>(`/api/v1/dns/${encodeURIComponent(domain)}`);

// Security
export interface SecurityStats { waf_blocked: number; bot_blocked: number; rate_blocked: number; hotlink_blocked: number; total_blocked: number; }
export interface BlockedRequest { time: string; ip: string; path: string; reason: string; ua: string; }
export const fetchSecurityStats = () => api<SecurityStats>('/api/v1/security/stats');
export const fetchSecurityBlocked = () => api<BlockedRequest[]>('/api/v1/security/blocked');

// Server IPs
export interface ServerIPInfo { ip: string; version: number; interface: string; primary: boolean; }
export const fetchServerIPs = () => api<{ ips: ServerIPInfo[]; public_ip: string }>('/api/v1/system/ips');

// Self-update
export interface UpdateInfo { current_version: string; latest_version: string; update_available: boolean; release_url: string; published_at: string; release_notes: string; download_url: string; }
export const checkUpdate = () => api<UpdateInfo>('/api/v1/system/update-check');
export const performUpdate = () => api<{ status: string; from: string; to: string; message: string }>('/api/v1/system/update', { method: 'POST' });

/** SSE stats endpoint URL (with auth token as query param for EventSource). */
export function sseStatsURL(): string {
  const params = token ? `?token=${encodeURIComponent(token)}` : '';
  return `${BASE}/api/v1/sse/stats${params}`;
}

/** Download the current server config as a YAML file. */
export async function fetchConfigExport(): Promise<void> {
  const headers: Record<string, string> = {};
  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }

  const res = await fetch(`${BASE}/api/v1/config/export`, { headers });

  if (res.status === 401) {
    clearToken();
    window.location.href = '/_uwas/dashboard/login';
    throw new Error('Unauthorized');
  }

  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(body.error || res.statusText);
  }

  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = 'uwas-config.yaml';
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}
