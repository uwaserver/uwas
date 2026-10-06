import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import DBExplorer from './DBExplorer';

// ── API boundary mock (only the boundary — the real component runs) ─────────
const apiMocks = vi.hoisted(() => ({
  fetchDatabases: vi.fn(),
  fetchDBTables: vi.fn(),
  fetchDBColumns: vi.fn(),
  runDBQuery: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

const flush = () => act(async () => {});

const DATABASES = [{ name: 'db1' }, { name: 'db2' }];

const TABLES_DB1 = [{ name: 'a_users', rows: '5', data_size: '1 KB', engine: 'InnoDB' }];
const TABLES_DB2 = [
  { name: 'b_orders', rows: '9', data_size: '3 KB', engine: 'InnoDB' },
  { name: 'b_users', rows: '7', data_size: '2 KB', engine: 'InnoDB' },
];

const COLS_B_ORDERS = [
  { name: 'order_id', type: 'int', nullable: 'NO', key: 'PRI', default: '', extra: 'auto_increment' },
];
const COLS_B_USERS = [
  { name: 'email', type: 'varchar(255)', nullable: 'NO', key: '', default: '', extra: '' },
];

type Gate = { tag: string; resolve: (v: unknown) => void; reject: (e: unknown) => void };

function renderPage() {
  return render(<DBExplorer />);
}

describe('DBExplorer selection-scoped loaders: superseded responses must not commit', () => {
  let gates: Gate[];

  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => {
      if (vi.isMockFunction(fn)) fn.mockReset();
    });
    gates = [];
    apiMocks.fetchDatabases.mockResolvedValue(DATABASES);
    apiMocks.fetchDBTables.mockImplementation((db: string) => {
      const p = new Promise((resolve, reject) => { gates.push({ tag: `tables:${db}`, resolve, reject }); });
      return p;
    });
    apiMocks.fetchDBColumns.mockImplementation((db: string, table: string) => {
      const p = new Promise((resolve, reject) => { gates.push({ tag: `columns:${db}.${table}`, resolve, reject }); });
      return p;
    });
  });

  async function settle(tag: string, data: { rows?: unknown[]; cols?: unknown[] } | { error: Error }) {
    await act(async () => {
      for (const g of gates.filter(x => x.tag === tag)) {
        if ('error' in data) g.reject(data.error);
        else if (tag.startsWith('tables:')) g.resolve(data.rows);
        else g.resolve(data.cols);
      }
    });
    await act(async () => {});
  }

  it('control: the auto-selected database renders its own tables', async () => {
    renderPage();
    await flush();
    await settle('tables:db1', { rows: TABLES_DB1 });

    expect(screen.getByText('db1')).toBeTruthy();
    expect(screen.getByText('a_users')).toBeTruthy();
  });

  it('keeps db2\u2019s table list when db1\u2019s slower tables fetch lands last', async () => {
    renderPage();
    await flush(); // mount auto-selects db1; tables:db1 parked

    // Switch to db2 while db1's table fetch is still in flight.
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'db2' } });
    await flush();
    expect(gates.filter(g => g.tag === 'tables:db2').length).toBe(1);

    // db2's fetch resolves first — the sidebar shows db2's tables.
    await settle('tables:db2', { rows: TABLES_DB2 });
    expect(screen.getByText('b_users')).toBeTruthy();

    // db1's stale fetch lands LAST and must be discarded.
    await settle('tables:db1', { rows: TABLES_DB1 });

    // RED pre-fix: the sidebar now lists db1's tables under the db2 selector;
    // a click there generates "SELECT * FROM a_users" against db2.
    expect(screen.getByText('b_users')).toBeTruthy();
    expect(screen.queryByText('a_users')).toBeNull();
  });

  it('keeps b_users\u2019s columns when b_orders\u2019s slower column fetch lands last', async () => {
    renderPage();
    await flush();
    await settle('tables:db1', { rows: TABLES_DB1 }); // db1 auto-selected

    // Switch to db2 and render its two tables.
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'db2' } });
    await flush();
    await settle('tables:db2', { rows: TABLES_DB2 });

    // Select b_orders as the query table, then b_users — two overlapping
    // column fetches for the same connection.
    fireEvent.click(screen.getByText('b_orders'));
    fireEvent.click(screen.getAllByRole('button', { name: /SELECT \*/ })[0]);
    await flush();
    fireEvent.click(screen.getByText('b_users'));
    fireEvent.click(screen.getAllByRole('button', { name: /SELECT \*/ })[1]);
    await flush();
    expect(gates.filter(g => g.tag === 'columns:db2.b_orders').length).toBe(1);
    expect(gates.filter(g => g.tag === 'columns:db2.b_users').length).toBe(1);

    // b_users' columns resolve first — the structure panel shows them.
    await settle('columns:db2.b_users', { cols: COLS_B_USERS });
    expect(screen.getByText('email')).toBeTruthy();

    // b_orders' stale columns land LAST and must be discarded.
    await settle('columns:db2.b_orders', { cols: COLS_B_ORDERS });

    // RED pre-fix: the structure panel shows b_orders' columns while
    // b_users is the selected table.
    expect(screen.getByText('email')).toBeTruthy();
    expect(screen.queryByText('order_id')).toBeNull();
  });
});
