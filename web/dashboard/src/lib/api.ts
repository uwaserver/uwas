const BASE = import.meta.env.DEV ? 'http://127.0.0.1:9443' : '';

let token = localStorage.getItem('uwas_token') || '';
let totpCode = '';

export function setToken(t: string) {
  token = t;
  localStorage.setItem('uwas_token', t);
}

export function getToken() {
  return token;
}

export function clearToken() {
  token = '';
  totpCode = '';
  localStorage.removeItem('uwas_token');
  localStorage.removeItem('uwas_totp_verified');
}

export function setTOTPCode(code: string) {
  totpCode = code;
  localStorage.setItem('uwas_totp_verified', 'true');
}

// Pin code for destructive operations
let pinCode = '';
export function setPinCode(pin: string) { pinCode = pin; }
export function clearPinCode() { pinCode = ''; }

// Global pin prompt callback — set by App.tsx, called when API returns pin_required
let pinPromptCallback: ((resolve: (pin: string) => void, reject: () => void) => void) | null = null;
export function onPinRequired(cb: typeof pinPromptCallback) { pinPromptCallback = cb; }

async function api<T>(path: string, options?: RequestInit): Promise<T> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  };
  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }
  if (pinCode) {
    headers['X-Pin-Code'] = pinCode;
  }
  if (totpCode) {
    headers['X-TOTP-Code'] = totpCode;
  }

  const res = await fetch(`${BASE}${path}`, { ...options, headers });

  if (res.status === 401) {
    clearToken();
    window.location.href = '/_uwas/dashboard/login';
    throw new Error('Unauthorized');
  }

  // 2FA required — redirect to login with 2FA prompt
  if (res.status === 403) {
    const body = await res.json().catch(() => ({ error: '' }));
    if (body.error === '2fa_required') {
      localStorage.removeItem('uwas_totp_verified');
      totpCode = '';
      window.location.href = '/_uwas/dashboard/login?2fa=required';
      throw new Error('2FA required');
    }
    if ((body.error === 'pin_required' || body.error === 'invalid_pin') && pinPromptCallback) {
      // Show global pin modal, wait for user input, retry the request
      const pin = await new Promise<string>((resolve, reject) => {
        pinPromptCallback!(resolve, reject);
      });
      pinCode = pin;
      // Retry the same request with pin
      const retryHeaders: Record<string, string> = { ...headers, 'X-Pin-Code': pin };
      const retryRes = await fetch(`${BASE}${path}`, { ...options, headers: retryHeaders });
      pinCode = '';
      if (!retryRes.ok) {
        const retryBody = await retryRes.json().catch(() => ({ error: retryRes.statusText }));
        throw new Error(retryBody.error || retryRes.statusText);
      }
      return retryRes.json();
    }
    if (body.error === 'pin_required' || body.error === 'invalid_pin') {
      throw new Error(body.error);
    }
    throw new Error(body.error || 'Forbidden');
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
  hostname: string;
  cpus: number;
  goroutines: number;
  memory_alloc: number;
  memory_sys: number;
  gc_cycles: number;
  pid: number;
  uptime: string;
  uptime_secs: number;
  os_name?: string;
  kernel?: string;
  ram_total_bytes?: number;
  ram_total_human?: string;
  ram_available_bytes?: number;
  ram_available_human?: string;
  load_1m?: string;
  load_5m?: string;
  load_15m?: string;
  disk_total_bytes?: number;
  disk_total_human?: string;
  disk_free_bytes?: number;
  disk_free_human?: string;
  disk_root_used_bytes?: number;
  timezone?: string;
  package_updates?: string;
  web_root: string;
  domain_count: number;
  disk_used_bytes?: number;
  disk_used_human?: string;
}

export const fetchHealth = () => api<HealthData>('/api/v1/health');
export const fetchSystem = () => api<SystemInfo>('/api/v1/system');
export const fetchStats = () => api<StatsData>('/api/v1/stats');
export const fetchDomains = () => api<DomainData[]>('/api/v1/domains');
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

export const fetchLogs = () => api<LogEntry[]>('/api/v1/logs');
export const addDomain = (domain: Record<string, unknown>) => api<DomainData>('/api/v1/domains', { method: 'POST', body: JSON.stringify(domain) });
export const updateDomain = (host: string, domain: Record<string, unknown>) => api<DomainData>(`/api/v1/domains/${encodeURIComponent(host)}`, { method: 'PUT', body: JSON.stringify(domain) });
export const deleteDomain = (host: string, cleanup = false) => api<{ status: string }>(`/api/v1/domains/${encodeURIComponent(host)}${cleanup ? '?cleanup=true' : ''}`, { method: 'DELETE' });

