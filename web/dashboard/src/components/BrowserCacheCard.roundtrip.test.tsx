import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import BrowserCacheCard from './BrowserCacheCard';
import type { DomainDetail } from '@/lib/api';

// F46304: config.MergeDomain replaces the whole browser_cache block when the
// patch carries the key, and the card built that block from enabled / html /
// assets / immutable_paths only. BrowserCache also has `immutable` (the
// Cache-Control sent for immutable_paths hits), which the card never shows,
// so every Save from the dashboard silently reset a custom value to the
// built-in default.

const apiMocks = vi.hoisted(() => ({
  updateDomain: vi.fn(),
  previewCacheControl: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

const flush = () => act(async () => {});

function detailWith(bc: Record<string, unknown>): DomainDetail {
  return {
    host: 'example.com',
    type: 'static',
    aliases: null,
    ssl: { mode: 'auto', cert: '', key: '', min_version: '' },
    root: '/var/www/example.com',
    browser_cache: bc,
  } as unknown as DomainDetail;
}

async function save(bc: Record<string, unknown>) {
  apiMocks.updateDomain.mockResolvedValue({});
  render(<BrowserCacheCard host="example.com" detail={detailWith(bc)} onSaved={() => {}} />);
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  await flush();
  expect(apiMocks.updateDomain).toHaveBeenCalledTimes(1);
  return apiMocks.updateDomain.mock.calls[0][1] as { browser_cache: Record<string, unknown> };
}

describe('BrowserCacheCard save round-trips the whole block', () => {
  beforeEach(() => {
    apiMocks.updateDomain.mockReset();
    apiMocks.previewCacheControl.mockReset();
  });

  it('keeps a custom immutable Cache-Control value', async () => {
    const body = await save({
      enabled: true,
      html: 'no-cache',
      assets: 'public, max-age=3600',
      immutable: 'public, max-age=600, immutable',
      immutable_paths: ['/assets/'],
    });
    console.log('EXPECTED: browser_cache.immutable = "public, max-age=600, immutable"\nACTUAL: ' + JSON.stringify(body.browser_cache));
    expect(body.browser_cache.immutable).toBe('public, max-age=600, immutable');
  });

  it('sends the managed fields exactly as shown', async () => {
    const body = await save({
      enabled: false,
      html: 'no-store',
      assets: '',
      immutable: 'public, max-age=60, immutable',
      immutable_paths: ['/a/', '/b/'],
    });
    expect(body.browser_cache).toMatchObject({
      enabled: false,
      html: 'no-store',
      assets: '',
      immutable_paths: ['/a/', '/b/'],
    });
  });

  // Control: when no custom value exists the key is not invented (the server
  // then uses its built-in default), so saving does not pin a value.
  it('control: no immutable key is sent when none was configured', async () => {
    const body = await save({ enabled: true, html: 'no-cache', assets: '', immutable_paths: [] });
    expect('immutable' in body.browser_cache).toBe(false);
  });
});
