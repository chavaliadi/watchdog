import { describe, it, expect, vi, beforeEach } from 'vitest';
import { request, ApiError } from '../api/client';

describe('API Client', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('parses successful JSON response', async () => {
    const mockData = [{ id: '123', name: 'API' }];
    globalThis.fetch = vi.fn().mockResolvedValueOnce({
      ok: true,
      status: 200,
      text: async () => JSON.stringify(mockData),
    } as unknown as Response);

    const result = await request<typeof mockData>('/monitors');
    expect(result).toEqual(mockData);
  });

  it('returns undefined on 204 No Content', async () => {
    globalThis.fetch = vi.fn().mockResolvedValueOnce({
      ok: true,
      status: 204,
      text: async () => '',
    } as unknown as Response);

    const result = await request<void>('/monitors/123', { method: 'DELETE' });
    expect(result).toBeUndefined();
  });

  it('normalizes backend error envelope', async () => {
    const errorResponse = {
      error: {
        code: 'INVALID_ARGUMENT',
        message: 'name must not be empty or whitespace-only',
        details: null,
      },
    };

    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 400,
      text: async () => JSON.stringify(errorResponse),
    } as unknown as Response);

    await expect(request('/monitors', { method: 'POST' })).rejects.toThrowError(
      ApiError
    );

    try {
      await request('/monitors', { method: 'POST' });
    } catch (err) {
      const apiErr = err as ApiError;
      expect(apiErr.status).toBe(400);
      expect(apiErr.code).toBe('INVALID_ARGUMENT');
      expect(apiErr.message).toBe('name must not be empty or whitespace-only');
    }
  });

  it('handles network failure cleanly without crashing', async () => {
    globalThis.fetch = vi.fn().mockRejectedValueOnce(new Error('Failed to fetch'));

    try {
      await request('/monitors');
      expect.unreachable('Should have thrown');
    } catch (err) {
      const apiErr = err as ApiError;
      expect(apiErr.code).toBe('NETWORK_ERROR');
      expect(apiErr.message).toContain('Unable to connect to server');
    }
  });
});
