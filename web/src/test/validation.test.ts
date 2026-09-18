import { describe, it, expect } from 'vitest';
import {
  validateMonitorForm,
  validateExpectedStatusRange,
} from '../utils/validation';

describe('validateExpectedStatusRange', () => {
  it('accepts valid single status code', () => {
    expect(validateExpectedStatusRange('200')).toBeNull();
    expect(validateExpectedStatusRange('404')).toBeNull();
    expect(validateExpectedStatusRange('599')).toBeNull();
  });

  it('accepts valid range A-B with 100 <= A <= B <= 599', () => {
    expect(validateExpectedStatusRange('200-299')).toBeNull();
    expect(validateExpectedStatusRange('200-200')).toBeNull();
    expect(validateExpectedStatusRange('100-599')).toBeNull();
  });

  it('rejects invalid single status code', () => {
    expect(validateExpectedStatusRange('99')).not.toBeNull();
    expect(validateExpectedStatusRange('600')).not.toBeNull();
    expect(validateExpectedStatusRange('abc')).not.toBeNull();
  });

  it('rejects invalid range', () => {
    expect(validateExpectedStatusRange('300-200')).not.toBeNull();
    expect(validateExpectedStatusRange('50-200')).not.toBeNull();
    expect(validateExpectedStatusRange('200-700')).not.toBeNull();
    expect(validateExpectedStatusRange('200-300-400')).not.toBeNull();
  });

  it('accepts empty string as optional', () => {
    expect(validateExpectedStatusRange('')).toBeNull();
    expect(validateExpectedStatusRange('   ')).toBeNull();
  });
});

describe('validateMonitorForm', () => {
  it('validates required name and length limit', () => {
    const resEmpty = validateMonitorForm({
      name: '',
      kind: 'http',
      target: 'https://example.com',
      interval_ms: 60000,
      timeout_ms: 5000,
    });
    expect(resEmpty.name).toBeDefined();

    const resTooLong = validateMonitorForm({
      name: 'a'.repeat(256),
      kind: 'http',
      target: 'https://example.com',
      interval_ms: 60000,
      timeout_ms: 5000,
    });
    expect(resTooLong.name).toBeDefined();
  });

  it('validates HTTP target URL format', () => {
    const invalidUrl = validateMonitorForm({
      name: 'Test',
      kind: 'http',
      target: 'not-a-url',
      interval_ms: 60000,
      timeout_ms: 5000,
    });
    expect(invalidUrl.target).toBeDefined();

    const nonHttp = validateMonitorForm({
      name: 'Test',
      kind: 'http',
      target: 'ftp://example.com',
      interval_ms: 60000,
      timeout_ms: 5000,
    });
    expect(nonHttp.target).toBeDefined();

    const validHttp = validateMonitorForm({
      name: 'Test',
      kind: 'http',
      target: 'https://api.example.com/health',
      interval_ms: 60000,
      timeout_ms: 5000,
    });
    expect(validHttp.target).toBeUndefined();
  });

  it('validates TCP target host:port format', () => {
    const invalidFormat = validateMonitorForm({
      name: 'Test',
      kind: 'tcp',
      target: 'example.com',
      interval_ms: 60000,
      timeout_ms: 5000,
    });
    expect(invalidFormat.target).toBeDefined();

    const portOutOfRange = validateMonitorForm({
      name: 'Test',
      kind: 'tcp',
      target: 'example.com:70000',
      interval_ms: 60000,
      timeout_ms: 5000,
    });
    expect(portOutOfRange.target).toBeDefined();

    const validTcp = validateMonitorForm({
      name: 'Test',
      kind: 'tcp',
      target: '127.0.0.1:5432',
      interval_ms: 60000,
      timeout_ms: 5000,
    });
    expect(validTcp.target).toBeUndefined();
  });

  it('validates interval_ms >= 1000 and timeout_ms >= 100', () => {
    const lowInterval = validateMonitorForm({
      name: 'Test',
      kind: 'http',
      target: 'https://example.com',
      interval_ms: 500,
      timeout_ms: 5000,
    });
    expect(lowInterval.interval_ms).toBeDefined();

    const lowTimeout = validateMonitorForm({
      name: 'Test',
      kind: 'http',
      target: 'https://example.com',
      interval_ms: 60000,
      timeout_ms: 50,
    });
    expect(lowTimeout.timeout_ms).toBeDefined();
  });

  it('CRITICAL: does NOT enforce timeout_ms <= interval_ms', () => {
    // Backend allows timeout_ms > interval_ms; frontend must NOT reject it.
    const result = validateMonitorForm({
      name: 'Test',
      kind: 'http',
      target: 'https://example.com',
      interval_ms: 1000,
      timeout_ms: 5000,
    });
    expect(result.interval_ms).toBeUndefined();
    expect(result.timeout_ms).toBeUndefined();
  });
});
