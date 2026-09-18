export type HealthState = 'UNKNOWN' | 'HEALTHY' | 'UNHEALTHY';

export interface MonitorStatus {
  monitor_id: string;
  state: HealthState;
  updated_at: string;
}
