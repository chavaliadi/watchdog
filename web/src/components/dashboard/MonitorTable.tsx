import React from 'react';
import { MonitorRow } from './MonitorRow';
import { Skeleton } from '../common/Skeleton';
import { Alert } from '../common/Alert';
import { Button } from '../common/Button';
import { Plus, RefreshCw, ServerOff, SearchX } from 'lucide-react';
import type { Monitor } from '../../types/monitor';
import type { MonitorStatus } from '../../types/status';

interface MonitorTableProps {
  monitors: Monitor[];
  statusMap: Record<string, MonitorStatus | undefined>;
  isLoading: boolean;
  error: Error | null;
  onRetry: () => void;
  onEdit: (monitor: Monitor) => void;
  onDelete: (monitor: Monitor) => void;
  onToggleEnabled: (id: string, currentEnabled: boolean) => void;
  onCreateClick: () => void;
  togglingId?: string | null;
  isFiltered?: boolean;
}

export const MonitorTable: React.FC<MonitorTableProps> = ({
  monitors,
  statusMap,
  isLoading,
  error,
  onRetry,
  onEdit,
  onDelete,
  onToggleEnabled,
  onCreateClick,
  togglingId,
  isFiltered = false,
}) => {
  if (error) {
    return (
      <div className="p-6 rounded-xl bg-zinc-900/60 border border-zinc-800 text-center">
        <Alert variant="error" title="Failed to Load Monitors" className="mb-4 text-left">
          {error.message || 'An error occurred while fetching the monitor list from the Go backend.'}
        </Alert>
        <Button variant="secondary" icon={<RefreshCw className="w-4 h-4" />} onClick={onRetry}>
          Retry Connection
        </Button>
      </div>
    );
  }

  if (isLoading) {
    return (
      <div className="rounded-xl border border-zinc-800 bg-zinc-900/70 overflow-hidden shadow-sm">
        <div className="divide-y divide-zinc-800/80">
          {[...Array(4)].map((_, i) => (
            <div key={i} className="p-4 flex items-center justify-between gap-4">
              <div className="space-y-2 flex-1">
                <Skeleton className="h-4 w-48" />
                <Skeleton className="h-3 w-72" />
              </div>
              <Skeleton className="h-6 w-16" />
              <Skeleton className="h-6 w-24" />
              <Skeleton className="h-6 w-12" />
            </div>
          ))}
        </div>
      </div>
    );
  }

  if (monitors.length === 0) {
    if (isFiltered) {
      return (
        <div className="rounded-xl border border-zinc-800/80 bg-zinc-900/40 p-10 text-center">
          <div className="w-10 h-10 rounded-full bg-zinc-800/60 flex items-center justify-center mx-auto mb-3 text-zinc-500">
            <SearchX className="w-5 h-5" />
          </div>
          <h3 className="text-sm font-semibold text-zinc-200">No matching endpoints</h3>
          <p className="text-xs text-zinc-400 mt-1 max-w-sm mx-auto">
            No monitors match the current search query or protocol filter.
          </p>
        </div>
      );
    }

    return (
      <div className="rounded-xl border border-zinc-800/80 bg-zinc-900/40 p-12 text-center">
        <div className="w-12 h-12 rounded-full bg-zinc-800/70 flex items-center justify-center mx-auto mb-3.5 text-zinc-400">
          <ServerOff className="w-6 h-6" />
        </div>
        <h3 className="text-base font-semibold text-zinc-100">No monitors configured</h3>
        <p className="text-xs text-zinc-400 mt-1.5 max-w-md mx-auto mb-5 leading-relaxed">
          Deployment Watchdog is not tracking any endpoints yet. Register an HTTP or TCP target to begin recurring probes, state transition tracking, and check history capture.
        </p>
        <Button
          variant="primary"
          icon={<Plus className="w-4 h-4" />}
          onClick={onCreateClick}
        >
          Add First Monitor
        </Button>
      </div>
    );
  }

  return (
    <div className="rounded-xl border border-zinc-800 bg-zinc-900/70 backdrop-blur-sm shadow-sm overflow-hidden">
      <div className="overflow-x-auto">
        <table className="w-full text-left border-collapse" role="table">
          <thead>
            <tr className="border-b border-zinc-800 bg-zinc-900/90 text-zinc-400 text-xs uppercase tracking-wider font-semibold font-mono">
              <th className="py-3 px-4">Endpoint / Target</th>
              <th className="py-3 px-4">Protocol</th>
              <th className="py-3 px-4">Health State</th>
              <th className="py-3 px-4">Interval / Timeout</th>
              <th className="py-3 px-4">Active</th>
              <th className="py-3 px-4 text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            {monitors.map((m) => (
              <MonitorRow
                key={m.id}
                monitor={m}
                status={statusMap[m.id]}
                onEdit={onEdit}
                onDelete={onDelete}
                onToggleEnabled={onToggleEnabled}
                isToggling={togglingId === m.id}
              />
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
};
