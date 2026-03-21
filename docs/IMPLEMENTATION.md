# UWAS — Implementation Guide

> This document defines how the design in SPECIFICATION.md will be implemented in Go,
> in what order, with which patterns, and using which stdlib/external packages.

---

## 1. Project Bootstrap

### 1.1 Go Module Setup

```bash
mkdir uwas && cd uwas
go mod init github.com/uwaserver/uwas
```

**Go version**: 1.23+ (generics, slog, improved TLS)

**Build command**:
```bash
# Development
go build -o uwas ./cmd/uwas

# Production (static binary, stripped, versioned)
CGO_ENABLED=0 go build -ldflags="-s -w \
  -X main.Version=$(git describe --tags) \
  -X main.Commit=$(git rev-parse --short HEAD) \
  -X main.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o uwas ./cmd/uwas
```

**Binary size target**: < 25MB (stripped). Go's `net/http` + `crypto/tls` alone is ~10MB, the rest is business logic.

### 1.2 External Dependencies

```bash
# Config parsing
go get gopkg.in/yaml.v3

# Compression (Phase 2)
go get github.com/andybalholm/brotli
go get github.com/klauspost/compress/zstd

# Extended stdlib
go get golang.org/x/crypto
go get golang.org/x/net/http2
```

### 1.3 Core Package Layout

```
cmd/uwas/main.go          → entry point, CLI dispatch
internal/
  config/                  → YAML parse, validation, hot reload
  server/                  → master/worker process, listener
  tls/                     → cert manager, ACME client
  router/                  → vhost lookup, request routing
  rewrite/                 → URL rewrite engine, .htaccess parser
  cache/                   → L1 memory + L2 disk, grace, ESI, purge
  handler/
    static/                → static file serving
    fastcgi/               → FastCGI protocol client + pool
    proxy/                 → reverse proxy, LB, health check
    redirect/              → redirect handler
  middleware/              → chain builder + all middleware impls
  admin/                   → REST API + dashboard
  mcp/                     → MCP server protocol
  logger/                  → structured logger (slog wrapper)
  metrics/                 → prometheus-compatible metrics
pkg/
  htaccess/                → .htaccess parser (public, reusable)
  fastcgi/                 → FastCGI protocol (public, reusable)
```

`internal/` Go convention: cannot be imported from outside.
`pkg/` reusable packages: the htaccess parser and fastcgi client can also be used as standalone projects.

---

## 2. Core Types & Interfaces

### 2.1 Request Context

Context carried throughout each request, read/written by every layer:

```go
// internal/router/context.go

type RequestContext struct {
    // Identity
    ID          string          // UUID v7 (sortable, timestamp-embedded)
    StartTime   time.Time

    // HTTP
    Request     *http.Request
    Response    *ResponseWriter // wrapped http.ResponseWriter

    // Routing
    VHost       *config.Domain  // matched virtual host config
    RemoteIP    string          // real client IP (after X-Forwarded-For)
    RemotePort  string
    ServerPort  string
    IsHTTPS     bool

    // Path Resolution
    OriginalURI string          // URI before rewrite
    RewrittenURI string         // URI after rewrite
    DocumentRoot string
    ResolvedPath string         // full path on the filesystem
    ScriptName   string         // PHP: /index.php
    PathInfo     string         // PHP: /controller/action

    // State
    CacheStatus string          // "HIT", "MISS", "BYPASS", "STALE"
    Upstream    string          // proxy: which backend the request was sent to

    // Metrics
    BytesSent   int64
    Duration    time.Duration
    TTFBDur     time.Duration   // time to first byte
}
```

### 2.2 ResponseWriter Wrapper

```go
// internal/router/response.go

type ResponseWriter struct {
    http.ResponseWriter
    statusCode    int
    bytesWritten  int64
    headerWritten bool
    startTime     time.Time
    ttfb          time.Duration
}

func (w *ResponseWriter) WriteHeader(code int) {
    if w.headerWritten {
        return
    }
    w.statusCode = code
    w.headerWritten = true
    w.ttfb = time.Since(w.startTime)
    w.ResponseWriter.WriteHeader(code)
}

func (w *ResponseWriter) Write(b []byte) (int, error) {
    if !w.headerWritten {
        w.WriteHeader(http.StatusOK)
    }
    n, err := w.ResponseWriter.Write(b)
    w.bytesWritten += int64(n)
    return n, err
}

// Hijack support (for WebSocket proxy)
func (w *ResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
    if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
        return hj.Hijack()
    }
    return nil, nil, fmt.Errorf("hijack not supported")
}

// Flush support (streaming responses)
func (w *ResponseWriter) Flush() {
    if f, ok := w.ResponseWriter.(http.Flusher); ok {
        f.Flush()
    }
}
```

### 2.3 Module Interface

```go
// internal/server/module.go

type Module interface {
    Name() string
    Init(cfg *config.Config) error
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
}

type HealthChecker interface {
    Health() HealthStatus
}

type HealthStatus struct {
    Healthy bool
    Message string
    Details map[string]any
}
```

### 2.4 Middleware Interface

```go
// internal/middleware/middleware.go

// Handler processes a request and writes a response.
type Handler func(ctx *router.RequestContext) error

// Middleware wraps a handler with additional behavior.
type Middleware func(next Handler) Handler

// Chain builds a middleware pipeline.
// Execution order: first middleware wraps outermost.
// chain(A, B, C)(handler) → A(B(C(handler)))
func Chain(middlewares ...Middleware) Middleware {
    return func(next Handler) Handler {
        for i := len(middlewares) - 1; i >= 0; i-- {
            next = middlewares[i](next)
        }
        return next
    }
}
```

Why a custom interface instead of `http.Handler`:
- `RequestContext` carries the state that every middleware needs
- Cross-cutting concerns like cache, rewrite, and logging write to the context
- Centralized error handling: the handler returns an `error`, and a top-level error handler catches it

### 2.5 Handler Interface

```go
// internal/handler/handler.go

// BackendHandler processes requests that pass through the middleware chain.
type BackendHandler interface {
    // CanHandle checks if this handler should process the request.
    // Called after rewrite + try_files resolution.
    CanHandle(ctx *router.RequestContext) bool

    // Handle processes the request and writes the response.
    Handle(ctx *router.RequestContext) error
}
```

Handler selection order:
1. Redirect handler (type: redirect)
2. Static file handler (resolved path is a file)
3. FastCGI handler (resolved path ends with .php or type: php with fallback)
4. Proxy handler (type: proxy)
5. 404

---

## 3. Server Core Implementation

### 3.1 Entry Point

```go
// cmd/uwas/main.go

func main() {
    cli := &CLI{}
    cli.Run(os.Args[1:])
}
```

CLI framework: our own minimal CLI parser (~200 lines). Uses `flag` stdlib for flag parsing, custom dispatcher for subcommand routing.

