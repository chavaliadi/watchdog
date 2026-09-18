import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { MonitorFormModal } from '../components/forms/MonitorFormModal';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as api from '../api/monitors';
import { ApiError } from '../api/client';
import type { Monitor } from '../types/monitor';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: false },
    mutations: { retry: false },
  },
});

const renderModal = (props: React.ComponentProps<typeof MonitorFormModal>) => {
  return render(
    <QueryClientProvider client={queryClient}>
      <MonitorFormModal {...props} />
    </QueryClientProvider>
  );
};

describe('MonitorFormModal', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders HTTP specific fields by default in Create mode', () => {
    renderModal({ isOpen: true, onClose: vi.fn() });

    expect(screen.getByText('Create New Monitor')).toBeInTheDocument();
    expect(screen.getByText('HTTP Method')).toBeInTheDocument();
    expect(screen.getByText('Expected Status Range')).toBeInTheDocument();
  });

  it('hides HTTP method and status range when switched to TCP', () => {
    renderModal({ isOpen: true, onClose: vi.fn() });

    const tcpButton = screen.getByText('TCP Connection');
    fireEvent.click(tcpButton);

    expect(screen.queryByText('HTTP Method')).not.toBeInTheDocument();
    expect(screen.queryByText('Expected Status Range')).not.toBeInTheDocument();
    expect(screen.getByPlaceholderText('db.internal:5432 or 127.0.0.1:8080')).toBeInTheDocument();
  });

  it('disables kind selection in Edit mode with explanation', () => {
    const mockMonitor: Monitor = {
      id: '11111111-1111-1111-1111-111111111111',
      name: 'Existing API',
      kind: 'http',
      target: 'https://api.example.com',
      method: 'GET',
      expected_status_range: '200',
      interval_ms: 60000,
      timeout_ms: 5000,
      enabled: true,
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    };

    renderModal({
      isOpen: true,
      onClose: vi.fn(),
      monitor: mockMonitor,
    });

    expect(screen.getByText('Edit Monitor')).toBeInTheDocument();
    expect(
      screen.getByText(
        'Protocol cannot be changed after creation. Delete and recreate to change protocol.'
      )
    ).toBeInTheDocument();
    const disabledKindInput = screen.getByDisplayValue('HTTP');
    expect(disabledKindInput).toBeDisabled();
  });

  it('displays authoritative server error message on API rejection', async () => {
    vi.spyOn(api, 'createMonitor').mockRejectedValueOnce(
      new ApiError(400, 'INVALID_ARGUMENT', 'invalid http method "FOO"')
    );

    renderModal({ isOpen: true, onClose: vi.fn() });

    // Fill valid form
    fireEvent.change(screen.getByPlaceholderText('e.g. Production API Gateway'), {
      target: { value: 'My API' },
    });
    fireEvent.change(screen.getByPlaceholderText('https://api.example.com/health'), {
      target: { value: 'https://api.example.com' },
    });

    // Submit form
    fireEvent.click(screen.getByText('Create Monitor'));

    await waitFor(() => {
      expect(screen.getByText('Backend Rejected Request')).toBeInTheDocument();
      expect(screen.getByText('invalid http method "FOO"')).toBeInTheDocument();
    });
  });
});
