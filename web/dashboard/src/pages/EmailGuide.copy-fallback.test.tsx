import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import EmailGuide from './EmailGuide';

// ── Mocks ───────────────────────────────────────────────────────────────────
const apiMocks = vi.hoisted(() => ({
  fetchDomains: vi.fn(),
  fetchServerIPs: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

// copyText resolves FALSE when every copy strategy failed (lib/clipboard
// contract) — the boundary under test.
const clipboardMocks = vi.hoisted(() => ({ copyText: vi.fn() }));
vi.mock('@/lib/clipboard', () => clipboardMocks);

function renderPage() {
  return render(
    <MemoryRouter>
      <EmailGuide />
    </MemoryRouter>,
  );
}

const flush = () => act(async () => {});

// The record rows copy via icon-only buttons (Copy/Check swap). Scope by the
// lucide-copy icon inside the button.
function recordCopyButtons() {
  return Array.from(document.querySelectorAll('svg.lucide-copy')).map(s => s.closest('button') as HTMLButtonElement);
}

describe('EmailGuide copy reports clipboard failure instead of false success', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => {
      if (vi.isMockFunction(fn)) fn.mockReset();
    });
    clipboardMocks.copyText.mockReset();
    apiMocks.fetchDomains.mockResolvedValue([
      { host: 'mail.example', type: 'static', aliases: null },
    ]);
    apiMocks.fetchServerIPs.mockResolvedValue({ ips: [], public_ip: '203.0.113.10' });
  });

  // Regression: the guide's DNS records are what the operator copies into
  // their DNS provider — a check icon on a failed copy sends them away with
  // records that were never copied.
  it('shows a copy-failed message when the clipboard write fails', async () => {
    clipboardMocks.copyText.mockResolvedValue(false);
    renderPage();
    await flush();

    const btns = recordCopyButtons();
    expect(btns.length).toBeGreaterThan(0);
    fireEvent.click(btns[0]);
    await flush();

    expect(clipboardMocks.copyText).toHaveBeenCalled();
    expect(screen.getByText(/Copy failed/i)).toBeTruthy();
  });

  // Control: a successful copy keeps the existing feedback (check icon).
  it('keeps the success signal when the clipboard write succeeds', async () => {
    clipboardMocks.copyText.mockResolvedValue(true);
    renderPage();
    await flush();

    const btns = recordCopyButtons();
    fireEvent.click(btns[0]);
    await flush();

    expect(screen.queryByText(/Copy failed/i)).toBeNull();
  });
});