```go
// internal/cli/root.go

type CLI struct {
    commands map[string]Command
}

type Command interface {
    Name() string
    Description() string
    Run(args []string) error
}

// Commands:
// - serve     → start server
// - reload    → send SIGHUP
// - stop      → send SIGTERM
// - status    → check running instance
// - domain    → domain management subcommands
// - cert      → certificate management
// - cache     → cache management
// - htaccess  → .htaccess tools
// - config    → config validation/testing
// - version   → version info
```

### 3.2 Server Startup Sequence

```go
// internal/server/server.go

type Server struct {
    config     *config.Config
    listeners  []*Listener       // TCP listeners (80, 443)
    tlsMgr     *tls.Manager      // Certificate lifecycle
    vhosts     *router.VHostRouter
    middleware  middleware.Handler // compiled middleware chain
    handlers   []handler.BackendHandler
    cache      *cache.Engine
    admin      *admin.Server
    mcp        *mcp.Server
    logger     *logger.Logger
    metrics    *metrics.Collector

    // Lifecycle
    ctx        context.Context
    cancel     context.CancelFunc
    wg         sync.WaitGroup
}

func (s *Server) Start() error {
    // 1. Parse & validate config
    cfg, err := config.Load(s.configPath)

    // 2. Initialize logger
    s.logger = logger.New(cfg.Global.LogLevel, cfg.Global.LogFormat)

    // 3. Initialize metrics collector
    s.metrics = metrics.New()

    // 4. Initialize TLS manager (cert loading, ACME setup)
    s.tlsMgr = tls.NewManager(cfg.Global.ACME, s.logger)
    s.tlsMgr.LoadExistingCerts()

    // 5. Initialize cache engine
    s.cache = cache.NewEngine(cfg.Global.Cache, s.logger)

    // 6. Build virtual host router
    s.vhosts = router.NewVHostRouter(cfg.Domains)

    // 7. Initialize handlers
    s.handlers = []handler.BackendHandler{
        redirect.New(),
        static.New(),
        fastcgi.New(cfg),
        proxy.New(cfg),
    }

    // 8. Build middleware chain (order matters!)
    s.middleware = middleware.Chain(
        middleware.Recovery(s.logger),       // panic recovery (always first)
        middleware.RequestID(),              // UUID v7
        middleware.RealIP(cfg),             // X-Forwarded-For handling
        middleware.Metrics(s.metrics),       // request metrics
        middleware.AccessLog(s.logger),      // structured access log
        middleware.RateLimit(cfg),           // per-IP rate limiting
        middleware.SecurityGuard(cfg),       // blocked paths, basic WAF
        middleware.SecurityHeaders(cfg),     // HSTS, CSP, etc.
        middleware.CORS(cfg),               // CORS headers
        middleware.Rewrite(cfg),            // URL rewrite engine
        middleware.Compression(cfg),        // gzip/brotli/zstd
        middleware.Cache(s.cache),          // cache lookup/store
    )(s.dispatch)

    // 9. Start TCP listeners
    s.startListeners(cfg)

    // 10. Start ACME renewal goroutine
    s.tlsMgr.StartRenewal(s.ctx)

    // 11. Start admin API server
    s.admin = admin.New(cfg, s)
    go s.admin.Start()

    // 12. Start MCP server
    s.mcp = mcp.New(cfg, s)
    go s.mcp.Start()

    // 13. Signal handling
    s.handleSignals()

    s.logger.Info("UWAS started",
        "version", Version,
        "domains", len(cfg.Domains),
        "listeners", s.listenerAddrs(),
    )

    // Block until shutdown
    <-s.ctx.Done()
    return s.shutdown()
}
```

### 3.3 HTTP Handler (Main Dispatch)

```go
// internal/server/dispatch.go

// dispatch is the final handler after all middleware.
// It selects and runs the appropriate backend handler.
func (s *Server) dispatch(ctx *router.RequestContext) error {
    // Virtual host lookup already done by middleware
    if ctx.VHost == nil {
        return ctx.Response.Error(http.StatusNotFound, "no matching virtual host")
    }

    // try_files resolution
    if err := s.resolvePath(ctx); err != nil {
        return err
    }

    // Find matching handler
    for _, h := range s.handlers {
        if h.CanHandle(ctx) {
            return h.Handle(ctx)
        }
    }

    // No handler matched
    return ctx.Response.Error(http.StatusNotFound, "not found")
}

// resolvePath implements try_files logic
func (s *Server) resolvePath(ctx *router.RequestContext) error {
    candidates := ctx.VHost.TryFiles
    if len(candidates) == 0 {
        // Default try_files based on domain type
        switch ctx.VHost.Type {
        case "php":
            candidates = []string{"$uri", "$uri/", "/index.php"}
        case "static":
            candidates = []string{"$uri", "$uri/", "$uri/index.html"}
        default:
            candidates = []string{"$uri"}
        }
    }

    for _, candidate := range candidates {
        resolved := expandTryFileVar(candidate, ctx)
        fullPath := filepath.Join(ctx.DocumentRoot, filepath.Clean("/"+resolved))

        // Security: ensure path stays within document root
        if !strings.HasPrefix(fullPath, ctx.DocumentRoot) {
            continue
        }

        stat, err := os.Stat(fullPath)
        if err != nil {
            continue
        }

        if stat.IsDir() {
            // Try index files within directory
            for _, idx := range ctx.VHost.IndexFiles() {
                idxPath := filepath.Join(fullPath, idx)
                if _, err := os.Stat(idxPath); err == nil {
                    ctx.ResolvedPath = idxPath
                    ctx.RewrittenURI = filepath.Join(resolved, idx)
                    return nil
                }
            }
            continue
        }

        ctx.ResolvedPath = fullPath
        ctx.RewrittenURI = resolved
        return nil
    }

    // Last candidate might be a named route (e.g., /index.php)
    // that doesn't exist as a file but should be handled by FastCGI
    last := candidates[len(candidates)-1]
    if !strings.HasPrefix(last, "$") {
        ctx.ResolvedPath = filepath.Join(ctx.DocumentRoot, filepath.Clean("/"+last))
        ctx.RewrittenURI = last
        return nil
    }

    return nil // handlers will check ResolvedPath
}
```

### 3.4 Listener Setup

