import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import { HealthBadge, KindBadge, PausedBadge } from '../components/common/Badge';

describe('HealthBadge', () => {
  it('renders HEALTHY badge with label and icon', () => {
    render(<HealthBadge state="HEALTHY" />);
    expect(screen.getByText('HEALTHY')).toBeInTheDocument();
  });

  it('renders UNHEALTHY badge with label and icon', () => {
    render(<HealthBadge state="UNHEALTHY" />);
    expect(screen.getByText('UNHEALTHY')).toBeInTheDocument();
  });

  it('renders UNKNOWN badge with label and icon', () => {
    render(<HealthBadge state="UNKNOWN" />);
    expect(screen.getByText('UNKNOWN')).toBeInTheDocument();
  });

  it('defaults undefined to UNKNOWN', () => {
    render(<HealthBadge state={undefined} />);
    expect(screen.getByText('UNKNOWN')).toBeInTheDocument();
  });
});

describe('PausedBadge', () => {
  it('renders PAUSED badge with label and pause icon', () => {
    render(<PausedBadge />);
    expect(screen.getByText('PAUSED')).toBeInTheDocument();
  });
});

describe('KindBadge', () => {
  it('renders HTTP protocol badge', () => {
    render(<KindBadge kind="http" />);
    expect(screen.getByText('http')).toBeInTheDocument();
  });

  it('renders TCP protocol badge', () => {
    render(<KindBadge kind="tcp" />);
    expect(screen.getByText('tcp')).toBeInTheDocument();
  });
});
