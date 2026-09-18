import React from 'react';
import { Card } from '../common/Card';
import { Settings, Clock, Radio } from 'lucide-react';
import { formatDuration, formatUtcDateTime } from '../../utils/formatters';
import type { Monitor } from '../../types/monitor';

interface ConfigPanelProps {
  monitor: Monitor;
  pollInterval: number;
}

export const ConfigPanel: React.FC<ConfigPanelProps> = ({ monitor, pollInterval }) => {
  return (
    <Card
      header={
        <div className="flex items-center justify-between w-full">
          <div className="flex items-center gap-2 text-sm font-semibold text-zinc-200">
            <Settings className="w-4 h-4 text-blue-400" />
            <span>Target Configuration</span>
          </div>
          <div className="flex items-center gap-1.5 text-xs text-zinc-400 font-mono">
            <Radio className="w-3.5 h-3.5 text-emerald-400 animate-pulse" />
            <span>Auto-poll: {formatDuration(pollInterval)}</span>
          </div>
        </div>
      }
      className="mb-6"
    >
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-6 text-sm">
        {/* Target */}
        <div>
          <span className="block text-xs font-semibold text-zinc-400 uppercase tracking-wider mb-1">
            {monitor.kind === 'http' ? 'Target URL' : 'Target Host:Port'}
          </span>
          <span className="font-mono text-zinc-200 break-all">{monitor.target}</span>
        </div>

        {/* Protocol Specific */}
        {monitor.kind === 'http' ? (
          <>
            <div>
              <span className="block text-xs font-semibold text-zinc-400 uppercase tracking-wider mb-1">
                HTTP Method
              </span>
              <span className="font-mono font-medium text-sky-400">
                {monitor.method || 'GET'}
              </span>
            </div>
            <div>
              <span className="block text-xs font-semibold text-zinc-400 uppercase tracking-wider mb-1">
                Expected Status Range
              </span>
              <span className="font-mono text-zinc-200">
                {monitor.expected_status_range || 'Default (2xx)'}
              </span>
            </div>
          </>
        ) : (
          <div>
            <span className="block text-xs font-semibold text-zinc-400 uppercase tracking-wider mb-1">
              Protocol Check
            </span>
            <span className="text-zinc-300">TCP Handshake Connection</span>
          </div>
        )}

        {/* Interval */}
        <div>
          <span className="block text-xs font-semibold text-zinc-400 uppercase tracking-wider mb-1">
            Execution Interval
          </span>
          <div className="flex items-center gap-1.5 text-zinc-200 font-mono">
            <Clock className="w-3.5 h-3.5 text-zinc-400" />
            <span>{formatDuration(monitor.interval_ms)}</span>
            <span className="text-xs text-zinc-400">({monitor.interval_ms} ms)</span>
          </div>
        </div>

        {/* Timeout */}
        <div>
          <span className="block text-xs font-semibold text-zinc-400 uppercase tracking-wider mb-1">
            Probe Timeout
          </span>
          <div className="flex items-center gap-1.5 text-zinc-200 font-mono">
            <Clock className="w-3.5 h-3.5 text-zinc-400" />
            <span>{formatDuration(monitor.timeout_ms)}</span>
            <span className="text-xs text-zinc-400">({monitor.timeout_ms} ms)</span>
          </div>
        </div>

        {/* Scheduling State */}
        <div>
          <span className="block text-xs font-semibold text-zinc-400 uppercase tracking-wider mb-1">
            Scheduler Status
          </span>
          <span
            className={`font-semibold text-xs px-2 py-0.5 rounded border inline-block ${
              monitor.enabled
                ? 'bg-blue-950/40 border-blue-800/60 text-blue-400'
                : 'bg-zinc-800 border-zinc-700 text-zinc-400'
            }`}
          >
            {monitor.enabled ? 'ACTIVE RUNNER' : 'STOPPED'}
          </span>
        </div>

        {/* Timestamps */}
        <div>
          <span className="block text-xs font-semibold text-zinc-400 uppercase tracking-wider mb-1">
            Created At
          </span>
          <span className="text-xs font-mono text-zinc-300">
            {formatUtcDateTime(monitor.created_at)}
          </span>
        </div>

        <div>
          <span className="block text-xs font-semibold text-zinc-400 uppercase tracking-wider mb-1">
            Last Config Update
          </span>
          <span className="text-xs font-mono text-zinc-300">
            {formatUtcDateTime(monitor.updated_at)}
          </span>
        </div>
      </div>
    </Card>
  );
};
