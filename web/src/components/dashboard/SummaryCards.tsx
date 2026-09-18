import React from 'react';
import { Activity, CheckCircle2, AlertTriangle, HelpCircle, PauseCircle } from 'lucide-react';
import { Skeleton } from '../common/Skeleton';
import type { MonitorSummary } from '../../hooks/useMonitors';

interface SummaryCardsProps {
  summary: MonitorSummary;
  isLoading: boolean;
}

export const SummaryCards: React.FC<SummaryCardsProps> = ({ summary, isLoading }) => {
  if (isLoading) {
    return (
      <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-3.5 mb-6">
        {[...Array(5)].map((_, i) => (
          <div
            key={i}
            className="p-4 rounded-xl bg-zinc-900/60 border border-zinc-800 space-y-2"
          >
            <Skeleton className="h-4 w-20" />
            <Skeleton className="h-7 w-12" />
          </div>
        ))}
      </div>
    );
  }

  const cards = [
    {
      label: 'Total Monitors',
      value: summary.total,
      icon: <Activity className="w-4 h-4 text-blue-400" />,
      color: 'text-zinc-100',
      border: 'border-zinc-800',
    },
    {
      label: 'Healthy',
      value: summary.healthy,
      icon: <CheckCircle2 className="w-4 h-4 text-emerald-400" />,
      color: 'text-emerald-400',
      border: 'border-emerald-900/40',
    },
    {
      label: 'Unhealthy',
      value: summary.unhealthy,
      icon: <AlertTriangle className="w-4 h-4 text-rose-400" />,
      color: 'text-rose-400',
      border: 'border-rose-900/40',
    },
    {
      label: 'Unknown',
      value: summary.unknown,
      icon: <HelpCircle className="w-4 h-4 text-zinc-400" />,
      color: 'text-zinc-400',
      border: 'border-zinc-800',
    },
    {
      label: 'Disabled',
      value: summary.disabled,
      icon: <PauseCircle className="w-4 h-4 text-amber-400" />,
      color: 'text-amber-400',
      border: 'border-amber-900/30',
    },
  ];

  return (
    <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-3.5 mb-6">
      {cards.map((c) => (
        <div
          key={c.label}
          className={`p-4 rounded-xl bg-zinc-900/70 backdrop-blur-sm border ${c.border} shadow-sm transition-all`}
        >
          <div className="flex items-center justify-between text-xs text-zinc-400 mb-1.5">
            <span>{c.label}</span>
            {c.icon}
          </div>
          <div className={`text-2xl font-bold font-mono tracking-tight ${c.color}`}>
            {c.value}
          </div>
        </div>
      ))}
    </div>
  );
};
