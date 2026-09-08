import { describe, it, expect } from 'vitest';
import { SECTIONS } from './settingsSections';

// admin.oauth is stored, merged and returned by the settings API, and no
// OAuth login flow exists. The section used to present itself as a working
// sign-in method, and told the operator that Allowed Emails restricts access.
// Nothing enforces either.
describe('OAuth settings section', () => {
  const oauth = SECTIONS.find(s => s.id === 'oauth');

  it('exists', () => {
    expect(oauth).toBeDefined();
  });

  it('says it is not implemented', () => {
    expect(oauth?.notice).toBeDefined();
    expect(oauth?.notice?.toLowerCase()).toContain('not implemented');
  });

  it('warns that allowed_emails does not restrict access', () => {
    expect(oauth?.notice).toContain('Allowed Emails');
  });

  it('does not claim allowed_emails is enforced', () => {
    const field = oauth?.fields.find(f => f.key === 'global.admin.oauth.allowed_emails');
    expect(field).toBeDefined();
    // The old help text — "Leave empty to allow any authenticated user" —
    // implies a non-empty list restricts access.
    expect(field?.help ?? '').not.toMatch(/leave empty to allow/i);
  });
});

// A notice must never quietly become decoration. There are two legitimate
// kinds: the "not implemented" disclaimer (only oauth — settings that are
// stored but acted on by nothing) and an operational caution on a feature that
// DOES work but has a footgun (autoblock and the watchdog, which need a restart
// and can lock people out if misconfigured). The disclaimer kind must stay
// unique to oauth; anything else carrying "not implemented" would be a section
// silently presenting dead settings as working.
describe('section notices', () => {
  it('reserve the not-implemented disclaimer for oauth alone', () => {
    const notImplemented = SECTIONS.filter(s => s.notice?.toLowerCase().includes('not implemented')).map(s => s.id);
    expect(notImplemented).toEqual(['oauth']);
  });

  it('limit notices to the sections that have earned one', () => {
    const withNotice = SECTIONS.filter(s => s.notice).map(s => s.id).sort();
    expect(withNotice).toEqual(['autoblock', 'oauth', 'watchdog']);
  });

  it('warn on the flood-protection sections that a restart is required', () => {
    for (const id of ['autoblock', 'watchdog']) {
      const sec = SECTIONS.find(s => s.id === id);
      expect(sec?.notice?.toLowerCase()).toContain('restart');
      // These are real features, not the oauth kind of dead setting.
      expect(sec?.notice?.toLowerCase()).not.toContain('not implemented');
    }
  });
});

// global.access_log.enabled shipped in v0.9.1 as a YAML-only setting: it was
// the one control that silenced the per-request log without also hiding
// certificate renewals, reloads and backups, and the only way to reach it was
// editing uwas.yaml by hand.
describe('Logging section', () => {
  const logging = SECTIONS.find(s => s.id === 'logging');

  it('exists', () => {
    expect(logging).toBeDefined();
  });

  it('exposes the request log toggle', () => {
    const field = logging?.fields.find(f => f.key === 'global.access_log.enabled');
    expect(field).toBeDefined();
    expect(field?.type).toBe('toggle');
  });

  it('says what turning it off does and does not affect', () => {
    const help = logging?.fields.find(f => f.key === 'global.access_log.enabled')?.help ?? '';
    // An operator reaching for this is trying to quieten journalctl; the help
    // has to steer them here rather than to log_level, which also hides the
    // messages worth keeping.
    expect(help).toMatch(/log level/i);
    expect(help).toMatch(/metrics|analytics/i);
  });

  it('keeps the log level control alongside it', () => {
    expect(logging?.fields.some(f => f.key === 'global.log_level')).toBe(true);
  });
});
