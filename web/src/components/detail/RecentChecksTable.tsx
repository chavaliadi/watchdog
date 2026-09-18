import React, { useState } from 'react';
import { Card } from '../common/Card';
import { Skeleton } from '../common/Skeleton';
import { Alert } from '../common/Alert';
import { Button } from '../common/Button';
import { CheckCircle2, XCircle, Clock, RefreshCw, AlertOctagon, ChevronDown, ChevronUp } from 'lucide-react';
import { formatRelativeTime, formatUtcDateTime } from '../../utils/formatters';
import type { CheckResult } from '../../types/check';

interface RecentChecksTableProps {
  checks: CheckResult[];
  isLoading: boolean;
  error: Error | null;
  limit: number;
  onLimitChange: (limit: number) => void;
  onRefresh: () => void;
  isRefetching: boolean;
}

export const RecentChecksTable: React.FC<RecentChecksTableProps> = ({
  checks,
  isLoading,
  error,
  limit,
  onLimitChange,
  onRefresh,
  isRefetching,
}) => {
  const [expandedRowId, setExpandedRowId] = useState<string | null>(null);

  const toggleExpand = (id: string) => {
    setExpandedRowId((prev) => (prev === id ? null : id));
  };

  return (
    <Card
      header={
        <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3 w-full">
          <div className="flex items-center gap-2">
            <Clock className="w-4 h-4 text-blue-400" />
            <h2 className="text-sm font-semibold text-zinc-100">Recent Check History</h2>
            <span className="text-xs text-zinc-400 font-mono">
              ({checks.length} results)
            </span>
          </div>

          <div className="flex items-center gap-2.5">
            {/* Limit selector */}
            <div className="flex items-center gap-1.5 text-xs text-zinc-400">
              <span>Show:</span>
              <select
                value={limit}
                onChange={(e) => onLimitChange(Number(e.target.value))}
                className="px-2 py-1 rounded bg-zinc-950 border border-zinc-700 text-zinc-200 text-xs font-mono focus:outline-none focus:border-blue-500"
              >
                <option value={10}>10</option>
                <option value={20}>20</option>
                <option value={50}>50</option>
                <option value={100}>100</option>
              </select>
            </div>

            <Button
              variant="ghost"
              size="sm"
              icon={<RefreshCw className={`w-3.5 h-3.5 ${isRefetching ? 'animate-spin text-blue-400' : ''}`} />}
              onClick={onRefresh}
              disabled={isRefetching}
              title="Refresh checks history"
            >
              Refresh
            </Button>
          </div>
        </div>
      }
    >
      {error ? (
        <div className="p-4 text-center">
          <Alert variant="error" title="Failed to load check results" className="mb-4 text-left">
            {error.message || 'Unable to retrieve check history.'}
          </Alert>
          <Button variant="secondary" size="sm" onClick={onRefresh}>
            Retry
          </Button>
        </div>
      ) : isLoading ? (
        <div className="divide-y divide-zinc-800">
          {[...Array(5)].map((_, i) => (
            <div key={i} className="py-3 px-2 flex items-center justify-between gap-4">
              <Skeleton className="h-5 w-16" />
              <Skeleton className="h-4 w-32" />
              <Skeleton className="h-4 w-12" />
              <Skeleton className="h-4 w-16" />
              <Skeleton className="h-4 w-24" />
            </div>
          ))}
        </div>
      ) : checks.length === 0 ? (
        <div className="p-10 text-center text-zinc-400">
          <AlertOctagon className="w-8 h-8 mx-auto mb-2.5 text-zinc-600" />
          <p className="text-sm font-medium text-zinc-300">No check results recorded yet</p>
          <p className="text-xs text-zinc-500 mt-1 max-w-sm mx-auto">
            The monitor has been scheduled and will run according to its interval. Check results will appear here after the first cycle completes.
          </p>
        </div>
      ) : (
        <div className="overflow-x-auto -mx-5 -my-5">
          <table className="w-full text-left border-collapse text-xs">
            <thead>
              <tr className="border-b border-zinc-800 bg-zinc-950/60 text-zinc-400 font-semibold uppercase tracking-wider">
                <th className="py-3 px-4">Result</th>
                <th className="py-3 px-4">Checked At</th>
                <th className="py-3 px-4">Status Code</th>
                <th className="py-3 px-4">Latency</th>
                <th className="py-3 px-4">Attempts</th>
                <th className="py-3 px-4">Error Category</th>
                <th className="py-3 px-4 text-right">Details</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-zinc-800/60">
              {checks.map((check) => {
                const hasError = !check.ok;
                const isExpanded = expandedRowId === check.id;

                return (
                  <React.Fragment key={check.id}>
                    <tr
                      className={`hover:bg-zinc-800/30 transition-colors font-mono ${
                        hasError ? 'bg-rose-950/10' : ''
                      }`}
                    >
                      {/* Pass / Fail */}
                      <td className="py-3 px-4 whitespace-nowrap">
                        {check.ok ? (
                          <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded text-[11px] font-medium bg-emerald-950/60 border border-emerald-800/80 text-emerald-400">
                            <CheckCircle2 className="w-3.5 h-3.5" />
                            <span>PASS</span>
                          </span>
                        ) : (
                          <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded text-[11px] font-medium bg-rose-950/60 border border-rose-800/80 text-rose-400">
                            <XCircle className="w-3.5 h-3.5" />
                            <span>FAIL</span>
                          </span>
                        )}
                      </td>

                      {/* Checked At */}
                      <td className="py-3 px-4 whitespace-nowrap text-zinc-300">
                        <span title={formatUtcDateTime(check.checked_at)}>
                          {formatRelativeTime(check.checked_at)}
                        </span>
                        <span className="text-zinc-400 block text-[10px]">
                          {formatUtcDateTime(check.checked_at)}
                        </span>
                      </td>

                      {/* Status Code */}
                      <td className="py-3 px-4 whitespace-nowrap">
                        {check.status_code && check.status_code > 0 ? (
                          <span
                            className={`px-1.5 py-0.5 rounded font-bold text-[11px] ${
                              check.status_code >= 200 && check.status_code < 300
                                ? 'bg-emerald-950/50 text-emerald-300 border border-emerald-800/60'
                                : check.status_code >= 400 && check.status_code < 500
                                ? 'bg-amber-950/50 text-amber-300 border border-amber-800/60'
                                : 'bg-rose-950/50 text-rose-300 border border-rose-800/60'
                            }`}
                          >
                            {check.status_code}
                          </span>
                        ) : (
                          <span className="text-zinc-400">-</span>
                        )}
                      </td>

                      {/* Latency */}
                      <td className="py-3 px-4 whitespace-nowrap">
                        <span
                          className={`font-semibold ${
                            check.latency_ms > 1000
                              ? 'text-amber-400'
                              : check.latency_ms > 2500
                              ? 'text-rose-400'
                              : 'text-zinc-200'
                          }`}
                        >
                          {check.latency_ms} ms
                        </span>
                      </td>

                      {/* Attempt Count */}
                      <td className="py-3 px-4 whitespace-nowrap text-zinc-300">
                        {check.attempt_count === 1 ? (
                          <span className="text-zinc-400">1 attempt</span>
                        ) : (
                          <span className="text-amber-400 font-semibold">
                            {check.attempt_count} attempts
                          </span>
                        )}
                      </td>

                      {/* Error Class */}
                      <td className="py-3 px-4 whitespace-nowrap">
                        {check.error_class ? (
                          <span className="px-1.5 py-0.5 rounded uppercase font-bold text-[10px] bg-rose-950/60 border border-rose-900 text-rose-400">
                            {check.error_class}
                          </span>
                        ) : (
                          <span className="text-zinc-400">none</span>
                        )}
                      </td>

                      {/* Error Details Toggle */}
                      <td className="py-3 px-4 whitespace-nowrap text-right">
                        {check.error_detail ? (
                          <button
                            onClick={() => toggleExpand(check.id)}
                            className="inline-flex items-center gap-1 text-[11px] font-sans text-blue-400 hover:text-blue-300 focus:outline-none cursor-pointer"
                          >
                            <span>{isExpanded ? 'Hide' : 'View error'}</span>
                            {isExpanded ? (
                              <ChevronUp className="w-3.5 h-3.5" />
                            ) : (
                              <ChevronDown className="w-3.5 h-3.5" />
                            )}
                          </button>
                        ) : (
                          <span className="text-zinc-400">-</span>
                        )}
                      </td>
                    </tr>

                    {/* Expandable Error Detail Row */}
                    {isExpanded && check.error_detail && (
                      <tr className="bg-zinc-950/80">
                        <td colSpan={7} className="px-4 py-3 border-b border-zinc-800">
                          <div className="p-3 rounded bg-zinc-900 border border-zinc-800 text-xs font-mono text-rose-300 whitespace-pre-wrap break-all">
                            <span className="text-zinc-500 font-sans block text-[11px] mb-1 font-semibold uppercase tracking-wider">
                              Failure Description
                            </span>
                            {check.error_detail}
                          </div>
                        </td>
                      </tr>
                    )}
                  </React.Fragment>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
};
