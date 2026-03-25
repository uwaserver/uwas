import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { ThemeProvider } from './hooks/useTheme';
import './index.css';
import App from './App.tsx';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ThemeProvider>
      <BrowserRouter basename="/_uwas/dashboard">
        <App />
      </BrowserRouter>
    </ThemeProvider>
  </StrictMode>,
);
