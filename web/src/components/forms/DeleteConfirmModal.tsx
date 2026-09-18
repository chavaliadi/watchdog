import React, { useState } from 'react';
import { Modal } from '../common/Modal';
import { Button } from '../common/Button';
import { Alert } from '../common/Alert';
import { AlertTriangle } from 'lucide-react';
import { ApiError } from '../../api/client';
import type { Monitor } from '../../types/monitor';

interface DeleteConfirmModalProps {
  isOpen: boolean;
  onClose: () => void;
  monitor: Monitor | null;
  onConfirm: (id: string) => Promise<void>;
}

export const DeleteConfirmModal: React.FC<DeleteConfirmModalProps> = ({
  isOpen,
  onClose,
  monitor,
  onConfirm,
}) => {
  const [isDeleting, setIsDeleting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (!monitor) return null;

  const handleDelete = async () => {
    setIsDeleting(true);
    setError(null);
    try {
      await onConfirm(monitor.id);
      setIsDeleting(false);
      onClose();
    } catch (err) {
      setIsDeleting(false);
      if (err instanceof ApiError) {
        setError(err.message);
      } else if (err instanceof Error) {
        setError(err.message);
      } else {
        setError('Failed to delete monitor.');
      }
    }
  };

  return (
    <Modal
      isOpen={isOpen}
      onClose={isDeleting ? () => {} : onClose}
      title="Delete Monitor"
      maxWidth="md"
    >
      <div className="space-y-4">
        {error && (
          <Alert variant="error" title="Deletion Failed">
            {error}
          </Alert>
        )}

        <div className="flex items-start gap-3 p-3.5 rounded-lg bg-rose-950/30 border border-rose-900/60 text-rose-300">
          <AlertTriangle className="w-5 h-5 text-rose-400 shrink-0 mt-0.5" />
          <div className="text-sm">
            <p className="font-semibold text-rose-200">
              Permanent Deletion Warning
            </p>
            <p className="mt-1 text-xs text-rose-300/90 leading-relaxed">
              Deleting this monitor will immediately stop active scheduler checks
              and permanently delete all associated configuration and historical check records.
            </p>
          </div>
        </div>

        <div className="p-3 bg-zinc-950 rounded-lg border border-zinc-800 text-sm">
          <div className="text-xs text-zinc-500 uppercase tracking-wider mb-1">
            Target to Delete
          </div>
          <div className="font-medium text-zinc-200">{monitor.name}</div>
          <div className="font-mono text-xs text-zinc-400 truncate mt-0.5">
            {monitor.target}
          </div>
        </div>

        <div className="flex items-center justify-end gap-3 pt-4 border-t border-zinc-800">
          <Button
            type="button"
            variant="ghost"
            onClick={onClose}
            disabled={isDeleting}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="danger"
            isLoading={isDeleting}
            onClick={handleDelete}
          >
            Delete Monitor
          </Button>
        </div>
      </div>
    </Modal>
  );
};
