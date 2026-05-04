import { useState, useEffect } from 'react';
import { Link as RouterLink } from 'react-router-dom';
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
  Plus,
  Play,
  Square,
  Trash2,
  Terminal as TerminalIcon,
  PackageCheck,
} from 'lucide-react';
import {
  fetchCloudflareStatus,
  connectCloudflare,
  disconnectCloudflare,
  purgeCloudflareCache,
  fetchCloudflareZones,
  importCloudflareZone,
  fetchCloudflareTunnels,
  createCloudflareTunnel,
  deleteCloudflareTunnel,
  startCloudflareTunnel,
  stopCloudflareTunnel,
  fetchCloudflareTunnelLogs,
  installCloudflared,
  type CloudflareStatus,
  type CloudflareZone,
  type CloudflareTunnel,
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

  // Tunnel state
  const [tunnels, setTunnels] = useState<CloudflareTunnel[]>([]);
  const [showTunnelForm, setShowTunnelForm] = useState(false);
  const [newTunnel, setNewTunnel] = useState({ name: '', hostname: '', local_target: 'http://localhost:8080' });
  const [tunnelBusy, setTunnelBusy] = useState<string>(''); // tunnel id currently busy
  const [installBusy, setInstallBusy] = useState(false);
  const [tunnelLogs, setTunnelLogs] = useState<{ id: string; text: string } | null>(null);

  useEffect(() => {
    loadData();
    // Refresh tunnel status every 5s when connected — cheap, only updates running flag.
    const id = window.setInterval(async () => {
      try {
        const s = await fetchCloudflareStatus();
        setStatus(s);
        if (s?.connected) {
          const t = await fetchCloudflareTunnels();
          setTunnels(t);
        }
      } catch { /* swallow background errors */ }
    }, 5000);
    return () => window.clearInterval(id);
  }, []);

  const loadData = async () => {
    try {
      setLoading(true);
      const s = await fetchCloudflareStatus();
      setStatus(s);
      if (s?.connected) {
        const [z, t] = await Promise.all([fetchCloudflareZones(), fetchCloudflareTunnels()]);
        setZones(z);
        setTunnels(t);
      } else {
        setZones([]);
        setTunnels([]);
      }
      setError('');
    } catch (err: any) {
      setError(err.message || 'Failed to load Cloudflare data');
    } finally {
      setLoading(false);
    }
  };

  const handleInstallCloudflared = async () => {
    setInstallBusy(true);
    setError('');
    setSuccess('');
    try {
      const info = await installCloudflared();
      if (info.installed) {
        setSuccess(`cloudflared installed${info.version ? ` (${info.version})` : ''}`);
      } else {
        setError('install completed but binary not detected');
      }
      await loadData();
    } catch (err: any) {
      setError(err.message || 'install failed');
    } finally {
      setInstallBusy(false);
    }
  };

  const handleCreateTunnel = async () => {
    if (!newTunnel.name || !newTunnel.hostname || !newTunnel.local_target) {
      setError('name, hostname and local target are required');
      return;
    }
    setTunnelBusy('__create__');
    setError('');
    setSuccess('');
    try {
      await createCloudflareTunnel(newTunnel.name, newTunnel.hostname, newTunnel.local_target);
      setSuccess(`Tunnel "${newTunnel.name}" created. Click Start to bring it up.`);
      setShowTunnelForm(false);
      setNewTunnel({ name: '', hostname: '', local_target: 'http://localhost:8080' });
      await loadData();
    } catch (err: any) {
      setError(err.message || 'create failed');
    } finally {
      setTunnelBusy('');
    }
  };

  const handleStartTunnel = async (id: string) => {
    setTunnelBusy(id);
    setError('');
    setSuccess('');
    try {
      await startCloudflareTunnel(id);
      setSuccess('Tunnel starting…');
      await loadData();
    } catch (err: any) {
      setError(err.message || 'start failed');
    } finally {
      setTunnelBusy('');
    }
  };

  const handleStopTunnel = async (id: string) => {
    setTunnelBusy(id);
    setError('');
    setSuccess('');
    try {
      await stopCloudflareTunnel(id);
      setSuccess('Tunnel stopped');
      await loadData();
    } catch (err: any) {
      setError(err.message || 'stop failed');
    } finally {
      setTunnelBusy('');
    }
  };

  const handleDeleteTunnel = async (id: string, name: string) => {
    if (!confirm(`Delete tunnel "${name}"? This removes it from Cloudflare and deletes the DNS record.`)) return;
    setTunnelBusy(id);
    setError('');
    setSuccess('');
    try {
      await deleteCloudflareTunnel(id);
      setSuccess(`Tunnel "${name}" deleted`);
      await loadData();
    } catch (err: any) {
      setError(err.message || 'delete failed');
    } finally {
      setTunnelBusy('');
    }
  };

  const handleViewLogs = async (id: string) => {
    try {
      const r = await fetchCloudflareTunnelLogs(id);
      setTunnelLogs({ id, text: r.logs || '(no log output yet)' });
    } catch (err: any) {
      setError(err.message || 'log fetch failed');
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
          <p><strong>Tunnels</strong> — exposes a local service (e.g. <code>http://localhost:8080</code>) to the public internet via Cloudflare's edge, without opening any inbound port on this server. UWAS creates the tunnel via the Cloudflare API, sets up the proxied DNS CNAME, fetches the connector token, and runs <code>cloudflared</code> with auto-restart on crash.</p>
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

          {/* Tunnels Section */}
          <div className="rounded-lg border border-border bg-card">
            <div className="flex items-center justify-between p-4 border-b border-border">
              <h2 className="text-lg font-semibold flex items-center gap-2">
                <Server className="h-5 w-5" />
                Tunnels
                {status?.cloudflared_installed && status?.cloudflared_version && (
                  <span className="text-xs font-normal text-muted-foreground">
                    cloudflared {status.cloudflared_version}
                  </span>
                )}
              </h2>
              <div className="flex items-center gap-2">
                {!status?.cloudflared_installed && (
                  <button
                    onClick={handleInstallCloudflared}
                    disabled={installBusy}
                    className="inline-flex items-center gap-1.5 rounded-md border border-border bg-card px-3 py-1.5 text-sm font-medium text-card-foreground transition hover:bg-accent disabled:opacity-50"
                  >
                    <PackageCheck className={`h-4 w-4 ${installBusy ? 'animate-pulse' : ''}`} />
                    {installBusy ? 'Installing…' : 'Install cloudflared'}
                  </button>
                )}
                <button
                  onClick={() => setShowTunnelForm(!showTunnelForm)}
                  disabled={!status?.cloudflared_installed}
                  className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground transition hover:bg-primary/90 disabled:opacity-50 disabled:cursor-not-allowed"
                  title={!status?.cloudflared_installed ? 'Install cloudflared first' : ''}
                >
                  <Plus className="h-4 w-4" />
                  New Tunnel
                </button>
              </div>
            </div>

            {!status?.cloudflared_installed && (
              <div className="border-b border-border bg-amber-50 p-4 text-sm text-amber-900 dark:bg-amber-950/30 dark:text-amber-200">
                The <code>cloudflared</code> binary is not installed on this server. Install it via the button above (Linux only — apt-based distros).
                Manual install instructions:{' '}
                <a
                  href="https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="underline"
                >
                  cloudflare.com docs
                  <ExternalLink className="h-3 w-3 inline ml-0.5" />
                </a>.
              </div>
            )}

            {showTunnelForm && (
              <div className="p-4 border-b border-border bg-muted/40 space-y-3">
                <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
                  <div>
                    <label className="block text-xs font-medium text-foreground mb-1">Tunnel name</label>
                    <input
                      type="text"
                      value={newTunnel.name}
                      onChange={(e) => setNewTunnel({ ...newTunnel, name: e.target.value })}
                      placeholder="my-app-tunnel"
                      className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                    />
                  </div>
                  <div>
                    <label className="block text-xs font-medium text-foreground mb-1">Public hostname</label>
                    <input
                      type="text"
                      value={newTunnel.hostname}
                      onChange={(e) => setNewTunnel({ ...newTunnel, hostname: e.target.value.toLowerCase() })}
                      placeholder="app.example.com"
                      className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                    />
                    <p className="text-[10px] text-muted-foreground mt-1">Must be in a connected Cloudflare zone.</p>
                  </div>
                  <div>
                    <label className="block text-xs font-medium text-foreground mb-1">Local target</label>
                    <input
                      type="text"
                      value={newTunnel.local_target}
                      onChange={(e) => setNewTunnel({ ...newTunnel, local_target: e.target.value })}
                      placeholder="http://localhost:8080"
                      className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm font-mono text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                    />
                    <p className="text-[10px] text-muted-foreground mt-1">e.g. http://localhost:8080, tcp://localhost:22, ssh://localhost:22</p>
                  </div>
                </div>
                <div className="flex justify-end gap-2">
                  <button
                    onClick={() => setShowTunnelForm(false)}
                    className="rounded-md border border-border bg-card px-3 py-1.5 text-sm font-medium text-card-foreground transition hover:bg-accent"
                  >
                    Cancel
                  </button>
                  <button
                    onClick={handleCreateTunnel}
                    disabled={tunnelBusy === '__create__'}
                    className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground transition hover:bg-primary/90 disabled:opacity-50"
                  >
                    <Plus className="h-4 w-4" />
                    {tunnelBusy === '__create__' ? 'Creating…' : 'Create Tunnel'}
                  </button>
                </div>
              </div>
            )}

            <div className="divide-y divide-border">
              {tunnels.length === 0 ? (
                <div className="p-8 text-center text-sm text-muted-foreground">
                  No tunnels configured. Create one to expose a local service via Cloudflare's edge.
                </div>
              ) : (
                tunnels.map((t) => (
                  <div key={t.id} className="p-4">
                    <div className="flex items-center justify-between gap-3">
                      <div className="flex items-center gap-3 min-w-0">
                        <div
                          className={`w-2 h-2 rounded-full shrink-0 ${t.running ? 'bg-green-500 animate-pulse' : 'bg-gray-400'}`}
                          title={t.running ? 'Running' : 'Stopped'}
                        />
                        <div className="min-w-0">
                          <p className="font-medium text-foreground truncate">
                            {t.name}
                            {' '}
                            <span className="text-xs font-normal text-muted-foreground">{t.hostname}</span>
                          </p>
                          <p className="text-xs text-muted-foreground font-mono truncate">
                            → {t.local_target}
                            {t.running && t.uptime && (
                              <span className="ml-2 text-green-600">up {t.uptime} (pid {t.pid})</span>
                            )}
                          </p>
                        </div>
                      </div>
                      <div className="flex items-center gap-1 shrink-0">
                        {t.running ? (
                          <button
                            onClick={() => handleStopTunnel(t.id)}
                            disabled={tunnelBusy === t.id}
                            className="p-2 rounded-md bg-red-50 text-red-600 hover:bg-red-100 transition disabled:opacity-50"
                            title="Stop"
                          >
                            <Square className="h-4 w-4" />
                          </button>
                        ) : (
                          <button
                            onClick={() => handleStartTunnel(t.id)}
                            disabled={tunnelBusy === t.id || !status?.cloudflared_installed}
                            className="p-2 rounded-md bg-green-50 text-green-600 hover:bg-green-100 transition disabled:opacity-50"
                            title={!status?.cloudflared_installed ? 'Install cloudflared first' : 'Start'}
                          >
                            <Play className="h-4 w-4" />
                          </button>
                        )}
                        <button
                          onClick={() => handleViewLogs(t.id)}
                          className="p-2 rounded-md hover:bg-accent transition"
                          title="View recent logs"
                        >
                          <TerminalIcon className="h-4 w-4" />
                        </button>
                        <a
                          href={`https://${t.hostname}`}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="p-2 rounded-md hover:bg-accent transition"
                          title="Open public URL"
                        >
                          <ExternalLink className="h-4 w-4" />
                        </a>
                        <button
                          onClick={() => handleDeleteTunnel(t.id, t.name)}
                          disabled={tunnelBusy === t.id}
                          className="p-2 rounded-md text-red-600 hover:bg-red-50 transition disabled:opacity-50"
                          title="Delete tunnel"
                        >
                          <Trash2 className="h-4 w-4" />
                        </button>
                      </div>
                    </div>

                    {tunnelLogs?.id === t.id && (
                      <div className="mt-3 rounded-md border border-border bg-black/90 p-3">
                        <div className="flex items-center justify-between mb-2">
                          <span className="text-xs text-gray-400">cloudflared logs (last 64 lines)</span>
                          <button
                            onClick={() => setTunnelLogs(null)}
                            className="text-xs text-gray-400 hover:text-white"
                          >
                            close
                          </button>
                        </div>
                        <pre className="text-[11px] font-mono text-gray-200 whitespace-pre-wrap max-h-64 overflow-auto">
                          {tunnelLogs.text}
                        </pre>
                      </div>
                    )}
                  </div>
                ))
              )}
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
                        <RouterLink
                          to={`/dns?domain=${encodeURIComponent(zone.name)}`}
                          className="inline-flex items-center gap-1.5 rounded-md border border-border bg-card px-3 py-1.5 text-sm font-medium text-card-foreground transition hover:bg-accent"
                        >
                          <SettingsIcon className="h-4 w-4" />
                          Manage DNS
                        </RouterLink>
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
