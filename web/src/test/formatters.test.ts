import { describe, it, expect } from 'vitest';
import {
  formatDuration,
  calculatePollInterval,
} from '../utils/formatters';

describe('formatDuration', () => {
  it('formats milliseconds', () => {
    expect(formatDuration(0)).toBe('0ms');
    expect(formatDuration(500)).toBe('500ms');
  });

  it('formats seconds', () => {
    expect(formatDuration(1000)).toBe('1s');
    expect(formatDuration(5000)).toBe('5s');
    expect(formatDuration(30000)).toBe('30s');
  });

  it('formats minutes', () => {
    expect(formatDuration(60000)).toBe('1m');
    expect(formatDuration(120000)).toBe('2m');
    expect(formatDuration(300000)).toBe('5m');
  });

  it('formats minutes and seconds', () => {
    expect(formatDuration(90000)).toBe('1m 30s');
    expect(formatDuration(150000)).toBe('2m 30s');
  });
});

describe('calculatePollInterval', () => {
  it('enforces minimum 10,000ms polling rate', () => {
    expect(calculatePollInterval(1000)).toBe(10_000);
    expect(calculatePollInterval(5000)).toBe(10_000);
    expect(calculatePollInterval(9999)).toBe(10_000);
  });

  it('tracks monitor interval when between 10s and 60s', () => {
    expect(calculatePollInterval(15_000)).toBe(15_000);
    expect(calculatePollInterval(30_000)).toBe(30_000);
    expect(calculatePollInterval(45_000)).toBe(45_000);
  });

  it('enforces maximum 60,000ms polling ceiling', () => {
    expect(calculatePollInterval(60_000)).toBe(60_000);
    expect(calculatePollInterval(120_000)).toBe(60_000);
    expect(calculatePollInterval(300_000)).toBe(60_000);
  });

  it('handles edge cases safely', () => {
    expect(calculatePollInterval(0)).toBe(10_000);
    expect(calculatePollInterval(NaN)).toBe(10_000);
  });
});
