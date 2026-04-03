import { useState, useEffect } from 'react';
import {
  Cloud,
  Link,
  RefreshCw,
  Trash2,
  Plus,
  CheckCircle,
  XCircle,
  Globe,
  Server,
  Key,
  Play,
  Square,
  Copy,
  ExternalLink,
  AlertTriangle,
} from 'lucide-react';
import {
  fetchCloudflareStatus,
  connectCloudflare,
  disconnectCloudflare,
  fetchCloudflareTunnels,
  createCloudflareTunnel,
  deleteCloudflareTunnel,
  startCloudflareTunnel,
  stopCloudflareTunnel,
  purgeCloudflareCache,
  fetchCloudflareZones,
  syncCloudflareDNS,
  type CloudflareStatus,
  type CloudflareTunnel,
  type CloudflareZone,
} from '@/lib/api';
import Card from '@/components/Card';

export default function Cloudflare() {
  const [status, setStatus] = useState<CloudflareStatus | null>(null);
  const [tunnels, setTunnels] = useState<CloudflareTunnel[]>([]);
  const [zones, setZones] = useState<CloudflareZone[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');

  // Connect form
  const [apiToken, setApiToken] = useState('');
  const [accountId, setAccountId] = useState('');

  // Tunnel form
  const [newTunnelName, setNewTunnelName] = useState('');
  const [newTunnelDomain, setNewTunnelDomain] = useState('');
  const [showTunnelForm, setShowTunnelForm] = useState(false);

  // Purge form
  const [purgeUrl, setPurgeUrl] = useState('');
  const [purgeEverything, setPurgeEverything] = useState(false);

  useEffect(() => {
    loadData();
  }, []);

  const loadData = async () => {
    try {
      setLoading(true);
      const [s, t, z] = await Promise.all([
        fetchCloudflareStatus(),
        fetchCloudflareTunnels(),
        fetchCloudflareZones(),
      ]);
      setStatus(s);
      setTunnels(t);
      setZones(z);
      setError('');
    } catch (err: any) {
      setError(err.message || 'Failed to load Cloudflare data');
    } finally {
      setLoading(false);
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

  const handleCreateTunnel = async () => {
    if (!newTunnelName || !newTunnelDomain) {
      setError('Tunnel name and domain are required');
      return;
    }
    try {
      setLoading(true);
      await createCloudflareTunnel(newTunnelName, newTunnelDomain);
      setSuccess('Tunnel created successfully');
      setShowTunnelForm(false);
      setNewTunnelName('');
      setNewTunnelDomain('');
      await loadData();
    } catch (err: any) {
      setError(err.message || 'Failed to create tunnel');
    } finally {
      setLoading(false);
    }
  };

  const handleDeleteTunnel = async (tunnelId: string) => {
    if (!confirm('Are you sure you want to delete this tunnel?')) return;
    try {
      setLoading(true);
      await deleteCloudflareTunnel(tunnelId);
      setSuccess('Tunnel deleted successfully');
      await loadData();
    } catch (err: any) {
      setError(err.message || 'Failed to delete tunnel');
    } finally {
      setLoading(false);
    }
  };

  const handleToggleTunnel = async (tunnelId: string, running: boolean) => {
    try {
      setLoading(true);
      if (running) {
        await stopCloudflareTunnel(tunnelId);
      } else {
        await startCloudflareTunnel(tunnelId);
      }
      setSuccess(`Tunnel ${running ? 'stopped' : 'started'} successfully`);
      await loadData();
    } catch (err: any) {
      setError(err.message || 'Failed to toggle tunnel');
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

  const handleSyncDNS = async (zoneId: string) => {
    try {
      setLoading(true);
      await syncCloudflareDNS(zoneId);
      setSuccess('DNS records synced successfully');
      await loadData();
    } catch (err: any) {
      setError(err.message || 'Failed to sync DNS');
    } finally {
      setLoading(false);
    }
  };

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text);
    setSuccess('Copied to clipboard');
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
            Manage Cloudflare tunnels, CDN cache, and DNS
          </p>
        </div>
        <button
          onClick={loadData}
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
              </h2>
              <button
                onClick={() => setShowTunnelForm(!showTunnelForm)}
                className="inline-flex items-center gap-2 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground transition hover:bg-primary/90"
              >
                <Plus className="h-4 w-4" />
                New Tunnel
              </button>
            </div>

            {showTunnelForm && (
              <div className="p-4 border-b border-border bg-muted/50">
                <div className="grid grid-cols-2 gap-4">
                  <div>
                    <label className="block text-sm font-medium text-foreground mb-1">
                      Tunnel Name
                    </label>
                    <input
                      type="text"
                      value={newTunnelName}
                      onChange={(e) => setNewTunnelName(e.target.value)}
                      placeholder="my-tunnel"
                      className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                    />
                  </div>
                  <div>
                    <label className="block text-sm font-medium text-foreground mb-1">
                      Domain
                    </label>
                    <input
                      type="text"
                      value={newTunnelDomain}
                      onChange={(e) => setNewTunnelDomain(e.target.value)}
                      placeholder="example.com"
                      className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                    />
                  </div>
                </div>
                <div className="flex justify-end mt-4">
                  <button
                    onClick={handleCreateTunnel}
                    disabled={loading || !newTunnelName || !newTunnelDomain}
                    className="inline-flex items-center gap-2 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground transition hover:bg-primary/90 disabled:opacity-50"
                  >
                    <Plus className="h-4 w-4" />
                    Create Tunnel
                  </button>
                </div>
              </div>
            )}

            <div className="divide-y divide-border">
              {tunnels.length === 0 ? (
                <div className="p-8 text-center text-muted-foreground">
                  No tunnels configured
                </div>
              ) : (
                tunnels.map((tunnel) => (
                  <div key={tunnel.id} className="p-4 flex items-center justify-between">
                    <div className="flex items-center gap-3">
                      <div className={`w-2 h-2 rounded-full ${tunnel.running ? 'bg-green-500' : 'bg-gray-400'}`} />
                      <div>
                        <p className="font-medium text-foreground">{tunnel.name}</p>
                        <p className="text-xs text-muted-foreground">{tunnel.domain}</p>
                        {tunnel.running && tunnel.connections && (
                          <p className="text-xs text-green-600">{tunnel.connections} connections</p>
                        )}
                      </div>
                    </div>
                    <div className="flex items-center gap-2">
                      <button
                        onClick={() => handleToggleTunnel(tunnel.id, tunnel.running)}
                        disabled={loading}
                        className={`p-2 rounded-md transition ${
                          tunnel.running
                            ? 'bg-red-50 text-red-600 hover:bg-red-100'
                            : 'bg-green-50 text-green-600 hover:bg-green-100'
                        }`}
                      >
                        {tunnel.running ? <Square className="h-4 w-4" /> : <Play className="h-4 w-4" />}
                      </button>
                      <button
                        onClick={() => copyToClipboard(tunnel.token)}
                        className="p-2 rounded-md hover:bg-accent transition"
                        title="Copy tunnel token"
                      >
                        <Copy className="h-4 w-4" />
                      </button>
                      <button
                        onClick={() => handleDeleteTunnel(tunnel.id)}
                        disabled={loading}
                        className="p-2 rounded-md text-red-600 hover:bg-red-50 transition"
                      >
                        <Trash2 className="h-4 w-4" />
                      </button>
                    </div>
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
                Zones & DNS
              </h2>
            </div>
            <div className="divide-y divide-border">
              {zones.length === 0 ? (
                <div className="p-8 text-center text-muted-foreground">
                  No zones found
                </div>
              ) : (
                zones.map((zone) => (
                  <div key={zone.id} className="p-4 flex items-center justify-between">
                    <div>
                      <p className="font-medium text-foreground">{zone.name}</p>
                      <p className="text-xs text-muted-foreground capitalize">{zone.status}</p>
                    </div>
                    <button
                      onClick={() => handleSyncDNS(zone.id)}
                      disabled={loading}
                      className="inline-flex items-center gap-2 rounded-md border border-border bg-card px-3 py-1.5 text-sm font-medium text-card-foreground transition hover:bg-accent disabled:opacity-50"
                    >
                      <RefreshCw className="h-4 w-4" />
                      Sync DNS
                    </button>
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
