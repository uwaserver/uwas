package admin

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	cfadmin "github.com/uwaserver/uwas/internal/admin/cloudflare"
	cfintegration "github.com/uwaserver/uwas/internal/cloudflare"
	"github.com/uwaserver/uwas/internal/config"
)

// cfDeps adapts admin.Server to the cloudflare.Deps interface.
type cfDeps struct {
	s *Server
}

func (d *cfDeps) RequireAdmin(w http.ResponseWriter, r *http.Request) bool {
	return d.s.requireAdmin(w, r)
}

func (d *cfDeps) LogInfo(msg string, args ...any)  { d.s.logger.Info(msg, args...) }
func (d *cfDeps) LogWarn(msg string, args ...any)  { d.s.logger.Warn(msg, args...) }
func (d *cfDeps) LogError(msg string, args ...any) { d.s.logger.Error(msg, args...) }

func (d *cfDeps) RecordAudit(r *http.Request, action, detail string, success bool) {
	d.s.recordAuditR(r, action, detail, success)
}

func (d *cfDeps) CloudflareIPRanges() ([]string, string) {
	d.s.configMu.RLock()
	defer d.s.configMu.RUnlock()
	ranges := append([]string(nil), d.s.config.Global.Cloudflare.IPRanges...)
	return ranges, d.s.config.Global.Cloudflare.LastSynced
}

func (d *cfDeps) SetCloudflareIPRanges(ranges []string, lastSynced string) {
	d.s.configMu.Lock()
	d.s.config.Global.Cloudflare.IPRanges = ranges
	if lastSynced != "" {
		d.s.config.Global.Cloudflare.LastSynced = lastSynced
	}
	d.s.configMu.Unlock()
}

func (d *cfDeps) PersistConfig()      { d.s.persistConfig() }
func (d *cfDeps) NotifyDomainChange() { d.s.notifyDomainChange() }

func (d *cfDeps) LoadCloudflareState() *cfadmin.State {
	cloudflareMu.RLock()
	defer cloudflareMu.RUnlock()
	if cloudflareConfig == nil {
		return nil
	}
	return toCFState(cloudflareConfig)
}

func (d *cfDeps) SaveCloudflareState(st *cfadmin.State) error {
	cloudflareMu.Lock()
	defer cloudflareMu.Unlock()
	if st == nil || (!st.Connected && st.Token == "") {
		cloudflareConfig = nil
	} else {
		cloudflareConfig = toAdminState(st)
	}
	return d.s.saveCloudflareStateLocked()
}

func (d *cfDeps) TunnelStatusOf(id string) (bool, int, string) {
	if d.s.cfRunner == nil {
		return false, 0, ""
	}
	st := d.s.cfRunner.StatusOf(id)
	return st.Running, st.PID, st.Uptime
}

func (d *cfDeps) TunnelStart(id, token string) error {
	if d.s.cfRunner == nil {
		return nil
	}
	return d.s.cfRunner.Start(id, token)
}

func (d *cfDeps) TunnelStop(id string) error {
	if d.s.cfRunner == nil {
		return fmt.Errorf("tunnel runner not initialized")
	}
	return d.s.cfRunner.Stop(id)
}

func (d *cfDeps) TunnelForget(id string) {
	if d.s.cfRunner != nil {
		d.s.cfRunner.Forget(id)
	}
}

func (d *cfDeps) TunnelTail(id string) string {
	if d.s.cfRunner == nil {
		return ""
	}
	return d.s.cfRunner.Tail(id)
}

func (d *cfDeps) AddDomain(dom config.Domain) {
	d.s.configMu.Lock()
	d.s.config.Domains = append(d.s.config.Domains, dom)
	d.s.configMu.Unlock()
}

func (d *cfDeps) ExistingDomains() map[string]bool {
	d.s.configMu.RLock()
	defer d.s.configMu.RUnlock()
	existing := make(map[string]bool, len(d.s.config.Domains))
	for _, d := range d.s.config.Domains {
		existing[strings.ToLower(d.Host)] = true
	}
	return existing
}

func (d *cfDeps) WebRoot() string {
	d.s.configMu.RLock()
	defer d.s.configMu.RUnlock()
	return d.s.config.Global.WebRoot
}

func (d *cfDeps) FetchIPRanges(r *http.Request) ([]string, error) {
	return cfintegration.FetchIPRanges(r.Context())
}

