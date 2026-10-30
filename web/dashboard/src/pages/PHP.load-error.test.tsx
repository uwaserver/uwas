import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import PHP from './PHP';
import { ConfirmContext } from '@/components/useConfirm';

// F54600: a failed PHP/instances/domains load was swallowed, so the page said
// "No PHP (FastCGI/FPM) detected" and offered to install PHP on a host that
// already runs it.

const apiMocks = vi.hoisted(() => ({
  fetchPHP: vi.fn(),
  fetchPHPInstallInfo: vi.fn(),
  installPHP: vi.fn(),
  updatePHP: vi.fn(),
  uninstallPHP: vi.fn(),
  fetchPHPInstallStatus: vi.fn(),
  fetchDomains: vi.fn(),
  fetchDomainPHPInstances: vi.fn(),
  assignDomainPHP: vi.fn(),
  unassignDomainPHP: vi.fn(),
  startDomainPHP: vi.fn(),
  stopDomainPHP: vi.fn(),
  fetchDomainPHPConfig: vi.fn(),
  updateDomainPHPConfig: vi.fn(),
  enablePHP: vi.fn(),
  disablePHP: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

const confirmValue = {
  confirmAction: vi.fn().mockResolvedValue(true),
  promptText: vi.fn().mockResolvedValue(null),
};

const flush = () => act(async () => {});

function renderPage() {
  return render(
    <ConfirmContext.Provider value={confirmValue}>
      <PHP />
    </ConfirmContext.Provider>,
  );
}

describe('PHP load failures', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => fn.mockReset());
    apiMocks.fetchPHP.mockResolvedValue([]);
    apiMocks.fetchDomainPHPInstances.mockResolvedValue([]);
    apiMocks.fetchDomains.mockResolvedValue([]);
    apiMocks.fetchPHPInstallStatus.mockResolvedValue({ status: 'idle' });
  });

  it('control: an empty successful load shows the install prompt', async () => {
    renderPage();
    await flush();
    expect(screen.getByText(/No PHP \(FastCGI\/FPM\) detected/)).toBeInTheDocument();
  });

  it('a failed load is reported instead of claiming PHP is not installed', async () => {
    apiMocks.fetchPHP.mockRejectedValue(new Error('php list: backend unavailable'));
    renderPage();
    await flush();
    expect(screen.getByText(/php list: backend unavailable/)).toBeInTheDocument();
    expect(screen.queryByText(/No PHP \(FastCGI\/FPM\) detected/)).toBeNull();
  });
});
