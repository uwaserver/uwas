import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import FeatureBanner from './FeatureBanner';

describe('FeatureBanner', () => {
  const enabledStatus = { enabled: true };
  const disabledStatus = { enabled: false };
  const disabledWithReason = { enabled: false, reason: 'license limit reached' };

  it('returns null when status is null', () => {
    const { container } = render(
      <FeatureBanner feature="webhooks" status={null} />,
    );
    expect(container.firstChild).toBeNull();
  });

  it('returns null when status is undefined', () => {
    const { container } = render(
      <FeatureBanner feature="webhooks" status={undefined} />,
    );
    expect(container.firstChild).toBeNull();
  });

  it('returns null when feature is enabled', () => {
    const { container } = render(
      <FeatureBanner feature="webhooks" status={enabledStatus} />,
    );
    expect(container.firstChild).toBeNull();
  });

  it('shows heading with formatted feature name when disabled', () => {
    render(
      <FeatureBanner feature="cron_monitor" status={disabledStatus} />,
    );

    expect(screen.getByText('cron monitor is not enabled on this server')).toBeInTheDocument();
  });

  it('shows custom label when provided', () => {
    render(
      <FeatureBanner
        feature="cron_monitor"
        label="Cron Monitor"
        status={disabledStatus}
      />,
    );

    expect(screen.getByText('Cron Monitor is not enabled on this server')).toBeInTheDocument();
  });

  it('shows reason when provided', () => {
    render(
      <FeatureBanner
        feature="apps"
        status={disabledWithReason}
      />,
    );

    expect(screen.getByText('license limit reached')).toBeInTheDocument();
  });

  it('shows hint about empty list', () => {
    render(
      <FeatureBanner feature="webhooks" status={disabledStatus} />,
    );

    expect(
      screen.getByText(/Empty list below ≠ no data/),
    ).toBeInTheDocument();
  });

  it('renders warning banner with amber styling', () => {
    render(
      <FeatureBanner feature="backups" status={disabledStatus} />,
    );

    // The banner div should have amber border styling
    const banner = document.querySelector('[class*="amber"]');
    expect(banner).toBeInTheDocument();
  });

  it('handles feature name without underscores', () => {
    render(
      <FeatureBanner feature="ssl" status={disabledStatus} />,
    );

    expect(screen.getByText('ssl is not enabled on this server')).toBeInTheDocument();
  });
});
