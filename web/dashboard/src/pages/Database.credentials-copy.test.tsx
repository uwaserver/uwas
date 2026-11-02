import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import { CredentialsPanel } from './Database';

// F62600: the new-database credentials are shown once ("will not be shown
// again"), but a failed clipboard write (plain-HTTP panel, denied permission)
// was ignored, so the operator believed the password was copied.

const copyMock = vi.hoisted(() => vi.fn());
vi.mock('@/lib/clipboard', () => ({ copyText: copyMock }));
vi.mock('@/lib/api', () => ({}));

const renderPanel = () =>
  render(<CredentialsPanel name="shop" user="shop_u" host="127.0.0.1" password="s3cret" onDismiss={() => {}} />);

describe('CredentialsPanel copy feedback', () => {
  beforeEach(() => { vi.clearAllMocks(); });

  it('tells the operator when the copy failed', async () => {
    copyMock.mockResolvedValue(false);
    renderPanel();
    await act(async () => { fireEvent.click(screen.getByTitle('Copy password')); });
    console.log('EXPECTED: a visible copy-failed message');
    console.log('ACTUAL: ' + !!screen.queryByText(/Copy failed/));
    expect(screen.getByText(/Copy failed/)).toBeTruthy();
  });

  it('confirms a successful copy and shows no error', async () => {
    copyMock.mockResolvedValue(true);
    renderPanel();
    await act(async () => { fireEvent.click(screen.getByTitle('Copy password')); });
    expect(copyMock).toHaveBeenCalledWith('s3cret');
    expect(screen.queryByText(/Copy failed/)).toBeNull();
    expect(screen.getByTitle('Copied')).toBeTruthy();
  });

  it('control: the credentials stay visible either way', async () => {
    copyMock.mockResolvedValue(false);
    renderPanel();
    expect(screen.getByText('s3cret')).toBeTruthy();
    expect(screen.getByText('shop_u')).toBeTruthy();
  });
});
