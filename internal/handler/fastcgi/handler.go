package fastcgi

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
	"github.com/uwaserver/uwas/pkg/fastcgi"
)

// Handler handles PHP requests via FastCGI.
type Handler struct {
	logger  *logger.Logger
	clients sync.Map // fpmAddress → *fastcgi.Client
}

func New(log *logger.Logger) *Handler {
	return &Handler{logger: log}
}

// Serve processes a PHP request via FastCGI.
func (h *Handler) Serve(ctx *router.RequestContext, domain *config.Domain) {
	client := h.getClient(domain.PHP.FPMAddress)

	// Split script name and path info using both original URI and resolved path
	scriptName, pathInfo := SplitScriptPath(
		ctx.OriginalURI,
		ctx.ResolvedPath,
		domain.Root,
		domain.PHP.IndexFiles,
	)
	scriptFilename := ScriptFilenameFromResolved(ctx.ResolvedPath, domain.Root, scriptName)

	// Build CGI environment
	env := BuildEnv(ctx, scriptFilename, scriptName, pathInfo, domain.PHP.Env)

	// Forward request body for non-GET/HEAD methods
	var stdin io.Reader
	if ctx.Request.Body != nil && ctx.Request.Method != "GET" && ctx.Request.Method != "HEAD" {
		stdin = ctx.Request.Body
	}

	resp, err := client.Execute(ctx.Request.Context(), env, stdin)
	if err != nil {
		h.logger.Error("fastcgi execute failed",
			"host", domain.Host,
			"script", scriptFilename,
			"error", err,
		)
		ctx.Response.Error(502, "502 Bad Gateway — FastCGI error")
		return
	}

	// Capture stderr for logging (once)
	stderrContent := string(resp.Stderr())
	if stderrContent != "" {
		h.logger.Warn("php stderr",
			"host", domain.Host,
			"script", scriptFilename,
			"stderr", stderrContent,
		)
	}

	// Capture stdout length BEFORE ParseHTTP consumes the buffer
	stdoutLen := len(resp.Stdout())

	// Parse FastCGI response into HTTP status, headers, body
	statusCode, headers, body := resp.ParseHTTP()

	// --- Empty stdout: PHP crashed or FPM returned nothing ---
	if stdoutLen == 0 {
		h.logger.Error("PHP empty response",
			"host", domain.Host, "uri", ctx.Request.RequestURI,
			"script", scriptFilename, "fpm", domain.PHP.FPMAddress,
		)
		ctx.Response.Header().Set("Content-Type", "text/html; charset=utf-8")
		ctx.Response.WriteHeader(500)
		ctx.Response.Write([]byte("<h1>500 Internal Server Error</h1>\n<p>PHP returned no output.</p>\n"))
		return
	}

	// --- X-Accel-Redirect / X-Sendfile: serve file instead of PHP body ---
	if served := h.tryServeFile(ctx, domain, headers); served {
		return
	}

	// Read body fully to detect empty HTML responses (WSOD)
	var bodyBytes []byte
	if body != nil {
		bodyBytes, _ = io.ReadAll(body)
	}

	// --- WSOD detection: headers present but body empty ---
	// PHP fatal error with display_errors=Off sends headers but no body.
	// Only flag text/html 200 on GET/POST — empty body is normal for:
	//   HEAD, 204 No Content, 302 redirect, DELETE, application/json, etc.
	isWSOD := len(bodyBytes) == 0 &&
		statusCode == 200 &&
		(ctx.Request.Method == "GET" || ctx.Request.Method == "POST") &&
		strings.Contains(headers.Get("Content-Type"), "text/html") &&
		headers.Get("Location") == "" // Location present = redirect, not WSOD

	if isWSOD {
		h.logger.Error("PHP WSOD: headers but no body",
			"host", domain.Host, "uri", ctx.Request.RequestURI,
			"script", scriptFilename, "fpm", domain.PHP.FPMAddress,
		)
		ctx.Response.Header().Set("Content-Type", "text/html; charset=utf-8")
		ctx.Response.WriteHeader(500)
		ctx.Response.Write([]byte("<h1>500 Internal Server Error</h1>\n"))
		ctx.Response.Write([]byte("<p>PHP returned headers but no content. Fatal error with display_errors=Off.</p>\n"))
		ctx.Response.Write([]byte("<p>Check: <code>tail -50 /var/log/php*.log</code></p>\n"))
		return
	}

	// --- Forward response to client ---
	// Remove hop-by-hop headers that PHP should not control.
	// Content-Length from PHP conflicts with gzip middleware (compressed size differs).
	// Transfer-Encoding is managed by Go's HTTP stack.
	// Status was already parsed and removed by ParseHTTP.
	headers.Del("Content-Length")
	headers.Del("Transfer-Encoding")
	headers.Del("Connection")

	for key, vals := range headers {
		for _, v := range vals {
			ctx.Response.Header().Add(key, v)
		}
	}
	ctx.Response.WriteHeader(statusCode)
	if len(bodyBytes) > 0 {
		ctx.Response.Write(bodyBytes)
	}
}

// tryServeFile handles X-Accel-Redirect and X-Sendfile headers.
// Returns true if a file was served (caller should return).
func (h *Handler) tryServeFile(ctx *router.RequestContext, domain *config.Domain, headers http.Header) bool {
	var filePath string
	if accel := headers.Get("X-Accel-Redirect"); accel != "" {
		headers.Del("X-Accel-Redirect")
		filePath = filepath.Join(domain.Root, filepath.Clean("/"+accel))
	} else if sendfile := headers.Get("X-Sendfile"); sendfile != "" {
		headers.Del("X-Sendfile")
		filePath = sendfile
	}
	if filePath == "" {
		return false
	}

	// Security: path must stay within document root
	absRoot, _ := filepath.Abs(domain.Root)
	absPath, _ := filepath.Abs(filePath)
	if !strings.HasPrefix(absPath, absRoot) {
		return false
	}

	f, err := os.Open(filePath)
	if err != nil {
		return false // fall through to normal response
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return false
	}

	for key, vals := range headers {
		for _, v := range vals {
			ctx.Response.Header().Add(key, v)
		}
	}
	http.ServeContent(ctx.Response, ctx.Request, info.Name(), info.ModTime(), f)
	return true
}

func (h *Handler) getClient(address string) *fastcgi.Client {
	if v, ok := h.clients.Load(address); ok {
		return v.(*fastcgi.Client)
	}

	client := fastcgi.NewClient(fastcgi.PoolConfig{
		Address: address,
		MaxIdle: 10,
		MaxOpen: 64,
	})

	actual, _ := h.clients.LoadOrStore(address, client)
	return actual.(*fastcgi.Client)
}
