import { BrowserRouter, Routes, Route } from 'react-router-dom'
import Navbar from '@/components/Navbar'
import Footer from '@/components/Footer'
import Home from '@/pages/Home'
import Docs from '@/pages/Docs'
import QuickStart from '@/pages/QuickStart'

export default function App() {
  return (
    <BrowserRouter>
      <div className="flex min-h-screen flex-col">
        <Navbar />
        <main className="flex-1">
          <Routes>
            <Route path="/" element={<Home />} />
            <Route path="/docs" element={<Docs />} />
            <Route path="/quickstart" element={<QuickStart />} />
          </Routes>
        </main>
        <Footer />
      </div>
    </BrowserRouter>
  )
}