// CF API operations — use admin's cfHTTPClient directly (test-mockable).
func (d *cfDeps) ValidateToken(token, accountID string) (string, error) {
	return cfValidateTokenImpl(token, accountID)
}

func (d *cfDeps) FetchZones(token string) ([]cfadmin.Zone, error) {
	return cfFetchZonesImpl(token)
}

func (d *cfDeps) FetchDNSRecords(token, zoneID string) ([]cfadmin.DNSRecord, error) {
	return cfFetchDNSRecordsImpl(token, zoneID)
}

func (d *cfDeps) PurgeCache(token, url string, everything bool) error {
	return cfPurgeCacheImpl(token, url, everything)
}

func (d *cfDeps) NormalizeCIDRs(ranges []string) ([]string, error) {
	return cfintegration.NormalizeCIDRs(ranges)
}

func (d *cfDeps) CreateTunnelAPI(token, accountID, name, hostname, localTarget string) (cfadmin.Tunnel, error) {
	cli := cfintegration.New(token, accountID)
	zone, err := cli.FindZoneByHostname(hostname)
	if err != nil {
		return cfadmin.Tunnel{}, fmt.Errorf("zone lookup: %w", err)
	}
	cft, err := cli.CreateTunnel(name)
	if err != nil {
		return cfadmin.Tunnel{}, fmt.Errorf("create tunnel: %w", err)
	}
	rules := []cfintegration.IngressRule{
		{Hostname: hostname, Service: localTarget},
		{Service: "http_status:404"},
	}
	if err := cli.PutTunnelConfig(cft.ID, rules); err != nil {
		_ = cli.DeleteTunnel(cft.ID)
		return cfadmin.Tunnel{}, fmt.Errorf("put tunnel config: %w", err)
	}
	recordID, err := cli.CreateTunnelCNAME(zone.ID, hostname, cft.ID)
	if err != nil {
		_ = cli.DeleteTunnel(cft.ID)
		return cfadmin.Tunnel{}, fmt.Errorf("create DNS CNAME: %w", err)
	}
	connectorToken, err := cli.GetTunnelToken(cft.ID)
	if err != nil {
		_ = cli.DeleteDNSRecord(zone.ID, recordID)
		_ = cli.DeleteTunnel(cft.ID)
		return cfadmin.Tunnel{}, fmt.Errorf("get connector token: %w", err)
	}
	return cfadmin.Tunnel{
		ID: cft.ID, Name: name, Hostname: hostname,
		LocalTarget: localTarget, ConnectorToken: connectorToken,
		ZoneID: zone.ID, DNSRecordID: recordID,
	}, nil
}

func (d *cfDeps) DeleteTunnelAPI(token, accountID, tunnelID string) error {
	cli := cfintegration.New(token, accountID)
	return cli.DeleteTunnel(tunnelID)
}

// toCFState converts the admin-internal cloudflareState to the sub-package type.
func toCFState(st *cloudflareState) *cfadmin.State {
	out := &cfadmin.State{
		SchemaVersion: st.SchemaVersion,
		Token:         st.Token,
		AccountID:     st.AccountID,
		Email:         st.Email,
		Connected:     st.Connected,
		UpdatedAt:     st.UpdatedAt,
	}
	out.Tunnels = make([]cfadmin.Tunnel, len(st.Tunnels))
	for i, t := range st.Tunnels {
		out.Tunnels[i] = cfadmin.Tunnel{
			ID:             t.ID,
			Name:           t.Name,
			Hostname:       t.Hostname,
			LocalTarget:    t.LocalTarget,
			ConnectorToken: t.ConnectorToken,
			ZoneID:         t.ZoneID,
			DNSRecordID:    t.DNSRecordID,
			CreatedAt:      t.CreatedAt,
			Domain:         t.Domain,
		}
	}
	return out
}

// (cfHandler is now a Server field)