export interface DomainDetail {
  host: string;
  ip?: string;
  aliases: string[] | null;
  type: string;
  ssl: { mode: string; cert: string; key: string; min_version: string };
  root: string;
  cache?: { enabled: boolean; ttl: number; rules?: { match: string; ttl: number; bypass: boolean }[] };
  security?: { blocked_paths: string[] | null; waf: { enabled: boolean; rules: string[] | null }; rate_limit?: { requests: number; window: string }; ip_whitelist?: string[] | null; ip_blacklist?: string[] | null; hotlink_protection?: { enabled: boolean; allowed_referers: string[] | null; extensions: string[] | null }; geo_block_countries?: string[] | null; geo_allow_countries?: string[] | null };
  resources?: { cpu_percent?: number; memory_mb?: number; pid_max?: number };
  basic_auth?: { enabled: boolean; username: string; password: string };
  bandwidth?: { enabled: boolean; monthly_limit: string; daily_limit: string; action: string };
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
  socket_path?: string;
  system_managed?: boolean;
  disabled?: boolean;
  domain_count: number;
  domains?: string[];
}

export const fetchPHP = () => api<PHPInstall[]>('/api/v1/php');
export const enablePHP = (version: string) => api<{ status: string }>(`/api/v1/php/${version}/enable`, { method: 'POST' });
export const fetchPHPConfigRaw = (version: string) => api<{ content: string }>(`/api/v1/php/${version}/config/raw`);
export const savePHPConfigRaw = (version: string, content: string) => api<{ status: string }>(`/api/v1/php/${version}/config/raw`, { method: 'PUT', body: JSON.stringify({ content }) });
export const fetchPHPConfig = (version: string) => api<Record<string, string>>(`/api/v1/php/${version}/config`);
export const updatePHPConfigKey = (version: string, key: string, value: string) => api<{ status: string }>(`/api/v1/php/${version}/config`, { method: 'PUT', body: JSON.stringify({ key, value }) });
export const disablePHP = (version: string) => api<{ status: string }>(`/api/v1/php/${version}/disable`, { method: 'POST' });

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
export interface WPInstallStatus {
  status: string;
  domain?: string;
  admin_url?: string;
  db_name?: string;
  db_user?: string;
  db_pass?: string;
  output?: string;
  error?: string;
}
export const installWordPress = (domain: string, dbHost?: string) =>
  api<{ status: string }>('/api/v1/wordpress/install', { method: 'POST', body: JSON.stringify({ domain, db_host: dbHost || 'localhost' }) });
export const fetchWPInstallStatus = () => api<WPInstallStatus>('/api/v1/wordpress/install/status');

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

// System Services
export interface SystemService { name: string; display: string; running: boolean; enabled: boolean; active: string; }
export const fetchServices = () => api<SystemService[]>('/api/v1/services');
export const startService = (name: string) => api<{ status: string }>(`/api/v1/services/${encodeURIComponent(name)}/start`, { method: 'POST' });
export const stopService = (name: string) => api<{ status: string }>(`/api/v1/services/${encodeURIComponent(name)}/stop`, { method: 'POST' });
export const restartService = (name: string) => api<{ status: string }>(`/api/v1/services/${encodeURIComponent(name)}/restart`, { method: 'POST' });

// Database
export interface DBStatus { installed: boolean; running: boolean; version: string; backend: string; }
export const startDB = () => api<{ status: string }>('/api/v1/database/start', { method: 'POST' });
export const stopDB = () => api<{ status: string }>('/api/v1/database/stop', { method: 'POST' });
export const restartDB = () => api<{ status: string }>('/api/v1/database/restart', { method: 'POST' });
export interface DBInfo { name: string; user: string; password?: string; host: string; size?: string; tables?: number; }
export const fetchDBStatus = () => api<DBStatus>('/api/v1/database/status');
export const fetchDatabases = () => api<DBInfo[]>('/api/v1/database/list');
export interface DBCreateResult { name: string; user: string; password: string; host: string; }
export const createDatabase = (name: string, user?: string, password?: string) =>
  api<DBCreateResult>('/api/v1/database/create', { method: 'POST', body: JSON.stringify({ name, user, password }) });
export const dropDatabase = (name: string) =>
  api<{ status: string }>(`/api/v1/database/${encodeURIComponent(name)}`, { method: 'DELETE' });
