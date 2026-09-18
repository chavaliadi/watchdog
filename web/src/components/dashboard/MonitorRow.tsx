import React from 'react';
import { Link } from 'react-router-dom';
import { ExternalLink, Edit2, Trash2, ArrowUpRight } from 'lucide-react';
import { HealthBadge, KindBadge } from '../common/Badge';
import { formatDuration, formatRelativeTime, formatUtcDateTime } from '../../utils/formatters';
import type { Monitor } from '../../types/monitor';
import type { MonitorStatus } from '../../types/status';

interface MonitorRowProps {
  monitor: Monitor;
  status: MonitorStatus | undefined;
  onEdit: (monitor: Monitor) => void;
  onDelete: (monitor: Monitor) => void;
  onToggleEnabled: (id: string, currentEnabled: boolean) => void;
  isToggling?: boolean;
}

export const MonitorRow: React.FC<MonitorRowProps> = ({
  monitor,
  status,
  onEdit,
  onDelete,
  onToggleEnabled,
  isToggling = false,
}) => {

  return (
    <tr className="border-b border-zinc-800/60 hover:bg-zinc-800/30 transition-colors group">
      {/* Name & Target */}
      <td className="py-3.5 px-4">
        <div className="flex flex-col">
          <Link
            to={`/monitors/${monitor.id}`}
            className="font-medium text-sm text-zinc-200 hover:text-blue-400 transition-colors flex items-center gap-1.5"
          >
            <span>{monitor.name}</span>
            <ArrowUpRight className="w-3.5 h-3.5 opacity-0 group-hover:opacity-100 text-zinc-400 transition-opacity" />
          </Link>
          <div className="flex items-center gap-1.5 text-xs text-zinc-400 font-mono mt-0.5 max-w-xs sm:max-w-md truncate">
            <span className="truncate">{monitor.target}</span>
            {monitor.kind === 'http' && (
              <a
                href={monitor.target}
                target="_blank"
                rel="noreferrer"
                className="text-zinc-500 hover:text-zinc-300"
                title="Open target in new tab"
              >
                <ExternalLink className="w-3 h-3" />
              </a>
            )}
          </div>
        </div>
      </td>

      {/* Kind */}
      <td className="py-3.5 px-4 whitespace-nowrap">
        <KindBadge kind={monitor.kind} size="sm" />
      </td>

      {/* Health State */}
      <td className="py-3.5 px-4 whitespace-nowrap">
        {monitor.enabled ? (
          <div className="flex flex-col items-start gap-0.5">
            <HealthBadge state={status?.state} size="sm" />
            {status?.updated_at && (
              <span
                className="text-[11px] text-zinc-400 font-mono"
                title={`Updated at: ${formatUtcDateTime(status.updated_at)}`}
              >
                {formatRelativeTime(status.updated_at)}
              </span>
            )}
          </div>
        ) : (
          <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-zinc-900 border border-zinc-800 text-zinc-400">
            PAUSED
          </span>
        )}
      </td>

      {/* Timing (Interval / Timeout) */}
      <td className="py-3.5 px-4 whitespace-nowrap font-mono text-xs text-zinc-300">
        <div>
          <span>{formatDuration(monitor.interval_ms)}</span>
          <span className="text-zinc-400 mx-1">/</span>
          <span className="text-zinc-400">{formatDuration(monitor.timeout_ms)}</span>
        </div>
      </td>

      {/* Enabled Toggle Switch */}
      <td className="py-3.5 px-4 whitespace-nowrap">
        <label className="relative inline-flex items-center cursor-pointer">
          <input
            type="checkbox"
            checked={monitor.enabled}
            disabled={isToggling}
            onChange={() => onToggleEnabled(monitor.id, monitor.enabled)}
            className="sr-only peer"
          />
          <div className="w-9 h-5 bg-zinc-800 peer-focus:outline-none peer-focus-visible:ring-2 peer-focus-visible:ring-blue-500 rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-zinc-300 after:border after:rounded-full after:h-4 after:w-4 after:transition-all peer-checked:bg-blue-600 peer-disabled:opacity-40"></div>
        </label>
      </td>

      {/* Actions */}
      <td className="py-3.5 px-4 whitespace-nowrap text-right text-xs">
        <div className="flex items-center justify-end gap-1">
          <button
            onClick={() => onEdit(monitor)}
            className="p-1.5 rounded-md text-zinc-400 hover:text-zinc-200 hover:bg-zinc-800 transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 cursor-pointer"
            title="Edit configuration"
            aria-label={`Edit ${monitor.name}`}
          >
            <Edit2 className="w-4 h-4" />
          </button>
          <button
            onClick={() => onDelete(monitor)}
            className="p-1.5 rounded-md text-zinc-400 hover:text-rose-400 hover:bg-zinc-800 transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-rose-500 cursor-pointer"
            title="Delete monitor"
            aria-label={`Delete ${monitor.name}`}
          >
            <Trash2 className="w-4 h-4" />
          </button>
        </div>
      </td>
    </tr>
  );
};