// toAdminState converts the sub-package state to admin-internal cloudflareState.
func toAdminState(st *cfadmin.State) *cloudflareState {
	out := &cloudflareState{
		SchemaVersion: st.SchemaVersion,
		Token:         st.Token,
		AccountID:     st.AccountID,
		Email:         st.Email,
		Connected:     st.Connected,
		UpdatedAt:     st.UpdatedAt,
	}
	out.Tunnels = make([]cloudflareTunnel, len(st.Tunnels))
	for i, t := range st.Tunnels {
		out.Tunnels[i] = cloudflareTunnel{
			ID: t.ID, Name: t.Name, Hostname: t.Hostname,
			LocalTarget: t.LocalTarget, ConnectorToken: t.ConnectorToken,
			ZoneID: t.ZoneID, DNSRecordID: t.DNSRecordID,
			CreatedAt: t.CreatedAt, Domain: t.Domain,
		}
	}
	return out
}

func (s *Server) initCloudflareHandler() {
	s.cfHandler = cfadmin.New(&cfDeps{s: s})
}

// ── Thin wrappers ──

func (s *Server) handleCloudflareStatus(w http.ResponseWriter, r *http.Request)      { s.cfHandler.Status(w, r) }
func (s *Server) handleCloudflareIPs(w http.ResponseWriter, r *http.Request)         { s.cfHandler.IPs(w, r) }
func (s *Server) handleCloudflareIPsUpdate(w http.ResponseWriter, r *http.Request)   { s.cfHandler.IPsUpdate(w, r) }
func (s *Server) handleCloudflareIPsSync(w http.ResponseWriter, r *http.Request)     { s.cfHandler.IPsSync(w, r) }
func (s *Server) handleCloudflareConnect(w http.ResponseWriter, r *http.Request)     { s.cfHandler.Connect(w, r) }
func (s *Server) handleCloudflareDisconnect(w http.ResponseWriter, r *http.Request)  { s.cfHandler.Disconnect(w, r) }
func (s *Server) handleCloudflareCachePurge(w http.ResponseWriter, r *http.Request)  { s.cfHandler.CachePurge(w, r) }
func (s *Server) handleCloudflareTunnels(w http.ResponseWriter, r *http.Request)     { s.cfHandler.Tunnels(w, r) }
func (s *Server) handleCloudflareTunnelCreate(w http.ResponseWriter, r *http.Request) { s.cfHandler.TunnelCreate(w, r) }
func (s *Server) handleCloudflareTunnelDelete(w http.ResponseWriter, r *http.Request) { s.cfHandler.TunnelDelete(w, r) }
func (s *Server) handleCloudflareTunnelStart(w http.ResponseWriter, r *http.Request)  { s.cfHandler.TunnelStart(w, r) }
func (s *Server) handleCloudflareTunnelStop(w http.ResponseWriter, r *http.Request)   { s.cfHandler.TunnelStop(w, r) }
func (s *Server) handleCloudflareTunnelLogs(w http.ResponseWriter, r *http.Request)   { s.cfHandler.TunnelLogs(w, r) }
func (s *Server) handleCloudflaredInstall(w http.ResponseWriter, r *http.Request)     { s.cfHandler.CloudflaredInstall(w, r) }
func (s *Server) handleCloudflareZones(w http.ResponseWriter, r *http.Request)        { s.cfHandler.Zones(w, r) }
func (s *Server) handleCloudflareZoneImport(w http.ResponseWriter, r *http.Request)   { s.cfHandler.ZoneImport(w, r) }

var _ cfadmin.Deps = (*cfDeps)(nil)

// Keep these types referenced — they're used by cloudflare_state.go and tests.
// Cloudflare state types and package-level vars (shared with cloudflare_state.go).

type cloudflareTunnel struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Hostname       string    `json:"hostname"`
	LocalTarget    string    `json:"local_target"`
	ConnectorToken string    `json:"connector_token"`
	ZoneID         string    `json:"zone_id"`
	DNSRecordID    string    `json:"dns_record_id"`
	CreatedAt      time.Time `json:"created_at,omitempty"`
	Domain         string    `json:"domain,omitempty"`
}

const cloudflareStateSchemaCurrent = 2

type cloudflareState struct {
	SchemaVersion int                `json:"schema_version,omitempty"`
	Token         string             `json:"token"`
	AccountID     string             `json:"account_id"`
	Email         string             `json:"email"`
	Tunnels       []cloudflareTunnel `json:"tunnels"`
	Connected     bool               `json:"connected"`
	UpdatedAt     time.Time          `json:"updated_at"`
}

var (
	cloudflareMu     sync.RWMutex
	cloudflareConfig *cloudflareState
)

// cfHTTPClient bounds outbound Cloudflare API calls.
var cfHTTPClient = &http.Client{Timeout: 30 * time.Second}

