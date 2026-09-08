// Settings section definitions.
//
// Kept out of Settings.tsx so they can be imported by tests: exporting a
// non-component from a component module trips react-refresh/only-export-
// components, which is an error in this project's lint config.

import { Activity, AlertTriangle, Archive, Ban, Clock, Cpu, Database, FileText, Globe, Lock, Paintbrush, Server, Shield, Users } from 'lucide-react';

export interface FieldDef {
  key: string;       // dot-path in YAML, e.g. "global.http_listen"
  label: string;
  type: 'text' | 'number' | 'toggle' | 'select' | 'secret' | 'textarea';
  placeholder?: string;
  options?: { value: string; label: string }[];
  help?: string;
  fullWidth?: boolean; // span all columns
}

export interface SectionDef {
  id: string;
  title: string;
  icon: React.ReactNode;
  iconColor: string;
  fields: FieldDef[];
  /** Shown above the fields. Use it when a section's settings are stored but
   *  not acted on, so the panel does not imply behaviour the server lacks. */
  notice?: string;
}

// ---------------------------------------------------------------------------
// Section definitions
// ---------------------------------------------------------------------------

export type SettingsTab = 'general' | 'security' | 'performance' | 'integrations';
export const SETTINGS_TABS: { id: SettingsTab; label: string }[] = [
  { id: 'general', label: 'General' },
  { id: 'security', label: 'Security' },
  { id: 'performance', label: 'Performance' },
  { id: 'integrations', label: 'Integrations' },
];

export const SECTION_GROUPS: Record<string, SettingsTab> = {
  server: 'general', timeouts: 'general', logging: 'general', branding: 'general',
  admin: 'security', acme: 'security', users: 'security', mcp: 'security', oauth: 'security',
  autoblock: 'security', watchdog: 'security',
  cache: 'performance',
  backup: 'integrations', alerting: 'integrations', trusted_proxies: 'integrations',
};

