import type { ReactNode } from 'react';

interface CardProps {
  icon: ReactNode;
  label: string;
  value: string | number;
  sub?: string;
  trend?: { value: string; positive: boolean };
  className?: string;
}

export default function Card({ icon, label, value, sub, trend, className = '' }: CardProps) {
  return (
    <div
      className={`rounded-lg border border-border bg-card p-5 shadow-md ${className}`}
    >
      <div className="flex items-center justify-between">
        <span className="text-muted-foreground">{icon}</span>
        {trend && (
          <span
            className={`text-xs font-medium ${
              trend.positive ? 'text-emerald-400' : 'text-red-400'
            }`}
          >
            {trend.positive ? '+' : ''}{trend.value}
          </span>
        )}
      </div>
      <p className="mt-3 text-2xl font-bold text-foreground">{value}</p>
      <p className="mt-1 text-sm text-muted-foreground">{label}</p>
      {sub && <p className="mt-0.5 text-xs text-muted-foreground">{sub}</p>}
    </div>
  );
}
