import React from 'react';
import clsx from 'clsx';
import { Loader2 } from 'lucide-react';

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: 'primary' | 'secondary' | 'danger' | 'ghost';
  size?: 'sm' | 'md' | 'lg';
  isLoading?: boolean;
  icon?: React.ReactNode;
}

export const Button: React.FC<ButtonProps> = ({
  children,
  variant = 'secondary',
  size = 'md',
  isLoading = false,
  disabled = false,
  icon,
  className,
  ...props
}) => {
  const baseStyles =
    'inline-flex items-center justify-center font-medium rounded-lg transition-colors duration-150 focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 focus-visible:ring-offset-2 focus-visible:ring-offset-zinc-950 disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer';

  const sizeStyles = {
    sm: 'px-2.5 py-1.5 text-xs gap-1.5',
    md: 'px-3.5 py-2 text-sm gap-2',
    lg: 'px-4.5 py-2.5 text-base gap-2.5',
  };

  const variantStyles = {
    primary:
      'bg-blue-600 hover:bg-blue-500 text-white shadow-sm border border-blue-500/50',
    secondary:
      'bg-zinc-800 hover:bg-zinc-700 text-zinc-100 border border-zinc-700 shadow-sm',
    danger:
      'bg-rose-600 hover:bg-rose-500 text-white border border-rose-500/50 shadow-sm',
    ghost:
      'bg-transparent hover:bg-zinc-800/80 text-zinc-300 hover:text-zinc-100 border border-transparent',
  };

  return (
    <button
      disabled={disabled || isLoading}
      className={clsx(baseStyles, sizeStyles[size], variantStyles[variant], className)}
      {...props}
    >
      {isLoading ? (
        <Loader2 className="w-4 h-4 animate-spin text-current" />
      ) : (
        icon
      )}
      {children}
    </button>
  );
};
