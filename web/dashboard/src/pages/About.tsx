import { useState, useEffect } from 'react';
import { GitBranch, ExternalLink, Heart, Shield, Server, Code2, BookOpen, Scale, RefreshCw } from 'lucide-react';
import { fetchSystem, fetchHealth, type SystemInfo, type HealthData } from '@/lib/api';

export default function About() {
  const [system, setSystem] = useState<SystemInfo | null>(null);
  const [health, setHealth] = useState<HealthData | null>(null);

  useEffect(() => {
    fetchSystem().then(setSystem).catch(() => {});
    fetchHealth().then(setHealth).catch(() => {});
  }, []);

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl font-bold sm:text-2xl text-foreground">About UWAS</h1>
        <p className="mt-1 text-sm text-muted-foreground">Unified Web Application Server</p>
      </div>

      {/* Hero */}
      <div className="rounded-lg border border-border bg-gradient-to-br from-blue-500/5 to-cyan-500/5 p-6">
        <div className="flex items-start gap-4">
          <div className="flex h-14 w-14 shrink-0 items-center justify-center rounded-xl bg-blue-600 text-white font-bold text-xl">U</div>
          <div>
            <h2 className="text-lg font-bold text-foreground">UWAS</h2>
            <p className="text-sm text-muted-foreground mt-1">
              One binary to serve them all. Replaces Apache + Nginx + Varnish + Caddy + cPanel with a single Go binary.
            </p>
            <div className="mt-3 flex flex-wrap gap-2">
              <a href="https://github.com/uwaserver/uwas" target="_blank" rel="noopener"
                className="inline-flex items-center gap-1.5 rounded-md bg-card border border-border px-3 py-1.5 text-xs font-medium text-card-foreground hover:bg-accent">
                  <GitBranch size={13} /> GitHub
              </a>
              <a href="https://uwaserver.com" target="_blank" rel="noopener"
                className="inline-flex items-center gap-1.5 rounded-md bg-card border border-border px-3 py-1.5 text-xs font-medium text-card-foreground hover:bg-accent">
                <ExternalLink size={13} /> Website
              </a>
              <a href="https://github.com/uwaserver/uwas/releases" target="_blank" rel="noopener"
                className="inline-flex items-center gap-1.5 rounded-md bg-card border border-border px-3 py-1.5 text-xs font-medium text-card-foreground hover:bg-accent">
                <RefreshCw size={13} /> Releases
              </a>
              <a href="https://github.com/uwaserver/uwas/issues" target="_blank" rel="noopener"
                className="inline-flex items-center gap-1.5 rounded-md bg-card border border-border px-3 py-1.5 text-xs font-medium text-card-foreground hover:bg-accent">
                <BookOpen size={13} /> Issues
              </a>
            </div>
          </div>
        </div>
      </div>

      {/* Version & System */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
        {[
          ['Version', system?.version || '—'],
          ['Go', system?.go_version || '—'],
          ['OS / Arch', system ? `${system.os}/${system.arch}` : '—'],
          ['Uptime', health?.uptime || '—'],
          ['CPUs', system?.cpus?.toString() || '—'],
          ['Status', health?.status === 'ok' ? 'Running' : '—'],
        ].map(([label, value]) => (
          <div key={label as string} className="rounded-lg border border-border bg-card px-4 py-3">
            <p className="text-[10px] text-muted-foreground">{label}</p>
            <p className="text-sm font-medium text-foreground">{value}</p>
          </div>
        ))}
      </div>

      {/* License */}
      <div className="rounded-lg border border-border bg-card p-5">
        <div className="flex items-center gap-2 mb-3">
          <Scale size={16} className="text-emerald-400" />
          <h2 className="text-sm font-semibold text-card-foreground">License</h2>
        </div>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div className="rounded-lg bg-emerald-500/5 border border-emerald-500/20 p-4">
            <h3 className="text-sm font-semibold text-emerald-400 mb-1">Open Source — AGPL-3.0</h3>
            <p className="text-xs text-muted-foreground">
              Free for community use. You can use, modify, and distribute UWAS under the AGPL-3.0 license.
              Modifications must be shared under the same license.
            </p>
            <a href="https://github.com/uwaserver/uwas/blob/main/LICENSE" target="_blank" rel="noopener"
              className="inline-flex items-center gap-1 mt-2 text-xs text-emerald-400 hover:underline">
              Read License <ExternalLink size={10} />
            </a>
          </div>
          <div className="rounded-lg bg-blue-500/5 border border-blue-500/20 p-4">
            <h3 className="text-sm font-semibold text-blue-400 mb-1">Commercial License</h3>
            <p className="text-xs text-muted-foreground">
              For enterprise and proprietary use without AGPL obligations.
              Includes priority support and SLAs.
            </p>
            <a href="https://uwaserver.com/enterprise" target="_blank" rel="noopener"
              className="inline-flex items-center gap-1 mt-2 text-xs text-blue-400 hover:underline">
              Enterprise Licensing <ExternalLink size={10} />
            </a>
          </div>
        </div>
      </div>

      {/* Features */}
      <div className="rounded-lg border border-border bg-card p-5">
        <div className="flex items-center gap-2 mb-3">
          <Server size={16} className="text-blue-400" />
          <h2 className="text-sm font-semibold text-card-foreground">What UWAS Replaces</h2>
        </div>
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-4">
          {[
            ['Apache / Nginx', 'HTTP/HTTPS/HTTP3 server'],
            ['Varnish', 'L1 memory + L2 disk cache'],
            ['Caddy', 'Auto HTTPS via ACME'],
            ['cPanel / Plesk', '35-page dashboard'],
            ['ModSecurity', 'Built-in WAF'],
            ['Fail2ban', 'Bot guard + rate limiting'],
            ['phpMyAdmin', 'Database management'],
            ['FileZilla Server', 'Built-in SFTP (pure Go)'],
          ].map(([name, desc]) => (
            <div key={name as string} className="rounded bg-background px-3 py-2">
              <p className="text-xs font-medium text-card-foreground">{name}</p>
              <p className="text-[10px] text-muted-foreground">{desc}</p>
            </div>
          ))}
        </div>
      </div>

      {/* Tech Stack */}
      <div className="rounded-lg border border-border bg-card p-5">
        <div className="flex items-center gap-2 mb-3">
          <Code2 size={16} className="text-purple-400" />
          <h2 className="text-sm font-semibold text-card-foreground">Tech Stack</h2>
        </div>
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
          {[
            ['Backend', 'Go 1.26+ (stdlib-first, 4 deps)'],
            ['Frontend', 'React 19 + TypeScript + Tailwind'],
            ['Build', 'Single binary (~14MB, CGO_ENABLED=0)'],
            ['Protocol', 'HTTP/1.1, HTTP/2, HTTP/3 (QUIC)'],
            ['PHP', 'FastCGI + .htaccess (mod_rewrite)'],
            ['TLS', 'ACME (Let\'s Encrypt) + SNI routing'],
          ].map(([label, value]) => (
            <div key={label as string} className="rounded bg-background px-3 py-2">
              <p className="text-xs font-medium text-card-foreground">{label}</p>
              <p className="text-[10px] text-muted-foreground">{value}</p>
            </div>
          ))}
        </div>
      </div>

      {/* Footer */}
      <div className="text-center py-4">
        <p className="flex items-center justify-center gap-1 text-xs text-muted-foreground">
          Made with <Heart size={12} className="text-red-500" /> by
          <a href="https://github.com/uwaserver" target="_blank" rel="noopener" className="text-blue-400 hover:underline">UWAS Team</a>
        </p>
        <p className="text-[10px] text-muted-foreground mt-1">
          <Shield size={10} className="inline mr-1" />
          AGPL-3.0 for community use &middot; Commercial license available
        </p>
      </div>
    </div>
  );
}
