import { useQuery, useQueries } from '@tanstack/react-query';
import { listMonitors, getMonitorStatus } from '../api/monitors';
import type { Monitor } from '../types/monitor';
import type { MonitorStatus, HealthState } from '../types/status';

export interface MonitorSummary {
  total: number;
  healthy: number;
  unhealthy: number;
  unknown: number;
  disabled: number;
}

export function useMonitors() {

  // 1. Fetch monitor list with 30s background polling, paused when tab is hidden
  const monitorsQuery = useQuery<Monitor[]>({
    queryKey: ['monitors'],
    queryFn: listMonitors,
    refetchInterval: 30_000,
    refetchIntervalInBackground: false,
    staleTime: 10_000,
  });

  const monitors = monitorsQuery.data || [];

  // 2. Concurrently fetch status for each monitor
  const statusQueries = useQueries({
    queries: monitors.map((m) => ({
      queryKey: ['monitors', m.id, 'status'],
      queryFn: () => getMonitorStatus(m.id),
      refetchInterval: 30_000,
      refetchIntervalInBackground: false,
      staleTime: 10_000,
    })),
  });

  // Map monitor_id to status object
  const statusMap: Record<string, MonitorStatus | undefined> = {};
  statusQueries.forEach((q) => {
    if (q.data) {
      statusMap[q.data.monitor_id] = q.data;
    }
  });

  // Derived summaries: disabled monitors do NOT count toward healthy/unhealthy/unknown
  const summary: MonitorSummary = {
    total: monitors.length,
    disabled: 0,
    healthy: 0,
    unhealthy: 0,
    unknown: 0,
  };

  monitors.forEach((m) => {
    if (!m.enabled) {
      summary.disabled += 1;
      return;
    }

    const state: HealthState = statusMap[m.id]?.state || 'UNKNOWN';
    if (state === 'HEALTHY') {
      summary.healthy += 1;
    } else if (state === 'UNHEALTHY') {
      summary.unhealthy += 1;
    } else {
      summary.unknown += 1;
    }
  });

  const isStatusLoading = statusQueries.some((q) => q.isLoading && !q.data);

  const refetchAll = async () => {
    await Promise.all([
      monitorsQuery.refetch(),
      ...statusQueries.map((q) => q.refetch()),
    ]);
  };

  return {
    monitors,
    statusMap,
    summary,
    isLoading: monitorsQuery.isLoading,
    isStatusLoading,
    isRefetching: monitorsQuery.isRefetching || statusQueries.some((q) => q.isRefetching),
    error: monitorsQuery.error,
    refetchAll,
  };
}
