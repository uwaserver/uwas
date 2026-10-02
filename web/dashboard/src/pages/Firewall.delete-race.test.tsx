import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import Firewall from './Firewall';
import { ConfirmContext } from '@/components/useConfirm';

// ── Mocks ───────────────────────────────────────────────────────────────────
const apiMocks = vi.hoisted(() => ({
  fetchFirewall: vi.fn(),
  firewallAllow: vi.fn(),
  firewallDeny: vi.fn(),
  firewallDeleteRule: vi.fn(),
  firewallMoveRule: vi.fn(),
  firewallEnable: vi.fn(),
  firewallDisable: vi.fn(),
  firewallConfirm: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);
// Firewall.tsx renders <AutoBlockPanel /> (own fetches + confirm modal) —
// irrelevant to the rules-table delete flow under test.
vi.mock('@/components/AutoBlockPanel', () => ({ default: () => null }));

const confirmValue = {
  confirmAction: vi.fn().mockResolvedValue(true),
  promptText: vi.fn().mockResolvedValue(null),
};

function renderPage() {
  return render(
    <ConfirmContext.Provider value={confirmValue}>
      <Firewall />
    </ConfirmContext.Provider>,
  );
}

// Flush mount effects + in-flight promise chains without advancing the clock.
const flush = () => act(async () => {});

const RULE_ALLOW_22 = {
  number: 1, action: 'ALLOW', from: 'anywhere', to: 'anywhere', port: '22', proto: 'tcp',
};
const RULE_DENY_80 = {
  number: 2, action: 'DENY', from: 'anywhere', to: 'anywhere', port: '80', proto: 'tcp',
};

// Click Delete on the FIRST row (rules render in list order), then confirm.
async function deleteFirstRow() {
  fireEvent.click(screen.getAllByRole('button', { name: 'Delete' })[0]);
  fireEvent.click(screen.getByRole('button', { name: 'Yes' }));
  await flush();
}

describe('Firewall delete verifies the rule number before deleting', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => {
      if (vi.isMockFunction(fn)) fn.mockReset();
    });
    apiMocks.fetchFirewall.mockResolvedValue({
      active: true, backend: 'nftables', rules: [RULE_ALLOW_22, RULE_DENY_80],
    });
    apiMocks.firewallDeleteRule.mockResolvedValue({ status: 'deleted' });
  });

  // Regression: firewall rule numbers are list POSITIONS — they renumber
  // whenever a rule above is added or removed (handlers_firewall.go deletes
  // by that number directly), and this page loads the list once and never
  // polls. If another admin / a CLI firewall change shifts the numbering
  // between load and confirm, "delete rule #1" would remove whatever rule
  // now holds number 1 — the operator asked to delete ALLOW 22/tcp, not the
  // rule that replaced it.
  it('refuses to delete when the number now maps to a different rule', async () => {
    apiMocks.fetchFirewall
      .mockResolvedValueOnce({ active: true, backend: 'nftables', rules: [RULE_ALLOW_22, RULE_DENY_80] })
      .mockResolvedValueOnce({ active: true, backend: 'nftables', rules: [RULE_DENY_80] }); // ALLOW 22 gone; DENY 80 renumbered to 1
    renderPage();
    await flush();
    await deleteFirstRow();

    expect(apiMocks.firewallDeleteRule).not.toHaveBeenCalled();
  });

  // Regression: same scenario, but the target still exists — shifted below
  // a new rule. Number 1 now holds a different rule, so acting on the stale
  // number would still delete the wrong rule; refuse and refresh.
  it('refuses when the target shifted to another number', async () => {
    const NEW_ALLOW_443 = { number: 1, action: 'ALLOW', from: 'anywhere', to: 'anywhere', port: '443', proto: 'tcp' };
    apiMocks.fetchFirewall
      .mockResolvedValueOnce({ active: true, backend: 'nftables', rules: [RULE_ALLOW_22, RULE_DENY_80] })
      .mockResolvedValueOnce({ active: true, backend: 'nftables', rules: [NEW_ALLOW_443, RULE_ALLOW_22, RULE_DENY_80] });
    renderPage();
    await flush();
    await deleteFirstRow();

    expect(apiMocks.firewallDeleteRule).not.toHaveBeenCalled();
  });

  // Control: an unchanged list deletes at the same number.
  it('deletes when the number still maps to the same rule', async () => {
    apiMocks.fetchFirewall
      .mockResolvedValueOnce({ active: true, backend: 'nftables', rules: [RULE_ALLOW_22, RULE_DENY_80] })
      .mockResolvedValueOnce({ active: true, backend: 'nftables', rules: [RULE_ALLOW_22, RULE_DENY_80] });
    renderPage();
    await flush();
    await deleteFirstRow();

    expect(apiMocks.firewallDeleteRule).toHaveBeenCalledWith(1);
  });
});

