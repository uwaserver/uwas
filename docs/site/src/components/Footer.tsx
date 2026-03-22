import { Link } from 'react-router-dom'
import { Github, Server, Heart } from 'lucide-react'

export default function Footer() {
  return (
    <footer className="border-t border-uwas-border bg-uwas-bg">
      <div className="mx-auto max-w-7xl px-4 py-12 sm:px-6 lg:px-8">
        <div className="grid grid-cols-1 gap-8 md:grid-cols-4">
          {/* Brand */}
          <div className="md:col-span-1">
            <div className="flex items-center gap-2.5">
              <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-uwas-blue">
                <Server className="h-5 w-5 text-white" />
              </div>
              <span className="text-xl font-bold text-uwas-text">UWAS</span>
            </div>
            <p className="mt-3 text-sm leading-relaxed text-uwas-text-muted">
              One binary to serve them all. A modern web application server replacing Apache, Nginx, Varnish, and Caddy.
            </p>
          </div>

          {/* Product */}
          <div>
            <h3 className="text-sm font-semibold uppercase tracking-wider text-uwas-text">Product</h3>
            <ul className="mt-3 space-y-2">
              <li><Link to="/" className="text-sm text-uwas-text-muted no-underline hover:text-uwas-blue-light">Home</Link></li>
              <li><Link to="/docs" className="text-sm text-uwas-text-muted no-underline hover:text-uwas-blue-light">Documentation</Link></li>
              <li><Link to="/quickstart" className="text-sm text-uwas-text-muted no-underline hover:text-uwas-blue-light">Quick Start</Link></li>
            </ul>
          </div>

          {/* Resources */}
          <div>
            <h3 className="text-sm font-semibold uppercase tracking-wider text-uwas-text">Resources</h3>
            <ul className="mt-3 space-y-2">
              <li><Link to="/docs#configuration" className="text-sm text-uwas-text-muted no-underline hover:text-uwas-blue-light">Configuration</Link></li>
              <li><Link to="/docs#tls" className="text-sm text-uwas-text-muted no-underline hover:text-uwas-blue-light">TLS / HTTPS</Link></li>
              <li><Link to="/docs#cli" className="text-sm text-uwas-text-muted no-underline hover:text-uwas-blue-light">CLI Reference</Link></li>
              <li><Link to="/docs#docker" className="text-sm text-uwas-text-muted no-underline hover:text-uwas-blue-light">Docker</Link></li>
            </ul>
          </div>

          {/* Community */}
          <div>
            <h3 className="text-sm font-semibold uppercase tracking-wider text-uwas-text">Community</h3>
            <ul className="mt-3 space-y-2">
              <li>
                <a
                  href="https://github.com/avrahambenaram/uwas"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1.5 text-sm text-uwas-text-muted no-underline hover:text-uwas-blue-light"
                >
                  <Github className="h-3.5 w-3.5" />
                  GitHub
                </a>
              </li>
              <li>
                <a
                  href="https://github.com/avrahambenaram/uwas/issues"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-sm text-uwas-text-muted no-underline hover:text-uwas-blue-light"
                >
                  Report an Issue
                </a>
              </li>
              <li>
                <a
                  href="https://github.com/avrahambenaram/uwas/releases"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-sm text-uwas-text-muted no-underline hover:text-uwas-blue-light"
                >
                  Releases
                </a>
              </li>
            </ul>
          </div>
        </div>

        <div className="mt-10 flex flex-col items-center justify-between gap-4 border-t border-uwas-border pt-8 sm:flex-row">
          <p className="text-sm text-uwas-text-muted">
            &copy; {new Date().getFullYear()} UWAS. Released under the MIT License.
          </p>
          <p className="flex items-center gap-1 text-sm text-uwas-text-muted">
            Made with <Heart className="h-3.5 w-3.5 text-red-500" /> by the UWAS community
          </p>
        </div>
      </div>
    </footer>
  )
}