// maskCloudflareToken returns the last 4 chars of the token prefixed with stars.
func maskCloudflareToken(token string) string {
	if len(token) <= 4 {
		return "****"
	}
	return "****" + token[len(token)-4:]
}

// validateLocalTarget enforces a small whitelist of cloudflared service URLs.
func validateLocalTarget(target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return fmt.Errorf("local_target is required (e.g. http://localhost:8080)")
	}
	if strings.HasPrefix(target, "http_status:") {
		return nil
	}
	for _, scheme := range []string{"http://", "https://", "tcp://", "ssh://", "rdp://", "unix:"} {
		if strings.HasPrefix(target, scheme) {
			return nil
		}
	}
	return fmt.Errorf("local_target must start with one of: http://, https://, tcp://, ssh://, rdp://, unix:, http_status")
}

// tunnelToView converts a cloudflareTunnel to a tunnelView (for test compat).
func (s *Server) tunnelToView(t cloudflareTunnel) tunnelView {
	view := tunnelView{
		ID: t.ID, Name: t.Name, Hostname: t.Hostname,
		LocalTarget: t.LocalTarget, CreatedAt: t.CreatedAt,
	}
	if s.cfRunner != nil {
		st := s.cfRunner.StatusOf(t.ID)
		view.Running = st.Running
		view.PID = st.PID
		view.Uptime = st.Uptime
	}
	return view
}

// findTunnel looks up a tunnel by ID.
func (s *Server) findTunnel(id string) (cloudflareTunnel, bool) {
	cloudflareMu.RLock()
	defer cloudflareMu.RUnlock()
	if cloudflareConfig == nil {
		return cloudflareTunnel{}, false
	}
	for _, t := range cloudflareConfig.Tunnels {
		if t.ID == id {
			return t, true
		}
	}
	return cloudflareTunnel{}, false
}

// validateCloudflareToken validates a token against the CF API.
func (s *Server) validateCloudflareToken(token, accountID string) (string, error) {
	return cfValidateTokenImpl(token, accountID)
}

// cfValidateTokenImpl uses the admin's cfHTTPClient.
func cfValidateTokenImpl(token, accountID string) (string, error) {
	return cfadmin.ValidateTokenWithClient(cfHTTPClient, token, accountID)
}

// fetchCloudflareZones fetches all zones from the CF API.
func (s *Server) fetchCloudflareZones(token string) ([]cloudflareZone, error) {
	zones, err := cfFetchZonesImpl(token)
	if err != nil { return nil, err }
	out := make([]cloudflareZone, len(zones))
	for i, z := range zones {
		out[i] = cloudflareZone{ID: z.ID, Name: z.Name, Status: z.Status, Plan: z.Plan}
	}
	return out, nil
}

func cfFetchZonesImpl(token string) ([]cfadmin.Zone, error) {
	return cfadmin.FetchZonesWithClient(cfHTTPClient, token)
}

// purgeCloudflareCache purges cache for a URL or everything.
func (s *Server) purgeCloudflareCache(token, url string, everything bool) error {
	return cfPurgeCacheImpl(token, url, everything)
}

func cfPurgeCacheImpl(token, url string, everything bool) error {
	return cfadmin.PurgeCacheWithClient(cfHTTPClient, token, url, everything)
}

// fetchCloudflareDNSRecords fetches DNS records for a zone.
func (s *Server) fetchCloudflareDNSRecords(token, zoneID string) ([]cloudflareDNSRecord, error) {
	records, err := cfFetchDNSRecordsImpl(token, zoneID)
	if err != nil { return nil, err }
	out := make([]cloudflareDNSRecord, len(records))
	for i, r := range records {
		out[i] = cloudflareDNSRecord{ID: r.ID, Type: r.Type, Name: r.Name, Content: r.Content, TTL: r.TTL, Proxied: r.Proxied, Priority: r.Priority}
	}
	return out, nil
}

func cfFetchDNSRecordsImpl(token, zoneID string) ([]cfadmin.DNSRecord, error) {
	return cfadmin.FetchDNSRecordsWithClient(cfHTTPClient, token, zoneID)
}

var (
	_ cloudflareTunnel          = cloudflareTunnel{}
	_ cloudflareState           = cloudflareState{}
	_ *cfintegration.Runner     = (*cfintegration.Runner)(nil)
)
