import React from 'react';
import clsx from 'clsx';
import { CheckCircle2, AlertTriangle, HelpCircle, PauseCircle, Globe, Cpu } from 'lucide-react';
import type { HealthState } from '../../types/status';
import type { MonitorKind } from '../../types/monitor';

interface HealthBadgeProps {
  state: HealthState | undefined;
  size?: 'sm' | 'md';
}

export const HealthBadge: React.FC<HealthBadgeProps> = ({ state = 'UNKNOWN', size = 'sm' }) => {
  const isSm = size === 'sm';

  switch (state) {
    case 'HEALTHY':
      return (
        <span
          className={clsx(
            'inline-flex items-center gap-1.5 font-medium rounded-md border font-mono',
            'bg-emerald-950/60 border-emerald-800/80 text-emerald-400 shadow-xs',
            isSm ? 'px-2 py-0.5 text-xs' : 'px-2.5 py-1 text-sm'
          )}
        >
          <CheckCircle2 className={isSm ? 'w-3.5 h-3.5' : 'w-4 h-4'} />
          <span>HEALTHY</span>
        </span>
      );
    case 'UNHEALTHY':
      return (
        <span
          className={clsx(
            'inline-flex items-center gap-1.5 font-medium rounded-md border font-mono',
            'bg-rose-950/60 border-rose-800/80 text-rose-400 shadow-xs',
            isSm ? 'px-2 py-0.5 text-xs' : 'px-2.5 py-1 text-sm'
          )}
        >
          <AlertTriangle className={isSm ? 'w-3.5 h-3.5' : 'w-4 h-4'} />
          <span>UNHEALTHY</span>
        </span>
      );
    case 'UNKNOWN':
    default:
      return (
        <span
          className={clsx(
            'inline-flex items-center gap-1.5 font-medium rounded-md border font-mono',
            'bg-zinc-900 border-zinc-800 text-zinc-400',
            isSm ? 'px-2 py-0.5 text-xs' : 'px-2.5 py-1 text-sm'
          )}
        >
          <HelpCircle className={isSm ? 'w-3.5 h-3.5' : 'w-4 h-4'} />
          <span>UNKNOWN</span>
        </span>
      );
  }
};

interface PausedBadgeProps {
  size?: 'sm' | 'md';
}

export const PausedBadge: React.FC<PausedBadgeProps> = ({ size = 'sm' }) => {
  const isSm = size === 'sm';

  return (
    <span
      className={clsx(
        'inline-flex items-center gap-1.5 font-medium rounded-md border font-mono',
        'bg-amber-950/40 border-amber-800/70 text-amber-300 shadow-xs',
        isSm ? 'px-2 py-0.5 text-xs' : 'px-2.5 py-1 text-sm'
      )}
    >
      <PauseCircle className={isSm ? 'w-3.5 h-3.5' : 'w-4 h-4'} />
      <span>PAUSED</span>
    </span>
  );
};

interface KindBadgeProps {
  kind: MonitorKind;
  size?: 'sm' | 'md';
}

export const KindBadge: React.FC<KindBadgeProps> = ({ kind, size = 'sm' }) => {
  const isSm = size === 'sm';
  const isHttp = kind === 'http';

  return (
    <span
      className={clsx(
        'inline-flex items-center gap-1 font-mono font-semibold rounded uppercase border tracking-wider',
        isHttp
          ? 'bg-sky-950/50 border-sky-800/60 text-sky-300'
          : 'bg-indigo-950/50 border-indigo-800/60 text-indigo-300',
        isSm ? 'px-1.5 py-0.5 text-[11px]' : 'px-2 py-1 text-xs'
      )}
    >
      {isHttp ? <Globe className="w-3 h-3" /> : <Cpu className="w-3 h-3" />}
      {kind}
    </span>
  );
};
