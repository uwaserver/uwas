import { useState, useEffect } from 'react';
import {
  Cloud,
  Link,
  RefreshCw,
  CheckCircle,
  XCircle,
  Globe,
  Server,
  Key,
  ExternalLink,
  AlertTriangle,
  Download,
  Settings as SettingsIcon,
  Info,
} from 'lucide-react';
import {
  fetchCloudflareStatus,
  connectCloudflare,
  disconnectCloudflare,
  purgeCloudflareCache,
  fetchCloudflareZones,
  importCloudflareZone,
  type CloudflareStatus,
  type CloudflareZone,
} from '@/lib/api';
import Card from '@/components/Card';

export default function Cloudflare() {
  const [status, setStatus] = useState<CloudflareStatus | null>(null);
  const [zones, setZones] = useState<CloudflareZone[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');
  const [showHelp, setShowHelp] = useState(false);

  // Connect form
  const [apiToken, setApiToken] = useState('');
  const [accountId, setAccountId] = useState('');

  // Purge form
  const [purgeUrl, setPurgeUrl] = useState('');
  const [purgeEverything, setPurgeEverything] = useState(false);

  // Per-zone import form
  const [importZoneId, setImportZoneId] = useState<string | null>(null);
  const [importType, setImportType] = useState<'static' | 'php' | 'proxy' | 'redirect'>('static');
  const [importRoot, setImportRoot] = useState('/var/www/{host}/public_html');
  const [importLoading, setImportLoading] = useState(false);

  useEffect(() => {
    loadData();
  }, []);

  const loadData = async () => {
    try {
      setLoading(true);
      const s = await fetchCloudflareStatus();
      setStatus(s);
      if (s?.connected) {
        const z = await fetchCloudflareZones();
        setZones(z);
      } else {
        setZones([]);
      }
      setError('');
    } catch (err: any) {
      setError(err.message || 'Failed to load Cloudflare data');
    } finally {
      setLoading(false);
    }
  };

  const handleImportZone = async (zoneId: string) => {
    setImportLoading(true);
    setError('');
    setSuccess('');
    try {
      const result = await importCloudflareZone(zoneId, importType, importRoot);
      const parts = [`Added ${result.added.length}`];
      if (result.skipped.length) parts.push(`skipped ${result.skipped.length} (already exists)`);
      parts.push(`out of ${result.total} hostname${result.total === 1 ? '' : 's'}`);
      setSuccess(parts.join(', '));
      setImportZoneId(null);
    } catch (err: any) {
      setError(err.message || 'Import failed');
    } finally {
      setImportLoading(false);
    }
  };

  const handleConnect = async () => {
    if (!apiToken || !accountId) {
      setError('API Token and Account ID are required');
      return;
    }
    try {
      setLoading(true);
      await connectCloudflare(apiToken, accountId);
      setSuccess('Connected to Cloudflare successfully');
      setApiToken('');
      setAccountId('');
      await loadData();
    } catch (err: any) {
      setError(err.message || 'Failed to connect');
    } finally {
      setLoading(false);
    }
  };

  const handleDisconnect = async () => {
    try {
      setLoading(true);
      await disconnectCloudflare();
      setSuccess('Disconnected from Cloudflare');
      await loadData();
    } catch (err: any) {
      setError(err.message || 'Failed to disconnect');
    } finally {
      setLoading(false);
    }
  };

  const handlePurgeCache = async () => {
    try {
      setLoading(true);
      await purgeCloudflareCache(purgeUrl, purgeEverything);
      setSuccess(purgeEverything ? 'Entire cache purged' : `Cache purged for ${purgeUrl}`);
      setPurgeUrl('');
      setPurgeEverything(false);
    } catch (err: any) {
      setError(err.message || 'Failed to purge cache');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2 text-foreground">
            <Cloud className="h-6 w-6 text-orange-500" />
            Cloudflare
          </h1>
          <p className="text-muted-foreground mt-1">
            Connect Cloudflare to import zones, purge CDN cache, and (soon) run real tunnels.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => setShowHelp((v) => !v)}
            className="inline-flex items-center gap-2 rounded-md border border-border bg-card px-3 py-2 text-sm font-medium text-card-foreground transition hover:bg-accent"
          >
            <Info className="h-4 w-4" />
            {showHelp ? 'Hide help' : 'What is this?'}
          </button>
          <button
            onClick={loadData}
            disabled={loading}
            className="inline-flex items-center gap-2 rounded-md border border-border bg-card px-4 py-2 text-sm font-medium text-card-foreground transition hover:bg-accent disabled:opacity-50"
          >
            <RefreshCw className={`h-4 w-4 ${loading ? 'animate-spin' : ''}`} />
            Refresh
          </button>
        </div>
      </div>

      {showHelp && (
        <div className="rounded-lg border border-border bg-card p-4 text-sm text-card-foreground space-y-2">
          <p><strong>Connection</strong> — stores your Cloudflare API token and Account ID on the server (mode 0600). The token is masked everywhere in the UI; only the last 4 chars are shown.</p>
          <p><strong>CDN Cache Purge</strong> — clears Cloudflare's edge cache for a specific URL or the entire account. Useful after deploys.</p>
          <p><strong>Zones</strong> — domains in your Cloudflare account. Use <em>Import to UWAS</em> to add a zone's hostnames as UWAS sites in one click, or <em>Manage DNS</em> to jump to the DNS editor.</p>
          <p><strong>Tunnels</strong> — coming in v0.2.0. Will install <code>cloudflared</code>, create real tunnels via the Cloudflare API, and keep them running with auto-restart.</p>
        </div>
      )}

      {/* Alerts */}
      {error && (
        <div className="rounded-md border border-red-200 bg-red-50 p-4 text-red-800">
          <div className="flex items-center gap-2">
            <AlertTriangle className="h-4 w-4" />
            <span>{error}</span>
          </div>
        </div>
      )}
      {success && (
        <div className="rounded-md border border-green-200 bg-green-50 p-4 text-green-800">
          <div className="flex items-center gap-2">
            <CheckCircle className="h-4 w-4" />
            <span>{success}</span>
          </div>
        </div>
      )}

      {/* Connection Status */}
      <Card
        icon={<Cloud className="h-4 w-4" />}
        label="Connection"
        value={status?.connected ? 'Connected' : 'Not Connected'}
        sub={status?.email || ''}
      />

      {!status?.connected ? (
        /* Connect Form */
        <div className="rounded-lg border border-border bg-card p-6">
          <h2 className="text-lg font-semibold mb-4 flex items-center gap-2">
            <Key className="h-5 w-5" />
            Connect to Cloudflare
          </h2>
          <div className="space-y-4">
            <div>
              <label className="block text-sm font-medium text-foreground mb-1">
                API Token
              </label>
              <input
                type="password"
                value={apiToken}
                onChange={(e) => setApiToken(e.target.value)}
                placeholder="Cloudflare API Token"
                className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
              />
              <p className="text-xs text-muted-foreground mt-1">
                Create a token with Zone:Read and Account:Read permissions at{' '}
                <a
                  href="https://dash.cloudflare.com/profile/api-tokens"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-blue-500 hover:underline"
                >
                  dash.cloudflare.com
                  <ExternalLink className="h-3 w-3 inline ml-1" />
                </a>
              </p>
            </div>
            <div>
              <label className="block text-sm font-medium text-foreground mb-1">
                Account ID
              </label>
              <input
                type="text"
                value={accountId}
                onChange={(e) => setAccountId(e.target.value)}
                placeholder="Cloudflare Account ID"
                className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
              />
            </div>
            <button
              onClick={handleConnect}
              disabled={loading || !apiToken || !accountId}
              className="inline-flex items-center gap-2 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground transition hover:bg-primary/90 disabled:opacity-50"
            >
              <Link className="h-4 w-4" />
              Connect
            </button>
          </div>
        </div>
      ) : (
        <>
          {/* Disconnect Button */}
          <div className="flex justify-end">
            <button
              onClick={handleDisconnect}
              disabled={loading}
              className="inline-flex items-center gap-2 rounded-md border border-red-200 bg-red-50 px-4 py-2 text-sm font-medium text-red-700 transition hover:bg-red-100 disabled:opacity-50"
            >
              <XCircle className="h-4 w-4" />
              Disconnect
            </button>
          </div>

          {/* Tunnels Section — Coming Soon */}
          <div className="rounded-lg border border-amber-300 bg-amber-50 p-4 dark:border-amber-700 dark:bg-amber-950/30">
            <div className="flex items-start gap-3">
              <Server className="h-5 w-5 text-amber-600 dark:text-amber-400 mt-0.5 shrink-0" />
              <div className="flex-1 min-w-0">
                <h3 className="font-semibold text-amber-900 dark:text-amber-200">
                  Cloudflare Tunnel — coming in v0.2.0
                </h3>
                <p className="text-sm text-amber-800 dark:text-amber-300/90 mt-1">
                  Real <code className="px-1 rounded bg-amber-100 dark:bg-amber-900/40">cloudflared</code> integration is in development.
                  The server will install the binary, create real tunnels via the Cloudflare API, manage DNS automatically,
                  and run the connector with auto-restart on crash.
                </p>
                <p className="text-xs text-amber-700 dark:text-amber-400 mt-2">
                  Until then, use the official cloudflared CLI on the server, or set up a tunnel directly at
                  {' '}
                  <a
                    href="https://one.dash.cloudflare.com/"
                    target="_blank"
                    rel="noopener noreferrer"
                    className="underline"
                  >
                    one.dash.cloudflare.com
                    <ExternalLink className="h-3 w-3 inline ml-0.5" />
                  </a>.
                </p>
              </div>
            </div>
          </div>

          {/* Cache Purge Section */}
          <div className="rounded-lg border border-border bg-card p-4">
            <h2 className="text-lg font-semibold mb-4 flex items-center gap-2">
              <RefreshCw className="h-5 w-5" />
              CDN Cache Purge
            </h2>
            <div className="space-y-4">
              <div>
                <label className="flex items-center gap-2">
                  <input
                    type="checkbox"
                    checked={purgeEverything}
                    onChange={(e) => setPurgeEverything(e.target.checked)}
                    className="rounded border-border"
                  />
                  <span className="text-sm text-foreground">Purge everything</span>
                </label>
              </div>
              {!purgeEverything && (
                <div>
                  <label className="block text-sm font-medium text-foreground mb-1">
                    URL to purge
                  </label>
                  <input
                    type="text"
                    value={purgeUrl}
                    onChange={(e) => setPurgeUrl(e.target.value)}
                    placeholder="https://example.com/path"
                    className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                  />
                </div>
              )}
              <button
                onClick={handlePurgeCache}
                disabled={loading || (!purgeEverything && !purgeUrl)}
                className="inline-flex items-center gap-2 rounded-md bg-orange-500 px-4 py-2 text-sm font-medium text-white transition hover:bg-orange-600 disabled:opacity-50"
              >
                <RefreshCw className="h-4 w-4" />
                Purge Cache
              </button>
            </div>
          </div>

          {/* Zones Section */}
          <div className="rounded-lg border border-border bg-card">
            <div className="p-4 border-b border-border">
              <h2 className="text-lg font-semibold flex items-center gap-2">
                <Globe className="h-5 w-5" />
                Zones
              </h2>
              <p className="text-xs text-muted-foreground mt-1">
                Domains in your Cloudflare account. Use <strong>Import to UWAS</strong> to add hostnames
                from a zone's DNS records as UWAS sites, or <strong>Manage DNS</strong> to edit records via the DNS page.
              </p>
            </div>
            <div className="divide-y divide-border">
              {zones.length === 0 ? (
                <div className="p-8 text-center text-muted-foreground">
                  No zones found in this account
                </div>
              ) : (
                zones.map((zone) => (
                  <div key={zone.id} className="p-4">
                    <div className="flex items-center justify-between gap-3">
                      <div className="min-w-0">
                        <p className="font-medium text-foreground truncate">{zone.name}</p>
                        <p className="text-xs text-muted-foreground">
                          <span className="capitalize">{zone.status}</span>
                          {zone.plan ? ` · ${zone.plan}` : ''}
                        </p>
                      </div>
                      <div className="flex items-center gap-2 shrink-0">
                        <a
                          href={`/dns?domain=${encodeURIComponent(zone.name)}`}
                          className="inline-flex items-center gap-1.5 rounded-md border border-border bg-card px-3 py-1.5 text-sm font-medium text-card-foreground transition hover:bg-accent"
                        >
                          <SettingsIcon className="h-4 w-4" />
                          Manage DNS
                        </a>
                        <button
                          onClick={() => {
                            setImportZoneId(importZoneId === zone.id ? null : zone.id);
                            setError('');
                            setSuccess('');
                          }}
                          disabled={loading}
                          className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground transition hover:bg-primary/90 disabled:opacity-50"
                        >
                          <Download className="h-4 w-4" />
                          Import to UWAS
                        </button>
                      </div>
                    </div>

                    {importZoneId === zone.id && (
                      <div className="mt-3 rounded-md border border-border bg-muted/40 p-3">
                        <p className="text-xs text-muted-foreground mb-3">
                          Pulls A / AAAA / CNAME records from <strong>{zone.name}</strong> and creates
                          a UWAS domain for every hostname not already configured.
                        </p>
                        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                          <div>
                            <label className="block text-xs font-medium text-foreground mb-1">
                              Default site type
                            </label>
                            <select
                              value={importType}
                              onChange={(e) => setImportType(e.target.value as 'static' | 'php' | 'proxy' | 'redirect')}
                              className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                            >
                              <option value="static">Static</option>
                              <option value="php">PHP</option>
                              <option value="proxy">Proxy</option>
                              <option value="redirect">Redirect</option>
                            </select>
                          </div>
                          <div>
                            <label className="block text-xs font-medium text-foreground mb-1">
                              Default web root
                            </label>
                            <input
                              type="text"
                              value={importRoot}
                              onChange={(e) => setImportRoot(e.target.value)}
                              placeholder="/var/www/{host}/public_html"
                              className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm font-mono text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                            />
                            <p className="text-[10px] text-muted-foreground mt-1">
                              <code>{'{host}'}</code> is replaced per imported hostname.
                            </p>
                          </div>
                        </div>
                        <div className="flex justify-end gap-2 mt-3">
                          <button
                            onClick={() => setImportZoneId(null)}
                            className="rounded-md border border-border bg-card px-3 py-1.5 text-sm font-medium text-card-foreground transition hover:bg-accent"
                          >
                            Cancel
                          </button>
                          <button
                            onClick={() => handleImportZone(zone.id)}
                            disabled={importLoading}
                            className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground transition hover:bg-primary/90 disabled:opacity-50"
                          >
                            <Download className={`h-4 w-4 ${importLoading ? 'animate-pulse' : ''}`} />
                            {importLoading ? 'Importing…' : 'Import zone'}
                          </button>
                        </div>
                      </div>
                    )}
                  </div>
                ))
              )}
            </div>
          </div>
        </>
      )}
    </div>
  );
}
