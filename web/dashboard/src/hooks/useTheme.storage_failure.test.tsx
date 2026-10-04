import { it, expect, vi, afterEach } from 'vitest';
import { createElement } from 'react';
import { render, screen, fireEvent, cleanup } from '@testing-library/react';
import { ThemeProvider, useTheme } from '@/hooks/useTheme';
function Probe(){ const { theme, toggle }=useTheme();return createElement('button',{'data-testid':'theme',onClick:toggle},theme); }
function mount(){return render(createElement(ThemeProvider,null,createElement(Probe)));}
function deny(kind:'read'|'write'|'both') {
 if(kind==='read'||kind==='both') vi.spyOn(Storage.prototype,'getItem').mockImplementation(()=>{throw new DOMException('Preference storage blocked','SecurityError')});
 if(kind==='write'||kind==='both') vi.spyOn(Storage.prototype,'setItem').mockImplementation(()=>{throw new DOMException('Preference storage unavailable','QuotaExceededError')});
}
afterEach(()=>{cleanup();vi.restoreAllMocks();localStorage.clear();document.documentElement.classList.remove('light')});
it('theme mounts and toggles when preference reads or writes are unavailable',()=>{
 for(const kind of ['read','write','both'] as const){deny(kind);mount();expect(screen.getByTestId('theme').textContent).toBe('dark');fireEvent.click(screen.getByTestId('theme'));expect(screen.getByTestId('theme').textContent).toBe('light');expect(document.documentElement.classList.contains('light')).toBe(true);fireEvent.click(screen.getByTestId('theme'));expect(screen.getByTestId('theme').textContent).toBe('dark');cleanup();vi.restoreAllMocks();localStorage.clear()}
 localStorage.setItem('uwas-theme','light');mount();expect(screen.getByTestId('theme').textContent).toBe('light');fireEvent.click(screen.getByTestId('theme'));expect(localStorage.getItem('uwas-theme')).toBe('dark');
});
