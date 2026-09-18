export type MonitorKind = 'http' | 'tcp';

export interface Monitor {
  id: string;
  name: string;
  kind: MonitorKind;
  target: string;
  method: string;
  expected_status_range: string;
  interval_ms: number;
  timeout_ms: number;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface CreateMonitorInput {
  name: string;
  kind: MonitorKind;
  target: string;
  method?: string;
  expected_status_range?: string;
  interval_ms?: number;
  timeout_ms?: number;
  enabled?: boolean;
}

export interface PatchMonitorInput {
  name?: string;
  target?: string;
  method?: string;
  expected_status_range?: string;
  interval_ms?: number;
  timeout_ms?: number;
  enabled?: boolean;
}