// ── Move: same positional-number hazard ─────────────────────────────────────
// handleFirewallMove moves by rule NUMBER too (handlers_firewall.go:140-164),
// and rule numbers renumber whenever the list changes. A stale number moves
// whichever rule now holds it — reordering first-match filtering precedence.
// Rows render in list order, so the second row's "Move up" button is
// getAllByRole('button', { name: 'Move up' })[1].
const NEW_ALLOW_443 = {
  number: 1, action: 'ALLOW', from: 'anywhere', to: 'anywhere', port: '443', proto: 'tcp',
};

describe('Firewall move verifies the rule number before moving', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => {
      if (vi.isMockFunction(fn)) fn.mockReset();
    });
    apiMocks.fetchFirewall.mockResolvedValue({
      active: true, backend: 'nftables', rules: [RULE_ALLOW_22, RULE_DENY_80],
    });
    apiMocks.firewallMoveRule.mockResolvedValue({ status: 'moved' });
  });

  async function moveUpSecondRow() {
    fireEvent.click(screen.getAllByRole('button', { name: 'Move up' })[1]);
    await flush();
  }

  // Regression: number 2 now maps to a different rule (ALLOW 22 was removed,
  // DENY 80 renumbered to 1, a new rule took 2) — the stale move would
  // displace the new rule instead of DENY 80.
  it('refuses to move when the number now maps to a different rule', async () => {
    apiMocks.fetchFirewall
      .mockResolvedValueOnce({ active: true, backend: 'nftables', rules: [RULE_ALLOW_22, RULE_DENY_80] })
      .mockResolvedValueOnce({ active: true, backend: 'nftables', rules: [{ ...RULE_DENY_80, number: 1 }, { ...NEW_ALLOW_443, number: 2 }] });
    renderPage();
    await flush();
    await moveUpSecondRow();

    expect(apiMocks.firewallMoveRule).not.toHaveBeenCalled();
  });

  // Regression: the target shifted to another number (a new rule inserted
  // above) — number 2 now holds ALLOW 22; moving IT would reorder the wrong
  // rule and leave DENY 80's precedence untouched.
  it('refuses when the target shifted to another number', async () => {
    apiMocks.fetchFirewall
      .mockResolvedValueOnce({ active: true, backend: 'nftables', rules: [RULE_ALLOW_22, RULE_DENY_80] })
      .mockResolvedValueOnce({ active: true, backend: 'nftables', rules: [{ ...NEW_ALLOW_443, number: 1 }, { ...RULE_ALLOW_22, number: 2 }, { ...RULE_DENY_80, number: 3 }] });
    renderPage();
    await flush();
    await moveUpSecondRow();

    expect(apiMocks.firewallMoveRule).not.toHaveBeenCalled();
  });

  // Control: an unchanged list moves at the same number.
  it('moves when the number still maps to the same rule', async () => {
    apiMocks.fetchFirewall
      .mockResolvedValueOnce({ active: true, backend: 'nftables', rules: [RULE_ALLOW_22, RULE_DENY_80] })
      .mockResolvedValueOnce({ active: true, backend: 'nftables', rules: [RULE_ALLOW_22, RULE_DENY_80] });
    renderPage();
    await flush();
    await moveUpSecondRow();

    expect(apiMocks.firewallMoveRule).toHaveBeenCalledWith(2, 'up');
  });
});
