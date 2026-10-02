import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import CloneStaging from './CloneStaging';

// ── Mocks ───────────────────────────────────────────────────────────────────
const apiMocks = vi.hoisted(() => ({
  fetchDomains: vi.fn(),
  cloneSite: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

// copyText resolves FALSE when every copy strategy failed (lib/clipboard
// contract) — the boundary under test.
const clipboardMocks = vi.hoisted(() => ({ copyText: vi.fn() }));
vi.mock('@/lib/clipboard', () => clipboardMocks);

function renderPage() {
  return render(<CloneStaging />);
}

const flush = () => act(async () => {});

const DOMAIN = { host: 'mail.example', type: 'static', aliases: null };

// Select the source, let the target auto-suggest, clone, and wait for the
// result panel (which holds the per-value copy buttons).
async function cloneAndRenderResult() {
  fireEvent.change(screen.getByRole('combobox'), { target: { value: DOMAIN.host } });
  await flush();
  fireEvent.click(screen.getByRole('button', { name: /clone site/i }));
  await flush();
}

// The result panel's copy buttons are icon-only (Copy/CheckIcon swap). The
// Clone Site button ALSO carries a CopyIcon — scope to the result card.
function resultCopyButtons() {
  const card = screen.getByText('Clone Complete').closest('div');
  return Array.from(card?.querySelectorAll('svg.lucide-copy') ?? []).map(s => s.closest('button') as HTMLButtonElement);
}

describe('CloneStaging copy reports clipboard failure instead of false success', () => {
  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => {
      if (vi.isMockFunction(fn)) fn.mockReset();
    });
    clipboardMocks.copyText.mockReset();
    apiMocks.fetchDomains.mockResolvedValue([DOMAIN]);
    apiMocks.cloneSite.mockResolvedValue({
      status: 'done',
      source_domain: DOMAIN.host,
      target_domain: 'staging.mail.example',
      target_root: '/var/www/staging.mail.example',
    });
  });

  // Regression: the result panel's values (target domain, docroot) are what
  // the operator copies into DNS and their notes — a check icon on a failed
  // copy sends them away with values that were never copied.
  it('shows a copy-failed message when the clipboard write fails', async () => {
    clipboardMocks.copyText.mockResolvedValue(false);
    renderPage();
    await flush();
    await cloneAndRenderResult();

    const btns = resultCopyButtons();
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
    await cloneAndRenderResult();

    const btns = resultCopyButtons();
    fireEvent.click(btns[0]);
    await flush();

    expect(screen.queryByText(/Copy failed/i)).toBeNull();
  });
});
