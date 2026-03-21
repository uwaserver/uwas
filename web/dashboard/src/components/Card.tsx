import type { ReactNode } from 'react';

interface CardProps {
  icon: ReactNode;
  label: string;
  value: string | number;
  trend?: { value: string; positive: boolean };
  className?: string;
}

export default function Card({ icon, label, value, trend, className = '' }: CardProps) {
  return (
    <div
      className={`rounded-lg border border-[#334155] bg-[#1e293b] p-5 shadow-md ${className}`}
    >
      <div className="flex items-center justify-between">
        <span className="text-slate-400">{icon}</span>
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
      <p className="mt-3 text-2xl font-bold text-slate-100">{value}</p>
      <p className="mt-1 text-sm text-slate-400">{label}</p>
    </div>
  );
}
