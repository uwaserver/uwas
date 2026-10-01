import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { setToken, getToken, getAuthHeaders, clearToken, getAuthMode, setTOTPCode, terminalWSURL, fetchStats, clearPinCode } from '@/lib/api'

describe('api token management', () => {
  beforeEach(() => {
    clearToken()
    sessionStorage.clear()
  })

  it('starts with empty token', () => {
    expect(getToken()).toBe('')
  })

  it('stores and retrieves API key token', () => {
    setToken('test-api-key-value', 'api_key')
    expect(getToken()).toBe('test-api-key-value')
    expect(getAuthMode()).toBe('api_key')
  })

  it('stores and retrieves session token', () => {
    setToken('sess_abc123', 'session')
    expect(getToken()).toBe('sess_abc123')
    expect(getAuthMode()).toBe('session')
  })

  it('returns auth header for API key mode', () => {
    setToken('test-api-key-value', 'api_key')
    const headers = getAuthHeaders()
    const authHdr = Object.keys(headers).find(k => k.toLowerCase() === 'authorization')
    expect(authHdr).toBeTruthy()
    const val = headers[authHdr || '']
    expect(val).toBe('Bearer' + ' test-api-key-value')
  })

  it('returns session header for session mode', () => {
    setToken('sess_xyz789', 'session')
    const headers = getAuthHeaders()
    expect(headers['X-Session-Token']).toBe('sess_xyz789')
  })

  it('returns empty headers when no token is set', () => {
    expect(Object.keys(getAuthHeaders())).toHaveLength(0)
  })

  it('clears token and auth mode on clearToken', () => {
    setToken('test-api-key-value', 'api_key')
    clearToken()
    expect(getToken()).toBe('')
    expect(getAuthMode()).toBe('api_key')
    expect(sessionStorage.getItem('uwas_token')).toBeNull()
    expect(sessionStorage.getItem('uwas_auth_mode')).toBeNull()
  })

  it('clears TOTP verified flag on clearToken', () => {
    setTOTPCode('123456')
    expect(sessionStorage.getItem('uwas_totp_verified')).toBe('true')
    clearToken()
    expect(sessionStorage.getItem('uwas_totp_verified')).toBeNull()
  })

  it('empty token clears session storage', () => {
    setToken('test-api-key-value', 'api_key')
    setToken('', 'api_key')
    expect(getToken()).toBe('')
    expect(sessionStorage.getItem('uwas_token')).toBeNull()
  })

  it('persists token across getToken calls', () => {
    setToken('test-api-key-value', 'api_key')
    expect(getToken()).toBe('test-api-key-value')
    expect(getToken()).toBe('test-api-key-value')
  })

  it('handles auth mode transitions', () => {
    setToken('test-mode-a', 'api_key')
    const h1 = getAuthHeaders()
    expect(h1).toHaveProperty('Authorization')
    setToken('sess-mode-b', 'session')
    const h2 = getAuthHeaders()
    expect(h2).toHaveProperty('X-Session-Token')
    expect(h2).not.toHaveProperty('X-Pin-Code')
    expect(getAuthMode()).toBe('session')
  })
})

describe('terminalWSURL pin lifecycle', () => {
  const fetchMock = vi.fn()

  beforeEach(() => {
    clearToken()
    clearPinCode()
    sessionStorage.clear()
    fetchMock.mockReset()
    vi.stubGlobal('fetch', fetchMock)
    // Default responder; individual tests override per call as needed.
    fetchMock.mockImplementation(async (url: string) =>
      new Response(JSON.stringify(url.includes('/auth/ticket') ? { ticket: 'tk-1' } : {}), { status: 200 }))
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  function callFor(path: string): { url: string; init: RequestInit } {
    const call = fetchMock.mock.calls.find(([u]) => String(u).includes(path))
    if (!call) throw new Error(`no fetch call for ${path}`)
    return { url: String(call[0]), init: call[1] as RequestInit }
  }

  // The ticket mint is the one request that must carry the PIN — the server
  // binds it into the single-use ticket. Afterwards the credential must be
  // gone: every other PIN flow in this module (403 retry, uploads, cPanel
  // migration) clears the global once its request is done.
  it('sends the PIN on the ticket mint but not on later API requests', async () => {
    await terminalWSURL('424242')
    expect(callFor('/auth/ticket').init.headers).toMatchObject({ 'X-Pin-Code': '424242' })

    await fetchStats()
    const statsHeaders = callFor('/api/v1/stats').init.headers as Record<string, string>
    expect(statsHeaders).not.toHaveProperty('X-Pin-Code')
  })

  it('does not leak the PIN into a later ticket mint made without a pin', async () => {
    await terminalWSURL('424242')
    await terminalWSURL()
    // Inspect the SECOND mint — the first legitimately carries the PIN.
    const mintCalls = fetchMock.mock.calls.filter(([u]) => String(u).includes('/auth/ticket'))
    expect(mintCalls.length).toBe(2)
    const mintHeaders = (mintCalls[1][1] as RequestInit).headers as Record<string, string>
    expect(mintHeaders).not.toHaveProperty('X-Pin-Code')
  })
})