```go
// internal/server/listener.go

func (s *Server) startListeners(cfg *config.Config) {
    // HTTP listener (:80)
    // Always needed: ACME HTTP-01 challenge + HTTP→HTTPS redirect
    httpLn, _ := net.Listen("tcp", ":80")
    httpSrv := &http.Server{
        Handler:      s.httpHandler(),  // redirect + ACME challenge
        ReadTimeout:  cfg.Global.Timeouts.Read,
        WriteTimeout: cfg.Global.Timeouts.Write,
        IdleTimeout:  cfg.Global.Timeouts.Idle,
        ErrorLog:     s.logger.StdLogger(),
    }
    s.wg.Add(1)
    go func() {
        defer s.wg.Done()
        httpSrv.Serve(httpLn)
    }()

    // HTTPS listener (:443)
    tlsConfig := &tls.Config{
        GetCertificate: s.tlsMgr.GetCertificate,
        MinVersion:     tls.VersionTLS12,
        NextProtos:     []string{"h2", "http/1.1"},
        CipherSuites:   preferredCiphers(),
    }

    httpsLn, _ := tls.Listen("tcp", ":443", tlsConfig)
    httpsSrv := &http.Server{
        Handler:      s.httpsHandler(), // main handler
        ReadTimeout:  cfg.Global.Timeouts.Read,
        WriteTimeout: cfg.Global.Timeouts.Write,
        IdleTimeout:  cfg.Global.Timeouts.Idle,
        ErrorLog:     s.logger.StdLogger(),
    }
    s.wg.Add(1)
    go func() {
        defer s.wg.Done()
        httpsSrv.Serve(httpsLn)
    }()
}

// httpHandler handles port 80:
// 1. ACME HTTP-01 challenge responses
// 2. Everything else → 301 redirect to HTTPS
func (s *Server) httpHandler() http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // ACME challenge?
        if s.tlsMgr.HandleHTTPChallenge(w, r) {
            return
        }
        // Redirect to HTTPS
        target := "https://" + r.Host + r.URL.RequestURI()
        http.Redirect(w, r, target, http.StatusMovedPermanently)
    })
}
```

### 3.5 Graceful Shutdown & Reload

```go
// internal/server/graceful.go

func (s *Server) handleSignals() {
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)

    go func() {
        for sig := range sigCh {
            switch sig {
            case syscall.SIGHUP:
                s.logger.Info("received SIGHUP, reloading config")
                s.reload()
            case syscall.SIGTERM, syscall.SIGINT:
                s.logger.Info("received shutdown signal")
                s.cancel() // triggers ctx.Done()
            }
        }
    }()
}

func (s *Server) reload() {
    // 1. Parse new config
    newCfg, err := config.Load(s.configPath)
    if err != nil {
        s.logger.Error("config reload failed", "error", err)
        return
    }

    // 2. Validate
    if err := newCfg.Validate(); err != nil {
        s.logger.Error("config validation failed", "error", err)
        return
    }

    // 3. Update virtual hosts
    s.vhosts.Update(newCfg.Domains)

    // 4. Update TLS (new domains may need certs)
    s.tlsMgr.UpdateDomains(newCfg.Domains)

    // 5. Update cache config
    s.cache.UpdateConfig(newCfg.Global.Cache)

    // 6. Rebuild middleware chain if needed
    // (rate limits, security rules may have changed)

    s.logger.Info("config reloaded successfully",
        "domains", len(newCfg.Domains),
    )
}

func (s *Server) shutdown() error {
    s.logger.Info("shutting down gracefully",
        "grace_period", s.config.Global.Timeouts.ShutdownGrace,
    )

    ctx, cancel := context.WithTimeout(
        context.Background(),
        s.config.Global.Timeouts.ShutdownGrace,
    )
    defer cancel()

    // Stop accepting new connections
    // Drain existing connections
    // Stop background goroutines (ACME renewal, health checks)
    // Flush logs and metrics
    // Close cache cleanly

    s.wg.Wait()
    s.logger.Info("shutdown complete")
    return nil
}
```

---

## 4. TLS & ACME Implementation

### 4.1 Certificate Manager

```go
// internal/tls/manager.go

type Manager struct {
    // Certificate storage: host → *tls.Certificate
    certs    sync.Map

    // ACME client
    acme     *acme.Client

    // Config
    config   config.ACMEConfig
    storage  *CertStorage       // disk persistence
    logger   *logger.Logger

    // Challenge handlers
    httpTokens sync.Map         // token → key authorization
}

// GetCertificate is called by crypto/tls on every TLS handshake.
// This is the SNI routing heart.
func (m *Manager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
    name := strings.ToLower(hello.ServerName)

    // 1. Exact match
    if cert, ok := m.certs.Load(name); ok {
        return cert.(*tls.Certificate), nil
    }

    // 2. Wildcard match: hello for "sub.example.com" → check "*.example.com"
    if parts := strings.SplitN(name, ".", 2); len(parts) == 2 {
        wildcard := "*." + parts[1]
        if cert, ok := m.certs.Load(wildcard); ok {
            return cert.(*tls.Certificate), nil
        }
    }

    // 3. On-demand TLS (if enabled)
    if m.config.OnDemand {
        return m.obtainOnDemand(hello)
    }

    // 4. Default cert (first loaded)
    if cert, ok := m.certs.Load("_default"); ok {
        return cert.(*tls.Certificate), nil
    }

    return nil, fmt.Errorf("no certificate for %s", name)
}
```

### 4.2 ACME Client

ACME protocol (RFC 8555) implementation. Go's `crypto` package provides all the required primitives:

```go
// internal/tls/acme/client.go

type Client struct {
    directoryURL string          // https://acme-v02.api.letsencrypt.org/directory
    accountKey   *ecdsa.PrivateKey
    directory    *Directory       // ACME directory URLs
    nonces       *NoncePool       // replay nonce management
    httpClient   *http.Client
    logger       *logger.Logger
}

// Directory contains ACME server endpoint URLs
type Directory struct {
    NewNonce   string `json:"newNonce"`
    NewAccount string `json:"newAccount"`
    NewOrder   string `json:"newOrder"`
    RevokeCert string `json:"revokeCert"`
    KeyChange  string `json:"keyChange"`
}

// ObtainCertificate performs the full ACME flow for a domain.
func (c *Client) ObtainCertificate(ctx context.Context, domains []string) (*tls.Certificate, error) {
    // 1. Fetch directory (if not cached)
    if err := c.ensureDirectory(ctx); err != nil {
        return nil, fmt.Errorf("directory fetch: %w", err)
    }

    // 2. Create/fetch account
    if err := c.ensureAccount(ctx); err != nil {
        return nil, fmt.Errorf("account: %w", err)
    }

    // 3. Create order
    order, err := c.newOrder(ctx, domains)
    if err != nil {
        return nil, fmt.Errorf("new order: %w", err)
    }

    // 4. Solve authorizations (challenges)
    for _, authzURL := range order.Authorizations {
        authz, _ := c.getAuthorization(ctx, authzURL)
        if authz.Status == "valid" {
            continue
        }
        if err := c.solveChallenge(ctx, authz); err != nil {
            return nil, fmt.Errorf("challenge for %s: %w", authz.Identifier.Value, err)
        }
    }

    // 5. Wait for order to be ready
    order, err = c.waitForOrder(ctx, order.URL)
    if err != nil {
        return nil, fmt.Errorf("order ready: %w", err)
    }

    // 6. Generate CSR (ECDSA P-256 key)
    certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
    if err != nil {
        return nil, err
    }
    csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
        DNSNames: domains,
    }, certKey)
    if err != nil {
        return nil, err
    }

    // 7. Finalize order (submit CSR)
    order, err = c.finalizeOrder(ctx, order.Finalize, csr)
    if err != nil {
        return nil, fmt.Errorf("finalize: %w", err)
    }

    // 8. Download certificate
    certPEM, err := c.downloadCert(ctx, order.Certificate)
    if err != nil {
        return nil, fmt.Errorf("download cert: %w", err)
    }

    // 9. Build tls.Certificate
    keyPEM := encodeECDSAKey(certKey)
    cert, err := tls.X509KeyPair(certPEM, keyPEM)
    if err != nil {
        return nil, err
    }

    return &cert, nil
}
```

