import { Github, Server, Heart } from 'lucide-react'

const footerLinks = {
  product: [
    { label: 'Features', href: '#features' },
    { label: 'Architecture', href: '#architecture' },
    { label: 'Quick Start', href: '#quickstart' },
    { label: 'Compare', href: '#compare' },
    { label: 'Releases', href: 'https://github.com/uwaserver/uwas/releases', external: true },
  ],
  documentation: [
    { label: 'Configuration', href: 'https://github.com/uwaserver/uwas#configuration', external: true },
    { label: 'TLS / HTTPS', href: 'https://github.com/uwaserver/uwas#tls', external: true },
    { label: 'PHP / FastCGI', href: 'https://github.com/uwaserver/uwas#php', external: true },
    { label: 'Caching', href: 'https://github.com/uwaserver/uwas#cache', external: true },
    { label: 'Reverse Proxy', href: 'https://github.com/uwaserver/uwas#proxy', external: true },
  ],
  community: [
    { label: 'GitHub', href: 'https://github.com/uwaserver/uwas', external: true, icon: Github },
    { label: 'Issues', href: 'https://github.com/uwaserver/uwas/issues', external: true },
    { label: 'Discussions', href: 'https://github.com/uwaserver/uwas/discussions', external: true },
    { label: 'Changelog', href: 'https://github.com/uwaserver/uwas/blob/main/CHANGELOG.md', external: true },
  ],
}

export default function Footer() {
  return (
    <footer className="border-t" style={{ borderColor: 'var(--border)', backgroundColor: 'var(--bg-primary)' }}>
      <div className="mx-auto max-w-7xl px-4 py-12 sm:px-6 lg:px-8">
        <div className="grid grid-cols-1 gap-8 md:grid-cols-4">
          {/* Brand */}
          <div className="md:col-span-1">
            <a href="#" className="flex items-center gap-2.5 no-underline">
              <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-blue-600">
                <Server className="h-5 w-5 text-white" />
              </div>
              <span className="text-xl font-bold" style={{ color: 'var(--text-primary)' }}>UWAS</span>
            </a>
            <p className="mt-3 text-sm leading-relaxed" style={{ color: 'var(--text-secondary)' }}>
              One binary to serve them all. A modern web application server replacing Apache, Nginx, Varnish, and Caddy. Written in pure Go.
            </p>
          </div>

          {/* Product */}
          <div>
            <h3
              className="text-sm font-semibold uppercase tracking-wider"
              style={{ color: 'var(--text-primary)' }}
            >
              Product
            </h3>
            <ul className="mt-3 space-y-2">
              {footerLinks.product.map((link) => (
                <li key={link.label}>
                  <a
                    href={link.href}
                    {...(link.external ? { target: '_blank', rel: 'noopener noreferrer' } : {})}
                    className="text-sm no-underline transition-colors hover:opacity-80"
                    style={{ color: 'var(--text-secondary)' }}
                  >
                    {link.label}
                  </a>
                </li>
              ))}
            </ul>
          </div>

          {/* Documentation */}
          <div>
            <h3
              className="text-sm font-semibold uppercase tracking-wider"
              style={{ color: 'var(--text-primary)' }}
            >
              Documentation
            </h3>
            <ul className="mt-3 space-y-2">
              {footerLinks.documentation.map((link) => (
                <li key={link.label}>
                  <a
                    href={link.href}
                    {...(link.external ? { target: '_blank', rel: 'noopener noreferrer' } : {})}
                    className="text-sm no-underline transition-colors hover:opacity-80"
                    style={{ color: 'var(--text-secondary)' }}
                  >
                    {link.label}
                  </a>
                </li>
              ))}
            </ul>
          </div>

          {/* Community */}
          <div>
            <h3
              className="text-sm font-semibold uppercase tracking-wider"
              style={{ color: 'var(--text-primary)' }}
            >
              Community
            </h3>
            <ul className="mt-3 space-y-2">
              {footerLinks.community.map((link) => (
                <li key={link.label}>
                  <a
                    href={link.href}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex items-center gap-1.5 text-sm no-underline transition-colors hover:opacity-80"
                    style={{ color: 'var(--text-secondary)' }}
                  >
                    {'icon' in link && link.icon && <link.icon className="h-3.5 w-3.5" />}
                    {link.label}
                  </a>
                </li>
              ))}
            </ul>
          </div>
        </div>

        {/* Bottom bar */}
        <div
          className="mt-10 flex flex-col items-center justify-between gap-4 border-t pt-8 sm:flex-row"
          style={{ borderColor: 'var(--border)' }}
        >
          <p className="text-sm" style={{ color: 'var(--text-secondary)' }}>
            &copy; {new Date().getFullYear()} UWAS. AGPL-3.0 &middot; Commercial license available.
          </p>
          <p className="flex items-center gap-1 text-sm" style={{ color: 'var(--text-secondary)' }}>
            Made with <Heart className="h-3.5 w-3.5 text-red-500" /> by Ersin KOÇ
          </p>
        </div>
      </div>
    </footer>
  )
}
