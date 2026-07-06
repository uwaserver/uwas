# sc-rce — Remote Code Execution scan results
> 
> **Status:** This scan was performed 2026-06-26. All findings have been
> reviewed and are **resolved** in the current codebase (v0.8.8, July 2026).
> See [SECURITY-REPORT.md](./SECURITY-REPORT.md) for the full status update
> with per-finding resolution tracking.
>

One-line summary: Go has no native eval/exec-code surface (no `plugin.Open`, `yaegi`, `go/ast`, or `text/template` with user input), but the per-domain PHP config API allows a non-admin domain manager to inject arbitrary php.ini directives via an unsanitized directive *value*, defeating the per-domain PHP sandbox (re-enabling `disable_functions`, escaping `open_basedir`, setting `auto_prepend_file`) → PHP code execution / sandbox escape.

---

## Finding: RCE-001 — php.ini directive injection via unsanitized per-domain config value (sandbox escape → RCE)

- **Severity:** High
- **Confidence:** 82
- **File:** internal/phpmanager/manager.go:737 (sink); bypassed control at manager.go:540; applied as `-c` at manager.go:324; reachable via internal/admin/handlers_php.go:534 (non-admin path at handlers_php.go:511-518)
- **CWE:** CWE-94 (Code Injection)

### Evidence

`SetDomainConfig` blocks dangerous *keys* but never inspects the *value*:

```go
// manager.go:540
if blockedPHPDirectives[key] {           // open_basedir, disable_functions,
    return fmt.Errorf("directive %q ...") // allow_url_include, auto_prepend_file, ...
}
...
inst.configOverrides[key] = value         // value stored verbatim
```

`buildDomainINI` then writes each override straight into a real php.ini with no
escaping/newline filtering, and appends them *after* the UWAS-enforced security
lines (so the attacker's directive wins on PHP last-value semantics):

```go
// manager.go:686 (UWAS sandbox, written first)
lines = append(lines, "disable_functions = exec,passthru,shell_exec,system,popen,pcntl_exec")
lines = append(lines, "allow_url_include = Off")
...
// manager.go:727-738 (attacker overrides, written last)
for _, k := range keys {
    lines = append(lines, fmt.Sprintf("%s = %s", k, overrides[k]))  // <-- no \n sanitization
}
```

The generated file is passed as the master config:

```go
// manager.go:324
args = append([]string{"-c", tmpINI}, args...)   // PHP_INI_SYSTEM scope
```

Reachable by non-admin users that manage the domain:

```go
// handlers_php.go:511-518  (handlePHPDomainConfigPut)
if user, ok := auth.UserFromContext(...); ok && user.Role != auth.RoleAdmin {
    if !s.authMgr.CanManageDomain(user, domain) { ... forbidden ... }
}
...
// handlers_php.go:534
s.phpMgr.SetDomainConfig(domain, req.Key, req.Value)
```

### Why it's exploitable

A non-admin user who is permitted to manage a domain sends:

```
PUT /api/v1/php/domains/{domain}/config
{"key":"memory_limit",
 "value":"128M\ndisable_functions =\nallow_url_include = On\nopen_basedir = /\nauto_prepend_file = /var/www/<domain>/web/shell.php"}
```

`memory_limit` passes the key blocklist. The newline-laden value is written
verbatim into the `-c` php.ini. Because these lines are appended after UWAS's
own `disable_functions`/`open_basedir`/`allow_url_include` lines and are applied
at PHP_INI_SYSTEM level, they:

1. clear `disable_functions` (re-enabling `system`/`shell_exec`/`exec` → OS command execution),
2. widen `open_basedir` to `/` (read arbitrary host files, cross-tenant access),
3. enable `allow_url_include`, and
4. set `auto_prepend_file` to an attacker-uploaded PHP file (runs on every request).

This defeats exactly the per-domain PHP isolation the blocklist
(`blockedPHPDirectives`) was built to enforce — a privilege escalation from
"manage my own PHP site" to arbitrary PHP/OS code execution and cross-tenant
compromise on shared hosting. The override is also persisted to the domain YAML
(`persistDomainPHPOverrides`) so it survives restarts.

Note: this affects the UWAS-managed php-cgi/php-fpm path (started with `-c tmpINI`).
The separate `.htaccess` `php_value` path (server_htaccess.go:152) emits `PHP_VALUE`
which PHP applies at PHP_INI_PERDIR and cannot override SYSTEM-level
`disable_functions`/`open_basedir`, so it is not an equivalent escape.

### Remediation

- Reject any override value containing `\n`, `\r`, or control characters in `SetDomainConfig`.
- Additionally validate the value against the directive's expected type/format (allowlist of safe chars), and quote values when emitting (`key = "value"`).
- Apply the same blocklist semantics to injected lines, or build the ini from a
  structured, escaped writer rather than `fmt.Sprintf("%s = %s")`.

---

## Secondary / informational

- **Global `SetConfig` / `updateINI` newline injection** (internal/phpmanager/ini.go:145, via internal/admin/handlers_php.go:168): same `key + " = " + value` pattern with no newline sanitization. Not reported as a separate vulnerability because the endpoint is admin-only (`requireAdmin`) and admins can already write raw php.ini via `SetConfigRaw`; it is not a privilege boundary. Worth fixing for defense-in-depth alongside RCE-001.

## Defenses observed (no finding)

- No Go dynamic-code surface: no `plugin.Open`, `yaegi`, `go/ast`/`go/parser` interpretation, and no `text/template`/`html/template` rendering of user input.
- FastCGI env builder strips inbound `PHP_*` HTTP headers (env.go:66) to block
  `PHP_ADMIN_VALUE`/`PHP_VALUE` header injection, and enforces `open_basedir`
  via `PHP_ADMIN_VALUE` per request (env.go:88-103).
- `SCRIPT_FILENAME`/`SCRIPT_NAME` derivation uses `pathsafe.RelativeToBase`
  containment (env.go:152), reducing FPM `SCRIPT_FILENAME` traversal risk.
- A per-domain directive key blocklist exists (manager.go:511-531) — the gap is
  that only keys, not values, are validated.
- OS-command sinks (`os/exec` across firewall, deploy, cron, terminal, etc.) are
  out of scope for this skill (covered by sc-cmdi) and were not double-reported here.
