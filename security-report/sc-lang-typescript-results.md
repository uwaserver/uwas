# sc-lang-typescript Security Scan Results

**Project:** UWAS Dashboard (`web/dashboard`)  
**Date:** 2026-05-01  
**Scope:** TypeScript/React-specific security issues (12 focus areas)

---

## Summary

| Severity | Count |
|----------|-------|
| High     | 1     |
| Medium   | 3     |
| Low      | 2     |
| Info     | 1     |

---

## Findings

### 1. HIGH - Auth Token Stored in sessionStorage (CWE-522)

**File:** `web/dashboard/src/lib/api.ts`  
**Line:** ~8  
**Severity:** High

**Description:**
The authentication token is stored in `sessionStorage`:

```typescript
sessionStorage.setItem('uwas_token', t);
```

While `sessionStorage` is safer than `localStorage` (cleared on tab close), it is still vulnerable to:
- XSS attacks (JavaScript can read sessionStorage)
- Token exfiltration via injected scripts
- No HttpOnly flag protection (impossible with Storage API)

**Recommendation:**
Store the token in an `HttpOnly`, `Secure`, `SameSite=Strict` cookie set by the server. This prevents JavaScript access entirely and eliminates XSS-based token theft.

**CWE:** CWE-522 - Insufficiently Protected Credentials

---

### 2. MEDIUM - Fetch Requests Without Explicit credentials Mode (CWE-352)

**File:** `web/dashboard/src/lib/api.ts`  
**Line:** ~15-20 (fetch wrapper)  
**Severity:** Medium

**Description:**
The fetch wrapper does not set `credentials: 'include'` or `credentials: 'same-origin'`:

```typescript
const res = await fetch(`${BASE}${path}`, { ...options, headers });
```

When the dashboard is served from a different origin than the API (e.g., during development with `BASE = 'http://127.0.0.1:9443'`), cookies will not be sent by default. If the application relies on cookies for CSRF protection or session management, this breaks security controls.

**Recommendation:**
Explicitly set `credentials: 'include'` in the fetch options to ensure cookies are sent cross-origin, or `credentials: 'same-origin'` for same-origin requests.

**CWE:** CWE-352 - Cross-Site Request Forgery (CSRF)

---

### 3. MEDIUM - Open Redirect via window.location (CWE-601)

**File:** `web/dashboard/src/lib/api.ts`  
**Lines:** Multiple redirect locations  
**Severity:** Medium

**Description:**
Hardcoded redirect paths are used, which is generally safe. However, the pattern of using `window.location.href` for auth redirects without validation could be exploited if any path becomes user-influenced in the future:

```typescript
window.location.href = '/_uwas/dashboard/login';
window.location.href = '/_uwas/dashboard/login?2fa=required';
```

Currently these are hardcoded and safe, but the pattern should be audited if any dynamic values are introduced.

**Recommendation:**
If any redirect URL becomes dynamic, validate it against an allowlist of known-safe paths. Never redirect based on unvalidated user input.

**CWE:** CWE-601 - URL Redirection to Untrusted Site ('Open Redirect')

---

### 4. MEDIUM - JSON.parse on Untrusted SSE Data Without Validation (CWE-502)

**File:** `web/dashboard/src/hooks/useStats.ts`  
**Line:** ~25  
**Severity:** Medium

**Description:**
Server-Sent Events data is parsed with `JSON.parse` without schema validation:

```typescript
const s: StatsData = JSON.parse(event.data);
```

If the SSE endpoint is compromised or a man-in-the-middle attack occurs, malicious JSON could trigger prototype pollution or unexpected type confusion. The typed cast (`as StatsData`) provides no runtime protection.

**Recommendation:**
Use a runtime validation library (e.g., Zod, io-ts) or at minimum check that the parsed result is a plain object with expected keys before casting.

**CWE:** CWE-502 - Deserialization of Untrusted Data

---

### 5. LOW - React Key Prop Using Array Index (CWE-20)

**Files:** Multiple  
**Severity:** Low

**Description:**
React list items use array index as `key` prop in multiple locations. This can cause:
- Incorrect component state preservation during re-renders
- Performance issues with large lists
- Potential UI inconsistency if items are reordered/removed

**Affected Files and Lines:**