export const installDatabase = () => api<{ status: string; task_id?: string }>('/api/v1/database/install', { method: 'POST' });
export const uninstallDatabase = () => api<{ status: string; output: string }>('/api/v1/database/uninstall', { method: 'POST' });
export const diagnoseDatabase = () => api<Record<string, any>>('/api/v1/database/diagnose');
export interface DBUser { user: string; host: string; }
export const fetchDBUsers = () => api<DBUser[]>('/api/v1/database/users');
export const changeDBPassword = (user: string, host: string, password: string) =>
  api<{ status: string }>('/api/v1/database/users/password', { method: 'POST', body: JSON.stringify({ user, host, password }) });
export const exportDatabase = (name: string) => `${BASE}/api/v1/database/${encodeURIComponent(name)}/export`;
export const importDatabase = async (name: string, file: File) => {
  const headers: Record<string, string> = {};
  if (token) headers['Authorization'] = `Bearer ${token}`;
  const res = await fetch(`${BASE}/api/v1/database/${encodeURIComponent(name)}/import`, { method: 'POST', headers, body: await file.text() });
  if (!res.ok) { const b = await res.json().catch(() => ({ error: res.statusText })); throw new Error(b.error || res.statusText); }
  return res.json();
};

// Docker Database Containers
export interface DockerDBContainer { id: string; name: string; engine: string; image: string; port: number; status: string; running: boolean; root_pass?: string; }
export interface DockerDBListResult { docker: boolean; version?: string; containers: DockerDBContainer[]; }
export const fetchDockerDBs = () => api<DockerDBListResult>('/api/v1/database/docker');
export const createDockerDB = (engine: string, name: string, port: number, root_pass: string, data_dir?: string) =>
  api<DockerDBContainer>('/api/v1/database/docker', { method: 'POST', body: JSON.stringify({ engine, name, port, root_pass, data_dir }) });
export const startDockerDB = (name: string) => api<{ status: string }>(`/api/v1/database/docker/${encodeURIComponent(name)}/start`, { method: 'POST' });
export const stopDockerDB = (name: string) => api<{ status: string }>(`/api/v1/database/docker/${encodeURIComponent(name)}/stop`, { method: 'POST' });
export const removeDockerDB = (name: string) => api<{ status: string }>(`/api/v1/database/docker/${encodeURIComponent(name)}`, { method: 'DELETE' });
export const fetchDockerDBDatabases = (name: string) => api<DBInfo[]>(`/api/v1/database/docker/${encodeURIComponent(name)}/databases`);
export const createDockerDBDatabase = (containerName: string, dbName: string, user?: string, password?: string) =>
  api<DBCreateResult>(`/api/v1/database/docker/${encodeURIComponent(containerName)}/databases`, { method: 'POST', body: JSON.stringify({ name: dbName, user, password }) });
export const dropDockerDBDatabase = (containerName: string, dbName: string) =>
  api<{ status: string }>(`/api/v1/database/docker/${encodeURIComponent(containerName)}/databases/${encodeURIComponent(dbName)}`, { method: 'DELETE' });

