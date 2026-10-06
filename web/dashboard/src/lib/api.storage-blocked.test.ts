import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

// Module-level storage access in api.ts must not crash the dashboard bundle
// when storage is blocked (hardened browsers, dom.storage.enabled=false,
// private modes). debugLog.ts and useTheme already guard this environment;
// api.ts is the shared foundation every page imports — an import-time throw
// here is a total outage, not a missing feature.

// NOTE: no static import of './api' — the module under test must be evaluated
// fresh (and with the stub in place) via dynamic import per test.

function blockedSessionStorage(mode: 'read' | 'write') {
  return {
    getItem: (_key: string) => {
      if (mode === 'read') throw new Error('SecurityError: storage blocked');
      return null;
    },
    setItem: (_key: string, _value: string) => {
      if (mode === 'write') throw new Error('QuotaExceededError: storage blocked');
    },
    removeItem: (_key: string) => {
      if (mode === 'write') throw new Error('QuotaExceededError: storage blocked');
    },
    clear: () => {},
  };
}

describe('api.ts survives blocked sessionStorage', () => {
  beforeEach(() => {
    vi.resetModules();
    vi.unstubAllGlobals();
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('control: imports and round-trips the token with working storage', async () => {
    const mod = await import('./api');
    mod.setToken('test-token-123', 'api_key');
    expect(mod.getToken()).toBe('test-token-123');
    mod.setTOTPCode('123456');
    mod.clearToken();
    expect(mod.getToken()).toBe('');
  });

  it('imports cleanly when sessionStorage reads are blocked', async () => {
    vi.stubGlobal('sessionStorage', blockedSessionStorage('read'));
    let mod: typeof import('./api') | undefined;
    let importError: unknown;
    try {
      mod = await import('./api');
    } catch (e) {
      importError = e;
    }
    // CONTRACT: module evaluation must not throw — a throw here means the
    // whole dashboard bundle fails to load for storage-blocked browsers.
    expect(importError).toBeUndefined();
    expect(mod!.getToken()).toBe('');
    expect(mod!.getAuthMode()).toBe('api_key');
  });

  it('token setters and clearToken do not throw when storage writes are blocked', async () => {
    vi.stubGlobal('sessionStorage', blockedSessionStorage('write'));
    const mod = await import('./api');
    // CONTRACT: login/logout flows must not break mid-way because a storage
    // write threw — the in-memory session still works for this page load.
    expect(() => mod.setToken('tok', 'session')).not.toThrow();
    expect(mod.getToken()).toBe('tok');
    expect(() => mod.setTOTPCode('654321')).not.toThrow();
    expect(() => mod.clearToken()).not.toThrow();
    expect(mod.getToken()).toBe('');
  });
});
