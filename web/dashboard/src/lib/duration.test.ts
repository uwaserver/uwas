import { describe, it, expect } from 'vitest';
import {
  normalizeWindowValue,
  parseDurationSeconds,
  canonicalDuration,
  WINDOW_OPTIONS,
  WINDOW_DEFAULT,
} from './duration';

describe('normalizeWindowValue', () => {
  // The bug: the server marshals a Duration as Go's String() ("1m0s"), the
  // select's option values were "1m"/"5m"/"15m", nothing matched, and the
  // panel silently showed the first option ("10 seconds") for a domain that
  // was actually limited per minute. Every stored form must map to the option
  // the select carries.
  it('maps Go canonical minute/second forms onto option values', () => {
    expect(normalizeWindowValue('1m0s')).toBe('1m0s');
    expect(normalizeWindowValue('5m0s')).toBe('5m0s');
    expect(normalizeWindowValue('15m0s')).toBe('15m0s');
    expect(normalizeWindowValue('10s')).toBe('10s');
    expect(normalizeWindowValue('30s')).toBe('30s');
  });

  it('maps legacy and equivalent forms onto the same option', () => {
    expect(normalizeWindowValue('1m')).toBe('1m0s'); // legacy select value
    expect(normalizeWindowValue('60s')).toBe('1m0s'); // bare seconds
    expect(normalizeWindowValue('300s')).toBe('5m0s');
    expect(normalizeWindowValue('900s')).toBe('15m0s');
  });

  // Regression: the API marshals a zero/absent Duration as the JSON *number* 0,
  // so the raw field reaches this helper as a number, not a string. Calling
  // (0).trim() threw a TypeError, and the caller's catch nulled the whole
  // domain detail — clicking a domain on the Security page showed nothing.
  it('handles the numeric window the API returns for an unset value', () => {
    expect(normalizeWindowValue(0)).toBe(WINDOW_DEFAULT);
    expect(normalizeWindowValue(60)).toBe('1m0s');   // number seconds -> preset
    expect(normalizeWindowValue(10)).toBe('10s');
    expect(normalizeWindowValue(45)).toBe('45s');     // number seconds -> canonical
    expect(() => normalizeWindowValue(0)).not.toThrow();
  });

  it('falls back to the default for empty or junk', () => {
    expect(normalizeWindowValue('')).toBe(WINDOW_DEFAULT);
    expect(normalizeWindowValue(undefined)).toBe(WINDOW_DEFAULT);
    expect(normalizeWindowValue(null)).toBe(WINDOW_DEFAULT);
    expect(normalizeWindowValue('abc')).toBe(WINDOW_DEFAULT);
    expect(normalizeWindowValue('10s!!')).toBe(WINDOW_DEFAULT);
  });

  it('keeps a non-preset but valid duration in a round-trippable canonical form', () => {
    // 45s is not a preset; it must still come back as something Go accepts and
    // re-emits unchanged, so a YAML-set value does not thrash on each reload.
    expect(normalizeWindowValue('45s')).toBe('45s');
    expect(normalizeWindowValue('2m')).toBe('2m0s');
    expect(normalizeWindowValue('90s')).toBe('1m30s');
  });

  // The option values the select renders must equal Go's Duration.String()
  // output, or the round trip breaks again. Pin the canonical forms.
  it('uses Go-canonical option values', () => {
    expect(WINDOW_OPTIONS.map((o) => o.value)).toEqual(['10s', '30s', '1m0s', '5m0s', '15m0s']);
  });
});

describe('parseDurationSeconds', () => {
  it('parses simple and compound durations', () => {
    expect(parseDurationSeconds('10s')).toBe(10);
    expect(parseDurationSeconds('1m')).toBe(60);
    expect(parseDurationSeconds('1m0s')).toBe(60);
    expect(parseDurationSeconds('1h30m')).toBe(5400);
  });
  it('accepts numbers as seconds (API returns 0 for unset)', () => {
    expect(parseDurationSeconds(0)).toBeNull();
    expect(parseDurationSeconds(60)).toBe(60);
    expect(parseDurationSeconds(-5)).toBeNull();
  });

  it('rejects non-durations', () => {
    expect(parseDurationSeconds('')).toBeNull();
    expect(parseDurationSeconds('abc')).toBeNull();
    expect(parseDurationSeconds('10s extra')).toBeNull();
  });
});

describe('canonicalDuration', () => {
  it('matches Go Duration.String() shape', () => {
    expect(canonicalDuration(10)).toBe('10s');
    expect(canonicalDuration(60)).toBe('1m0s');
    expect(canonicalDuration(90)).toBe('1m30s');
    expect(canonicalDuration(3600)).toBe('1h0m0s');
  });
});
