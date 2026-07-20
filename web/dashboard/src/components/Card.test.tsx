import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import Card from './Card';
import { Zap } from 'lucide-react';

describe('Card', () => {
  const icon = <Zap size={20} role="img" aria-label="zap" />;

  it('renders icon, label, and value', () => {
    render(<Card icon={icon} label="Requests" value="1,234" />);

    expect(screen.getByRole('img', { name: 'zap' })).toBeInTheDocument();
    expect(screen.getByText('1,234')).toBeInTheDocument();
    expect(screen.getByText('Requests')).toBeInTheDocument();
  });

  it('renders sub label when provided', () => {
    render(<Card icon={icon} label="Bandwidth" value="4.2 GB" sub="prev: 3.1 GB" />);

    expect(screen.getByText('prev: 3.1 GB')).toBeInTheDocument();
  });

  it('does not render sub when omitted', () => {
    const { container } = render(<Card icon={icon} label="CPU" value="42%" />);

    // Only two text nodes: value and label — no sub <p>
    expect(container.querySelectorAll('p')).toHaveLength(2);
  });

  it('renders trend with positive value', () => {
    render(
      <Card
        icon={icon}
        label="Traffic"
        value="2.3 GB"
        trend={{ value: '12%', positive: true }}
      />,
    );

    const trendEl = screen.getByText('+12%');
    expect(trendEl).toBeInTheDocument();
    expect(trendEl.className).toContain('text-emerald-400');
  });

  it('renders trend with negative value', () => {
    render(
      <Card
        icon={icon}
        label="Errors"
        value="23"
        trend={{ value: '5%', positive: false }}
      />,
    );

    const trendEl = screen.getByText('5%');
    expect(trendEl).toBeInTheDocument();
    expect(trendEl.className).toContain('text-red-400');
  });

  it('does not render trend section when not provided', () => {
    const { container } = render(<Card icon={icon} label="Uptime" value="99.9%" />);

    // The trend span resides inside the flex header — no "text-xs font-medium" classes
    const trendSpans = container.querySelectorAll('span.text-xs.font-medium');
    expect(trendSpans).toHaveLength(0);
  });

  it('applies custom className alongside defaults', () => {
    const { container } = render(
      <Card icon={icon} label="Test" value="0" className="extra-class" />,
    );

    const div = container.firstChild as HTMLElement;
    expect(div.className).toContain('rounded-lg');
    expect(div.className).toContain('border-border');
    expect(div.className).toContain('extra-class');
  });

  it('handles numeric value', () => {
    render(<Card icon={icon} label="Count" value={100} />);

    expect(screen.getByText('100')).toBeInTheDocument();
  });
});
