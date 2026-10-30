import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import Firewall from './Firewall';
import { ConfirmContext } from '@/components/useConfirm';

// F55600: a failed initial firewall status request left fw=null, so the page
// said "Firewall Inactive / No rules are being enforced / No firewall rules
// configured" and offered "Enable Firewall" as if the state were known.

const apiMocks = vi.hoisted(() => ({
  fetchFirewall: vi.fn(), firewallAllow: vi.fn(), firewallDeny: vi.fn(),
  firewallDeleteRule: vi.fn(), firewallMoveRule: vi.fn(), firewallEnable: vi.fn(),
  firewallDisable: vi.fn(), firewallConfirm: vi.fn(),
}));
vi.mock('@/lib/api', () => apiMocks);
vi.mock('@/components/AutoBlockPanel', () => ({ default: () => null }));

const confirmValue = { confirmAction: vi.fn().mockResolvedValue(true), promptText: vi.fn().mockResolvedValue(null) };
const flush = () => act(async () => {});
const renderPage = () => render(<ConfirmContext.Provider value={confirmValue}><Firewall /></ConfirmContext.Provider>);

describe('Firewall initial load failure', () => {
  beforeEach(() => { vi.clearAllMocks(); });

  it('shows the error and not a made-up inactive/empty state', async () => {
    apiMocks.fetchFirewall.mockRejectedValue(new Error('upstream 502'));
    renderPage(); await flush();
    console.log('EXPECTED: error shown; no "Firewall Inactive", no empty-rules message');
    console.log('ACTUAL: inactive=' + !!screen.queryByText(/Firewall Inactive/) + ' empty=' + !!screen.queryByText(/No firewall rules configured/));
    expect(screen.getByText(/upstream 502/)).toBeTruthy();
    expect(screen.queryByText(/Firewall Inactive/)).toBeNull();
    expect(screen.queryByText(/No firewall rules configured/)).toBeNull();
    expect((screen.getByRole('button', { name: /Enable Firewall/ }) as HTMLButtonElement).disabled).toBe(true);
  });

  it('control: loaded inactive firewall with no rules says so', async () => {
    apiMocks.fetchFirewall.mockResolvedValue({ active: false, rules: [], backend: 'ufw' });
    renderPage(); await flush();
    expect(screen.getByText(/Firewall Inactive/)).toBeTruthy();
    expect(screen.getByText(/No firewall rules configured/)).toBeTruthy();
    expect((screen.getByRole('button', { name: /Enable Firewall/ }) as HTMLButtonElement).disabled).toBe(false);
  });
});
