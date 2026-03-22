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
}

export interface DomainData {
  host: string;
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

export const fetchHealth = () => api<HealthData>('/api/v1/health');
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

export const fetchLogs = () => api<LogEntry[]>('/api/v1/logs');
export const addDomain = (domain: Record<string, unknown>) => api<DomainData>('/api/v1/domains', { method: 'POST', body: JSON.stringify(domain) });
export const deleteDomain = (host: string) => api<{ status: string }>(`/api/v1/domains/${encodeURIComponent(host)}`, { method: 'DELETE' });

export interface DomainDetail {
  host: string;
  aliases: string[] | null;
  type: string;
  ssl: string;
  root: string;
  cache?: { enabled: boolean; ttl: number; rules?: { match: string; ttl: number; bypass: boolean }[] };
  security?: { blocked_paths: string[] | null; waf: boolean; rate_limit?: { requests: number; window: string } };
  php?: { fpm_address: string; index_files: string[] | null; timeout: number; upload_max_size: string };
  proxy?: { upstreams: string[] | null; algorithm: string; health_check?: { path: string; interval: string } };
  redirect?: { target: string; status_code: number };
  htaccess?: { enabled: boolean };
}

export interface CertInfo {
  host: string;
  ssl_mode: string;
  status: string;
  issuer: string;
}

export const fetchDomainDetail = (host: string) => api<DomainDetail>(`/api/v1/domains/${encodeURIComponent(host)}`);
export const fetchCerts = () => api<CertInfo[]>('/api/v1/certs');

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
