import React from 'react';
import { Link } from 'react-router-dom';
import { ChevronLeft, Edit2, Trash2, RefreshCw, Power } from 'lucide-react';
import { HealthBadge, KindBadge, PausedBadge } from '../common/Badge';
import { Button } from '../common/Button';
import { formatRelativeTime, formatUtcDateTime } from '../../utils/formatters';
import type { Monitor } from '../../types/monitor';
import type { MonitorStatus } from '../../types/status';

interface DetailHeaderProps {
  monitor: Monitor;
  status: MonitorStatus | undefined;
  isRefetching: boolean;
  onRefresh: () => void;
  onEdit: () => void;
  onDelete: () => void;
  onToggleEnabled: () => void;
  isToggling?: boolean;
}

export const DetailHeader: React.FC<DetailHeaderProps> = ({
  monitor,
  status,
  isRefetching,
  onRefresh,
  onEdit,
  onDelete,
  onToggleEnabled,
  isToggling = false,
}) => {
  return (
    <div className="mb-6 space-y-4">
      {/* Breadcrumb */}
      <div>
        <Link
          to="/"
          className="inline-flex items-center gap-1.5 text-xs font-medium text-zinc-400 hover:text-zinc-200 transition-colors"
        >
          <ChevronLeft className="w-4 h-4" />
          <span>Back to Dashboard</span>
        </Link>
      </div>

      {/* Main Header Container */}
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4 p-5 rounded-xl bg-zinc-900/70 border border-zinc-800 backdrop-blur-sm shadow-sm">
        <div className="space-y-1.5">
          <div className="flex flex-wrap items-center gap-2.5">
            <h1 className="text-xl font-bold text-zinc-100 tracking-tight">
              {monitor.name}
            </h1>
            <KindBadge kind={monitor.kind} size="md" />
            {monitor.enabled ? (
              <HealthBadge state={status?.state} size="md" />
            ) : (
              <PausedBadge size="md" />
            )}
          </div>
          <p className="text-xs sm:text-sm font-mono text-zinc-400 truncate max-w-2xl">
            {monitor.target}
          </p>
          {status?.updated_at && (
            <p className="text-xs text-zinc-400">
              Health status transitioned {formatRelativeTime(status.updated_at)}{' '}
              <span className="text-zinc-400 font-mono">
                ({formatUtcDateTime(status.updated_at)})
              </span>
            </p>
          )}
        </div>

        {/* Action Controls */}
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="secondary"
            size="sm"
            icon={<RefreshCw className={`w-3.5 h-3.5 ${isRefetching ? 'animate-spin text-blue-400' : ''}`} />}
            onClick={onRefresh}
            disabled={isRefetching}
            title="Refresh now"
          >
            Refresh
          </Button>

          <Button
            variant={monitor.enabled ? 'secondary' : 'primary'}
            size="sm"
            icon={<Power className="w-3.5 h-3.5" />}
            isLoading={isToggling}
            onClick={onToggleEnabled}
            title={monitor.enabled ? 'Pause monitoring' : 'Resume monitoring'}
          >
            {monitor.enabled ? 'Pause' : 'Resume'}
          </Button>

          <Button
            variant="secondary"
            size="sm"
            icon={<Edit2 className="w-3.5 h-3.5" />}
            onClick={onEdit}
            title="Edit configuration"
          >
            Edit
          </Button>

          <Button
            variant="danger"
            size="sm"
            icon={<Trash2 className="w-3.5 h-3.5" />}
            onClick={onDelete}
            title="Delete monitor"
          >
            Delete
          </Button>
        </div>
      </div>
    </div>
  );
};
