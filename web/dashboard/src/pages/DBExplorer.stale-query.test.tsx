import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';
import DBExplorer from './DBExplorer';

// F33702: a query still running when the operator switches database used to
// commit its rows (and the "Query executed" toast) under the newly selected
// database, so db2 appeared to return db1's data. The tables/columns loaders
// already discard superseded responses; the query runner did not.

const apiMocks = vi.hoisted(() => ({
  fetchDatabases: vi.fn(),
  fetchDBTables: vi.fn(),
  fetchDBColumns: vi.fn(),
  runDBQuery: vi.fn(),
}));

vi.mock('@/lib/api', () => apiMocks);

const flush = () => act(async () => {});

describe('DBExplorer query result must belong to the selected database', () => {
  let resolveQuery!: (v: unknown) => void;

  beforeEach(() => {
    Object.values(apiMocks).forEach(fn => fn.mockReset());
    apiMocks.fetchDatabases.mockResolvedValue([{ name: 'db1' }, { name: 'db2' }]);
    apiMocks.fetchDBTables.mockResolvedValue([]);
    apiMocks.fetchDBColumns.mockResolvedValue([]);
    apiMocks.runDBQuery.mockImplementation(
      () => new Promise(resolve => { resolveQuery = resolve; }),
    );
  });

  it('drops a query result that finishes after the database was switched', async () => {
    render(<DBExplorer />);
    await flush();

    fireEvent.click(screen.getByRole('button', { name: /execute query/i }));
    await flush();
    expect(apiMocks.runDBQuery).toHaveBeenCalledWith('db1', expect.any(String));

    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'db2' } });
    await flush();

    await act(async () => {
      resolveQuery({ columns: ['secret'], rows: [['from-db1']], count: 1 });
    });
    await flush();

    console.log('EXPECTED: db1 rows not shown while db2 is selected');
    console.log('ACTUAL: shows db1 row=' + !!screen.queryByText('from-db1'));
    expect(screen.queryByText('from-db1')).toBeNull();
    expect(screen.queryByText(/query executed/i)).toBeNull();
  });

  it('control: a query that finishes on the same database is shown', async () => {
    render(<DBExplorer />);
    await flush();

    fireEvent.click(screen.getByRole('button', { name: /execute query/i }));
    await flush();
    await act(async () => {
      resolveQuery({ columns: ['secret'], rows: [['from-db1']], count: 1 });
    });
    await flush();
    expect(screen.getByText('from-db1')).toBeTruthy();
  });
});
