import { useState, useEffect } from 'react'
import Navbar from '@/components/Navbar'
import Hero from '@/components/Hero'
import Features from '@/components/Features'
import Pipeline from '@/components/Pipeline'
import QuickStart from '@/components/QuickStart'
import Comparison from '@/components/Comparison'
import Performance from '@/components/Performance'
import CTA from '@/components/CTA'
import Footer from '@/components/Footer'

export default function App() {
  const [theme, setTheme] = useState<'dark' | 'light'>(() => {
    if (typeof window !== 'undefined') {
      const saved = localStorage.getItem('uwas-theme')
      if (saved === 'light' || saved === 'dark') return saved
    }
    return 'dark'
  })

  useEffect(() => {
    document.documentElement.classList.toggle('light', theme === 'light')
    localStorage.setItem('uwas-theme', theme)
  }, [theme])

  function toggleTheme() {
    setTheme((prev) => (prev === 'dark' ? 'light' : 'dark'))
  }

  return (
    <div className="min-h-screen" style={{ backgroundColor: 'var(--bg-primary)', color: 'var(--text-primary)' }}>
      <Navbar theme={theme} onToggleTheme={toggleTheme} />
      <main>
        <Hero />
        <Features />
        <Pipeline />
        <QuickStart />
        <Comparison />
        <Performance />
        <CTA />
      </main>
      <Footer />
    </div>
  )
}
