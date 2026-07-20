import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import { ThemeProvider, useTheme } from './useTheme';

// ── Test harness ────────────────────────────────────────────────────────────

function TestComponent() {
  const { theme, toggle } = useTheme();
  return (
    <div>
      <span data-testid="theme">{theme}</span>
      <button data-testid="toggle" onClick={toggle}>
        Toggle
      </button>
    </div>
  );
}

async function renderWithProvider() {
  let result: ReturnType<typeof render>;
  await act(async () => {
    result = render(
      <ThemeProvider>
        <TestComponent />
      </ThemeProvider>,
    );
  });
  return result!;
}

describe('useTheme', () => {
  beforeEach(() => {
    localStorage.clear();
    document.documentElement.classList.remove('light');
  });

  afterEach(() => {
    localStorage.clear();
    document.documentElement.classList.remove('light');
  });

  it('defaults to dark theme when no saved preference', async () => {
    await renderWithProvider();

    expect(screen.getByTestId('theme').textContent).toBe('dark');
  });

  it('reads saved light theme from localStorage', async () => {
    localStorage.setItem('uwas-theme', 'light');
    await renderWithProvider();

    expect(screen.getByTestId('theme').textContent).toBe('light');
  });

  it('reads saved dark theme from localStorage', async () => {
    localStorage.setItem('uwas-theme', 'dark');
    await renderWithProvider();

    expect(screen.getByTestId('theme').textContent).toBe('dark');
  });

  it('persists theme to localStorage on toggle', async () => {
    await renderWithProvider();

    expect(screen.getByTestId('theme').textContent).toBe('dark');

    await act(async () => {
      fireEvent.click(screen.getByTestId('toggle'));
    });

    expect(screen.getByTestId('theme').textContent).toBe('light');
    expect(localStorage.getItem('uwas-theme')).toBe('light');
  });

  it('toggles back to dark on second toggle', async () => {
    await renderWithProvider();

    await act(async () => {
      fireEvent.click(screen.getByTestId('toggle'));
    });
    expect(screen.getByTestId('theme').textContent).toBe('light');

    await act(async () => {
      fireEvent.click(screen.getByTestId('toggle'));
    });
    expect(screen.getByTestId('theme').textContent).toBe('dark');
  });

  it('adds light class to documentElement when theme is light', async () => {
    await renderWithProvider();

    await act(async () => {
      fireEvent.click(screen.getByTestId('toggle'));
    });
    expect(document.documentElement.classList.contains('light')).toBe(true);
  });

  it('removes light class from documentElement when theme is dark', async () => {
    // First set to light
    localStorage.setItem('uwas-theme', 'light');
    await renderWithProvider();
    expect(document.documentElement.classList.contains('light')).toBe(true);

    // Toggle to dark
    await act(async () => {
      fireEvent.click(screen.getByTestId('toggle'));
    });
    expect(document.documentElement.classList.contains('light')).toBe(false);
  });

  it('handles invalid localStorage value by defaulting to dark', async () => {
    localStorage.setItem('uwas-theme', 'invalid');
    await renderWithProvider();

    expect(screen.getByTestId('theme').textContent).toBe('dark');
  });
});
