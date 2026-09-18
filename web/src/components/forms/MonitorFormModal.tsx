import React, { useState, useEffect } from 'react';
import { Modal } from '../common/Modal';
import { Button } from '../common/Button';
import { Alert } from '../common/Alert';
import { useCreateMonitor, usePatchMonitor } from '../../hooks/useMonitorMutations';
import { validateMonitorForm, type ValidationErrors } from '../../utils/validation';
import { ApiError } from '../../api/client';
import type { Monitor, MonitorKind } from '../../types/monitor';

interface MonitorFormModalProps {
  isOpen: boolean;
  onClose: () => void;
  monitor?: Monitor | null; // If provided, Edit mode; otherwise Create mode
  onSuccess?: (monitor: Monitor) => void;
}

export const MonitorFormModal: React.FC<MonitorFormModalProps> = ({
  isOpen,
  onClose,
  monitor,
  onSuccess,
}) => {
  const isEdit = Boolean(monitor);
  const createMutation = useCreateMonitor();
  const patchMutation = usePatchMonitor();

  // Form State
  const [name, setName] = useState('');
  const [kind, setKind] = useState<MonitorKind>('http');
  const [target, setTarget] = useState('');
  const [method, setMethod] = useState('GET');
  const [expectedStatusRange, setExpectedStatusRange] = useState('200-299');
  const [intervalMs, setIntervalMs] = useState<number | string>(60000);
  const [timeoutMs, setTimeoutMs] = useState<number | string>(5000);
  const [enabled, setEnabled] = useState(true);

  // Error States
  const [fieldErrors, setFieldErrors] = useState<ValidationErrors>({});
  const [serverError, setServerError] = useState<string | null>(null);

  // Synchronize state when opening / switching between edit and create
  useEffect(() => {
    if (isOpen) {
      setServerError(null);
      setFieldErrors({});
      if (monitor) {
        setName(monitor.name);
        setKind(monitor.kind);
        setTarget(monitor.target);
        setMethod(monitor.method || 'GET');
        setExpectedStatusRange(monitor.expected_status_range || '');
        setIntervalMs(monitor.interval_ms);
        setTimeoutMs(monitor.timeout_ms);
        setEnabled(monitor.enabled);
      } else {
        setName('');
        setKind('http');
        setTarget('');
        setMethod('GET');
        setExpectedStatusRange('200-299');
        setIntervalMs(60000);
        setTimeoutMs(5000);
        setEnabled(true);
      }
    }
  }, [isOpen, monitor]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setServerError(null);

    // 1. Client-side validation
    const errors = validateMonitorForm({
      name,
      kind,
      target,
      method: kind === 'http' ? method : undefined,
      expected_status_range: kind === 'http' ? expectedStatusRange : undefined,
      interval_ms: intervalMs,
      timeout_ms: timeoutMs,
    });

    if (Object.keys(errors).length > 0) {
      setFieldErrors(errors);
      return;
    }
    setFieldErrors({});

    try {
      if (isEdit && monitor) {
        // PATCH only editable fields (kind is omitted because immutable)
        const updated = await patchMutation.mutateAsync({
          id: monitor.id,
          input: {
            name: name.trim(),
            target: target.trim(),
            method: kind === 'http' ? method : undefined,
            expected_status_range:
              kind === 'http' ? expectedStatusRange.trim() : undefined,
            interval_ms: Number(intervalMs),
            timeout_ms: Number(timeoutMs),
            enabled,
          },
        });
        onSuccess?.(updated);
        onClose();
      } else {
        // POST new monitor
        const created = await createMutation.mutateAsync({
          name: name.trim(),
          kind,
          target: target.trim(),
          method: kind === 'http' ? method : undefined,
          expected_status_range:
            kind === 'http' ? expectedStatusRange.trim() : undefined,
          interval_ms: Number(intervalMs),
          timeout_ms: Number(timeoutMs),
          enabled,
        });
        onSuccess?.(created);
        onClose();
      }
    } catch (err) {
      // Preserve form inputs and display authoritative backend message
      if (err instanceof ApiError) {
        setServerError(err.message);
      } else if (err instanceof Error) {
        setServerError(err.message);
      } else {
        setServerError('An unexpected error occurred while saving the monitor.');
      }
    }
  };

  const isSubmitting = createMutation.isPending || patchMutation.isPending;

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={isEdit ? 'Edit Monitor' : 'Create New Monitor'}
      description={
        isEdit
          ? `Updating configuration for ${monitor?.name}`
          : 'Configure a new HTTP or TCP target to monitor.'
      }
      maxWidth="lg"
    >
      <form onSubmit={handleSubmit} className="space-y-4">
        {serverError && (
          <Alert
            variant="error"
            title="Backend Rejected Request"
            onDismiss={() => setServerError(null)}
          >
            {serverError}
          </Alert>
        )}

        {/* Name */}
        <div>
          <label className="block text-xs font-semibold text-zinc-300 uppercase tracking-wider mb-1.5">
            Monitor Name <span className="text-rose-500">*</span>
          </label>
          <input
            type="text"
            value={name}
            onChange={(e) => {
              setName(e.target.value);
              if (fieldErrors.name) setFieldErrors((prev) => ({ ...prev, name: undefined }));
            }}
            placeholder="e.g. Production API Gateway"
            className="w-full px-3.5 py-2 rounded-lg bg-zinc-950 border border-zinc-700 text-zinc-100 placeholder-zinc-500 text-sm focus:outline-none focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
          />
          {fieldErrors.name && (
            <p className="mt-1 text-xs text-rose-400">{fieldErrors.name}</p>
          )}
        </div>

        {/* Kind / Protocol */}
        <div>
          <label className="block text-xs font-semibold text-zinc-300 uppercase tracking-wider mb-1.5">
            Protocol Kind
          </label>
          {isEdit ? (
            <div className="flex items-center gap-3">
              <input
                type="text"
                value={kind.toUpperCase()}
                disabled
                className="w-28 px-3 py-1.5 rounded-lg bg-zinc-950/50 border border-zinc-800 text-zinc-400 font-mono text-sm cursor-not-allowed uppercase"
              />
              <span className="text-xs text-zinc-500">
                Protocol cannot be changed after creation. Delete and recreate to change protocol.
              </span>
            </div>
          ) : (
            <div className="grid grid-cols-2 gap-3">
              <button
                type="button"
                onClick={() => setKind('http')}
                className={`py-2 px-3 rounded-lg border text-sm font-medium flex items-center justify-center gap-2 cursor-pointer transition-all ${
                  kind === 'http'
                    ? 'bg-blue-600/20 border-blue-500 text-blue-300'
                    : 'bg-zinc-950 border-zinc-800 text-zinc-400 hover:border-zinc-700'
                }`}
              >
                <span>HTTP / HTTPS</span>
              </button>
              <button
                type="button"
                onClick={() => setKind('tcp')}
                className={`py-2 px-3 rounded-lg border text-sm font-medium flex items-center justify-center gap-2 cursor-pointer transition-all ${
                  kind === 'tcp'
                    ? 'bg-purple-600/20 border-purple-500 text-purple-300'
                    : 'bg-zinc-950 border-zinc-800 text-zinc-400 hover:border-zinc-700'
                }`}
              >
                <span>TCP Connection</span>
              </button>
            </div>
          )}
        </div>

        {/* Target */}
        <div>
          <label className="block text-xs font-semibold text-zinc-300 uppercase tracking-wider mb-1.5">
            {kind === 'http' ? 'Target URL' : 'Target Host:Port'}{' '}
            <span className="text-rose-500">*</span>
          </label>
          <input
            type="text"
            value={target}
            onChange={(e) => {
              setTarget(e.target.value);
              if (fieldErrors.target) setFieldErrors((prev) => ({ ...prev, target: undefined }));
            }}
            placeholder={
              kind === 'http'
                ? 'https://api.example.com/health'
                : 'db.internal:5432 or 127.0.0.1:8080'
            }
            className="w-full px-3.5 py-2 rounded-lg bg-zinc-950 border border-zinc-700 text-zinc-100 font-mono placeholder-zinc-500 text-sm focus:outline-none focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
          />
          {fieldErrors.target && (
            <p className="mt-1 text-xs text-rose-400">{fieldErrors.target}</p>
          )}
        </div>

        {/* HTTP Specific Fields */}
        {kind === 'http' && (
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 p-3.5 bg-zinc-950/40 rounded-lg border border-zinc-800/80">
            <div>
              <label className="block text-xs font-semibold text-zinc-300 uppercase tracking-wider mb-1.5">
                HTTP Method
              </label>
              <select
                value={method}
                onChange={(e) => setMethod(e.target.value)}
                className="w-full px-3 py-2 rounded-lg bg-zinc-950 border border-zinc-700 text-zinc-100 font-mono text-sm focus:outline-none focus:border-blue-500"
              >
                <option value="GET">GET</option>
                <option value="POST">POST</option>
                <option value="PUT">PUT</option>
                <option value="HEAD">HEAD</option>
                <option value="DELETE">DELETE</option>
                <option value="PATCH">PATCH</option>
              </select>
            </div>

            <div>
              <label className="block text-xs font-semibold text-zinc-300 uppercase tracking-wider mb-1.5">
                Expected Status Range
              </label>
              <input
                type="text"
                value={expectedStatusRange}
                onChange={(e) => {
                  setExpectedStatusRange(e.target.value);
                  if (fieldErrors.expected_status_range) {
                    setFieldErrors((prev) => ({ ...prev, expected_status_range: undefined }));
                  }
                }}
                placeholder="200 or 200-299"
                className="w-full px-3 py-2 rounded-lg bg-zinc-950 border border-zinc-700 text-zinc-100 font-mono text-sm placeholder-zinc-500 focus:outline-none focus:border-blue-500"
              />
              {fieldErrors.expected_status_range && (
                <p className="mt-1 text-xs text-rose-400">{fieldErrors.expected_status_range}</p>
              )}
            </div>
          </div>
        )}

        {/* Timing Settings */}
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div>
            <label className="block text-xs font-semibold text-zinc-300 uppercase tracking-wider mb-1.5">
              Check Interval (ms) <span className="text-rose-500">*</span>
            </label>
            <input
              type="number"
              min="1000"
              step="500"
              value={intervalMs}
              onChange={(e) => {
                setIntervalMs(e.target.value);
                if (fieldErrors.interval_ms) {
                  setFieldErrors((prev) => ({ ...prev, interval_ms: undefined }));
                }
              }}
              className="w-full px-3.5 py-2 rounded-lg bg-zinc-950 border border-zinc-700 text-zinc-100 font-mono text-sm focus:outline-none focus:border-blue-500"
            />
            <span className="text-[11px] text-zinc-500">Min 1000ms (1s). Default 60000ms (1m)</span>
            {fieldErrors.interval_ms && (
              <p className="mt-1 text-xs text-rose-400">{fieldErrors.interval_ms}</p>
            )}
          </div>

          <div>
            <label className="block text-xs font-semibold text-zinc-300 uppercase tracking-wider mb-1.5">
              Timeout (ms) <span className="text-rose-500">*</span>
            </label>
            <input
              type="number"
              min="100"
              step="100"
              value={timeoutMs}
              onChange={(e) => {
                setTimeoutMs(e.target.value);
                if (fieldErrors.timeout_ms) {
                  setFieldErrors((prev) => ({ ...prev, timeout_ms: undefined }));
                }
              }}
              className="w-full px-3.5 py-2 rounded-lg bg-zinc-950 border border-zinc-700 text-zinc-100 font-mono text-sm focus:outline-none focus:border-blue-500"
            />
            <span className="text-[11px] text-zinc-500">Min 100ms. Default 5000ms (5s)</span>
            {fieldErrors.timeout_ms && (
              <p className="mt-1 text-xs text-rose-400">{fieldErrors.timeout_ms}</p>
            )}
          </div>
        </div>

        {/* Enabled checkbox */}
        <div className="flex items-center gap-2.5 pt-1">
          <input
            id="monitor-enabled"
            type="checkbox"
            checked={enabled}
            onChange={(e) => setEnabled(e.target.checked)}
            className="w-4 h-4 rounded bg-zinc-950 border-zinc-700 text-blue-600 focus:ring-blue-500 focus:ring-offset-zinc-950"
          />
          <label htmlFor="monitor-enabled" className="text-sm text-zinc-200 cursor-pointer">
            Enable recurring schedule immediately
          </label>
        </div>

        {/* Actions */}
        <div className="flex items-center justify-end gap-3 pt-4 border-t border-zinc-800">
          <Button
            type="button"
            variant="ghost"
            onClick={onClose}
            disabled={isSubmitting}
          >
            Cancel
          </Button>
          <Button
            type="submit"
            variant="primary"
            isLoading={isSubmitting}
          >
            {isEdit ? 'Save Changes' : 'Create Monitor'}
          </Button>
        </div>
      </form>
    </Modal>
  );
};
