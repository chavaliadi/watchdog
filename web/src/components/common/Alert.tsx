import React from 'react';
import { AlertCircle, AlertTriangle, CheckCircle2, Info, X } from 'lucide-react';
import clsx from 'clsx';

export interface AlertProps {
  variant?: 'error' | 'warning' | 'info' | 'success';
  title?: string;
  children: React.ReactNode;
  onDismiss?: () => void;
  className?: string;
}

export const Alert: React.FC<AlertProps> = ({
  variant = 'error',
  title,
  children,
  onDismiss,
  className,
}) => {
  const variantStyles = {
    error: 'bg-rose-950/40 border-rose-800/80 text-rose-200',
    warning: 'bg-amber-950/40 border-amber-800/80 text-amber-200',
    info: 'bg-sky-950/40 border-sky-800/80 text-sky-200',
    success: 'bg-emerald-950/40 border-emerald-800/80 text-emerald-200',
  };

  const icons = {
    error: <AlertCircle className="w-5 h-5 text-rose-400 shrink-0 mt-0.5" />,
    warning: <AlertTriangle className="w-5 h-5 text-amber-400 shrink-0 mt-0.5" />,
    info: <Info className="w-5 h-5 text-sky-400 shrink-0 mt-0.5" />,
    success: <CheckCircle2 className="w-5 h-5 text-emerald-400 shrink-0 mt-0.5" />,
  };

  return (
    <div
      role="alert"
      className={clsx(
        'flex items-start gap-3 p-4 rounded-lg border text-sm',
        variantStyles[variant],
        className
      )}
    >
      {icons[variant]}
      <div className="flex-1 min-w-0">
        {title && <h4 className="font-semibold mb-1">{title}</h4>}
        <div className="text-xs sm:text-sm leading-relaxed">{children}</div>
      </div>
      {onDismiss && (
        <button
          onClick={onDismiss}
          className="p-1 -mr-1 -mt-1 rounded text-zinc-400 hover:text-zinc-200 focus:outline-none focus-visible:ring-1 focus-visible:ring-zinc-400"
          aria-label="Dismiss alert"
        >
          <X className="w-4 h-4" />
        </button>
      )}
    </div>
  );
};
