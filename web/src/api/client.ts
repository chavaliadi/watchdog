import type { ApiErrorEnvelope } from '../types/api';

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    public message: string,
    public details: unknown = null
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

const getBaseUrl = (): string => {
  const envUrl = import.meta.env.VITE_API_BASE_URL;
  if (typeof envUrl === 'string' && envUrl.trim() !== '') {
    return envUrl.trim().replace(/\/+$/, '');
  }
  return '';
};

export async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const baseUrl = getBaseUrl();
  const normalizedPath = path.startsWith('/') ? path : `/${path}`;
  const url = `${baseUrl}${normalizedPath}`;

  const headers: HeadersInit = {
    Accept: 'application/json',
    ...options.headers,
  };

  // Only set Content-Type if there's a body
  if (options.body && !('Content-Type' in (options.headers || {}))) {
    (headers as Record<string, string>)['Content-Type'] = 'application/json';
  }

  let response: Response;
  try {
    response = await fetch(url, {
      ...options,
      headers,
    });
  } catch (err) {
    const errorMsg = err instanceof Error ? err.message : 'Network request failed';
    throw new ApiError(0, 'NETWORK_ERROR', `Unable to connect to server: ${errorMsg}`);
  }

  // 204 No Content (e.g. DELETE)
  if (response.status === 204) {
    return undefined as unknown as T;
  }

  let data: unknown;
  try {
    const text = await response.text();
    data = text ? JSON.parse(text) : null;
  } catch {
    if (!response.ok) {
      throw new ApiError(
        response.status,
        'MALFORMED_RESPONSE',
        `Server returned non-JSON response with HTTP status ${response.status}`
      );
    }
    throw new ApiError(
      response.status,
      'PARSE_ERROR',
      'Failed to parse JSON response from server'
    );
  }

  if (!response.ok) {
    const errorEnvelope = data as ApiErrorEnvelope | null;
    const errPayload = errorEnvelope?.error;

    if (errPayload && typeof errPayload.message === 'string') {
      throw new ApiError(
        response.status,
        errPayload.code || 'API_ERROR',
        errPayload.message,
        errPayload.details
      );
    }

    throw new ApiError(
      response.status,
      'HTTP_ERROR',
      response.statusText || `Request failed with HTTP status ${response.status}`
    );
  }

  return data as T;
}
