export function formatDuration(ms: number): string {
  if (isNaN(ms) || ms < 0) return '0ms';
  if (ms < 1000) return `${ms}ms`;
  if (ms % 60000 === 0) return `${ms / 60000}m`;
  if (ms >= 60000) {
    const minutes = Math.floor(ms / 60000);
    const seconds = Math.round((ms % 60000) / 1000);
    return seconds > 0 ? `${minutes}m ${seconds}s` : `${minutes}m`;
  }
  if (ms % 1000 === 0) return `${ms / 1000}s`;
  return `${(ms / 1000).toFixed(1)}s`;
}

export function formatRelativeTime(dateString: string): string {
  if (!dateString) return 'Never';
  const date = new Date(dateString);
  if (isNaN(date.getTime())) return 'Invalid date';

  const diffMs = Date.now() - date.getTime();
  if (diffMs < 5000) return 'Just now';

  const diffSec = Math.floor(diffMs / 1000);
  if (diffSec < 60) return `${diffSec}s ago`;

  const diffMin = Math.floor(diffSec / 60);
  if (diffMin < 60) return `${diffMin}m ago`;

  const diffHr = Math.floor(diffMin / 60);
  if (diffHr < 24) return `${diffHr}h ago`;

  const diffDay = Math.floor(diffHr / 24);
  return `${diffDay}d ago`;
}

export function formatUtcDateTime(dateString: string): string {
  if (!dateString) return '-';
  const date = new Date(dateString);
  if (isNaN(date.getTime())) return '-';
  return date.toISOString().replace('T', ' ').replace(/\.\d{3}Z$/, ' UTC');
}

/**
 * Approved dynamic detail polling policy:
 * pollInterval = max(10,000ms, min(monitor.interval_ms, 60,000ms))
 */
export function calculatePollInterval(intervalMs: number): number {
  if (!intervalMs || isNaN(intervalMs)) return 10_000;
  return Math.max(10_000, Math.min(intervalMs, 60_000));
}
