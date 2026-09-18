import React, { useState, useMemo } from 'react';
import { SummaryCards } from '../components/dashboard/SummaryCards';
import { FilterBar } from '../components/dashboard/FilterBar';
import { MonitorTable } from '../components/dashboard/MonitorTable';
import { MonitorFormModal } from '../components/forms/MonitorFormModal';
import { DeleteConfirmModal } from '../components/forms/DeleteConfirmModal';
import { Alert } from '../components/common/Alert';
import { useMonitors } from '../hooks/useMonitors';
import { useToggleMonitor, useDeleteMonitor } from '../hooks/useMonitorMutations';
import { ApiError } from '../api/client';
import type { Monitor } from '../types/monitor';

export const DashboardView: React.FC = () => {
  const {
    monitors,
    statusMap,
    summary,
    isLoading,
    error,
    refetchAll,
  } = useMonitors();

  const toggleMutation = useToggleMonitor();
  const deleteMutation = useDeleteMonitor();

  // Filters
  const [search, setSearch] = useState('');
  const [kindFilter, setKindFilter] = useState<'all' | 'http' | 'tcp'>('all');
  const [statusFilter, setStatusFilter] = useState<
    'all' | 'healthy' | 'unhealthy' | 'unknown' | 'disabled'
  >('all');

  // Modals state
  const [isCreateOpen, setIsCreateOpen] = useState(false);
  const [editingMonitor, setEditingMonitor] = useState<Monitor | null>(null);
  const [deletingMonitor, setDeletingMonitor] = useState<Monitor | null>(null);

  // Mutation error feedback
  const [mutationError, setMutationError] = useState<string | null>(null);
  const [togglingId, setTogglingId] = useState<string | null>(null);

  // Filtered monitors
  const filteredMonitors = useMemo(() => {
    return monitors.filter((m) => {
      // 1. Search filter (name or target)
      if (search.trim()) {
        const query = search.toLowerCase();
        const matchName = m.name.toLowerCase().includes(query);
        const matchTarget = m.target.toLowerCase().includes(query);
        if (!matchName && !matchTarget) return false;
      }

      // 2. Kind filter
      if (kindFilter !== 'all' && m.kind !== kindFilter) {
        return false;
      }

      // 3. Status filter
      if (statusFilter !== 'all') {
        if (statusFilter === 'disabled') {
          if (m.enabled) return false;
        } else {
          if (!m.enabled) return false;
          const state = statusMap[m.id]?.state || 'UNKNOWN';
          if (statusFilter === 'healthy' && state !== 'HEALTHY') return false;
          if (statusFilter === 'unhealthy' && state !== 'UNHEALTHY') return false;
          if (statusFilter === 'unknown' && state !== 'UNKNOWN') return false;
        }
      }

      return true;
    });
  }, [monitors, statusMap, search, kindFilter, statusFilter]);

  // Handle optimistic toggle
  const handleToggleEnabled = async (id: string, currentEnabled: boolean) => {
    setMutationError(null);
    setTogglingId(id);
    try {
      await toggleMutation.mutateAsync({ id, currentEnabled });
    } catch (err) {
      if (err instanceof ApiError) {
        setMutationError(`Failed to toggle monitor state: ${err.message}`);
      } else if (err instanceof Error) {
        setMutationError(`Failed to toggle monitor state: ${err.message}`);
      } else {
        setMutationError('Failed to toggle monitor state.');
      }
    } finally {
      setTogglingId(null);
    }
  };

  // Handle delete
  const handleDeleteConfirm = async (id: string) => {
    await deleteMutation.mutateAsync(id);
  };

  return (
    <div>
      {mutationError && (
        <Alert
          variant="error"
          title="Operation Failed"
          className="mb-5"
          onDismiss={() => setMutationError(null)}
        >
          {mutationError}
        </Alert>
      )}

      {/* Summary Statistics */}
      <SummaryCards summary={summary} isLoading={isLoading} />

      {/* Search & Filters */}
      <FilterBar
        search={search}
        onSearchChange={setSearch}
        kindFilter={kindFilter}
        onKindFilterChange={setKindFilter}
        statusFilter={statusFilter}
        onStatusFilterChange={setStatusFilter}
        onCreateClick={() => setIsCreateOpen(true)}
      />

      {/* Monitor List Table */}
      <MonitorTable
        monitors={filteredMonitors}
        statusMap={statusMap}
        isLoading={isLoading}
        error={error}
        onRetry={refetchAll}
        onEdit={(m) => setEditingMonitor(m)}
        onDelete={(m) => setDeletingMonitor(m)}
        onToggleEnabled={handleToggleEnabled}
        onCreateClick={() => setIsCreateOpen(true)}
        togglingId={togglingId}
      />

      {/* Create Modal */}
      <MonitorFormModal
        isOpen={isCreateOpen}
        onClose={() => setIsCreateOpen(false)}
      />

      {/* Edit Modal */}
      <MonitorFormModal
        isOpen={Boolean(editingMonitor)}
        onClose={() => setEditingMonitor(null)}
        monitor={editingMonitor}
      />

      {/* Delete Confirmation Modal */}
      <DeleteConfirmModal
        isOpen={Boolean(deletingMonitor)}
        onClose={() => setDeletingMonitor(null)}
        monitor={deletingMonitor}
        onConfirm={handleDeleteConfirm}
      />
    </div>
  );
};
