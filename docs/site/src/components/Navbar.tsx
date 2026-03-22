import { useState } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { Menu, X, Github, Server } from 'lucide-react'

export default function Navbar() {
  const [mobileOpen, setMobileOpen] = useState(false)
  const location = useLocation()

  const links = [
    { to: '/', label: 'Home' },
    { to: '/docs', label: 'Docs' },
    { to: '/quickstart', label: 'Quick Start' },
  ]

  function isActive(path: string) {
    if (path === '/') return location.pathname === '/'
    return location.pathname.startsWith(path)
  }

  return (
    <nav className="fixed top-0 left-0 right-0 z-50 border-b border-uwas-border bg-uwas-bg/80 backdrop-blur-xl">
      <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
        <div className="flex h-16 items-center justify-between">
          {/* Logo */}
          <Link to="/" className="flex items-center gap-2.5 no-underline">
            <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-uwas-blue">
              <Server className="h-5 w-5 text-white" />
            </div>
            <span className="text-xl font-bold tracking-tight text-uwas-text">UWAS</span>
          </Link>

          {/* Desktop links */}
          <div className="hidden items-center gap-1 md:flex">
            {links.map((link) => (
              <Link
                key={link.to}
                to={link.to}
                className={`rounded-lg px-3.5 py-2 text-sm font-medium no-underline transition-colors ${
                  isActive(link.to)
                    ? 'bg-uwas-blue/10 text-uwas-blue-light'
                    : 'text-uwas-text-muted hover:bg-uwas-bg-light hover:text-uwas-text'
                }`}
              >
                {link.label}
              </Link>
            ))}
            <a
              href="https://github.com/avrahambenaram/uwas"
              target="_blank"
              rel="noopener noreferrer"
              className="ml-2 flex items-center gap-2 rounded-lg border border-uwas-border px-3.5 py-2 text-sm font-medium text-uwas-text-muted no-underline transition-colors hover:border-uwas-blue hover:text-uwas-text"
            >
              <Github className="h-4 w-4" />
              GitHub
            </a>
          </div>

          {/* Mobile toggle */}
          <button
            onClick={() => setMobileOpen(!mobileOpen)}
            className="rounded-lg p-2 text-uwas-text-muted transition-colors hover:bg-uwas-bg-light md:hidden"
          >
            {mobileOpen ? <X className="h-5 w-5" /> : <Menu className="h-5 w-5" />}
          </button>
        </div>
      </div>

      {/* Mobile menu */}
      {mobileOpen && (
        <div className="border-t border-uwas-border bg-uwas-bg md:hidden">
          <div className="space-y-1 px-4 py-3">
            {links.map((link) => (
              <Link
                key={link.to}
                to={link.to}
                onClick={() => setMobileOpen(false)}
                className={`block rounded-lg px-3 py-2.5 text-sm font-medium no-underline transition-colors ${
                  isActive(link.to)
                    ? 'bg-uwas-blue/10 text-uwas-blue-light'
                    : 'text-uwas-text-muted hover:bg-uwas-bg-light hover:text-uwas-text'
                }`}
              >
                {link.label}
              </Link>
            ))}
            <a
              href="https://github.com/avrahambenaram/uwas"
              target="_blank"
              rel="noopener noreferrer"
              className="flex items-center gap-2 rounded-lg px-3 py-2.5 text-sm font-medium text-uwas-text-muted no-underline transition-colors hover:bg-uwas-bg-light hover:text-uwas-text"
            >
              <Github className="h-4 w-4" />
              GitHub
            </a>
          </div>
        </div>
      )}
    </nav>
  )
}