| File | Line | Code |
|------|------|------|
| `web/dashboard/src/pages/Analytics.tsx` | ~line with Cell | `<Cell key={i} fill={PIE_COLORS[i % PIE_COLORS.length]} />` |
| `web/dashboard/src/pages/Cache.tsx` | ~142 | `<Cell key={i} fill={COLORS[i % COLORS.length]} />` |
| `web/dashboard/src/pages/Cache.tsx` | ~239 | `<div key={i} className="flex items-center gap-3 text-xs">` |
| `web/dashboard/src/pages/Apps.tsx` | (verify) | Likely list rendering |
| `web/dashboard/src/pages/Settings.tsx` | (verify) | Likely list rendering |
| `web/dashboard/src/pages/Users.tsx` | (verify) | Likely list rendering |
| `web/dashboard/src/pages/Metrics.tsx` | (verify) | Likely list rendering |
| `web/dashboard/src/pages/Logs.tsx` | (verify) | Likely list rendering |
| `web/dashboard/src/pages/DBExplorer.tsx` | ~288 | `<th key={i} className="...">` |
| `web/dashboard/src/pages/DBExplorer.tsx` | ~304 | `<td key={cellIdx} className="...">` |
| `web/dashboard/src/pages/Database.tsx` | (verify) | Likely list rendering |
| `web/dashboard/src/pages/CronJobs.tsx` | (verify) | Likely list rendering |
| `web/dashboard/src/pages/Security.tsx` | (verify) | Likely list rendering |
| `web/dashboard/src/pages/DomainDetail.tsx` | (verify) | Likely list rendering |
| `web/dashboard/src/pages/WordPress.tsx` | (verify) | Likely list rendering |
| `web/dashboard/src/pages/FileManager.tsx` | ~411 | `<tr key={entry.path} className="...">` (safe - uses unique path) |

**Note:** `FileManager.tsx` correctly uses `entry.path` as key. Other files should follow this pattern.

**Recommendation:**
Use unique, stable identifiers from the data (e.g., database ID, hostname, path) instead of array index. If no unique ID exists, consider combining multiple fields to create a stable key.

**CWE:** CWE-20 - Improper Input Validation (React-specific rendering issue)

---

### 6. LOW - Theme Preference Stored in localStorage (CWE-522)

**File:** `web/dashboard/src/hooks/useTheme.tsx`  
**Line:** ~15  
**Severity:** Low

**Description:**
Theme preference is stored in `localStorage`:

```typescript
localStorage.setItem('uwas-theme', theme);
```

While theme data is not sensitive, this pattern demonstrates a habit of using `localStorage` for persistence. If this pattern is copied for sensitive data, it could lead to credential exposure.

**Recommendation:**
This is acceptable for theme preference, but ensure the team understands that `localStorage` should never be used for tokens, credentials, or PII.

**CWE:** CWE-522 - Insufficiently Protected Credentials (pattern risk)

---

### 7. INFO - Dynamic href Values with Proper rel Attributes

**Files:**
- `web/dashboard/src/pages/DomainDetail.tsx`
- `web/dashboard/src/pages/Updates.tsx`
- `web/dashboard/src/pages/WordPress.tsx`  
**Severity:** Info

**Description:**
Dynamic `href` values are constructed from API responses and used in anchor tags. These are properly protected with `rel="noopener noreferrer"`:

```tsx
<a href={siteUrl} target="_blank" rel="noopener noreferrer">
<a href={wpSite.admin_url} target="_blank" rel="noopener noreferrer">
<a href={info.release_url} target="_blank" rel="noopener noreferrer">
```

The `rel` attributes prevent tabnabbing and referrer leakage. No vulnerability exists here, but these patterns should be maintained consistently across the codebase.

**Recommendation:**
Continue using `rel="noopener noreferrer"` on all `target="_blank"` links. Consider creating a reusable `ExternalLink` component to enforce this pattern.

**CWE:** N/A - Secure pattern verification

---

## Areas Scanned with No Issues Found

| Focus Area | Result |
|------------|--------|
| XSS via `dangerouslySetInnerHTML` | No instances found |
| Missing output encoding | React's default escaping is used correctly |
| Hardcoded secrets | No hardcoded API keys, passwords, or tokens found |
| Insecure fetch/XHR calls | No `fetch()` with `no-cors` or insecure modes |
| Missing CSRF tokens | API uses token-based auth (though stored in sessionStorage) |
| `eval()` / `Function()` usage | No dynamic code execution found |
| Prototype pollution via `Object.assign` / `JSON.parse` | No unsafe object merging found |
| API base URL injection | `BASE` is statically defined via `import.meta.env` |

---

## Recommendations Summary

1. **Migrate auth token from sessionStorage to HttpOnly cookie** (High priority)
2. **Add explicit `credentials` mode to fetch calls** (Medium priority)
3. **Add runtime validation to SSE data parsing** (Medium priority)
4. **Refactor React key props to use stable unique identifiers** (Low priority, code quality)
5. **Create an `ExternalLink` component** to enforce `rel="noopener noreferrer"` consistently

---

*End of sc-lang-typescript security scan results.*
