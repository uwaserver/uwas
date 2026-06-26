# sc-ldap results

No issues found by sc-ldap.

## Scan notes

- No LDAP client libraries are present: `go.mod`/`go.sum` contain no `go-ldap` or any
  LDAP package; `web/dashboard/package.json` has no LDAP dependency.
- No LDAP API usage in Go source (`ldap.`, `InitialDirContext`, `DirectorySearcher`,
  filter/DN construction, `ldap_escape`, `escape_filter_chars`) outside of vendored/minified
  JS bundles where substrings appear coincidentally
  (`internal/admin/dashboard/dist/assets/*.js`).
- Authentication is implemented via TOTP 2FA, multi-user RBAC, sessions, and timing-safe
  API-key comparison (`crypto/subtle.ConstantTimeCompare`) — there is no directory-service
  (LDAP/AD) bind or search code path reachable from any HTTP handler, CLI, SFTP, or proxy.

Defenses observed: no attack surface exists for this vulnerability class.
