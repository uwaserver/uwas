// Duration helpers for the window <select>s on the Security and Domain pages.
//
// The server stores a window as a Go time.Duration and marshals it with
// Duration.String(), which is canonical: 1 minute is "1m0s", not "1m", and
// 5 minutes is "5m0s". A <select> whose option values were "1m"/"5m"/"15m"
// therefore never matched a stored minute value, so the browser fell back to
// rendering the FIRST option — the panel showed "10 seconds" while the domain
// was actually limited per minute. normalizeWindowValue maps whatever the API
// returns (canonical "1m0s", a legacy "1m", or a bare-second "60s") onto the
// option value the select actually carries, so the control reflects reality.

export const WINDOW_DEFAULT = '1m0s';

// Option values must equal Go's Duration.String() output so save → store →
// reload round-trips without drift.
export const WINDOW_OPTIONS: { value: string; seconds: number }[] = [
  { value: '10s', seconds: 10 },
  { value: '30s', seconds: 30 },
  { value: '1m0s', seconds: 60 },
  { value: '5m0s', seconds: 300 },
  { value: '15m0s', seconds: 900 },
];

// parseDurationSeconds parses a simple Go-style duration ("10s", "1m", "1m0s",
// "1h30m") into whole seconds, or null if it is not a duration.
//
// Accepts a number too: the API marshals a zero Duration as the JSON number 0
// (and could return a positive number as seconds), so a caller passing the raw
// field must not blow up on `(0).trim()`. A number <= 0 means "unset".
export function parseDurationSeconds(v: string | number | undefined | null): number | null {
  if (typeof v === 'number') {
    return Number.isFinite(v) && v > 0 ? Math.round(v) : null;
  }
  const s = (v ?? '').trim();
  if (s === '') return null;
  const re = /(\d+)(h|m|s)/g;
  let m: RegExpExecArray | null;
  let secs = 0;
  let matched = false;
  let consumed = 0;
  while ((m = re.exec(s)) !== null) {
    matched = true;
    consumed += m[0].length;
    const n = parseInt(m[1], 10);
    secs += m[2] === 'h' ? n * 3600 : m[2] === 'm' ? n * 60 : n;
  }
  // Reject strings with trailing junk ("10s!" or "abc"): every character must
  // have belonged to a unit group.
  if (!matched || consumed !== s.length) return null;
  return secs;
}

// normalizeWindowValue maps any stored window onto a canonical option value.
// An unknown-but-valid duration is returned in canonical form so it still
// round-trips even if it is not one of the presets; anything unparseable falls
// back to the default.
export function normalizeWindowValue(v: string | number | undefined | null): string {
  const secs = parseDurationSeconds(v);
  if (secs === null) return WINDOW_DEFAULT;
  const preset = WINDOW_OPTIONS.find((o) => o.seconds === secs);
  if (preset) return preset.value;
  return canonicalDuration(secs);
}

// canonicalDuration renders whole seconds the way Go's Duration.String() does,
// so a non-preset value the operator set via YAML still matches on reload.
export function canonicalDuration(totalSeconds: number): string {
  if (totalSeconds <= 0) return '0s';
  const h = Math.floor(totalSeconds / 3600);
  const m = Math.floor((totalSeconds % 3600) / 60);
  const s = totalSeconds % 60;
  let out = '';
  if (h > 0) out += `${h}h`;
  if (h > 0 || m > 0) out += `${m}m`;
  out += `${s}s`;
  return out;
}
