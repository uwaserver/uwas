import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import Security from './Security';

// F46305: config.MergeDomain replaces the whole security block whenever the
// patch carries a `security` key. The Security page's save built that block
// from waf / rate_limit / blocked_paths / ip lists / hotlink / geo but never
// included cloudflare_only, so saving ANY setting on this page silently
// turned off a domain's "only accept Cloudflare" origin lock.

const apiMocks = vi.hoisted(() => ({
  fetchSecurityStats: vi.fn(),
  fetchSecurityBlocked: vi.fn(),
  fetchDomains: vi.fn(),
  fetchDomainDetail: vi.fn(),
  updateDomain: vi.fn(),
  fetchFeatures: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

const flush = () => act(async () => {});

const STATS = { waf_blocked: 0, bot_blocked: 0, rate_blocked: 0, hotlink_blocked: 0, total_blocked: 0 };

function detail(security: Record<string, unknown>) {
  return {
    host: 'x.com',
    type: 'static',
    aliases: null,
    ssl: { mode: 'auto', cert: '', key: '', min_version: '' },
    root: '/var/www/x',
    security,
  };
}

async function openAndSave(security: Record<string, unknown>) {
  apiMocks.fetchSecurityStats.mockResolvedValue(STATS);
  apiMocks.fetchSecurityBlocked.mockResolvedValue([]);
  apiMocks.fetchDomainDetail.mockResolvedValue(detail(security));
  apiMocks.updateDomain.mockResolvedValue({});
  render(<Security />);
  await flush();
  fireEvent.click(screen.getByRole('button', { name: /per-domain/i }));
  await flush();
  fireEvent.click(screen.getByText('x.com'));
  await flush();
  fireEvent.click(screen.getByRole('button', { name: /Save Security Settings/ }));
  await flush();
  expect(apiMocks.updateDomain).toHaveBeenCalledTimes(1);
  return apiMocks.updateDomain.mock.calls[0][1] as { security: Record<string, unknown> };
}

describe('Security page save round-trips the whole security block', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => fn.mockReset());
    apiMocks.fetchDomains.mockResolvedValue([{ id: 'd1', host: 'x.com', type: 'static', ssl: 'off' }]);
    apiMocks.fetchFeatures.mockResolvedValue({});
  });

  it('keeps cloudflare_only on', async () => {
    const body = await openAndSave({
      waf: { enabled: true },
      cloudflare_only: true,
      blocked_paths: [],
    });
    console.log('EXPECTED: security.cloudflare_only = true\nACTUAL: ' + JSON.stringify(body.security));
    expect(body.security.cloudflare_only).toBe(true);
  });

  it('keeps the other saved sections (rate limit, lists, hotlink, geo)', async () => {
    const body = await openAndSave({
      waf: { enabled: true, bypass_paths: ['/api/'] },
      cloudflare_only: true,
      rate_limit: { requests: 100, window: '1m0s', by: 'ip' },
      ip_whitelist: ['192.0.2.1'],
      ip_blacklist: ['198.51.100.9'],
      hotlink_protection: { enabled: true, allowed_referers: ['example.org'] },
      geo_block_countries: ['XX'],
      geo_allow_countries: ['TR'],
      blocked_paths: ['/.env'],
    });
    expect(body.security).toMatchObject({
      cloudflare_only: true,
      ip_whitelist: ['192.0.2.1'],
      ip_blacklist: ['198.51.100.9'],
      geo_block_countries: ['XX'],
      geo_allow_countries: ['TR'],
      blocked_paths: ['/.env'],
    });
  });

  // Control: an unset lock stays unset (false), it is not turned on.
  it('control: cloudflare_only stays off when it was off', async () => {
    const body = await openAndSave({ waf: { enabled: false }, blocked_paths: [] });
    expect(body.security.cloudflare_only ?? false).toBe(false);
  });
});
