export type DebugLogLevel = 'info' | 'success' | 'warn' | 'error';

export interface DebugLogEntry {
  id: number;
  time: string;
  level: DebugLogLevel;
  scope: string;
  message: string;
  detail?: string;
  duration_ms?: number;
}

type Listener = () => void;

const maxEntries = 500;
const storageKey = 'uwas_debug_log_enabled';

let entries: DebugLogEntry[] = [];
let enabled = localStorage.getItem(storageKey) === 'true';
let nextID = 1;
const listeners = new Set<Listener>();

function emitChange() {
  for (const listener of listeners) listener();
}

export function subscribeDebugLog(listener: Listener) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function getDebugLogSnapshot() {
  return { enabled, entries };
}

export function setDebugLogEnabled(value: boolean) {
  enabled = value;
  localStorage.setItem(storageKey, value ? 'true' : 'false');
  emitChange();
}

export function clearDebugLog() {
  entries = [];
  emitChange();
}

export function addDebugLog(entry: Omit<DebugLogEntry, 'id' | 'time'>) {
  if (!enabled) return;
  entries = [
    {
      id: nextID++,
      time: new Date().toISOString(),
      ...entry,
    },
    ...entries,
  ].slice(0, maxEntries);
  emitChange();
}

export function formatDebugDetail(value: unknown): string | undefined {
  if (value === undefined || value === null || value === '') return undefined;
  const limit = 30000;
  const truncate = (s: string) => (s.length > limit ? `${s.slice(0, limit)}\n... truncated ${s.length - limit} chars` : s);
  if (typeof value === 'string') return truncate(redactDebugText(value));
  try {
    return truncate(redactDebugText(JSON.stringify(value, null, 2)));
  } catch {
    return truncate(redactDebugText(String(value)));
  }
}

export function redactDebugText(input: string): string {
  return input
    .replace(/(authorization"\s*:\s*")([^"]+)/gi, '$1***')
    .replace(/(x-pin-code"\s*:\s*")([^"]+)/gi, '$1***')
    .replace(/(x-totp-code"\s*:\s*")([^"]+)/gi, '$1***')
    .replace(/(git_token"\s*:\s*")([^"]+)/gi, '$1***')
    .replace(/(password"\s*:\s*")([^"]+)/gi, '$1***')
    .replace(/(root_pass"\s*:\s*")([^"]+)/gi, '$1***')
    .replace(/(token"\s*:\s*")([^"]+)/gi, '$1***')
    .replace(/(Bearer\s+)[A-Za-z0-9._~+/=-]+/gi, '$1***')
    .replace(/(gh[pousr]_[A-Za-z0-9_]+)/g, 'gh*_***');
}
