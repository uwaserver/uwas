import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import CronJobs from './CronJobs';
import Services from './Services';
import Analytics from './Analytics';
import Logs from './Logs';
import Metrics from './Metrics';
import EmailGuide from './EmailGuide';

// F56600-F56605: a failed list request left the red error banner (or, for
// EmailGuide, nothing at all) while the table also claimed "No cron jobs
// configured", "No services found", ... presenting an unknown state as an
// empty one. Same class as F55601-F55603.

const api = vi.hoisted(() => ({
  fetchCronJobs: vi.fn(), addCronJob: vi.fn(), deleteCronJob: vi.fn(), fetchDomains: vi.fn(),
  fetchCronMonitor: vi.fn(), executeCron: vi.fn(), fetchFeatures: vi.fn(),
  fetchServices: vi.fn(), startService: vi.fn(), stopService: vi.fn(), restartService: vi.fn(),
  fetchAnalytics: vi.fn(), fetchBandwidth: vi.fn(), resetBandwidth: vi.fn(),
  fetchLogs: vi.fn(), fetchMetrics: vi.fn(), fetchServerIPs: vi.fn(),
}));
vi.mock('@/lib/api', () => api);
vi.mock('@/components/useConfirm', () => ({ useConfirm: () => ({ confirmAction: vi.fn() }) }));

const flush = () => act(async () => { for (let i = 0; i < 10; i++) await Promise.resolve(); });
const mount = async (ui: React.ReactElement) => { render(<MemoryRouter>{ui}</MemoryRouter>); await flush(); };

describe('failed loads are not shown as empty', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.fetchFeatures.mockResolvedValue({});
    api.fetchCronMonitor.mockResolvedValue([]);
    api.fetchBandwidth.mockResolvedValue([]);
    api.fetchServerIPs.mockResolvedValue({ public_ip: '' });
  });

  it('CronJobs: error without the empty message; empty list says so', async () => {
    api.fetchCronJobs.mockRejectedValue(new Error('upstream 502'));
    api.fetchDomains.mockResolvedValue([]);
    await mount(<CronJobs />);
    console.log('ACTUAL: empty=' + !!screen.queryByText(/No cron jobs configured/));
    expect(screen.getByText(/upstream 502/)).toBeTruthy();
    expect(screen.queryByText(/No cron jobs configured/)).toBeNull();
  });
  it('CronJobs control', async () => {
    api.fetchCronJobs.mockResolvedValue(null);
    api.fetchDomains.mockResolvedValue([]);
    await mount(<CronJobs />);
    expect(screen.getByText(/No cron jobs configured/)).toBeTruthy();
  });

  it('Services: error without the empty message; empty list says so', async () => {
    api.fetchServices.mockRejectedValue(new Error('upstream 502'));
    await mount(<Services />);
    console.log('ACTUAL: empty=' + !!screen.queryByText(/No services found/));
    expect(screen.getByText(/upstream 502/)).toBeTruthy();
    expect(screen.queryByText(/No services found/)).toBeNull();
  });
  it('Services control', async () => {
    api.fetchServices.mockResolvedValue(null);
    await mount(<Services />);
    expect(screen.getByText(/No services found/)).toBeTruthy();
  });

  it('Analytics: error without the empty message; empty list says so', async () => {
    api.fetchAnalytics.mockRejectedValue(new Error('upstream 502'));
    await mount(<Analytics />);
    console.log('ACTUAL: empty=' + !!screen.queryByText(/No analytics data yet/));
    expect(screen.getByText(/upstream 502/)).toBeTruthy();
    expect(screen.queryByText(/No analytics data yet/)).toBeNull();
  });
  it('Analytics control', async () => {
    api.fetchAnalytics.mockResolvedValue(null);
    await mount(<Analytics />);
    expect(screen.getByText(/No analytics data yet/)).toBeTruthy();
  });

  it('Logs: error without the empty message; empty list says so', async () => {
    api.fetchLogs.mockRejectedValue(new Error('upstream 502'));
    await mount(<Logs />);
    console.log('ACTUAL: empty=' + !!screen.queryByText(/No log entries found/));
    expect(screen.getByText(/upstream 502/)).toBeTruthy();
    expect(screen.queryByText(/No log entries found/)).toBeNull();
  });
  it('Logs control', async () => {
    api.fetchLogs.mockResolvedValue([]);
    await mount(<Logs />);
    expect(screen.getByText(/No log entries found/)).toBeTruthy();
  });

  it('Metrics: error without the empty message; empty list says so', async () => {
    api.fetchMetrics.mockRejectedValue(new Error('upstream 502'));
    await mount(<Metrics />);
    console.log('ACTUAL: empty=' + !!screen.queryByText(/No metrics available/));
    expect(screen.getByText(/upstream 502/)).toBeTruthy();
    expect(screen.queryByText(/No metrics available/)).toBeNull();
  });
  it('Metrics control', async () => {
    api.fetchMetrics.mockResolvedValue('');
    await mount(<Metrics />);
    expect(screen.getByText(/No metrics available/)).toBeTruthy();
  });

  it('EmailGuide: a failed domain load is reported, not "No domains yet"', async () => {
    api.fetchDomains.mockRejectedValue(new Error('upstream 502'));
    await mount(<EmailGuide />);
    console.log('ACTUAL: empty=' + !!screen.queryByText(/No domains yet/));
    expect(screen.getByText(/upstream 502/)).toBeTruthy();
    expect(screen.queryByText(/No domains yet/)).toBeNull();
  });
  it('EmailGuide control', async () => {
    api.fetchDomains.mockResolvedValue([]);
    await mount(<EmailGuide />);
    expect(screen.getByText(/No domains yet/)).toBeTruthy();
  });
});
