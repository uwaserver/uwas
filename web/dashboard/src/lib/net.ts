/**
 * isPubliclyRoutable reports whether a git host could plausibly reach this
 * hostname. Loopback, RFC1918, link-local and .local names cannot receive a
 * webhook from GitHub no matter how the hook is configured.
 *
 * Deliberately a hostname check and nothing more: a public address can still
 * be firewalled, so this catches the common mistake rather than proving
 * reachability.
 */
export function isPubliclyRoutable(hostname: string): boolean {
  const h = hostname.toLowerCase().replace(/^\[|\]$/g, '');
  if (h === 'localhost' || h.endsWith('.localhost') || h.endsWith('.local')) return false;
  if (h === '::1' || h.startsWith('fe80:') || h.startsWith('fc') || h.startsWith('fd')) return false;
  const v4 = h.match(/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/);
  if (v4) {
    const [a, b] = [Number(v4[1]), Number(v4[2])];
    if (a === 127 || a === 10 || a === 0) return false;
    if (a === 192 && b === 168) return false;
    if (a === 172 && b >= 16 && b <= 31) return false;
    if (a === 169 && b === 254) return false;
    if (a === 100 && b >= 64 && b <= 127) return false; // CGNAT
  }
  return true;
}
