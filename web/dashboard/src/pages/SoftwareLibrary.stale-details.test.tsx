import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import SoftwareLibrary from './SoftwareLibrary';

// ── API boundary mock (only the boundary — the real component runs) ─────────
const apiMocks = vi.hoisted(() => ({
  fetchSoftwareTemplates: vi.fn(),
  fetchSoftwareInstances: vi.fn(),
  fetchSoftwareMonitorSummary: vi.fn(),
  fetchSoftwareLogs: vi.fn(),
  fetchSoftwareMonitor: vi.fn(),
  fetchSoftwareProcesses: vi.fn(),
  fetchSoftwareBackups: vi.fn(),
  checkSoftwarePort: vi.fn(),
  installSoftware: vi.fn(),
  startSoftware: vi.fn(),
  stopSoftware: vi.fn(),
  restartSoftware: vi.fn(),
  updateSoftware: vi.fn(),
  deleteSoftware: vi.fn(),
  backupSoftware: vi.fn(),
  backupAllSoftware: vi.fn(),
  updateAllSoftware: vi.fn(),
  restoreSoftwareBackup: vi.fn(),
  deleteSoftwareBackup: vi.fn(),
  fetchSoftwareLogsLegacy: vi.fn(),
  connectSoftwareDomain: vi.fn(),
  disconnectSoftwareDomain: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

const flush = () => act(async () => {});

const INSTANCES = [
  { name: 'n8n-a', template: 'n8n', project: 'n8n-a', status: 'running', has_web: false },
  { name: 'pg-b', template: 'postgres', project: 'pg-b', status: 'running', has_web: false },
];

const MON_A = {
  total_cpu_percent: 11.11, total_memory: 1000, total_network_input: 0,
  total_network_output: 0, containers: [], volumes: [],
};
const MON_B = {
  total_cpu_percent: 22.22, total_memory: 2000, total_network_input: 0,
  total_network_output: 0, containers: [], volumes: [],
};
const PROC_A = [{ container_id: 'ca', pid: 111, service: 'svc-a', command: 'a-cmd' }];
const PROC_B = [{ container_id: 'cb', pid: 222, service: 'svc-b', command: 'b-cmd' }];
const BACKUP_A = [{ path: '/backups/a.tar', name: 'a-backup', size: 1, volume_key: 'va', created_at: '2026-01-01T00:00:00Z' }];
const BACKUP_B = [{ path: '/backups/b.tar', name: 'b-backup', size: 2, volume_key: 'vb', created_at: '2026-01-02T00:00:00Z' }];

type Gate = { name: string; kind: 'monitor' | 'processes' | 'backups' | 'logs'; resolve: (v: unknown) => void; reject: (e: unknown) => void };

function renderPage() {
  return render(<SoftwareLibrary />);
}

// Resolve (or reject) the three parked detail fetches for one instance.
async function settle(gates: Gate[], name: string, data: { monitor: unknown; processes: unknown; backups: unknown } | { error: Error }) {
  await act(async () => {
    for (const g of gates.filter(x => x.name === name)) {
      if ('error' in data) g.reject(data.error);
      else if (g.kind === 'monitor') g.resolve(data.monitor);
      else if (g.kind === 'processes') g.resolve(data.processes);
      else if (g.kind === 'backups') g.resolve(data.backups);
    }
  });
  await act(async () => {});
}

async function resolveLogs(gates: Gate[], name: string, text: string | { error: Error }) {
  await act(async () => {
    for (const g of gates.filter(x => x.name === name && x.kind === 'logs')) {
      if (typeof text === 'string') g.resolve({ logs: text });
      else g.reject(text.error);
    }
  });
  await act(async () => {});
}

describe('SoftwareLibrary detail drawers: superseded instance responses must not commit', () => {
  let gates: Gate[];

  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => {
      if (vi.isMockFunction(fn)) fn.mockReset();
    });
    gates = [];
    apiMocks.fetchSoftwareTemplates.mockResolvedValue([]);
    apiMocks.fetchSoftwareInstances.mockResolvedValue(INSTANCES);
    apiMocks.fetchSoftwareMonitorSummary.mockResolvedValue({ container_count: 2 });
    apiMocks.fetchSoftwareMonitor.mockImplementation((name: string) => {
      const p = new Promise((resolve, reject) => { gates.push({ name, kind: 'monitor', resolve, reject }); });
      return p;
    });
    apiMocks.fetchSoftwareProcesses.mockImplementation((name: string) => {
      const p = new Promise((resolve, reject) => { gates.push({ name, kind: 'processes', resolve, reject }); });
      return p;
    });
    apiMocks.fetchSoftwareBackups.mockImplementation((name: string) => {
      const p = new Promise((resolve, reject) => { gates.push({ name, kind: 'backups', resolve, reject }); });
      return p;
    });
    apiMocks.fetchSoftwareLogs.mockImplementation((name: string) => {
      const p = new Promise((resolve, reject) => { gates.push({ name, kind: 'logs', resolve, reject }); });
      return p;
    });
  });

  it('control: a single instance monitor renders its own data', async () => {
    renderPage();
    await flush();
    fireEvent.click(screen.getAllByRole('button', { name: 'Monitor' })[0]); // n8n-a
    await flush();
    await settle(gates, 'n8n-a', { monitor: MON_A, processes: PROC_A, backups: BACKUP_A });

    expect(screen.getByText('n8n-a monitor')).toBeTruthy();
    expect(screen.getByText('11.11%')).toBeTruthy();
    expect(screen.getByText('a-backup')).toBeTruthy();
  });

  it('keeps pg-b\u2019s monitor when n8n-a\u2019s slower detail fetches land last', async () => {
    renderPage();
    await flush();

    const monitorButtons = screen.getAllByRole('button', { name: 'Monitor' });
    fireEvent.click(monitorButtons[0]); // n8n-a — its three fetches hang
    await flush();
    fireEvent.click(monitorButtons[1]); // pg-b — opened while n8n-a is in flight
    await flush();

    // pg-b's fetches resolve first; the panel shows pg-b's data.
    await settle(gates, 'pg-b', { monitor: MON_B, processes: PROC_B, backups: BACKUP_B });
    expect(screen.getByText('pg-b monitor')).toBeTruthy();
    expect(screen.getByText('22.22%')).toBeTruthy();
    expect(screen.getByText('b-backup')).toBeTruthy();

    // n8n-a's stale fetches land LAST and must be discarded — not commit
    // n8n-a's metrics/processes/backups into the open pg-b panel.
    await settle(gates, 'n8n-a', { monitor: MON_A, processes: PROC_A, backups: BACKUP_A });

    // RED pre-fix: the panel now shows n8n-a's CPU (11.11%) and a-backup
    // under the "pg-b monitor" header.
    expect(screen.getByText('pg-b monitor')).toBeTruthy();
    expect(screen.getByText('22.22%')).toBeTruthy();
    expect(screen.getByText('b-backup')).toBeTruthy();
    expect(screen.queryByText('11.11%')).toBeNull();
    expect(screen.queryByText('a-backup')).toBeNull();
  });

  it('a stale failure does not close the open instance\u2019s monitor', async () => {
    renderPage();
    await flush();

    const monitorButtons = screen.getAllByRole('button', { name: 'Monitor' });
    fireEvent.click(monitorButtons[0]);
    await flush();
    fireEvent.click(monitorButtons[1]);
    await flush();
    await settle(gates, 'pg-b', { monitor: MON_B, processes: PROC_B, backups: BACKUP_B });

    await settle(gates, 'n8n-a', { error: new Error('n8n-a inspect failed') });

    // RED pre-fix: the stale rejection ran setStatus(error) AND
    // setMonitorFor('') — closing the open pg-b panel entirely.
    expect(screen.getByText('pg-b monitor')).toBeTruthy();
    expect(screen.getByText('22.22%')).toBeTruthy();
    expect(screen.queryByText(/n8n-a inspect failed/)).toBeNull();
  });

  it('keeps pg-b\u2019s logs when n8n-a\u2019s slower log fetch lands last', async () => {
    renderPage();
    await flush();

    const logsButtons = screen.getAllByRole('button', { name: 'Logs' });
    fireEvent.click(logsButtons[0]); // n8n-a — log fetch hangs
    await flush();
    fireEvent.click(logsButtons[1]); // pg-b
    await flush();
    await resolveLogs(gates, 'pg-b', 'LOGS-B');
    expect(screen.getByText('pg-b logs')).toBeTruthy();
    expect(screen.getByText('LOGS-B')).toBeTruthy();

    await resolveLogs(gates, 'n8n-a', 'LOGS-A');

    // RED pre-fix: the stale response renders n8n-a's logs under the
    // "pg-b logs" header.
    expect(screen.getByText('LOGS-B')).toBeTruthy();
    expect(screen.queryByText('LOGS-A')).toBeNull();
  });
});
