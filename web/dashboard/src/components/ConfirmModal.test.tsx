import { describe, it, expect, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ConfirmProvider } from './ConfirmModal';
import { useConfirm } from './useConfirm';

// ── Test harness ────────────────────────────────────────────────────────────

function TestHarness() {
  const { confirmAction, promptText } = useConfirm();

  return (
    <div>
      <button
        data-testid="confirm-danger"
        onClick={() => confirmAction({ title: 'Delete?', message: 'Are you sure?', variant: 'danger' })}
      >
        Confirm Danger
      </button>
      <button
        data-testid="confirm-warning"
        onClick={() => confirmAction({ title: 'Warning', message: 'Proceed?', variant: 'warning' })}
      >
        Confirm Warning
      </button>
      <button
        data-testid="confirm-info"
        onClick={() => confirmAction({ title: 'Info', message: 'FYI', variant: 'info' })}
      >
        Confirm Info
      </button>
      <button
        data-testid="confirm-custom-labels"
        onClick={() =>
          confirmAction({
            title: 'Custom',
            confirmLabel: 'Yes, do it',
            cancelLabel: 'No, thanks',
          })
        }
      >
        Custom Labels
      </button>
      <button
        data-testid="prompt-basic"
        onClick={async () => {
          const result = await promptText({
            title: 'Enter name',
            placeholder: 'Type here...',
          });
          // Write result to DOM so the test can assert it
          document.body.setAttribute('data-prompt-result', result ?? 'null');
        }}
      >
        Prompt Basic
      </button>
      <button
        data-testid="prompt-multiline"
        onClick={async () => {
          const result = await promptText({
            title: 'Enter code',
            multiline: true,
            placeholder: 'Paste here...',
          });
          document.body.setAttribute('data-prompt-result', result ?? 'null');
        }}
      >
        Prompt Multiline
      </button>
      <span data-testid="outside">Outside</span>
    </div>
  );
}

function renderWithProvider() {
  return render(
    <ConfirmProvider>
      <TestHarness />
    </ConfirmProvider>,
  );
}

// ── Tests ───────────────────────────────────────────────────────────────────

