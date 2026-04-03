import { useState, useEffect } from 'react';
import {
  Database,
  Table2,
  Play,
  RefreshCw,
  ChevronRight,
  ChevronDown,
  Columns,
  Search,
  AlertCircle,
  CheckCircle2,
  Loader2,
} from 'lucide-react';
import {
  fetchDatabases,
  fetchDBTables,
  fetchDBColumns,
  runDBQuery,
  type DBInfo,
} from '@/lib/api';

interface DBTable {
  name: string;
  rows: string;
  data_size: string;
  engine: string;
}

interface DBColumn {
  name: string;
  type: string;
  nullable: string;
  key: string;
  default: string;
  extra: string;
}

interface QueryResult {
  columns: string[];
  rows: any[][];
  count: number;
}

// Simple card component with header and content
function SimpleCard({ title, icon, children }: { title: string; icon: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="rounded-lg border border-border bg-card p-4 shadow-sm">
      <div className="flex items-center gap-2 mb-3 pb-2 border-b border-border">
        {icon}
        <h3 className="font-semibold text-card-foreground">{title}</h3>
      </div>
      {children}
    </div>
  );
}

export default function DBExplorer() {
  const [databases, setDatabases] = useState<DBInfo[]>([]);
  const [selectedDB, setSelectedDB] = useState<string>('');
  const [tables, setTables] = useState<DBTable[]>([]);
  const [selectedTable, setSelectedTable] = useState<string>('');
  const [columns, setColumns] = useState<DBColumn[]>([]);
  const [query, setQuery] = useState<string>('SELECT * FROM users LIMIT 10');
  const [queryResult, setQueryResult] = useState<QueryResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>('');
  const [success, setSuccess] = useState<string>('');
  const [expandedTables, setExpandedTables] = useState<Set<string>>(new Set());

  // Load databases on mount
  useEffect(() => {
    loadDatabases();
  }, []);

  // Load tables when database is selected
  useEffect(() => {
    if (selectedDB) {
      loadTables(selectedDB);
      setSelectedTable('');
      setColumns([]);
    }
  }, [selectedDB]);

  // Load columns when table is selected
  useEffect(() => {
    if (selectedDB && selectedTable) {
      loadColumns(selectedDB, selectedTable);
    }
  }, [selectedDB, selectedTable]);

  const loadDatabases = async () => {
    try {
      setLoading(true);
      const dbs = await fetchDatabases();
      setDatabases(dbs);
      if (dbs.length > 0 && !selectedDB) {
        setSelectedDB(dbs[0].name);
      }
    } catch (err: any) {
      setError(err.message || 'Failed to load databases');
    } finally {
      setLoading(false);
    }
  };

  const loadTables = async (db: string) => {
    try {
      setLoading(true);
      const tbls = await fetchDBTables(db);
      setTables(tbls);
    } catch (err: any) {
      setError(err.message || 'Failed to load tables');
    } finally {
      setLoading(false);
    }
  };

  const loadColumns = async (db: string, table: string) => {
    try {
      const cols = await fetchDBColumns(db, table);
      setColumns(cols);
    } catch (err: any) {
      setError(err.message || 'Failed to load columns');
    }
  };

  const executeQuery = async () => {
    if (!selectedDB || !query.trim()) return;

    try {
      setLoading(true);
      setError('');
      setSuccess('');
      const result = await runDBQuery(selectedDB, query);
      setQueryResult(result);
      setSuccess(`Query executed successfully. ${result.count} rows returned.`);
    } catch (err: any) {
      setError(err.message || 'Query execution failed');
      setQueryResult(null);
    } finally {
      setLoading(false);
    }
  };

  const toggleTableExpand = (tableName: string) => {
    const newExpanded = new Set(expandedTables);
    if (newExpanded.has(tableName)) {
      newExpanded.delete(tableName);
    } else {
      newExpanded.add(tableName);
    }
    setExpandedTables(newExpanded);
  };

  const selectTableForQuery = (tableName: string) => {
    setSelectedTable(tableName);
    setQuery(`SELECT * FROM ${tableName} LIMIT 100`);
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2 text-foreground">
            <Database className="h-6 w-6" />
            Database Explorer
          </h1>
          <p className="text-muted-foreground mt-1">
            Browse tables, execute SQL queries, and explore your database structure
          </p>
        </div>
        <button
          onClick={loadDatabases}
          disabled={loading}
          className="inline-flex items-center gap-2 rounded-md border border-border bg-card px-4 py-2 text-sm font-medium text-card-foreground transition hover:bg-accent disabled:opacity-50"
        >
          <RefreshCw className={`h-4 w-4 ${loading ? 'animate-spin' : ''}`} />
          Refresh
        </button>
      </div>

      {/* Alerts */}
      {error && (
        <div className="rounded-md border border-red-200 bg-red-50 p-4 text-red-800">
          <div className="flex items-center gap-2">
            <AlertCircle className="h-4 w-4" />
            <span>{error}</span>
          </div>
        </div>
      )}
      {success && (
        <div className="rounded-md border border-green-200 bg-green-50 p-4 text-green-800">
          <div className="flex items-center gap-2">
            <CheckCircle2 className="h-4 w-4" />
            <span>{success}</span>
          </div>
        </div>
      )}

      <div className="grid grid-cols-12 gap-4">
        {/* Left Sidebar - Database & Tables */}
        <div className="col-span-3">
          <SimpleCard title="Database" icon={<Database className="h-4 w-4" />}>
            <select
              value={selectedDB}
              onChange={(e) => setSelectedDB(e.target.value)}
              className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
            >
              <option value="">Select database</option>
              {databases.map((db) => (
                <option key={db.name} value={db.name}>
                  {db.name}
                </option>
              ))}
            </select>

            <div className="mt-4 space-y-1 max-h-96 overflow-y-auto">
              {tables.map((table) => (
                <div key={table.name}>
                  <button
                    onClick={() => toggleTableExpand(table.name)}
                    className={`w-full flex items-center gap-2 px-2 py-1.5 rounded text-sm hover:bg-accent transition-colors text-left ${
                      selectedTable === table.name ? 'bg-accent' : ''
                    }`}
                  >
                    {expandedTables.has(table.name) ? (
                      <ChevronDown className="h-3 w-3" />
                    ) : (
                      <ChevronRight className="h-3 w-3" />
                    )}
                    <Table2 className="h-4 w-4 text-blue-500" />
                    <span className="flex-1 truncate">{table.name}</span>
                    <span className="text-xs text-muted-foreground">{table.rows}</span>
                  </button>
                  {expandedTables.has(table.name) && (
                    <div className="ml-6 mt-1 space-y-1">
                      <button
                        onClick={() => selectTableForQuery(table.name)}
                        className="w-full flex items-center gap-2 px-2 py-1 rounded text-xs hover:bg-accent transition-colors text-left text-muted-foreground"
                      >
                        <Search className="h-3 w-3" />
                        SELECT *
                      </button>
                    </div>
                  )}
                </div>
              ))}
            </div>
          </SimpleCard>
        </div>

        {/* Main Content - Query Editor & Results */}
        <div className="col-span-9 space-y-4">
          {/* Query Editor */}
          <SimpleCard title="SQL Query" icon={<Play className="h-4 w-4" />}>
            <textarea
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Enter SQL query..."
              className="w-full h-24 p-3 font-mono text-sm border border-border rounded-md resize-none focus:outline-none focus:ring-2 focus:ring-ring bg-background text-foreground"
              spellCheck={false}
            />
            <div className="flex justify-end mt-3">
              <button
                onClick={executeQuery}
                disabled={!selectedDB || !query.trim() || loading}
                className="inline-flex items-center gap-2 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground transition hover:bg-primary/90 disabled:opacity-50"
              >
                {loading ? (
                  <Loader2 className="h-4 w-4 animate-spin" />
                ) : (
                  <Play className="h-4 w-4" />
                )}
                Execute Query
              </button>
            </div>
          </SimpleCard>

          {/* Results Table */}
          {queryResult && (
            <SimpleCard title={`Results (${queryResult.count} rows)`} icon={<Table2 className="h-4 w-4" />}>
              <div className="overflow-auto max-h-96">
                <table className="w-full text-sm">
                  <thead className="bg-muted sticky top-0">
                    <tr>
                      {queryResult.columns.map((col, i) => (
                        <th
                          key={i}
                          className="px-3 py-2 text-left font-medium text-muted-foreground border-b"
                        >
                          {col}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {queryResult.rows.map((row, rowIdx) => (
                      <tr
                        key={rowIdx}
                        className="border-b hover:bg-accent/50 transition-colors"
                      >
                        {row.map((cell: any, cellIdx: number) => (
                          <td
                            key={cellIdx}
                            className="px-3 py-2 font-mono text-xs truncate max-w-xs"
                            title={String(cell)}
                          >
                            {cell === null ? (
                              <span className="text-muted-foreground italic">NULL</span>
                            ) : (
                              String(cell)
                            )}
                          </td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </SimpleCard>
          )}

          {/* Table Structure */}
          {selectedTable && columns.length > 0 && !queryResult && (
            <SimpleCard title={`Table Structure: ${selectedTable}`} icon={<Columns className="h-4 w-4" />}>
              <div className="overflow-auto max-h-64">
                <table className="w-full text-sm">
                  <thead className="bg-muted">
                    <tr>
                      <th className="px-3 py-2 text-left font-medium">Column</th>
                      <th className="px-3 py-2 text-left font-medium">Type</th>
                      <th className="px-3 py-2 text-left font-medium">Nullable</th>
                      <th className="px-3 py-2 text-left font-medium">Key</th>
                      <th className="px-3 py-2 text-left font-medium">Default</th>
                      <th className="px-3 py-2 text-left font-medium">Extra</th>
                    </tr>
                  </thead>
                  <tbody>
                    {columns.map((col, idx) => (
                      <tr key={idx} className="border-b hover:bg-accent/50">
                        <td className="px-3 py-2 font-medium">{col.name}</td>
                        <td className="px-3 py-2 text-muted-foreground">{col.type}</td>
                        <td className="px-3 py-2">
                          <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${
                            col.nullable === 'YES' ? 'bg-secondary text-secondary-foreground' : 'bg-primary text-primary-foreground'
                          }`}>
                            {col.nullable}
                          </span>
                        </td>
                        <td className="px-3 py-2">
                          {col.key && (
                            <span className="inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium text-yellow-600 border-yellow-200">
                              {col.key}
                            </span>
                          )}
                        </td>
                        <td className="px-3 py-2 text-muted-foreground">
                          {col.default || '-'}
                        </td>
                        <td className="px-3 py-2 text-muted-foreground">
                          {col.extra || '-'}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </SimpleCard>
          )}
        </div>
      </div>
    </div>
  );
}
