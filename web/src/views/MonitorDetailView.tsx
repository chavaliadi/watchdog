import React, { useState } from 'react';
import { useParams, useNavigate, Link } from 'react-router-dom';
import { DetailHeader } from '../components/detail/DetailHeader';
import { ConfigPanel } from '../components/detail/ConfigPanel';
import { RecentChecksTable } from '../components/detail/RecentChecksTable';
import { MonitorFormModal } from '../components/forms/MonitorFormModal';
import { DeleteConfirmModal } from '../components/forms/DeleteConfirmModal';
import { Alert } from '../components/common/Alert';
import { Skeleton } from '../components/common/Skeleton';
import { Button } from '../components/common/Button';
import { useMonitorDetail } from '../hooks/useMonitorDetail';
import { useToggleMonitor, useDeleteMonitor } from '../hooks/useMonitorMutations';
import { ArrowLeft, RefreshCw } from 'lucide-react';
import { ApiError } from '../api/client';

export const MonitorDetailView: React.FC = () => {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();

  const [limit, setLimit] = useState(20);
  const [isEditOpen, setIsEditOpen] = useState(false);
  const [isDeleteOpen, setIsDeleteOpen] = useState(false);
  const [mutationError, setMutationError] = useState<string | null>(null);

  const {
    monitor,
    status,
    checks,
    isLoading,
    isRefetching,
    error,
    checksError,
    pollInterval,
    refetchAll,
  } = useMonitorDetail(id || '', limit);

  const toggleMutation = useToggleMonitor();
  const deleteMutation = useDeleteMonitor();

  // Optimistic enable/disable toggle
  const handleToggleEnabled = async () => {
    if (!monitor) return;
    setMutationError(null);
    try {
      await toggleMutation.mutateAsync({
        id: monitor.id,
        currentEnabled: monitor.enabled,
      });
    } catch (err) {
      if (err instanceof ApiError) {
        setMutationError(`Failed to update scheduling state: ${err.message}`);
      } else if (err instanceof Error) {
        setMutationError(`Failed to update scheduling state: ${err.message}`);
      } else {
        setMutationError('Failed to update scheduling state.');
      }
    }
  };

  // Delete monitor and navigate back to dashboard
  const handleDeleteConfirm = async (monitorId: string) => {
    await deleteMutation.mutateAsync(monitorId);
    navigate('/');
  };

  if (!id) {
    return (
      <div className="p-8 text-center">
        <Alert variant="error" title="Invalid URL">
          No monitor ID was specified in the route.
        </Alert>
        <Link to="/" className="inline-block mt-4 text-sm text-blue-400 hover:underline">
          Return to Dashboard
        </Link>
      </div>
    );
  }

  if (isLoading) {
    return (
      <div className="space-y-6">
        <Skeleton className="h-6 w-32" />
        <Skeleton className="h-28 w-full rounded-xl" />
        <Skeleton className="h-44 w-full rounded-xl" />
        <Skeleton className="h-64 w-full rounded-xl" />
      </div>
    );
  }

  if (error || !monitor) {
    return (
      <div className="p-8 text-center rounded-xl bg-zinc-900/60 border border-zinc-800">
        <Alert variant="error" title="Monitor Not Found" className="mb-6 text-left max-w-lg mx-auto">
          {error instanceof ApiError && error.status === 404
            ? 'The requested monitor does not exist or has been deleted.'
            : error?.message || 'Unable to retrieve monitor details.'}
        </Alert>
        <div className="flex items-center justify-center gap-3">
          <Button
            variant="secondary"
            icon={<ArrowLeft className="w-4 h-4" />}
            onClick={() => navigate('/')}
          >
            Return to Dashboard
          </Button>
          <Button
            variant="ghost"
            icon={<RefreshCw className="w-4 h-4" />}
            onClick={refetchAll}
          >
            Retry
          </Button>
        </div>
      </div>
    );
  }

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

      {/* Header section with status, toggle, and actions */}
      <DetailHeader
        monitor={monitor}
        status={status}
        isRefetching={isRefetching}
        onRefresh={refetchAll}
        onEdit={() => setIsEditOpen(true)}
        onDelete={() => setIsDeleteOpen(true)}
        onToggleEnabled={handleToggleEnabled}
        isToggling={toggleMutation.isPending}
      />

      {/* Configuration Metadata Panel */}
      <ConfigPanel monitor={monitor} pollInterval={pollInterval} />

      {/* Live Recent Checks Table */}
      <RecentChecksTable
        checks={checks}
        isLoading={false}
        error={checksError}
        limit={limit}
        onLimitChange={setLimit}
        onRefresh={refetchAll}
        isRefetching={isRefetching}
      />

      {/* Edit Modal */}
      <MonitorFormModal
        isOpen={isEditOpen}
        onClose={() => setIsEditOpen(false)}
        monitor={monitor}
      />

      {/* Delete Confirmation Modal */}
      <DeleteConfirmModal
        isOpen={isDeleteOpen}
        onClose={() => setIsDeleteOpen(false)}
        monitor={monitor}
        onConfirm={handleDeleteConfirm}
      />
    </div>
  );
};
