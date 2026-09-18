import React from 'react';
import clsx from 'clsx';

export interface CardProps extends React.HTMLAttributes<HTMLDivElement> {
  header?: React.ReactNode;
  footer?: React.ReactNode;
}

export const Card: React.FC<CardProps> = ({
  children,
  header,
  footer,
  className,
  ...props
}) => {
  return (
    <div
      className={clsx(
        'bg-zinc-900/70 backdrop-blur-sm border border-zinc-800 rounded-xl overflow-hidden shadow-sm',
        className
      )}
      {...props}
    >
      {header && (
        <div className="px-5 py-4 border-b border-zinc-800/80 bg-zinc-900/50 flex items-center justify-between">
          {header}
        </div>
      )}
      <div className="p-5">{children}</div>
      {footer && (
        <div className="px-5 py-3 border-t border-zinc-800/80 bg-zinc-900/30">
          {footer}
        </div>
      )}
    </div>
  );
};
