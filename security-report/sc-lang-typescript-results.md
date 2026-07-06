# sc-lang-typescript results
> 
> **Status:** This scan was performed 2026-06-26. All findings have been
> reviewed and are **resolved** in the current codebase (v0.8.8, July 2026).
> See [SECURITY-REPORT.md](./SECURITY-REPORT.md) for the full status update
> with per-finding resolution tracking.
>

One-line summary: The React 19 / TypeScript dashboard (`web/dashboard/`) is unusually clean — no `eval`/`Function`, no `dangerouslySetInnerHTML`/`innerHTML`, no `as any`/`@ts-ignore`, tokens never placed in URLs, SSE/WebSocket auth via short-lived single-use tickets, debug-log redaction of secrets. Only one low-severity, defense-in-depth observation (auth token in `sessionStorage` with no CSP on the dashboard).

## Scope
- Frontend: `web/dashboard/src` (42 pages, `lib/api.ts`, hooks, components).
- Checked against the TS/JS checklist: prototype pollution, eval/Function, DOM XSS, child_process, vm, dynamic require/import, middleware ordering, JWT/client storage, `as any`/`@ts-ignore`, `dangerouslySetInnerHTML`/SSR injection, ORM raw queries, WebSocket/postMessage, ReDoS, path traversal, insecure randomness, CORS, SSRF, template injection, timing, error leakage, secrets exposure.
- Note: the server, FastCGI, file manager, SFTP, terminal PTY backend, proxy, WAF and auth are implemented in Go, not TypeScript — those belong to the Go scanner (`sc-lang-go`) and are out of scope here.

## Findings

### TS-001: Auth/session token stored in sessionStorage (defense-in-depth)
- Severity: low
- Confidence: 35
- File: `web/dashboard/src/lib/api.ts:5-17`
- CWE: CWE-922 (Insecure Storage of Sensitive Information)
- Evidence:
  ```ts
  let token = sessionStorage.getItem('uwas_token') || '';
  let authMode = sessionStorage.getItem('uwas_auth_mode') || 'api_key';
  ...
  sessionStorage.setItem('uwas_token', t);
  ```
- Why it could matter: A bearer/session token in `sessionStorage` is readable by any JavaScript running in the page origin, so a single XSS would exfiltrate it (cf. checklist item 9 — client-side JWT/token storage). The dashboard is also served without a Content-Security-Policy: `Content-Security-Policy` is only set for tenant domains via `internal/config/domain.go:124`, and `web/dashboard/index.html` ships no CSP meta tag, so there is no second layer if a script injection ever lands.
- Mitigations already present (why this is low, not higher):
  - The app has **no DOM XSS sinks** — no `dangerouslySetInnerHTML`, `innerHTML`, `outerHTML`, `insertAdjacentHTML`, `document.write`, `eval`, or `new Function` anywhere in `src/`. All user/server data is rendered through JSX text nodes (e.g. terminal output into a `<pre>` via React state in `pages/Terminal.tsx:39-44`), which React auto-escapes.
  - Tokens are never put in URLs; SSE and WebSocket auth use short-lived single-use tickets (`lib/api.ts:806-819`, `obtainTicket()` / `statsSSEURL` / `terminalWSURL`).
  - Image previews are fetched with auth headers and shown via blob URLs, not `window.open(serverPath)` (`pages/FileManager.tsx:188-204`).
  - Debug logs redact `Authorization`, `Bearer`, pin/TOTP, passwords and GitHub tokens (`lib/debugLog.ts:redactDebugText`).
- Remediation (optional hardening): Move the session token to an `httpOnly; Secure; SameSite=Strict` cookie set by the Go API and drop the `sessionStorage` copy; and have the Go admin server emit a strict `Content-Security-Policy` (e.g. `default-src 'self'; script-src 'self'; object-src 'none'; frame-ancestors 'none'`) on the `/_uwas/dashboard` responses for defense-in-depth.

## Defenses observed (no finding required)
- No prototype-pollution-prone recursive merge of untrusted JSON; no user-controlled `require()`/`import()`.
- `encodeURIComponent` used consistently on path/query parameters in `lib/api.ts` and pages (e.g. `FileManager.tsx`, `Apps.tsx:1428`).
- External links use `target="_blank" rel="noopener noreferrer"` (DomainDetail, WordPress, Updates).
- WebSocket message handler appends to React state only (no DOM injection); no `postMessage`/origin pitfalls.
- No `Math.random()` used for security values on the client; no client-side regex built from user input (no ReDoS surface).
- `package.json` has no lifecycle scripts (no `preinstall`/`postinstall`/`prepare`); dependencies are pinned with caret ranges and a small, reputable set (react, react-router, recharts, lucide, @xyflow). No obvious typosquats.
- TypeScript strict project references; zero `as any` / `as unknown as` / `@ts-ignore` in `src/`.
