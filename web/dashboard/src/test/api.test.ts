import { describe, it, expect, beforeEach } from 'vitest'
import { setToken, getToken, getAuthHeaders, clearToken, getAuthMode, setTOTPCode } from '@/lib/api'

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
    expect(getAuthMode()).toBe('session')
  })
})
