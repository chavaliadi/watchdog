import React from 'react';
import { Search, Plus } from 'lucide-react';
import { Button } from '../common/Button';

interface FilterBarProps {
  search: string;
  onSearchChange: (search: string) => void;
  kindFilter: 'all' | 'http' | 'tcp';
  onKindFilterChange: (kind: 'all' | 'http' | 'tcp') => void;
  statusFilter: 'all' | 'healthy' | 'unhealthy' | 'unknown' | 'disabled';
  onStatusFilterChange: (
    status: 'all' | 'healthy' | 'unhealthy' | 'unknown' | 'disabled'
  ) => void;
  onCreateClick: () => void;
}

export const FilterBar: React.FC<FilterBarProps> = ({
  search,
  onSearchChange,
  kindFilter,
  onKindFilterChange,
  statusFilter,
  onStatusFilterChange,
  onCreateClick,
}) => {
  return (
    <div className="flex flex-col sm:flex-row items-stretch sm:items-center justify-between gap-3 mb-5">
      <div className="flex flex-wrap items-center gap-2.5 flex-1">
        {/* Search */}
        <div className="relative min-w-[200px] flex-1 sm:max-w-xs">
          <Search className="w-4 h-4 text-zinc-500 absolute left-3 top-1/2 -translate-y-1/2" />
          <input
            type="text"
            value={search}
            onChange={(e) => onSearchChange(e.target.value)}
            placeholder="Search monitors..."
            className="w-full pl-9 pr-3 py-1.5 rounded-lg bg-zinc-900 border border-zinc-800 text-zinc-200 placeholder-zinc-500 text-sm focus:outline-none focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
          />
        </div>

        {/* Kind Filter */}
        <select
          value={kindFilter}
          onChange={(e) => onKindFilterChange(e.target.value as 'all' | 'http' | 'tcp')}
          className="px-2.5 py-1.5 rounded-lg bg-zinc-900 border border-zinc-800 text-zinc-300 text-xs font-medium focus:outline-none focus:border-blue-500"
        >
          <option value="all">All Protocols</option>
          <option value="http">HTTP Only</option>
          <option value="tcp">TCP Only</option>
        </select>

        {/* Status Filter */}
        <select
          value={statusFilter}
          onChange={(e) =>
            onStatusFilterChange(
              e.target.value as 'all' | 'healthy' | 'unhealthy' | 'unknown' | 'disabled'
            )
          }
          className="px-2.5 py-1.5 rounded-lg bg-zinc-900 border border-zinc-800 text-zinc-300 text-xs font-medium focus:outline-none focus:border-blue-500"
        >
          <option value="all">All Statuses</option>
          <option value="healthy">Healthy</option>
          <option value="unhealthy">Unhealthy</option>
          <option value="unknown">Unknown</option>
          <option value="disabled">Disabled</option>
        </select>
      </div>

      <Button
        variant="primary"
        size="sm"
        icon={<Plus className="w-4 h-4" />}
        onClick={onCreateClick}
      >
        New Monitor
      </Button>
    </div>
  );
};
