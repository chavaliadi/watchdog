import { useQuery } from '@tanstack/react-query';
import { getMonitor, getMonitorStatus, getMonitorChecks } from '../api/monitors';
import { calculatePollInterval } from '../utils/formatters';
import type { Monitor } from '../types/monitor';
import type { MonitorStatus } from '../types/status';
import type { CheckResult } from '../types/check';

export function useMonitorDetail(id: string, limit: number = 20) {
  // 1. Monitor configuration query (static data, staleTime 60s)
  const monitorQuery = useQuery<Monitor>({
    queryKey: ['monitors', id],
    queryFn: () => getMonitor(id),
    enabled: Boolean(id),
    staleTime: 60_000,
  });

  const monitor = monitorQuery.data;

  // Dynamic poll interval based on monitor.interval_ms:
  // pollInterval = max(10,000ms, min(monitor.interval_ms, 60,000ms))
  const pollInterval = monitor
    ? calculatePollInterval(monitor.interval_ms)
    : 10_000;

  // 2. Health status query with dynamic polling
  const statusQuery = useQuery<MonitorStatus>({
    queryKey: ['monitors', id, 'status'],
    queryFn: () => getMonitorStatus(id),
    enabled: Boolean(id),
    refetchInterval: pollInterval,
    refetchIntervalInBackground: false,
    staleTime: 5_000,
  });

  // 3. Recent checks query with dynamic polling
  const checksQuery = useQuery<CheckResult[]>({
    queryKey: ['monitors', id, 'checks', { limit }],
    queryFn: () => getMonitorChecks(id, limit),
    enabled: Boolean(id),
    refetchInterval: pollInterval,
    refetchIntervalInBackground: false,
    staleTime: 5_000,
  });

  const refetchAll = async () => {
    await Promise.all([
      monitorQuery.refetch(),
      statusQuery.refetch(),
      checksQuery.refetch(),
    ]);
  };

  return {
    monitor,
    status: statusQuery.data,
    checks: checksQuery.data || [],
    isLoading: monitorQuery.isLoading,
    isStatusLoading: statusQuery.isLoading,
    isChecksLoading: checksQuery.isLoading,
    isRefetching:
      monitorQuery.isRefetching ||
      statusQuery.isRefetching ||
      checksQuery.isRefetching,
    error: monitorQuery.error || statusQuery.error || checksQuery.error,
    checksError: checksQuery.error,
    pollInterval,
    refetchAll,
  };
}
