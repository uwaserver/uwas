import { useState } from 'react'
import { Menu, X, Github, Server, Sun, Moon } from 'lucide-react'

interface NavbarProps {
  theme: 'dark' | 'light'
  onToggleTheme: () => void
}

const navLinks = [
  { href: '#features', label: 'Features' },
  { href: '#architecture', label: 'Architecture' },
  { href: '#quickstart', label: 'Quick Start' },
  { href: '#compare', label: 'Compare' },
]

export default function Navbar({ theme, onToggleTheme }: NavbarProps) {
  const [mobileOpen, setMobileOpen] = useState(false)

  function handleNavClick() {
    setMobileOpen(false)
  }

  return (
    <nav
      className="sticky top-0 z-50 border-b backdrop-blur-xl"
      style={{
        backgroundColor: theme === 'dark' ? 'rgba(10, 10, 15, 0.8)' : 'rgba(255, 255, 255, 0.8)',
        borderColor: 'var(--border)',
      }}
    >
      <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
        <div className="flex h-16 items-center justify-between">
          {/* Logo */}
          <a href="#" className="flex items-center gap-2.5 no-underline">
            <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-blue-600">
              <Server className="h-5 w-5 text-white" />
            </div>
            <span className="text-xl font-bold tracking-tight" style={{ color: 'var(--text-primary)' }}>
              UWAS
            </span>
          </a>

          {/* Desktop nav */}
          <div className="hidden items-center gap-1 md:flex">
            {navLinks.map((link) => (
              <a
                key={link.href}
                href={link.href}
                className="rounded-lg px-3.5 py-2 text-sm font-medium no-underline transition-colors hover:opacity-80"
                style={{ color: 'var(--text-secondary)' }}
              >
                {link.label}
              </a>
            ))}

            {/* Theme toggle */}
            <button
              onClick={onToggleTheme}
              className="ml-2 rounded-lg p-2 transition-colors hover:opacity-80"
              style={{ color: 'var(--text-secondary)' }}
              title={`Switch to ${theme === 'dark' ? 'light' : 'dark'} mode`}
            >
              {theme === 'dark' ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
            </button>

            {/* GitHub */}
            <a
              href="https://github.com/uwaserver/uwas"
              target="_blank"
              rel="noopener noreferrer"
              className="ml-1 flex items-center gap-2 rounded-lg border px-3.5 py-2 text-sm font-medium no-underline transition-colors hover:opacity-80"
              style={{ borderColor: 'var(--border)', color: 'var(--text-secondary)' }}
            >
              <Github className="h-4 w-4" />
              GitHub
            </a>
          </div>

          {/* Mobile toggle */}
          <div className="flex items-center gap-2 md:hidden">
            <button
              onClick={onToggleTheme}
              className="rounded-lg p-2 transition-colors"
              style={{ color: 'var(--text-secondary)' }}
            >
              {theme === 'dark' ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
            </button>
            <button
              onClick={() => setMobileOpen(!mobileOpen)}
              className="rounded-lg p-2 transition-colors"
              style={{ color: 'var(--text-secondary)' }}
            >
              {mobileOpen ? <X className="h-5 w-5" /> : <Menu className="h-5 w-5" />}
            </button>
          </div>
        </div>
      </div>

      {/* Mobile menu */}
      {mobileOpen && (
        <div
          className="border-t md:hidden"
          style={{ backgroundColor: 'var(--bg-primary)', borderColor: 'var(--border)' }}
        >
          <div className="space-y-1 px-4 py-3">
            {navLinks.map((link) => (
              <a
                key={link.href}
                href={link.href}
                onClick={handleNavClick}
                className="block rounded-lg px-3 py-2.5 text-sm font-medium no-underline transition-colors"
                style={{ color: 'var(--text-secondary)' }}
              >
                {link.label}
              </a>
            ))}
            <a
              href="https://github.com/uwaserver/uwas"
              target="_blank"
              rel="noopener noreferrer"
              onClick={handleNavClick}
              className="flex items-center gap-2 rounded-lg px-3 py-2.5 text-sm font-medium no-underline transition-colors"
              style={{ color: 'var(--text-secondary)' }}
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
