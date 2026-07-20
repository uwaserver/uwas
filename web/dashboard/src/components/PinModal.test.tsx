import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import PinModal from './PinModal';

describe('PinModal', () => {
  const onConfirm = vi.fn();
  const onCancel = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('returns null when open is false', () => {
    const { container } = render(
      <PinModal open={false} onConfirm={onConfirm} onCancel={onCancel} />,
    );
    expect(container.firstChild).toBeNull();
  });

  it('renders the dialog when open is true', () => {
    render(<PinModal open={true} onConfirm={onConfirm} onCancel={onCancel} />);

    expect(screen.getByText('Pin Required')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('Enter pin code')).toBeInTheDocument();
    expect(screen.getByText('Cancel')).toBeInTheDocument();
    expect(screen.getByText('Confirm')).toBeInTheDocument();
  });

  it('shows custom title and message', () => {
    render(
      <PinModal
        open={true}
        title="Delete Domain"
        message="Enter your PIN to confirm domain deletion."
        onConfirm={onConfirm}
        onCancel={onCancel}
      />,
    );

    expect(screen.getByText('Delete Domain')).toBeInTheDocument();
    expect(screen.getByText('Enter your PIN to confirm domain deletion.')).toBeInTheDocument();
  });

  it('uses default message when not provided', () => {
    render(<PinModal open={true} onConfirm={onConfirm} onCancel={onCancel} />);

    expect(
      screen.getByText('Enter your pin code to confirm this destructive operation.'),
    ).toBeInTheDocument();
  });

  it('calls onConfirm with the trimmed pin when submitted', async () => {
    render(<PinModal open={true} onConfirm={onConfirm} onCancel={onCancel} />);

    const input = screen.getByPlaceholderText('Enter pin code');
    await userEvent.type(input, ' 1234 ');

    fireEvent.click(screen.getByText('Confirm'));

    expect(onConfirm).toHaveBeenCalledWith('1234');
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it('shows validation error when pin is empty', async () => {
    render(<PinModal open={true} onConfirm={onConfirm} onCancel={onCancel} />);

    fireEvent.click(screen.getByText('Confirm'));

    expect(screen.getByText('Pin code required')).toBeInTheDocument();
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it('shows validation error when pin is only whitespace', async () => {
    render(<PinModal open={true} onConfirm={onConfirm} onCancel={onCancel} />);

    const input = screen.getByPlaceholderText('Enter pin code');
    await userEvent.type(input, '   ');

    fireEvent.click(screen.getByText('Confirm'));

    expect(screen.getByText('Pin code required')).toBeInTheDocument();
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it('clears error when user starts typing after validation failure', async () => {
    render(<PinModal open={true} onConfirm={onConfirm} onCancel={onCancel} />);

    fireEvent.click(screen.getByText('Confirm'));
    expect(screen.getByText('Pin code required')).toBeInTheDocument();

    const input = screen.getByPlaceholderText('Enter pin code');
    await userEvent.type(input, '1');

    expect(screen.queryByText('Pin code required')).not.toBeInTheDocument();
  });

  it('shows red border when validation fails', async () => {
    render(<PinModal open={true} onConfirm={onConfirm} onCancel={onCancel} />);

    fireEvent.click(screen.getByText('Confirm'));

    const input = screen.getByPlaceholderText('Enter pin code');
    expect(input.className).toContain('border-red-500');
  });

  it('calls onCancel and resets state when Cancel is clicked', async () => {
    render(<PinModal open={true} onConfirm={onConfirm} onCancel={onCancel} />);

    const input = screen.getByPlaceholderText('Enter pin code') as HTMLInputElement;
    await userEvent.type(input, '1234');

    fireEvent.click(screen.getByText('Cancel'));

    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('calls onCancel and resets state when backdrop is clicked', async () => {
    render(<PinModal open={true} onConfirm={onConfirm} onCancel={onCancel} />);

    const backdrop = document.querySelector('div.fixed.inset-0');
    fireEvent.click(backdrop!);

    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('does not close when clicking inside the modal', async () => {
    render(<PinModal open={true} onConfirm={onConfirm} onCancel={onCancel} />);

    const modalCard = document.querySelector('.max-w-sm');
    fireEvent.click(modalCard!);

    expect(onCancel).not.toHaveBeenCalled();
  });

  it('has password input type for pin', () => {
    render(<PinModal open={true} onConfirm={onConfirm} onCancel={onCancel} />);

    const input = screen.getByPlaceholderText('Enter pin code');
    expect(input).toHaveAttribute('type', 'password');
  });

  it('has autocomplete off', () => {
    render(<PinModal open={true} onConfirm={onConfirm} onCancel={onCancel} />);

    const input = screen.getByPlaceholderText('Enter pin code');
    expect(input).toHaveAttribute('autocomplete', 'off');
  });
});
