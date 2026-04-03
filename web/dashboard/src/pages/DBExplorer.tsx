import React, { useState, useEffect, useCallback } from 'react';
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
  queryDB,
  type DBInfo,
  type DBTable,
  type DBColumn,
} from '@/lib/api';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Alert, AlertDescription } from '@/components/ui/alert';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';

interface QueryResult {
  columns: string[];
  rows: any[][];
  count: number;
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
      const result = await queryDB(selectedDB, query);
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
          <h1 className="text-2xl font-bold flex items-center gap-2">
            <Database className="h-6 w-6" />
            Database Explorer
          </h1>
          <p className="text-muted-foreground mt-1">
            Browse tables, execute SQL queries, and explore your database structure
          </p>
        </div>
        <Button variant="outline" onClick={loadDatabases} disabled={loading}>
          <RefreshCw className={`h-4 w-4 mr-2 ${loading ? 'animate-spin' : ''}`} />
          Refresh
        </Button>
      </div>

      {/* Alerts */}
      {error && (
        <Alert variant="destructive">
          <AlertCircle className="h-4 w-4" />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {success && (
        <Alert className="bg-green-50 border-green-200">
          <CheckCircle2 className="h-4 w-4 text-green-600" />
          <AlertDescription className="text-green-800">{success}</AlertDescription>
        </Alert>
      )}

      <div className="grid grid-cols-12 gap-4">
        {/* Left Sidebar - Database & Tables */}
        <Card className="col-span-3 h-[calc(100vh-280px)]">
          <CardHeader className="pb-3">
            <CardTitle className="text-sm font-medium flex items-center gap-2">
              <Database className="h-4 w-4" />
              Database
            </CardTitle>
            <Select value={selectedDB} onValueChange={setSelectedDB}>
              <SelectTrigger>
                <SelectValue placeholder="Select database" />
              </SelectTrigger>
              <SelectContent>
                {databases.map((db) => (
                  <SelectItem key={db.name} value={db.name}>
                    {db.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </CardHeader>
          <CardContent className="pt-0 overflow-y-auto h-[calc(100%-80px)]">
            <div className="space-y-1">
              {tables.map((table) => (
                <div key={table.name}>
                  <button
                    onClick={() => toggleTableExpand(table.name)}
                    className={`w-full flex items-center gap-2 px-2 py-1.5 rounded text-sm hover:bg-accent transition-colors ${
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
                    <Badge variant="secondary" className="text-xs">
                      {table.rows}
                    </Badge>
                  </button>
                  {expandedTables.has(table.name) && (
                    <div className="ml-6 mt-1 space-y-1">
                      <button
                        onClick={() => selectTableForQuery(table.name)}
                        className="w-full flex items-center gap-2 px-2 py-1 rounded text-xs hover:bg-accent transition-colors text-left"
                      >
                        <Search className="h-3 w-3" />
                        SELECT *
                      </button>
                    </div>
                  )}
                </div>
              ))}
            </div>
          </CardContent>
        </Card>

        {/* Main Content - Query Editor & Results */}
        <div className="col-span-9 space-y-4">
          {/* Query Editor */}
          <Card>
            <CardHeader className="pb-3">
              <CardTitle className="text-sm font-medium flex items-center gap-2">
                <Play className="h-4 w-4" />
                SQL Query
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <textarea
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Enter SQL query..."
                className="w-full h-24 p-3 font-mono text-sm border rounded-md resize-none focus:outline-none focus:ring-2 focus:ring-ring"
                spellCheck={false}
              />
              <div className="flex justify-end">
                <Button
                  onClick={executeQuery}
                  disabled={!selectedDB || !query.trim() || loading}
                >
                  {loading ? (
                    <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                  ) : (
                    <Play className="h-4 w-4 mr-2" />
                  )}
                  Execute Query
                </Button>
              </div>
            </CardContent>
          </Card>

          {/* Results Table */}
          {queryResult && (
            <Card className="h-[calc(100vh-480px)]">
              <CardHeader className="pb-3">
                <CardTitle className="text-sm font-medium flex items-center gap-2">
                  <Table2 className="h-4 w-4" />
                  Results
                  <Badge variant="secondary">{queryResult.count} rows</Badge>
                </CardTitle>
              </CardHeader>
              <CardContent className="pt-0 overflow-auto h-[calc(100%-60px)]">
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
              </CardContent>
            </Card>
          )}

          {/* Table Structure */}
          {selectedTable && columns.length > 0 && !queryResult && (
            <Card>
              <CardHeader className="pb-3">
                <CardTitle className="text-sm font-medium flex items-center gap-2">
                  <Columns className="h-4 w-4" />
                  Table Structure: {selectedTable}
                </CardTitle>
              </CardHeader>
              <CardContent className="pt-0 overflow-auto max-h-64">
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
                          <Badge variant={col.nullable === 'YES' ? 'secondary' : 'default'}>
                            {col.nullable}
                          </Badge>
                        </td>
                        <td className="px-3 py-2">
                          {col.key && (
                            <Badge variant="outline" className="text-yellow-600">
                              {col.key}
                            </Badge>
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
              </CardContent>
            </Card>
          )}
        </div>
      </div>
    </div>
  );
}