export const SECTIONS: SectionDef[] = [
  {
    id: 'server',
    title: 'Server',
    icon: <Server size={18} />,
    iconColor: 'text-blue-400',
    fields: [
      { key: 'global.http_listen', label: 'HTTP Listen', type: 'text', placeholder: ':80' },
      { key: 'global.https_listen', label: 'HTTPS Listen', type: 'text', placeholder: ':443' },
      { key: 'global.http3', label: 'HTTP/3 (QUIC)', type: 'toggle', help: 'Enable HTTP/3 via QUIC. Requires HTTPS. Advertised via Alt-Svc header.' },
      { key: 'global.worker_count', label: 'Worker Count', type: 'text', placeholder: 'auto', help: '"auto" or a number' },
      { key: 'global.max_connections', label: 'Max Connections', type: 'number', placeholder: '65536' },
      { key: 'global.pid_file', label: 'PID File', type: 'text', placeholder: '/var/run/uwas.pid' },
      { key: 'global.web_root', label: 'Web Root', type: 'text', placeholder: '/var/www' },
    ],
  },
  {
    id: 'timeouts',
    title: 'Timeouts',
    icon: <Clock size={18} />,
    iconColor: 'text-amber-400',
    fields: [
      { key: 'global.timeouts.read', label: 'Read Timeout', type: 'text', placeholder: '30s' },
      { key: 'global.timeouts.read_header', label: 'Read Header Timeout', type: 'text', placeholder: '10s' },
      { key: 'global.timeouts.write', label: 'Write Timeout', type: 'text', placeholder: '60s' },
      { key: 'global.timeouts.idle', label: 'Idle Timeout', type: 'text', placeholder: '120s' },
      { key: 'global.timeouts.shutdown_grace', label: 'Shutdown Grace', type: 'text', placeholder: '30s' },
      { key: 'global.timeouts.max_header_bytes', label: 'Max Header Bytes', type: 'number', placeholder: '1048576' },
    ],
  },
  {
    id: 'admin',
    title: 'Admin',
    icon: <Shield size={18} />,
    iconColor: 'text-purple-400',
    fields: [
      { key: 'global.admin.enabled', label: 'Enabled', type: 'toggle' },
      { key: 'global.admin.listen', label: 'Listen Address', type: 'text', placeholder: '127.0.0.1:9443', help: 'Loopback by default. Binding a public address exposes the admin API — put it behind a tunnel or a firewall rule if you do.' },
      { key: 'global.admin.api_key', label: 'API Key', type: 'secret' },
    ],
  },
  {
    id: 'acme',
    title: 'ACME / Let\'s Encrypt',
    icon: <Lock size={18} />,
    iconColor: 'text-emerald-400',
    fields: [
      { key: 'global.acme.email', label: 'Email', type: 'text', placeholder: 'admin@example.com' },
      { key: 'global.acme.ca_url', label: 'CA URL', type: 'text', placeholder: 'https://acme-v02.api.letsencrypt.org/directory' },
      { key: 'global.acme.storage', label: 'Storage Path', type: 'text', placeholder: '/var/lib/uwas/certs' },
      { key: 'global.acme.dns_provider', label: 'DNS Provider', type: 'text', placeholder: 'cloudflare' },
      { key: 'global.acme.on_demand', label: 'On Demand', type: 'toggle' },
      { key: 'global.acme.on_demand_ask', label: 'On Demand Ask URL', type: 'text', placeholder: 'https://example.com/check' },
    ],
  },
  {
    id: 'cache',
    title: 'Cache',
    icon: <Database size={18} />,
    iconColor: 'text-cyan-400',
    fields: [
      { key: 'global.cache.enabled', label: 'Enabled', type: 'toggle' },
      { key: 'global.cache.memory_limit', label: 'Memory Limit', type: 'text', placeholder: '512MB', help: 'e.g. 256MB, 1GB' },
      { key: 'global.cache.disk_path', label: 'Disk Path', type: 'text', placeholder: '/var/cache/uwas' },
      { key: 'global.cache.disk_limit', label: 'Disk Limit', type: 'text', placeholder: '10GB' },
      { key: 'global.cache.default_ttl', label: 'Default TTL (seconds)', type: 'number', placeholder: '3600' },
      { key: 'global.cache.grace_ttl', label: 'Grace TTL (seconds)', type: 'number', placeholder: '86400', help: 'How long past its TTL an entry may still be served while a refresh runs. Applies only with stale_while_revalidate on.' },
      { key: 'global.cache.stale_while_revalidate', label: 'Stale While Revalidate', type: 'toggle' },
      { key: 'global.cache.purge_key', label: 'Purge Key', type: 'secret' },
    ],
  },
  {
    id: 'backup',
    title: 'Backup',
    icon: <Archive size={18} />,
    iconColor: 'text-orange-400',
    fields: [
      { key: 'global.backup.enabled', label: 'Enabled', type: 'toggle' },
      { key: 'global.backup.provider', label: 'Provider', type: 'select', options: [
        { value: 'local', label: 'Local' },
        { value: 's3', label: 'S3' },
        { value: 'sftp', label: 'SFTP' },
      ]},
      { key: 'global.backup.schedule', label: 'Schedule', type: 'text', placeholder: '24h' },
      { key: 'global.backup.keep', label: 'Keep Last N', type: 'number', placeholder: '7' },
    ],
  },
  {
    id: 'trusted_proxies',
    title: 'Trusted Proxies',
    icon: <Globe size={18} />,
    iconColor: 'text-teal-400',
    fields: [
      { key: 'global.trusted_proxies', label: 'Trusted Proxy CIDRs', type: 'textarea', placeholder: '10.0.0.0/8\n172.16.0.0/12\n192.168.0.0/16', help: 'One CIDR per line. Used to trust X-Forwarded-For headers from these sources.', fullWidth: true },
    ],
  },
  {
    id: 'logging',
    title: 'Logging',
    icon: <FileText size={18} />,
    iconColor: 'text-rose-400',
    fields: [
      { key: 'global.log_level', label: 'Log Level', type: 'select', options: [
        { value: 'debug', label: 'Debug' },
        { value: 'info', label: 'Info' },
        { value: 'warn', label: 'Warn' },
        { value: 'error', label: 'Error' },
      ]},
      { key: 'global.log_format', label: 'Log Format', type: 'select', options: [
        { value: 'text', label: 'Text' },
        { value: 'json', label: 'JSON' },
      ]},
      { key: 'global.access_log.enabled', label: 'Request Log', type: 'toggle', help: 'Writes one line per request to the main log. Turn it off to quieten journalctl without raising the log level, which would also hide certificate renewals, reloads and backups. Metrics, analytics, the panel log view and per-domain access_log files are unaffected.', fullWidth: true },
    ],
  },
  {
    id: 'alerting',
    title: 'Alerting & Notifications',
    icon: <AlertTriangle size={18} />,
    iconColor: 'text-yellow-400',
    fields: [
      { key: 'global.alerting.enabled', label: 'Enabled', type: 'toggle' },
      { key: 'global.alerting.webhook_url', label: 'Generic Webhook URL', type: 'text', placeholder: 'https://example.com/webhook', help: 'Receives JSON POST with alert data' },
      { key: 'global.alerting.slack_url', label: 'Slack Webhook URL', type: 'text', placeholder: 'https://hooks.slack.com/services/T.../B.../xxx', help: 'Slack > Apps > Incoming Webhooks > Add' },
      { key: 'global.alerting.telegram_token', label: 'Telegram Bot Token', type: 'secret', placeholder: '123456789:ABCdefGHIjklMNOpqrSTUvwxYZ', help: 'Message @BotFather on Telegram → /newbot' },
      { key: 'global.alerting.telegram_chat_id', label: 'Telegram Chat ID', type: 'text', placeholder: '-1001234567890', help: 'Use @userinfobot or @getidsbot to find your chat ID' },
      { key: 'global.alerting.email_smtp_host', label: 'SMTP Host', type: 'text', placeholder: 'smtp.gmail.com:587' },
      { key: 'global.alerting.email_from', label: 'Email From', type: 'text', placeholder: 'alerts@example.com' },
      { key: 'global.alerting.email_to', label: 'Email To', type: 'text', placeholder: 'admin@example.com' },
    ],
  },
  {
    id: 'mcp',
    title: 'MCP',
    icon: <Cpu size={18} />,
    iconColor: 'text-indigo-400',
    fields: [
      { key: 'global.mcp.enabled', label: 'Enabled', type: 'toggle' },
      { key: 'global.mcp.listen', label: 'Listen Address', type: 'text', placeholder: '127.0.0.1:9444' },
    ],
  },
  {
    id: 'users',
    title: 'Multi-User Auth',
    icon: <Users size={18} />,
    iconColor: 'text-cyan-400',
    fields: [
      { key: 'global.users.enabled', label: 'Enable Multi-User Auth', type: 'toggle' },
      { key: 'global.users.allow_reseller', label: 'Allow Resellers', type: 'toggle' },
    ],
  },
  {
    id: 'oauth',
    title: 'OAuth2 / SSO Login',
    icon: <Globe size={18} />,
    iconColor: 'text-violet-400',
    notice:
      'Not implemented. These values are stored and returned by the API, but no OAuth login flow exists — ' +
      'enabling this does not add a sign-in method, and Allowed Emails does not restrict who can reach the panel. ' +
      'The panel stays protected by its existing authentication only.',
    fields: [
      { key: 'global.admin.oauth.enabled', label: 'Enable OAuth2 Login', type: 'toggle' },
      { key: 'global.admin.oauth.google_client_id', label: 'Google Client ID', type: 'text', placeholder: 'xxxx.apps.googleusercontent.com' },
      { key: 'global.admin.oauth.google_client_secret', label: 'Google Client Secret', type: 'secret', placeholder: 'GOCSPX-xxx' },
      { key: 'global.admin.oauth.github_client_id', label: 'GitHub Client ID', type: 'text', placeholder: 'Iv1.xxx' },
      { key: 'global.admin.oauth.github_client_secret', label: 'GitHub Client Secret', type: 'secret', placeholder: 'ghp_xxx' },
      { key: 'global.admin.oauth.allowed_emails', label: 'Allowed Emails (comma-separated)', type: 'text', placeholder: 'admin@example.com, dev@example.com', help: 'Not enforced — no OAuth flow exists to authenticate against' },
    ],
  },
  {
    id: 'branding',
    title: 'White-Label Branding',
    icon: <Paintbrush size={18} />,
    iconColor: 'text-pink-400',
    fields: [
      { key: 'global.admin.branding.name', label: 'Panel Name', type: 'text', placeholder: 'My Hosting Panel' },
      { key: 'global.admin.branding.logo_url', label: 'Logo URL', type: 'text', placeholder: 'https://example.com/logo.svg or data:image/svg+xml;...' },
      { key: 'global.admin.branding.favicon_url', label: 'Favicon URL', type: 'text', placeholder: 'https://example.com/favicon.ico' },
      { key: 'global.admin.branding.primary_color', label: 'Primary Color', type: 'text', placeholder: '#3b82f6' },
      { key: 'global.admin.branding.footer_text', label: 'Footer Text', type: 'text', placeholder: 'Powered by UWAS' },
    ],
  },
  {
    id: 'autoblock',
    title: 'Auto-Block (flood protection)',
    icon: <Ban size={18} />,
    iconColor: 'text-red-400',
    notice:
      'Blocks abusive source IPs at the connection accept path — the only layer that sees a TLS-handshake flood, ' +
      'which the WAF and rate limiter never do. Enabling, disabling, and threshold changes take effect after a ' +
      'service restart (the listener guard is wired at startup); the whitelist and firewall sync refresh on reload. ' +
      'Run with Dry Run on first and watch the logs before enforcing. Behind a CDN, make sure the Cloudflare IP ranges ' +
      'and trusted proxies are set — otherwise a busy edge IP gets blocked and takes the site offline for everyone it serves. ' +
      'The whitelist itself is edited in the Config Editor (raw YAML). Live blocks are managed on the Firewall page.',
    fields: [
      { key: 'global.autoblock.enabled', label: 'Enable Auto-Block', type: 'toggle', help: 'Takes effect after a service restart' },
      { key: 'global.autoblock.dry_run', label: 'Dry Run (detect only)', type: 'toggle', help: 'Log what would be blocked without enforcing. Calibrate thresholds here first.' },
      { key: 'global.autoblock.firewall_sync', label: 'Firewall Sync (ufw/iptables)', type: 'toggle', help: 'Push blocks to the kernel so the SYN is refused before it reaches the process' },
      { key: 'global.autoblock.feed_rate_hits', label: 'Escalate Rate-Limit Hits', type: 'toggle', help: 'On: repeated 429s count toward Max Rate-Limit Hits and can hard-block the IP. Off: rate limiting stays a soft throttle (safer behind NAT).' },
      { key: 'global.autoblock.window', label: 'Window', type: 'text', placeholder: '60s', help: 'Counting period for every threshold below' },
      { key: 'global.autoblock.max_connections', label: 'Max New Connections', type: 'number', placeholder: '600', help: 'New TCP connections per IP per window. 0 disables (false-positives on connection-heavy or NAT-shared clients).' },
      { key: 'global.autoblock.max_aborts', label: 'Max Handshake Aborts', type: 'number', placeholder: '60', help: 'Connections closed without sending a byte — the TLS-flood signature' },
      { key: 'global.autoblock.max_concurrent', label: 'Max Concurrent Connections', type: 'number', placeholder: '150', help: '0 disables this check' },
      { key: 'global.autoblock.max_waf_hits', label: 'Max WAF Hits', type: 'number', placeholder: '15', help: 'WAF + bot-guard rejections per IP per window' },
      { key: 'global.autoblock.max_rate_hits', label: 'Max Rate-Limit Hits', type: 'number', placeholder: '120' },
      { key: 'global.autoblock.max_not_found', label: 'Max 404s', type: 'number', placeholder: '200', help: 'Catches vulnerability-scanning sweeps' },
      { key: 'global.autoblock.block_duration', label: 'Block Duration', type: 'text', placeholder: '15m', help: 'First offence' },
      { key: 'global.autoblock.escalate', label: 'Escalate Repeat Offenders', type: 'toggle', help: '15m → 1h → 4h … up to the cap below' },
      { key: 'global.autoblock.max_block_duration', label: 'Max Block Duration', type: 'text', placeholder: '24h', help: 'Escalation cap' },
      { key: 'global.autoblock.state_path', label: 'State File', type: 'text', placeholder: '/var/lib/uwas/autoblock.json', help: 'Active blocks persist across restarts' },
    ],
  },
  {
    id: 'watchdog',
    title: 'Liveness Watchdog',
    icon: <Activity size={18} />,
    iconColor: 'text-emerald-400',
    notice:
      "Proves the server is still answering by probing its own listener, and stops feeding systemd's watchdog when it " +
      'is not — turning a wedged-but-running process into a restart. Requires WatchdogSec in the systemd unit (the ' +
      'installer writes a drop-in). Enabling or disabling takes effect after a service restart. Leave this off unless ' +
      'WatchdogSec is set, or systemd will restart a healthy server on a timer.',
    fields: [
      { key: 'global.watchdog.enabled', label: 'Enable Watchdog', type: 'toggle', help: 'Takes effect after a service restart' },
      { key: 'global.watchdog.interval', label: 'Probe Interval', type: 'text', placeholder: '15s', help: 'Auto-reduced to half of WatchdogSec if that is shorter' },
      { key: 'global.watchdog.timeout', label: 'Probe Timeout', type: 'text', placeholder: '5s', help: 'Must be shorter than the interval' },
      { key: 'global.watchdog.failures', label: 'Failures Before Action', type: 'number', placeholder: '3', help: 'Consecutive failed probes before the server is treated as wedged' },
      { key: 'global.watchdog.self_restart', label: 'Self-Restart (no systemd)', type: 'toggle', help: 'Exit the process on failure when there is no systemd watchdog (Docker, plain supervisor)' },
    ],
  },
];