**JWS Signing** (all ACME POSTs are signed with JWS):

```go
// internal/tls/acme/jws.go

func (c *Client) signedRequest(ctx context.Context, url string, payload any) (*http.Response, error) {
    // 1. Get fresh nonce
    nonce, _ := c.nonces.Get(ctx)

    // 2. Build JWS protected header
    protected := map[string]any{
        "alg":   "ES256",
        "nonce": nonce,
        "url":   url,
    }
    // Use JWK for new-account, kid for everything else
    if c.accountURL != "" {
        protected["kid"] = c.accountURL
    } else {
        protected["jwk"] = ecdsaJWK(c.accountKey)
    }

    // 3. Encode payload (empty string for POST-as-GET)
    var payloadB64 string
    if payload != nil {
        payloadJSON, _ := json.Marshal(payload)
        payloadB64 = base64url(payloadJSON)
    }

    // 4. Sign: ECDSA-SHA256(protected_b64 + "." + payload_b64)
    protectedB64 := base64url(mustJSON(protected))
    sigInput := protectedB64 + "." + payloadB64
    hash := sha256.Sum256([]byte(sigInput))
    r, s, _ := ecdsa.Sign(rand.Reader, c.accountKey, hash[:])
    sig := append(padTo32(r.Bytes()), padTo32(s.Bytes())...)

    // 5. Build JWS body
    body := map[string]string{
        "protected": protectedB64,
        "payload":   payloadB64,
        "signature": base64url(sig),
    }

    // 6. POST to ACME server
    req, _ := http.NewRequestWithContext(ctx, "POST", url, jsonReader(body))
    req.Header.Set("Content-Type", "application/jose+json")
    resp, err := c.httpClient.Do(req)

    // 7. Save nonce from response
    if n := resp.Header.Get("Replay-Nonce"); n != "" {
        c.nonces.Put(n)
    }

    return resp, err
}
```

### 4.3 Certificate Renewal

```go
// internal/tls/renewal.go

func (m *Manager) StartRenewal(ctx context.Context) {
    go func() {
        ticker := time.NewTicker(12 * time.Hour) // check twice daily
        defer ticker.Stop()

        for {
            select {
            case <-ctx.Done():
                return
            case <-ticker.C:
                m.checkRenewals(ctx)
            }
        }
    }()
}

func (m *Manager) checkRenewals(ctx context.Context) {
    m.certs.Range(func(key, value any) bool {
        host := key.(string)
        cert := value.(*tls.Certificate)

        leaf := cert.Leaf
        if leaf == nil {
            // Parse leaf cert for expiry info
            leaf, _ = x509.ParseCertificate(cert.Certificate[0])
        }

        remaining := time.Until(leaf.NotAfter)

        // Renew if < 30 days remaining
        if remaining < 30*24*time.Hour {
            m.logger.Info("renewing certificate",
                "host", host,
                "expires_in", remaining.Round(time.Hour),
            )

            newCert, err := m.acme.ObtainCertificate(ctx, leaf.DNSNames)
            if err != nil {
                m.logger.Error("renewal failed",
                    "host", host,
                    "error", err,
                )
                // Will retry on next ticker
                return true
            }

            // Hot-swap certificate
            m.certs.Store(host, newCert)
            m.storage.Save(host, newCert)

            m.logger.Info("certificate renewed", "host", host)
        }

        return true
    })
}
```

---

## 5. FastCGI Client Implementation

### 5.1 Protocol

FastCGI binary protocol: 8-byte header + variable payload.

```go
// pkg/fastcgi/protocol.go

// Record header (8 bytes, fixed)
type header struct {
    Version       uint8  // Always 1
    Type          uint8  // Record type
    RequestID     uint16 // Multiplex ID
    ContentLength uint16 // Payload length
    PaddingLength uint8  // Padding after content
    Reserved      uint8
}

// Record types
const (
    typeBeginRequest    uint8 = 1
    typeAbortRequest    uint8 = 2
    typeEndRequest      uint8 = 3
    typeParams          uint8 = 4
    typeStdin           uint8 = 5
    typeStdout          uint8 = 6
    typeStderr          uint8 = 7
    typeGetValues       uint8 = 8
    typeGetValuesResult uint8 = 9
)

// Roles
const (
    roleResponder uint16 = 1
    roleAuthorizer uint16 = 2
    roleFilter     uint16 = 3
)

func encodeHeader(h *header) []byte {
    b := make([]byte, 8)
    b[0] = h.Version
    b[1] = h.Type
    binary.BigEndian.PutUint16(b[2:4], h.RequestID)
    binary.BigEndian.PutUint16(b[4:6], h.ContentLength)
    b[6] = h.PaddingLength
    b[7] = h.Reserved
    return b
}

// Name-value pair encoding (length-prefixed)
func encodeParam(name, value string) []byte {
    var buf bytes.Buffer
    encodeLength(&buf, len(name))
    encodeLength(&buf, len(value))
    buf.WriteString(name)
    buf.WriteString(value)
    return buf.Bytes()
}

func encodeLength(buf *bytes.Buffer, n int) {
    if n < 128 {
        buf.WriteByte(byte(n))
    } else {
        // 4-byte encoding with high bit set
        b := make([]byte, 4)
        binary.BigEndian.PutUint32(b, uint32(n)|0x80000000)
        buf.Write(b)
    }
}
```

### 5.2 Connection Pool

```go
// pkg/fastcgi/pool.go

type Pool struct {
    address  string          // "unix:/var/run/php-fpm.sock" or "tcp:127.0.0.1:9000"
    maxIdle  int
    maxOpen  int
    idle     chan *conn
    active   atomic.Int32
    mu       sync.Mutex
}

type conn struct {
    netConn   net.Conn
    createdAt time.Time
    usedAt    time.Time
}

func (p *Pool) Get(ctx context.Context) (*conn, error) {
    // 1. Try idle connection
    select {
    case c := <-p.idle:
        if time.Since(c.usedAt) > 30*time.Second {
            // Stale connection, close and create new
            c.netConn.Close()
            return p.create(ctx)
        }
        c.usedAt = time.Now()
        return c, nil
    default:
    }

    // 2. Create new if under limit
    if int(p.active.Load()) < p.maxOpen {
        return p.create(ctx)
    }

    // 3. Wait for idle connection
    select {
    case c := <-p.idle:
        c.usedAt = time.Now()
        return c, nil
    case <-ctx.Done():
        return nil, ctx.Err()
    }
}

func (p *Pool) Put(c *conn) {
    c.usedAt = time.Now()
    select {
    case p.idle <- c:
    default:
        // Pool full, close excess connection
        c.netConn.Close()
        p.active.Add(-1)
    }
}
```

