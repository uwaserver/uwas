import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import Sidebar from './Sidebar';

// ── Mocks ──────────────────────────────────────────────────────────────────

const mockFetchSystem = vi.fn();
const mockFetchBranding = vi.fn();
const mockFetchFeatures = vi.fn();
const mockLogout = vi.fn().mockResolvedValue(undefined);
const mockToggleTheme = vi.fn();

vi.mock('@/lib/api', () => ({
  fetchSystem: () => mockFetchSystem(),
  fetchBranding: () => mockFetchBranding(),
  fetchFeatures: () => mockFetchFeatures(),
  logout: () => mockLogout(),
}));

vi.mock('@/hooks/useTheme', () => ({
  useTheme: () => ({ theme: 'dark', toggle: mockToggleTheme }),
}));

// ── Helpers ─────────────────────────────────────────────────────────────────

async function renderSidebar(
  overrides?: {
    version?: string;
    branding?: Record<string, unknown>;
    features?: Record<string, unknown>;
  },
  initialEntries = ['/'],
) {
  const { version = 'v0.8.9', branding = {}, features = {} } = overrides ?? {};
  mockFetchSystem.mockResolvedValue({ version });
  mockFetchBranding.mockResolvedValue(branding);
  mockFetchFeatures.mockResolvedValue(features);

  let result: ReturnType<typeof render>;
  await act(async () => {
    result = render(
      <MemoryRouter initialEntries={initialEntries}>
        <Sidebar />
      </MemoryRouter>,
    );
  });
  return result!;
}

/** Click a group heading (e.g. "Sites") to toggle its sub-items. */
function expandGroup(label: string) {
  const btn = screen.getByText(label);
  fireEvent.click(btn);
}

// ── Tests ───────────────────────────────────────────────────────────────────

describe('Sidebar', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders all navigation group headings', async () => {
    await renderSidebar();

    const groups = ['Dashboard', 'Sites', 'Server', 'Performance', 'Security', 'System'];
    for (const group of groups) {
      expect(screen.getByText(group)).toBeInTheDocument();
    }
  });

  it('shows navigation links when their group is expanded', async () => {
    await renderSidebar();

    // Groups start collapsed — expand them
    expandGroup('Sites');
    expect(screen.getByText('Domains')).toBeInTheDocument();
    expect(screen.getByText('DNS')).toBeInTheDocument();
    expect(screen.getByText('Certificates')).toBeInTheDocument();

    expandGroup('Server');
    expect(screen.getByText('PHP')).toBeInTheDocument();
    expect(screen.getByText('Applications')).toBeInTheDocument();
    expect(screen.getByText('Database')).toBeInTheDocument();

    expandGroup('Performance');
    expect(screen.getByText('Cache')).toBeInTheDocument();
    expect(screen.getByText('Metrics')).toBeInTheDocument();
    expect(screen.getByText('Logs')).toBeInTheDocument();

    expandGroup('System');
    expect(screen.getByText('Backups')).toBeInTheDocument();
    expect(screen.getByText('Settings')).toBeInTheDocument();
    expect(screen.getByText('About')).toBeInTheDocument();
  });

  it('toggles group collapse on button click', async () => {
    await renderSidebar();

    // Sites group starts collapsed
    expect(screen.queryByText('Domains')).not.toBeInTheDocument();

    expandGroup('Sites');
    expect(screen.getByText('Domains')).toBeInTheDocument();

    // Click again to collapse
    expandGroup('Sites');
    expect(screen.queryByText('Domains')).not.toBeInTheDocument();
  });

  it('auto-expands group when a child route is active', async () => {
    await renderSidebar({}, ['/dns']); // /dns → Sites group

    await waitFor(() => {
      expect(screen.getByText('DNS')).toBeInTheDocument();
    });
  });

  it('renders version badge from API', async () => {
    await renderSidebar();

    await waitFor(() => {
      expect(screen.getByText('v0.8.9')).toBeInTheDocument();
    });
  });

  it('renders branding name from API', async () => {
    await renderSidebar({ branding: { name: 'MyServer' } });

    await waitFor(() => {
      expect(screen.getByText('MyServer')).toBeInTheDocument();
    });
  });

  it('renders custom logo when branding provides logo_url', async () => {
    await renderSidebar({ branding: { logo_url: '/custom-logo.svg', name: 'MyServer' } });

    const img = await screen.findByRole('img');
    expect(img).toHaveAttribute('src', '/custom-logo.svg');
  });

  it('dims a nav item when its feature is disabled', async () => {
    await renderSidebar({
      features: { apps: { enabled: false, reason: 'license limit' } },
    });

    await waitFor(() => {
      expandGroup('Server');
    });

    const appsLink = screen.getByText('Applications').closest('a');
    expect(appsLink?.className).toContain('opacity-50');
    expect(screen.getByText('off')).toBeInTheDocument();
  });

  it('renders theme toggle and logout buttons', async () => {
    await renderSidebar();

    expect(screen.getByText('Light Mode')).toBeInTheDocument(); // dark theme → shows "Light Mode"
    expect(screen.getByText('Logout')).toBeInTheDocument();
  });

  it('calls theme toggle on button click', async () => {
    await renderSidebar();

    fireEvent.click(screen.getByText('Light Mode'));
    expect(mockToggleTheme).toHaveBeenCalledTimes(1);
  });

  it('calls logout on logout button click', async () => {
    await renderSidebar();

    await act(async () => {
      fireEvent.click(screen.getByText('Logout'));
    });
    expect(mockLogout).toHaveBeenCalledTimes(1);
  });

  it('shows mobile menu toggle button', async () => {
    await renderSidebar();

    const toggleBtn = screen.getByLabelText('Toggle menu');
    expect(toggleBtn).toBeInTheDocument();

    // Sidebar starts translated off-screen on mobile
    const aside = document.querySelector('aside');
    expect(aside?.className).toContain('-translate-x-full');
  });

  it('opens mobile menu when toggle button is clicked', async () => {
    await renderSidebar();
    fireEvent.click(screen.getByLabelText('Toggle menu'));

    const aside = document.querySelector('aside');
    expect(aside?.className).toContain('translate-x-0');
  });

  it('closes mobile menu when backdrop is clicked', async () => {
    await renderSidebar();

    // Open
    fireEvent.click(screen.getByLabelText('Toggle menu'));
    expect(document.querySelector('aside')?.className).toContain('translate-x-0');

    // Backdrop exists
    const backdrops = document.querySelectorAll('.fixed.inset-0');
    // One may be the backdrop, another the sidebar's own wrapper
    const backdrop = Array.from(backdrops).find(
      (el) => el.tagName === 'DIV' && !el.querySelector('aside'),
    );
    expect(backdrop).toBeInTheDocument();

    // Click backdrop
    fireEvent.click(backdrop!);
    expect(document.querySelector('aside')?.className).toContain('-translate-x-full');
  });

  it('closes mobile menu when a nav link is clicked', async () => {
    await renderSidebar();

    // Open mobile menu
    fireEvent.click(screen.getByLabelText('Toggle menu'));

    // Expand a group
    expandGroup('System');
    // Click About link (inside the group)
    fireEvent.click(screen.getByText('About'));

    // Sidebar should close
    expect(document.querySelector('aside')?.className).toContain('-translate-x-full');
  });
});
