import { describe, it, expect } from 'vitest';
import { isPubliclyRoutable } from '@/lib/net';

describe('isPubliclyRoutable', () => {
  // A git host cannot deliver to any of these, so the panel must warn rather
  // than hand out a URL that silently never fires.
  it.each([
    'localhost',
    'app.localhost',
    'panel.local',
    '127.0.0.1',
    '10.0.0.5',
    '192.168.1.20',
    '172.16.4.1',
    '172.31.255.254',
    '169.254.1.1',
    '100.64.0.1',
    '::1',
  ])('treats %s as unreachable', host => {
    expect(isPubliclyRoutable(host)).toBe(false);
  });

  it.each([
    '95.130.170.135',
    'panel.example.com',
    '8.8.8.8',
    '172.32.0.1',
    '172.15.0.1',
  ])('treats %s as reachable', host => {
    expect(isPubliclyRoutable(host)).toBe(true);
  });

  it('is case-insensitive', () => {
    expect(isPubliclyRoutable('LOCALHOST')).toBe(false);
  });
});