### 5.3 Request Execution

```go
// pkg/fastcgi/client.go

func (c *Client) Execute(ctx context.Context, env map[string]string, stdin io.Reader) (*Response, error) {
    conn, err := c.pool.Get(ctx)
    if err != nil {
        return nil, err
    }
    defer c.pool.Put(conn)

    requestID := uint16(1) // single request per connection (simpler)

    // 1. Send FCGI_BEGIN_REQUEST
    beginBody := make([]byte, 8)
    binary.BigEndian.PutUint16(beginBody[0:2], roleResponder)
    beginBody[2] = 1 // FCGI_KEEP_CONN
    conn.writeRecord(typeBeginRequest, requestID, beginBody)

    // 2. Send FCGI_PARAMS
    var paramsBuf bytes.Buffer
    for k, v := range env {
        paramsBuf.Write(encodeParam(k, v))
    }
    conn.writeRecord(typeParams, requestID, paramsBuf.Bytes())
    conn.writeRecord(typeParams, requestID, nil) // empty params = end

    // 3. Send FCGI_STDIN (request body)
    if stdin != nil {
        buf := make([]byte, 65535) // max record payload
        for {
            n, err := stdin.Read(buf)
            if n > 0 {
                conn.writeRecord(typeStdin, requestID, buf[:n])
            }
            if err == io.EOF {
                break
            }
            if err != nil {
                return nil, err
            }
        }
    }
    conn.writeRecord(typeStdin, requestID, nil) // empty stdin = end

    // 4. Read response (FCGI_STDOUT + FCGI_STDERR + FCGI_END_REQUEST)
    resp := &Response{}
    for {
        rec, err := conn.readRecord()
        if err != nil {
            return nil, err
        }

        switch rec.Type {
        case typeStdout:
            resp.stdout.Write(rec.Content)
        case typeStderr:
            resp.stderr.Write(rec.Content)
        case typeEndRequest:
            resp.appStatus = binary.BigEndian.Uint32(rec.Content[0:4])
            return resp, nil
        }
    }
}

// Response parsing: split HTTP headers from body
// PHP-FPM returns "Status: 200 OK\r\nContent-Type: text/html\r\n\r\n<html>..."
func (r *Response) ParseHTTP() (statusCode int, headers http.Header, body io.Reader) {
    reader := bufio.NewReader(&r.stdout)
    tp := textproto.NewReader(reader)

    mimeHeader, _ := tp.ReadMIMEHeader()
    headers = http.Header(mimeHeader)

    // Status header → status code
    if status := headers.Get("Status"); status != "" {
        code, _ := strconv.Atoi(status[:3])
        statusCode = code
        headers.Del("Status")
    } else {
        statusCode = 200
    }

    body = reader // remaining bytes = body
    return
}
```

---

## 6. Rewrite Engine Implementation

### 6.1 .htaccess Parser

```go
// pkg/htaccess/parser.go

type Parser struct {
    // Parses Apache .htaccess files into internal rule representation
}

type Directive struct {
    Name     string     // "RewriteRule", "Redirect", "Header", etc.
    Args     []string   // directive arguments
    Block    []Directive // for <IfModule>, <FilesMatch> blocks
    Negated  bool       // "!" prefix
    LineNum  int
}

func Parse(reader io.Reader) ([]Directive, error) {
    scanner := bufio.NewScanner(reader)
    var directives []Directive
    var blockStack []*Directive

    for scanner.Scan() {
        line := strings.TrimSpace(scanner.Text())

        // Skip comments and empty lines
        if line == "" || strings.HasPrefix(line, "#") {
            continue
        }

        // Handle line continuation (\)
        for strings.HasSuffix(line, "\\") {
            scanner.Scan()
            line = line[:len(line)-1] + " " + strings.TrimSpace(scanner.Text())
        }

        // Block open: <IfModule mod_rewrite.c>
        if strings.HasPrefix(line, "<") && !strings.HasPrefix(line, "</") {
            blockName, args := parseBlockOpen(line)
            block := &Directive{Name: blockName, Args: args}
            blockStack = append(blockStack, block)
            continue
        }

        // Block close: </IfModule>
        if strings.HasPrefix(line, "</") {
            if len(blockStack) > 0 {
                closed := blockStack[len(blockStack)-1]
                blockStack = blockStack[:len(blockStack)-1]
                if len(blockStack) > 0 {
                    parent := blockStack[len(blockStack)-1]
                    parent.Block = append(parent.Block, *closed)
                } else {
                    directives = append(directives, *closed)
                }
            }
            continue
        }

        // Normal directive
        d := parseDirective(line)
        if len(blockStack) > 0 {
            current := blockStack[len(blockStack)-1]
            current.Block = append(current.Block, d)
        } else {
            directives = append(directives, d)
        }
    }

    return directives, nil
}
```

### 6.2 .htaccess → Internal Rules Converter

```go
// pkg/htaccess/converter.go

func Convert(directives []Directive) (*RuleSet, error) {
    rules := &RuleSet{}

    var pendingConds []RewriteCondition

    for _, d := range directives {
        switch strings.ToLower(d.Name) {

        case "rewriteengine":
            rules.RewriteEnabled = strings.EqualFold(d.Args[0], "on")

        case "rewritecond":
            cond, err := parseRewriteCond(d.Args)
            if err != nil {
                return nil, fmt.Errorf("line %d: %w", d.LineNum, err)
            }
            pendingConds = append(pendingConds, cond)

        case "rewriterule":
            rule, err := parseRewriteRule(d.Args)
            if err != nil {
                return nil, fmt.Errorf("line %d: %w", d.LineNum, err)
            }
            rule.Conditions = pendingConds
            pendingConds = nil // reset
            rules.Rewrites = append(rules.Rewrites, rule)

        case "redirect", "redirectmatch":
            rules.Redirects = append(rules.Redirects, parseRedirect(d))

        case "errordocument":
            code, _ := strconv.Atoi(d.Args[0])
            rules.ErrorDocuments[code] = d.Args[1]

        case "directoryindex":
            rules.DirectoryIndex = d.Args

        case "header":
            rules.Headers = append(rules.Headers, parseHeaderDirective(d))

        case "expiresactive":
            rules.ExpiresActive = strings.EqualFold(d.Args[0], "on")

        case "expiresbytype":
            rules.ExpiresByType[d.Args[0]] = d.Args[1]

        case "options":
            for _, opt := range d.Args {
                switch strings.ToLower(opt) {
                case "-indexes":
                    rules.DirectoryListing = false
                case "+indexes":
                    rules.DirectoryListing = true
                case "-followsymlinks":
                    rules.FollowSymlinks = false
                }
            }

        case "authtype":
            rules.AuthType = d.Args[0]

        case "authname":
            rules.AuthName = strings.Trim(d.Args[0], `"`)

        case "authuserfile":
            rules.AuthUserFile = d.Args[0]

        case "require":
            rules.Require = strings.Join(d.Args, " ")

        case "<ifmodule>", "ifmodule":
            // Recursively convert block contents
            // IfModule always evaluates to true in UWAS
            blockRules, _ := Convert(d.Block)
            rules.Merge(blockRules)

        case "<filesmatch>", "filesmatch":
            rules.FilesMatch = append(rules.FilesMatch, FilesMatchRule{
                Pattern: d.Args[0],
                Rules:   d.Block,
            })
        }
    }

    return rules, nil
}
```

---

## 7. Cache Engine Implementation

### 7.1 Sharded Memory Cache

```go
// internal/cache/memory.go

