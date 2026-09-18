import { request } from './client';
import type {
  Monitor,
  CreateMonitorInput,
  PatchMonitorInput,
} from '../types/monitor';
import type { MonitorStatus } from '../types/status';
import type { CheckResult } from '../types/check';

export async function listMonitors(): Promise<Monitor[]> {
  return request<Monitor[]>('/monitors', { method: 'GET' });
}

export async function getMonitor(id: string): Promise<Monitor> {
  return request<Monitor>(`/monitors/${encodeURIComponent(id)}`, { method: 'GET' });
}

export async function getMonitorStatus(id: string): Promise<MonitorStatus> {
  return request<MonitorStatus>(`/monitors/${encodeURIComponent(id)}/status`, {
    method: 'GET',
  });
}

export async function getMonitorChecks(
  id: string,
  limit: number = 20
): Promise<CheckResult[]> {
  const query = new URLSearchParams({ limit: String(limit) }).toString();
  return request<CheckResult[]>(
    `/monitors/${encodeURIComponent(id)}/checks?${query}`,
    { method: 'GET' }
  );
}

export async function createMonitor(input: CreateMonitorInput): Promise<Monitor> {
  // Clean payload according to protocol
  const payload: Record<string, unknown> = {
    name: input.name.trim(),
    kind: input.kind,
    target: input.target.trim(),
  };

  if (input.kind === 'http') {
    if (input.method) payload.method = input.method.trim();
    if (input.expected_status_range) {
      payload.expected_status_range = input.expected_status_range.trim();
    }
  }

  if (typeof input.interval_ms === 'number') payload.interval_ms = input.interval_ms;
  if (typeof input.timeout_ms === 'number') payload.timeout_ms = input.timeout_ms;
  if (typeof input.enabled === 'boolean') payload.enabled = input.enabled;

  return request<Monitor>('/monitors', {
    method: 'POST',
    body: JSON.stringify(payload),
  });
}

export async function patchMonitor(
  id: string,
  input: PatchMonitorInput
): Promise<Monitor> {
  // Note: kind is immutable in backend and is omitted from PATCH
  const payload: Record<string, unknown> = {};

  if (input.name !== undefined) payload.name = input.name.trim();
  if (input.target !== undefined) payload.target = input.target.trim();
  if (input.method !== undefined) payload.method = input.method.trim();
  if (input.expected_status_range !== undefined) {
    payload.expected_status_range = input.expected_status_range.trim();
  }
  if (input.interval_ms !== undefined) payload.interval_ms = input.interval_ms;
  if (input.timeout_ms !== undefined) payload.timeout_ms = input.timeout_ms;
  if (input.enabled !== undefined) payload.enabled = input.enabled;

  return request<Monitor>(`/monitors/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: JSON.stringify(payload),
  });
}

export async function deleteMonitor(id: string): Promise<void> {
  return request<void>(`/monitors/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  });
}