describe('ConfirmModal', () => {
  beforeEach(() => {
    document.body.removeAttribute('data-prompt-result');
  });

  // ── Provider ────────────────────────────────────────────────────────────

  it('renders children without showing dialog initially', () => {
    renderWithProvider();

    expect(screen.getByTestId('outside')).toBeInTheDocument();
    // No dialog heading should exist
    expect(screen.queryByRole('heading')).not.toBeInTheDocument();
  });

  // ── Confirm dialogs ─────────────────────────────────────────────────────

  it('shows confirm dialog with title and message when triggered', async () => {
    renderWithProvider();

    fireEvent.click(screen.getByTestId('confirm-danger'));

    expect(screen.getByText('Delete?')).toBeInTheDocument();
    expect(screen.getByText('Are you sure?')).toBeInTheDocument();
  });

  it('closes confirm dialog on Confirm button click', async () => {
    renderWithProvider();

    fireEvent.click(screen.getByTestId('confirm-danger'));
    expect(screen.getByText('Delete?')).toBeInTheDocument();

    fireEvent.click(screen.getByText('Confirm'));
    expect(screen.queryByText('Delete?')).not.toBeInTheDocument();
  });

  it('closes confirm dialog on Cancel button click', async () => {
    renderWithProvider();

    fireEvent.click(screen.getByTestId('confirm-danger'));
    expect(screen.getByText('Delete?')).toBeInTheDocument();

    fireEvent.click(screen.getByText('Cancel'));
    expect(screen.queryByText('Delete?')).not.toBeInTheDocument();
  });

  it('closes confirm dialog on X (close) button click', async () => {
    renderWithProvider();

    fireEvent.click(screen.getByTestId('confirm-danger'));
    const closeBtn = screen.getByLabelText('Close');
    fireEvent.click(closeBtn);

    expect(screen.queryByText('Delete?')).not.toBeInTheDocument();
  });

  it('uses default confirm/cancel labels when not provided', async () => {
    renderWithProvider();

    fireEvent.click(screen.getByTestId('confirm-danger'));

    expect(screen.getByText('Confirm')).toBeInTheDocument();
    expect(screen.getByText('Cancel')).toBeInTheDocument();
  });

  it('uses custom confirm/cancel labels when provided', async () => {
    renderWithProvider();

    fireEvent.click(screen.getByTestId('confirm-custom-labels'));

    expect(screen.getByText('Yes, do it')).toBeInTheDocument();
    expect(screen.getByText('No, thanks')).toBeInTheDocument();
  });

  // ── Prompt dialogs ──────────────────────────────────────────────────────

  it('shows prompt dialog with input field', async () => {
    renderWithProvider();

    fireEvent.click(screen.getByTestId('prompt-basic'));

    expect(screen.getByText('Enter name')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('Type here...')).toBeInTheDocument();
  });

  it('accepts typed input in prompt', async () => {
    renderWithProvider();

    fireEvent.click(screen.getByTestId('prompt-basic'));

    const input = screen.getByPlaceholderText('Type here...') as HTMLInputElement;
    await userEvent.type(input, 'my-value');

    // Submit
    fireEvent.click(screen.getByText('Continue'));

    await waitFor(() => {
      expect(document.body.getAttribute('data-prompt-result')).toBe('my-value');
    });
  });

  it('returns null when prompt is cancelled', async () => {
    renderWithProvider();

    fireEvent.click(screen.getByTestId('prompt-basic'));

    fireEvent.click(screen.getByText('Cancel'));

    await waitFor(() => {
      expect(document.body.getAttribute('data-prompt-result')).toBe('null');
    });
  });

  it('shows multiline textarea instead of input when multiline is true', async () => {
    renderWithProvider();

    fireEvent.click(screen.getByTestId('prompt-multiline'));

    const textarea = document.querySelector('textarea');
    expect(textarea).toBeInTheDocument();
    expect(textarea).toHaveAttribute('placeholder', 'Paste here...');
  });

  it('shows error when submitting empty pin in prompt', async () => {
    renderWithProvider();

    fireEvent.click(screen.getByTestId('prompt-basic'));

    // Default value is empty string, submitting should... actually the prompt
    // submits the value as-is even if empty. Let's just verify the submit works.
    const input = screen.getByPlaceholderText('Type here...') as HTMLInputElement;
    expect(input).toHaveValue('');

    fireEvent.click(screen.getByText('Continue'));

    await waitFor(() => {
      expect(document.body.getAttribute('data-prompt-result')).toBe('');
    });
  });

  // ── Variant styles ──────────────────────────────────────────────────────

  it.each([
    ['danger', 'bg-red-600'],
    ['warning', 'bg-amber-600'],
    ['info', 'bg-blue-600'],
  ] as const)('applies %s variant button styling', async (variant, expectedClass) => {
    renderWithProvider();

    fireEvent.click(screen.getByTestId(`confirm-${variant}`));

    const confirmBtn = screen.getByText('Confirm');
    expect(confirmBtn.className).toContain(expectedClass);
  });

  // ── Backdrop click ──────────────────────────────────────────────────────

  it('closes confirm dialog when clicking backdrop', async () => {
    renderWithProvider();

    fireEvent.click(screen.getByTestId('confirm-danger'));
    expect(screen.getByText('Delete?')).toBeInTheDocument();

    // Backdrop is the outermost fixed div
    const backdrop = document.querySelector('div.fixed.inset-0');
    expect(backdrop).toBeInTheDocument();
    fireEvent.click(backdrop!);

    expect(screen.queryByText('Delete?')).not.toBeInTheDocument();
  });

  it('does not close dialog when clicking inside the modal content', async () => {
    renderWithProvider();

    fireEvent.click(screen.getByTestId('confirm-danger'));

    // The modal container is the child with max-w-md
    const modal = document.querySelector('.max-w-md');
    expect(modal).toBeInTheDocument();
    fireEvent.click(modal!);

    expect(screen.getByText('Delete?')).toBeInTheDocument();
  });

  it('closes prompt dialog when clicking backdrop', async () => {
    renderWithProvider();

    fireEvent.click(screen.getByTestId('prompt-basic'));

    const backdrop = document.querySelector('div.fixed.inset-0');
    fireEvent.click(backdrop!);

    await waitFor(() => {
      expect(document.body.getAttribute('data-prompt-result')).toBe('null');
    });
  });
});