const shardCount = 256

type MemoryCache struct {
    shards    [shardCount]shard
    maxBytes  int64
    usedBytes atomic.Int64
    stats     CacheStats
}

type shard struct {
    mu    sync.RWMutex
    items map[string]*entry
    lru   *lruList // doubly-linked list for LRU eviction
}

type entry struct {
    key        string
    value      *CachedResponse
    size       int64
    created    time.Time
    ttl        time.Duration
    graceTTL   time.Duration
    tags       []string
    hits       atomic.Int64
    lruElement *lruElement
}

func (mc *MemoryCache) Get(key string) (*CachedResponse, CacheStatus) {
    s := &mc.shards[shardIndex(key)]
    s.mu.RLock()
    e, ok := s.items[key]
    s.mu.RUnlock()

    if !ok {
        mc.stats.Misses.Add(1)
        return nil, CacheMiss
    }

    e.hits.Add(1)
    now := time.Now()
    age := now.Sub(e.created)

    // Fresh hit
    if age < e.ttl {
        s.mu.Lock()
        s.lru.MoveToFront(e.lruElement) // update LRU position
        s.mu.Unlock()
        mc.stats.Hits.Add(1)
        return e.value, CacheHit
    }

    // Stale but within grace period
    if age < e.ttl+e.graceTTL {
        mc.stats.StaleHits.Add(1)
        return e.value, CacheStale // caller should async-revalidate
    }

    // Expired beyond grace
    mc.stats.Misses.Add(1)
    return nil, CacheExpired
}

func (mc *MemoryCache) Set(key string, resp *CachedResponse, ttl, graceTTL time.Duration, tags []string) {
    size := resp.Size()

    s := &mc.shards[shardIndex(key)]
    s.mu.Lock()
    defer s.mu.Unlock()

    // Evict if over memory limit
    for mc.usedBytes.Load()+size > mc.maxBytes {
        mc.evictLRU(s)
    }

    e := &entry{
        key:      key,
        value:    resp,
        size:     size,
        created:  time.Now(),
        ttl:      ttl,
        graceTTL: graceTTL,
        tags:     tags,
    }
    e.lruElement = s.lru.PushFront(e)
    s.items[key] = e
    mc.usedBytes.Add(size)
}

func (mc *MemoryCache) PurgeByTag(tag string) int {
    purged := 0
    for i := range mc.shards {
        s := &mc.shards[i]
        s.mu.Lock()
        for key, e := range s.items {
            for _, t := range e.tags {
                if t == tag {
                    s.lru.Remove(e.lruElement)
                    delete(s.items, key)
                    mc.usedBytes.Add(-e.size)
                    purged++
                    break
                }
            }
        }
        s.mu.Unlock()
    }
    return purged
}

func shardIndex(key string) uint8 {
    h := fnv.New32a()
    h.Write([]byte(key))
    return uint8(h.Sum32() % shardCount)
}
```

### 7.2 Cache Middleware

```go
// internal/middleware/cache.go

func Cache(engine *cache.Engine) Middleware {
    return func(next Handler) Handler {
        return func(ctx *router.RequestContext) error {
            // Only cache GET/HEAD
            if ctx.Request.Method != "GET" && ctx.Request.Method != "HEAD" {
                ctx.CacheStatus = "BYPASS"
                return next(ctx)
            }

            // Check bypass rules
            if engine.ShouldBypass(ctx) {
                ctx.CacheStatus = "BYPASS"
                return next(ctx)
            }

            // Generate cache key
            key := engine.Key(ctx)

            // Lookup
            cached, status := engine.Get(key)

            switch status {
            case cache.CacheHit:
                ctx.CacheStatus = "HIT"
                return writeCachedResponse(ctx, cached)

            case cache.CacheStale:
                ctx.CacheStatus = "STALE"
                // Serve stale immediately
                go engine.AsyncRevalidate(key, ctx.Clone(), next)
                return writeCachedResponse(ctx, cached)

            default:
                ctx.CacheStatus = "MISS"
            }

            // Cache miss: execute handler with response capture
            capture := newResponseCapture(ctx.Response)
            ctx.Response = capture

            if err := next(ctx); err != nil {
                return err
            }

            // Store in cache if response is cacheable
            if engine.IsCacheable(capture) {
                ttl, graceTTL := engine.TTLFor(ctx)
                tags := engine.TagsFor(ctx)
                engine.Set(key, capture.ToCachedResponse(), ttl, graceTTL, tags)
            }

            return nil
        }
    }
}
```

---

## 8. Reverse Proxy Implementation

```go
// internal/handler/proxy/handler.go

type ProxyHandler struct {
    pools map[string]*UpstreamPool // domain → pool
}

func (h *ProxyHandler) Handle(ctx *router.RequestContext) error {
    pool := h.pools[ctx.VHost.Host]

    // Select backend
    backend, err := pool.Select(ctx)
    if err != nil {
        return ctx.Response.Error(503, "no healthy upstream")
    }

    ctx.Upstream = backend.Address

    // Build upstream request
    upReq := ctx.Request.Clone(ctx.Request.Context())
    upReq.URL.Scheme = backend.Scheme()
    upReq.URL.Host = backend.HostPort()
    upReq.Host = ctx.Request.Host // preserve original Host

    // Add proxy headers
    upReq.Header.Set("X-Real-IP", ctx.RemoteIP)
    upReq.Header.Set("X-Forwarded-For", ctx.RemoteIP)
    upReq.Header.Set("X-Forwarded-Proto", ctx.Proto())
    upReq.Header.Set("X-Forwarded-Host", ctx.Request.Host)

    // WebSocket upgrade?
    if isWebSocketUpgrade(ctx.Request) {
        return h.proxyWebSocket(ctx, backend)
    }

    // Execute proxy request
    start := time.Now()
    resp, err := backend.Transport.RoundTrip(upReq)
    if err != nil {
        pool.RecordFailure(backend)
        // Circuit breaker check
        if pool.CircuitOpen(backend) {
            return ctx.Response.Error(503, "circuit open")
        }
        return ctx.Response.Error(502, "bad gateway")
    }
    defer resp.Body.Close()

    pool.RecordSuccess(backend, time.Since(start))

    // Copy response headers
    for k, vv := range resp.Header {
        for _, v := range vv {
            ctx.Response.Header().Add(k, v)
        }
    }

    ctx.Response.WriteHeader(resp.StatusCode)
    io.Copy(ctx.Response, resp.Body)

    return nil
}
```

### 8.1 Load Balancer Algorithms

```go
// internal/handler/proxy/balancer.go

