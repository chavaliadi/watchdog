export type ErrorClass =
  | ''
  | 'dns'
  | 'conn_refused'
  | 'tls'
  | 'timeout'
  | 'status'
  | 'assertion';

export interface CheckResult {
  id: string;
  monitor_id: string;
  ok: boolean;
  status_code?: number;
  latency_ms: number;
  error_class?: ErrorClass;
  error_detail?: string;
  attempt_count: number;
  checked_at: string;
}
