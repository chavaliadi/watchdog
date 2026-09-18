import { useMutation, useQueryClient } from '@tanstack/react-query';
import {
  createMonitor,
  patchMonitor,
  deleteMonitor,
} from '../api/monitors';
import type {
  Monitor,
  CreateMonitorInput,
  PatchMonitorInput,
} from '../types/monitor';

export function useCreateMonitor() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (input: CreateMonitorInput) => createMonitor(input),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['monitors'] });
    },
  });
}

export function usePatchMonitor() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: ({
      id,
      input,
    }: {
      id: string;
      input: PatchMonitorInput;
    }) => patchMonitor(id, input),
    onSuccess: (updated) => {
      queryClient.invalidateQueries({ queryKey: ['monitors'] });
      queryClient.invalidateQueries({ queryKey: ['monitors', updated.id] });
    },
  });
}

interface ToggleContext {
  previousMonitors?: Monitor[];
  previousMonitor?: Monitor;
}

export function useToggleMonitor() {
  const queryClient = useQueryClient();

  return useMutation<
    Monitor,
    Error,
    { id: string; currentEnabled: boolean },
    ToggleContext
  >({
    mutationFn: ({ id, currentEnabled }) =>
      patchMonitor(id, { enabled: !currentEnabled }),

    onMutate: async ({ id, currentEnabled }) => {
      // 1. Cancel in-flight queries so they don't overwrite optimistic update
      await queryClient.cancelQueries({ queryKey: ['monitors'] });
      await queryClient.cancelQueries({ queryKey: ['monitors', id] });

      // 2. Snapshot previous values
      const previousMonitors = queryClient.getQueryData<Monitor[]>(['monitors']);
      const previousMonitor = queryClient.getQueryData<Monitor>(['monitors', id]);

      const nextEnabled = !currentEnabled;

      // 3. Optimistically update monitor list cache
      if (previousMonitors) {
        queryClient.setQueryData<Monitor[]>(['monitors'], (old) =>
          old
            ? old.map((m) =>
                m.id === id ? { ...m, enabled: nextEnabled } : m
              )
            : []
        );
      }

      // 4. Optimistically update single monitor cache
      if (previousMonitor) {
        queryClient.setQueryData<Monitor>(['monitors', id], {
          ...previousMonitor,
          enabled: nextEnabled,
        });
      }

      return { previousMonitors, previousMonitor };
    },

    onError: (_err, { id }, context) => {
      // Rollback to snapshots on error
      if (context?.previousMonitors) {
        queryClient.setQueryData(['monitors'], context.previousMonitors);
      }
      if (context?.previousMonitor) {
        queryClient.setQueryData(['monitors', id], context.previousMonitor);
      }
    },

    onSettled: (_data, _error, { id }) => {
      // Authoritative reconciliation
      queryClient.invalidateQueries({ queryKey: ['monitors'] });
      queryClient.invalidateQueries({ queryKey: ['monitors', id] });
      queryClient.invalidateQueries({ queryKey: ['monitors', id, 'status'] });
    },
  });
}

export function useDeleteMonitor() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (id: string) => deleteMonitor(id),
    onSuccess: (_data, id) => {
      queryClient.invalidateQueries({ queryKey: ['monitors'] });
      queryClient.removeQueries({ queryKey: ['monitors', id] });
    },
  });
}