type Balancer interface {
    Select(backends []*Backend, ctx *router.RequestContext) *Backend
}

// Round Robin with weights (smooth weighted round robin)
type WeightedRR struct {
    mu       sync.Mutex
    current  []int // current weight per backend
}

func (wrr *WeightedRR) Select(backends []*Backend, ctx *router.RequestContext) *Backend {
    wrr.mu.Lock()
    defer wrr.mu.Unlock()

    if len(wrr.current) != len(backends) {
        wrr.current = make([]int, len(backends))
    }

    totalWeight := 0
    best := -1

    for i, b := range backends {
        if b.State.Load() != stateHealthy {
            continue
        }
        wrr.current[i] += b.Weight
        totalWeight += b.Weight
        if best == -1 || wrr.current[i] > wrr.current[best] {
            best = i
        }
    }

    if best == -1 {
        return nil
    }

    wrr.current[best] -= totalWeight
    return backends[best]
}

// Least connections
type LeastConn struct{}

func (lc *LeastConn) Select(backends []*Backend, ctx *router.RequestContext) *Backend {
    var best *Backend
    minConns := int32(math.MaxInt32)

    for _, b := range backends {
        if b.State.Load() != stateHealthy {
            continue
        }
        conns := b.ActiveConns.Load()
        if conns < minConns {
            minConns = conns
            best = b
        }
    }
    return best
}

// IP Hash (consistent hashing for session affinity)
type IPHash struct{}

func (ih *IPHash) Select(backends []*Backend, ctx *router.RequestContext) *Backend {
    healthy := filterHealthy(backends)
    if len(healthy) == 0 {
        return nil
    }
    h := fnv.New32a()
    h.Write([]byte(ctx.RemoteIP))
    idx := int(h.Sum32()) % len(healthy)
    return healthy[idx]
}
```

---

## 9. Structured Logger

We wrap Go 1.21+'s `log/slog` package — no external dependency:

```go
// internal/logger/logger.go

type Logger struct {
    slog   *slog.Logger
    level  slog.Level
    format string // "json" | "text"
}

func New(level, format string) *Logger {
    var handler slog.Handler

    opts := &slog.HandlerOptions{
        Level: parseLevel(level),
    }

    switch format {
    case "json":
        handler = slog.NewJSONHandler(os.Stdout, opts)
    default:
        handler = slog.NewTextHandler(os.Stdout, opts)
    }

    return &Logger{
        slog: slog.New(handler),
    }
}

func (l *Logger) Info(msg string, args ...any)  { l.slog.Info(msg, args...) }
func (l *Logger) Error(msg string, args ...any) { l.slog.Error(msg, args...) }
func (l *Logger) Warn(msg string, args ...any)  { l.slog.Warn(msg, args...) }
func (l *Logger) Debug(msg string, args ...any) { l.slog.Debug(msg, args...) }

// AccessLog writes a structured access log entry
func (l *Logger) AccessLog(ctx *router.RequestContext) {
    l.slog.Info("access",
        "request_id", ctx.ID,
        "remote_ip",  ctx.RemoteIP,
        "method",     ctx.Request.Method,
        "host",       ctx.Request.Host,
        "path",       ctx.OriginalURI,
        "status",     ctx.Response.StatusCode(),
        "bytes",      ctx.Response.BytesWritten(),
        "duration_ms", ctx.Duration.Milliseconds(),
        "ttfb_ms",    ctx.TTFBDur.Milliseconds(),
        "cache",      ctx.CacheStatus,
        "upstream",   ctx.Upstream,
        "user_agent", ctx.Request.UserAgent(),
        "tls",        ctx.TLSVersion(),
    )
}
```

---

## 10. Admin REST API

```go
// internal/admin/api.go

type AdminServer struct {
    server  *server.Server
    config  *config.Config
    router  *http.ServeMux
}

func New(cfg *config.Config, srv *server.Server) *AdminServer {
    a := &AdminServer{config: cfg, server: srv}

    mux := http.NewServeMux()

    // All routes require API key auth
    auth := a.authMiddleware

    // Domain management
    mux.HandleFunc("GET /api/v1/domains", auth(a.listDomains))
    mux.HandleFunc("POST /api/v1/domains", auth(a.addDomain))
    mux.HandleFunc("GET /api/v1/domains/{host}", auth(a.getDomain))
    mux.HandleFunc("PUT /api/v1/domains/{host}", auth(a.updateDomain))
    mux.HandleFunc("DELETE /api/v1/domains/{host}", auth(a.deleteDomain))

    // Cache management
    mux.HandleFunc("POST /api/v1/cache/purge", auth(a.purgeCache))
    mux.HandleFunc("GET /api/v1/cache/stats", auth(a.cacheStats))
    mux.HandleFunc("DELETE /api/v1/cache", auth(a.clearCache))

    // Certificate management
    mux.HandleFunc("GET /api/v1/certs", auth(a.listCerts))
    mux.HandleFunc("POST /api/v1/certs/{host}/renew", auth(a.renewCert))

    // Server management
    mux.HandleFunc("POST /api/v1/reload", auth(a.reload))
    mux.HandleFunc("GET /api/v1/health", a.health) // no auth needed
    mux.HandleFunc("GET /api/v1/stats", auth(a.stats))
    mux.HandleFunc("GET /api/v1/metrics", auth(a.metrics))

    // Dashboard (embedded HTML)
    mux.HandleFunc("GET /_uwas/dashboard", a.dashboard)

    a.router = mux
    return a
}

func (a *AdminServer) Start() error {
    srv := &http.Server{
        Addr:    a.config.Global.Admin.Listen,
        Handler: a.router,
    }
    return srv.ListenAndServeTLS("", "") // uses TLS from main server
}

func (a *AdminServer) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        key := r.Header.Get("Authorization")
        key = strings.TrimPrefix(key, "Bearer ")
        if key != a.config.Global.Admin.APIKey {
            http.Error(w, "unauthorized", http.StatusUnauthorized)
            return
        }
        next(w, r)
    }
}
```

---

## 11. Build & Test Strategy

### 11.1 Build Targets

```makefile
# Makefile

