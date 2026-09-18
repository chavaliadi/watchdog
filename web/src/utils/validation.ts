import type { MonitorKind } from '../types/monitor';

export interface ValidationErrors {
  name?: string;
  target?: string;
  method?: string;
  expected_status_range?: string;
  interval_ms?: string;
  timeout_ms?: string;
}

export function validateExpectedStatusRange(val: string): string | null {
  const trimmed = val.trim();
  if (!trimmed) return null;

  const parts = trimmed.split('-');
  if (parts.length === 1) {
    const code = Number(parts[0]);
    if (!Number.isInteger(code) || code < 100 || code > 599) {
      return 'Expected status must be between 100 and 599';
    }
    return null;
  }
  if (parts.length === 2) {
    const start = Number(parts[0]);
    const end = Number(parts[1]);
    if (
      !Number.isInteger(start) ||
      !Number.isInteger(end) ||
      start < 100 ||
      end > 599 ||
      start > end
    ) {
      return 'Expected status range must be A-B with 100 <= A <= B <= 599';
    }
    return null;
  }
  return 'Invalid status range format. Use e.g. "200" or "200-299"';
}

export function validateMonitorForm(values: {
  name: string;
  kind: MonitorKind;
  target: string;
  method?: string;
  expected_status_range?: string;
  interval_ms: number | string;
  timeout_ms: number | string;
}): ValidationErrors {
  const errors: ValidationErrors = {};

  // Name validation
  const trimmedName = values.name.trim();
  if (!trimmedName) {
    errors.name = 'Monitor name is required';
  } else if (trimmedName.length > 255) {
    errors.name = 'Monitor name must not exceed 255 characters';
  }

  // Target validation
  const trimmedTarget = values.target.trim();
  if (!trimmedTarget) {
    errors.target = 'Target is required';
  } else if (values.kind === 'http') {
    try {
      const url = new URL(trimmedTarget);
      if (url.protocol !== 'http:' && url.protocol !== 'https:') {
        errors.target = 'HTTP target must use http:// or https://';
      } else if (!url.host) {
        errors.target = 'HTTP target must include a valid host';
      }
    } catch {
      errors.target = 'HTTP target must be a valid URL (e.g. https://example.com/health)';
    }
  } else if (values.kind === 'tcp') {
    const tcpMatch = trimmedTarget.match(/^([^:]+):(\d+)$/);
    if (!tcpMatch) {
      errors.target = 'TCP target must be in host:port format (e.g. db.internal:5432)';
    } else {
      const port = Number(tcpMatch[2]);
      if (port < 1 || port > 65535) {
        errors.target = 'TCP port must be between 1 and 65535';
      }
    }
  }

  // Protocol specific validations
  if (values.kind === 'http') {
    if (values.method) {
      const allowedMethods = ['GET', 'POST', 'PUT', 'HEAD', 'DELETE', 'PATCH'];
      if (!allowedMethods.includes(values.method.toUpperCase())) {
        errors.method = `Method must be one of ${allowedMethods.join(', ')}`;
      }
    }

    if (values.expected_status_range) {
      const rangeError = validateExpectedStatusRange(values.expected_status_range);
      if (rangeError) {
        errors.expected_status_range = rangeError;
      }
    }
  }

  // Interval validation
  const interval = Number(values.interval_ms);
  if (isNaN(interval) || !Number.isInteger(interval) || interval < 1000) {
    errors.interval_ms = 'Interval must be at least 1,000 ms (1 second)';
  }

  // Timeout validation
  const timeout = Number(values.timeout_ms);
  if (isNaN(timeout) || !Number.isInteger(timeout) || timeout < 100) {
    errors.timeout_ms = 'Timeout must be at least 100 ms';
  }
  // Note: Backend does NOT enforce timeout_ms <= interval_ms. Frontend must NOT enforce it.

  return errors;
}