// User Login (multi-user auth)
export interface LoginResult { status: string; token: string; user_id: string; username: string; role: string; domains: string[]; expires_at: string; }
export const loginUser = (username: string, password: string) =>
  api<LoginResult>('/api/v1/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) });

// Webhooks
export interface WebhookEntry { url: string; events: string[]; headers: Record<string, string>; secret: string; retry: number; timeout: number; enabled: boolean; }
export const fetchWebhooks = () => api<WebhookEntry[]>('/api/v1/webhooks');
export const createWebhook = (wh: Partial<WebhookEntry>) => api<{ success: boolean }>('/api/v1/webhooks', { method: 'POST', body: JSON.stringify(wh) });
export const deleteWebhook = (id: number) => api<{ success: boolean }>(`/api/v1/webhooks/${id}`, { method: 'DELETE' });
export const testWebhook = (url: string) => api<{ success: boolean; message: string }>('/api/v1/webhooks/test', { method: 'POST', body: JSON.stringify({ url }) });

// Admin Users (multi-user auth)
export interface AdminUser { username: string; role: string; email: string; domains: string[]; created_at: string; api_key?: string; }
export interface AdminUserCreated extends AdminUser { password: string; api_key: string; }
export const fetchAdminUsers = () => api<AdminUser[]>('/api/v1/auth/users');
export const createAdminUser = (user: { username: string; password: string; role: string; email?: string; domains?: string[] }) =>
  api<AdminUserCreated>('/api/v1/auth/users', { method: 'POST', body: JSON.stringify(user) });
export const deleteAdminUser = (username: string) => api<{ status: string }>(`/api/v1/auth/users/${encodeURIComponent(username)}`, { method: 'DELETE' });
export const changeAdminPassword = (username: string, password: string) =>
  api<{ status: string }>(`/api/v1/auth/users/${encodeURIComponent(username)}/password`, { method: 'POST', body: JSON.stringify({ password }) });
export const regenAdminApiKey = (username: string) =>
  api<{ api_key: string }>(`/api/v1/auth/users/${encodeURIComponent(username)}/apikey`, { method: 'POST' });

// Bandwidth
export interface BandwidthStatus { host: string; monthly_bytes: number; daily_bytes: number; monthly_limit: number; daily_limit: number; monthly_pct: number; daily_pct: number; blocked: boolean; throttled: boolean; }
export const fetchBandwidth = () => api<BandwidthStatus[]>('/api/v1/bandwidth');
export const resetBandwidth = (host: string) => api<{ status: string }>(`/api/v1/bandwidth/${encodeURIComponent(host)}/reset`, { method: 'POST' });

// Cron Monitoring
export interface CronExecution { id: string; domain: string; command: string; schedule: string; started_at: string; ended_at: string; duration: number; exit_code: number; success: boolean; output: string; error?: string; }
export interface CronJobStatus { domain: string; command: string; schedule: string; last_run?: CronExecution; last_success?: CronExecution; last_failure?: CronExecution; success_count: number; failure_count: number; consecutive_fail: number; history: CronExecution[]; }
export const fetchCronMonitor = () => api<CronJobStatus[]>('/api/v1/cron/monitor');
export const executeCron = (domain: string, schedule: string, command: string) =>
  api<CronExecution>('/api/v1/cron/execute', { method: 'POST', body: JSON.stringify({ domain, schedule, command }) });

// DNS
export interface DNSResult { domain: string; a: string[]; aaaa: string[]; cname?: string; mx: string[]; ns: string[]; txt: string[]; points_here: boolean; server_ips: string[]; error?: string; }
export const checkDNS = (domain: string) => api<DNSResult>(`/api/v1/dns/${encodeURIComponent(domain)}`);

// DNS record management (Cloudflare)
export interface DNSRecord { id: string; type: string; name: string; content: string; ttl: number; proxied: boolean; priority: number; }
export const fetchDNSRecords = (domain: string) => api<{ zone_id: string; zone: string; records: DNSRecord[] }>(`/api/v1/dns/${encodeURIComponent(domain)}/records`);
export const createDNSRecord = (domain: string, rec: Partial<DNSRecord>) => api<DNSRecord>(`/api/v1/dns/${encodeURIComponent(domain)}/records`, { method: 'POST', body: JSON.stringify(rec) });
export const updateDNSRecord = (domain: string, id: string, rec: Partial<DNSRecord>) => api<DNSRecord>(`/api/v1/dns/${encodeURIComponent(domain)}/records/${id}`, { method: 'PUT', body: JSON.stringify(rec) });
export const deleteDNSRecord = (domain: string, id: string) => api<{ status: string }>(`/api/v1/dns/${encodeURIComponent(domain)}/records/${id}`, { method: 'DELETE' });
export const syncDNS = (domain: string) => api<{ status: string; ip: string }>(`/api/v1/dns/${encodeURIComponent(domain)}/sync`, { method: 'POST' });

// Security
export interface SecurityStats { waf_blocked: number; bot_blocked: number; rate_blocked: number; hotlink_blocked: number; total_blocked: number; }
export interface BlockedRequest { time: string; ip: string; path: string; reason: string; ua: string; }
export const fetchSecurityStats = () => api<SecurityStats>('/api/v1/security/stats');
export const fetchSecurityBlocked = () => api<BlockedRequest[]>('/api/v1/security/blocked');

// Domain health
export interface DomainHealth { host: string; status: string; code: number; ms: number; error?: string; }
export const fetchDomainHealth = () => api<DomainHealth[]>('/api/v1/domains/health');

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

// ── WordPress Site Management ──────────────────────────

export interface WPPlugin { name: string; version: string; status: string; update: string; }
export interface WPTheme { name: string; version: string; status: string; update: string; }
export interface WPSiteHealth { core_update: boolean; plugin_updates: number; theme_updates: number; php_version: string; debug: boolean; ssl: boolean; file_edit: boolean; }
export interface WPPermissions { wp_config: string; wp_content: string; uploads: string; htaccess: string; owner: string; writable: boolean; }
export interface WPSite {
  domain: string; web_root: string; version: string;
  db_name: string; db_user: string; db_host: string;
  site_url: string; admin_url: string;
  plugins: WPPlugin[]; themes: WPTheme[];
  health: WPSiteHealth; permissions: WPPermissions;
  updated_at: string;
}

export const fetchWPSites = () => api<WPSite[]>('/api/v1/wordpress/sites');
export const fetchWPSiteDetail = (domain: string) => api<WPSite>(`/api/v1/wordpress/sites/${encodeURIComponent(domain)}/detail`);
export const wpUpdateCore = (domain: string) =>
  api<{ status: string; output: string }>(`/api/v1/wordpress/sites/${encodeURIComponent(domain)}/update-core`, { method: 'POST' });
export const wpUpdatePlugins = (domain: string) =>
  api<{ status: string; output: string }>(`/api/v1/wordpress/sites/${encodeURIComponent(domain)}/update-plugins`, { method: 'POST' });
export const wpPluginAction = (domain: string, action: string, plugin: string) =>
  api<{ status: string; output: string }>(`/api/v1/wordpress/sites/${encodeURIComponent(domain)}/plugin/${action}/${encodeURIComponent(plugin)}`, { method: 'POST' });
export const wpFixPermissions = (domain: string) =>
  api<{ status: string; output: string }>(`/api/v1/wordpress/sites/${encodeURIComponent(domain)}/fix-permissions`, { method: 'POST' });
export const wpToggleDebug = (domain: string, enable: boolean) =>
  api<{ status: string; debug: boolean }>(`/api/v1/wordpress/sites/${encodeURIComponent(domain)}/debug`, { method: 'POST', body: JSON.stringify({ enable }) });
export const wpErrorLog = (domain: string) =>
  api<{ log: string; size?: number; message?: string }>(`/api/v1/wordpress/sites/${encodeURIComponent(domain)}/error-log`);

// WordPress Users
export interface WPUserInfo { id: string; login: string; email: string; role: string; registered?: string; }
export const wpListUsers = (domain: string) =>
  api<WPUserInfo[]>(`/api/v1/wordpress/sites/${encodeURIComponent(domain)}/users`);
export const wpChangePassword = (domain: string, username: string, password: string) =>
  api<{ status: string }>(`/api/v1/wordpress/sites/${encodeURIComponent(domain)}/change-password`, { method: 'POST', body: JSON.stringify({ username, password }) });

// WordPress Security
export interface WPSecurityStatus {
  xmlrpc_disabled: boolean; file_edit_disabled: boolean; debug_enabled: boolean;
  ssl_forced: boolean; auto_updates_core: string; auto_updates_plugins: boolean;
  auto_updates_themes: boolean; table_prefix: string; php_version: string;
  wp_version: string; directory_listing_blocked: boolean; wp_cron_disabled: boolean;
}
export const wpSecurityStatus = (domain: string) =>
  api<WPSecurityStatus>(`/api/v1/wordpress/sites/${encodeURIComponent(domain)}/security`);
export const wpHarden = (domain: string, opts: { disable_xmlrpc?: boolean; disable_file_edit?: boolean; force_ssl_admin?: boolean; disable_wp_cron?: boolean; block_dir_listing?: boolean }) =>
  api<{ status: string; output: string }>(`/api/v1/wordpress/sites/${encodeURIComponent(domain)}/harden`, { method: 'POST', body: JSON.stringify(opts) });

// WordPress DB Optimization
export const wpOptimizeDB = (domain: string) =>
  api<{ output: string; revisions_deleted: number; spam_deleted: number; trash_deleted: number; transients_cleaned: number; tables_optimized: number }>(`/api/v1/wordpress/sites/${encodeURIComponent(domain)}/optimize-db`, { method: 'POST' });

// ── Per-domain Stats ──────────────────────────────────

export type DomainStatsMap = Record<string, { requests: number; bytes_out: number; status_2xx: number; status_3xx: number; status_4xx: number; status_5xx: number }>;
export const fetchDomainStats = () => api<DomainStatsMap>('/api/v1/stats/domains');

// ── 2FA / TOTP ────────────────────────────────────────

export function fetch2FAStatus(): Promise<{ enabled: boolean }> {
  return api('/api/v1/auth/2fa/status');
}

export function setup2FA(): Promise<{ secret: string; uri: string }> {
  return api('/api/v1/auth/2fa/setup', { method: 'POST' });
}

export function verify2FA(code: string): Promise<{ status: string }> {
  return api('/api/v1/auth/2fa/verify', { method: 'POST', body: JSON.stringify({ code }) });
}

export function disable2FA(code: string): Promise<{ status: string }> {
  return api('/api/v1/auth/2fa/disable', { method: 'POST', body: JSON.stringify({ code }) });
}

// ── Settings (structured key-value) ──────────────────

export function fetchSettings(): Promise<Record<string, any>> {
  return api('/api/v1/settings');
}

export function saveSettings(updates: Record<string, any>): Promise<{ status: string; updated: number }> {
  return api('/api/v1/settings', { method: 'PUT', body: JSON.stringify(updates) });
}

// ── Package Installer ─────────────────────────────────

export interface PackageInfo {
  id: string;
  name: string;
  description: string;
  installed: boolean;
  version?: string;
  category: string;
  required: boolean;
  used_by?: string;
  warning?: string;
  can_remove: boolean;
}

export const fetchPackages = () => api<PackageInfo[]>('/api/v1/packages');
export const installPackage = (id: string) =>
  api<{ status: string; package: string }>('/api/v1/packages/install', { method: 'POST', body: JSON.stringify({ id }) });
export const removePackage = (id: string) =>
  api<{ status: string; package: string }>('/api/v1/packages/install', { method: 'POST', body: JSON.stringify({ id, action: 'remove' }) });

// ── Clone / Staging ─────────────────────────────────

export interface CloneRequest {
  source_domain: string;
  target_domain: string;
  source_root?: string;
  target_root?: string;
  source_db?: string;
  target_db?: string;
  db_user?: string;
  db_pass?: string;
}

export interface CloneResult {
  status: string;
  source_domain: string;
  target_domain: string;
  target_root: string;
  target_db?: string;
  output: string;
  error?: string;
  duration?: string;
}

export const cloneSite = (req: CloneRequest) =>
  api<CloneResult>('/api/v1/clone', { method: 'POST', body: JSON.stringify(req) });

// ── Site Migration (remote → local) ─────────────────

export interface MigrateRequest {
  source_host: string;
  source_port?: string;
  source_path: string;
  ssh_key?: string;
  ssh_pass?: string;
  domain: string;
  local_root?: string;
  db_host?: string;
  db_name?: string;
  db_user?: string;
  db_pass?: string;
}

export interface MigrateResult {
  status: string;
  domain: string;
  files_sync: string;
  db_import: string;
  output: string;
  error?: string;
  started_at: string;
  finished_at?: string;
  duration?: string;
}

export const migrateSite = (req: MigrateRequest) =>
  api<MigrateResult>('/api/v1/migrate', { method: 'POST', body: JSON.stringify(req) });

// ── Installation Tasks ──────────────────────────────

export interface InstallTask {
  id: string;
  type: string;
  name: string;
  action: string;
  status: 'queued' | 'running' | 'done' | 'error';
  output: string;
  error?: string;
  started_at?: string;
  ended_at?: string;
  created_at: string;
}

export const fetchTasks = () => api<InstallTask[]>('/api/v1/tasks');
export const fetchTask = (id: string) => api<InstallTask>(`/api/v1/tasks/${encodeURIComponent(id)}`);

// ── App Process Manager (Node.js, Python, Ruby, Go) ──

export interface AppInstance {
  domain: string;
  runtime: string;
  command: string;
  port: number;
  pid: number;
  running: boolean;
  uptime?: string;
  started_at?: string;
  env?: Record<string, string>;
}

export const fetchApps = () => api<AppInstance[]>('/api/v1/apps');
export const fetchApp = (domain: string) => api<AppInstance>(`/api/v1/apps/${encodeURIComponent(domain)}`);
export const startApp = (domain: string) => api<{ status: string }>(`/api/v1/apps/${encodeURIComponent(domain)}/start`, { method: 'POST' });
export const stopApp = (domain: string) => api<{ status: string }>(`/api/v1/apps/${encodeURIComponent(domain)}/stop`, { method: 'POST' });
export const restartApp = (domain: string) => api<{ status: string }>(`/api/v1/apps/${encodeURIComponent(domain)}/restart`, { method: 'POST' });

// ── Web Terminal ──

export function terminalWSURL(): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const host = import.meta.env.DEV ? '127.0.0.1:9443' : window.location.host;
  const params = token ? `?token=${encodeURIComponent(token)}` : '';
  return `${proto}//${host}/api/v1/terminal${params}`;
}