VERSION ?= $(shell git describe --tags 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS  = -s -w -X main.Version=$(VERSION) -X main.Commit=$(COMMIT)

.PHONY: build dev test lint clean

build:
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/uwas ./cmd/uwas

dev:
	go build -race -o bin/uwas-dev ./cmd/uwas

test:
	go test -race -cover ./...

test-integration:
	go test -race -tags=integration -timeout=120s ./test/...

lint:
	go vet ./...
	staticcheck ./...

bench:
	go test -bench=. -benchmem ./internal/cache/...
	go test -bench=. -benchmem ./internal/rewrite/...
	go test -bench=. -benchmem ./pkg/fastcgi/...

clean:
	rm -rf bin/

docker:
	docker build -t uwas:$(VERSION) .
```

### 11.2 Test Strategy

```
Unit tests (go test):
├── config/         → YAML parsing, validation, edge cases
├── router/         → vhost matching, try_files resolution
├── rewrite/        → regex rules, conditions, flags, backreferences
├── cache/          → LRU eviction, grace mode, tag purge, concurrent access
├── fastcgi/        → protocol encoding/decoding, response parsing
├── proxy/          → balancer algorithms, circuit breaker state machine
├── middleware/      → each middleware independently
├── htaccess/       → parser correctness, directive conversion
└── tls/acme/       → JWS signing, challenge handling

Integration tests (requires running services):
├── php_test.go     → real PHP-FPM + WordPress permalink test
├── proxy_test.go   → real upstream server + health check
├── tls_test.go     → Let's Encrypt staging environment
├── cache_test.go   → real HTTP requests + cache validation
└── htaccess_test.go → real .htaccess files from WordPress/Laravel

Benchmark tests:
├── cache/memory_bench_test.go   → concurrent read/write throughput
├── rewrite/engine_bench_test.go → regex evaluation per request
├── fastcgi/protocol_bench_test.go → encode/decode speed
└── router/vhost_bench_test.go   → lookup with 100/1000/10000 domains
```

### 11.3 Dockerfile

```dockerfile
# Multi-stage build
FROM golang:1.23-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o uwas ./cmd/uwas

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /build/uwas /usr/local/bin/uwas
RUN mkdir -p /etc/uwas /var/lib/uwas /var/cache/uwas /var/log/uwas
EXPOSE 80 443 9443
ENTRYPOINT ["uwas"]
CMD ["serve"]
```

---

## 12. Implementation Order (Build Sequence)

Definitive build order — each step depends on the previous one:

### Sprint 1: Skeleton (3 days)
1. `cmd/uwas/main.go` — CLI skeleton
2. `internal/config/` — YAML config parser + validation
3. `internal/logger/` — structured logger (slog wrapper)
4. `internal/server/server.go` — basic HTTP server start/stop
5. **Output**: `uwas serve` runs and returns "Hello UWAS" on port 80

### Sprint 2: Static Serving (3 days)
6. `internal/router/context.go` — RequestContext
7. `internal/router/vhost.go` — virtual host router
8. `internal/handler/static/` — file serving (ServeContent, ETag, MIME)
9. `internal/server/dispatch.go` — try_files logic
10. **Output**: Multi-domain static file serving works

### Sprint 3: TLS (4 days)
11. `internal/tls/manager.go` — cert manager + SNI routing
12. `internal/tls/acme/client.go` — ACME protocol (directory, account, nonce)
13. `internal/tls/acme/jws.go` — JWS signing
14. `internal/tls/acme/challenge.go` — HTTP-01 solver
15. `internal/tls/storage.go` — cert disk persistence
16. `internal/tls/renewal.go` — auto-renewal goroutine
17. **Output**: Auto HTTPS works (tested on Let's Encrypt staging)

### Sprint 4: FastCGI / PHP (4 days)
18. `pkg/fastcgi/protocol.go` — binary protocol
19. `pkg/fastcgi/client.go` — request execution
20. `pkg/fastcgi/pool.go` — connection pool
21. `internal/handler/fastcgi/handler.go` — PHP handler
22. `internal/handler/fastcgi/env.go` — CGI env builder
23. **Output**: WordPress works (except pretty permalinks)

### Sprint 5: Rewrite Engine (4 days)
24. `internal/rewrite/rule.go` — RewriteRule types
25. `internal/rewrite/condition.go` — RewriteCond evaluation
26. `internal/rewrite/engine.go` — rule processor
27. `internal/rewrite/variables.go` — server variable resolver
28. `pkg/htaccess/parser.go` — .htaccess parser
29. `pkg/htaccess/converter.go` — directive → internal rules
30. **Output**: WordPress pretty permalinks work, .htaccess supported

### Sprint 6: Middleware (3 days)
31. `internal/middleware/chain.go` — chain builder
32. `internal/middleware/requestid.go`
33. `internal/middleware/realip.go`
34. `internal/middleware/compress.go` — gzip (brotli in Phase 2)
35. `internal/middleware/headers.go` — security headers
36. `internal/middleware/ratelimit.go` — token bucket
37. `internal/middleware/security.go` — blocked paths, basic WAF
38. **Output**: Production-ready middleware stack

### Sprint 7: Cache (5 days)
39. `internal/cache/memory.go` — sharded LRU
40. `internal/cache/disk.go` — L2 disk cache
41. `internal/cache/key.go` — key generation
42. `internal/cache/grace.go` — grace mode + async revalidation
43. `internal/cache/purge.go` — tag/path purge
44. `internal/middleware/cache.go` — cache middleware
45. **Output**: Full caching layer (including Varnish-like grace mode)

### Sprint 8: Reverse Proxy (4 days)
46. `internal/handler/proxy/handler.go` — proxy handler
47. `internal/handler/proxy/balancer.go` — LB algorithms
48. `internal/handler/proxy/upstream.go` — upstream pool
49. `internal/handler/proxy/health.go` — health checker
50. `internal/handler/proxy/circuit.go` — circuit breaker
51. `internal/handler/proxy/websocket.go` — WS proxy
52. **Output**: Full reverse proxy + load balancer

### Sprint 9: Admin & MCP (3 days)
53. `internal/admin/api.go` — REST API
54. `internal/admin/dashboard.go` — embedded HTML
55. `internal/metrics/collector.go` — Prometheus metrics
56. `internal/mcp/server.go` — MCP protocol
57. `internal/mcp/tools.go` — tool implementations
58. **Output**: Full management layer

### Sprint 10: Polish (3 days)
59. CLI subcommands (domain, cert, cache, htaccess, config)
60. Error pages, graceful shutdown refinement
61. Integration tests, benchmark suite
62. Documentation, README, examples
63. Docker image, Makefile
64. **Output**: v0.1.0 release ready

**Total: ~36 days (~7 weeks, without buffer)**
**Realistic (+ buffer + testing): 8-10 weeks**

---

## 13. Critical Path Dependencies

```
                    Sprint 1: Skeleton
                         │
                    Sprint 2: Static
                    │         │
              Sprint 3: TLS   │
                    │         │
              Sprint 4: FastCGI
                    │
              Sprint 5: Rewrite
                    │
              Sprint 6: Middleware ─────┐
                    │                   │
              Sprint 7: Cache     Sprint 8: Proxy
                    │                   │
                    └───────┬───────────┘
                            │
                      Sprint 9: Admin/MCP
                            │
                      Sprint 10: Polish
```

Sprint 3 (TLS) and Sprint 4 (FastCGI) can be worked on in parallel.
Sprint 7 (Cache) and Sprint 8 (Proxy) can be worked on in parallel.
