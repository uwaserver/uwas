# sc-ssti results

No issues found by sc-ssti.

## Scope and defenses observed

- The Go backend uses **no server-side template engine**. There are zero imports of
  `text/template` or `html/template`, and no third-party templating libraries
  (`go.mod` deps are limited to `yaml.v3`, `brotli`, `quic-go`, `x/crypto`, `x/sync`).
- The only `tmpl` identifier (`internal/tls/manager.go:336`) is an `*x509.Certificate`
  struct used for self-signed cert generation — not a text/HTML template, so no
  injection surface.
- Notification channels (`internal/notify/channels.go:134,163,211-212`) build message
  bodies with `fmt.Sprintf`, but these are outbound payloads to Slack/Telegram/SMTP,
  not template-engine compilation. No user input is compiled as template code.
- The dashboard is a **client-side React 19 SPA** (Vite). No server-side rendering and
  no `nunjucks`/`handlebars`/`pug`/`ejs`/`mustache` dependencies in
  `web/dashboard/package.json`.
- No Python/Jinja2, PHP/Twig, Ruby/ERB, Java/Freemarker, or Velocity code is shipped in
  the binary.

Conclusion: there is no reachable SSTI surface in UWAS.
