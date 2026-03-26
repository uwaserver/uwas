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

	// Execute FastCGI request — ALWAYS forward body for POST/PUT/PATCH/DELETE
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

	// Log stderr if any (PHP errors)
	if stderrBytes := resp.Stderr(); len(stderrBytes) > 0 {
		h.logger.Warn("php stderr",
			"host", domain.Host,
			"script", scriptFilename,
			"stderr", string(stderrBytes),
		)
	}

	// Log stderr (PHP errors)
	stderrContent := string(resp.Stderr())

	// Check stdout length BEFORE ParseHTTP (which consumes the buffer)
	stdoutLen := len(resp.Stdout())

	// Parse HTTP response from FastCGI
	statusCode, headers, body := resp.ParseHTTP()

	// Completely empty stdout = PHP crashed or didn't respond at all
	if stdoutLen == 0 {
		h.logger.Error("PHP returned empty response (WSOD)",
			"host", domain.Host,
			"uri", ctx.Request.RequestURI,
			"script", scriptFilename,
			"fpm_address", domain.PHP.FPMAddress,
			"stderr", stderrContent,
		)
		ctx.Response.Header().Set("Content-Type", "text/html; charset=utf-8")
		ctx.Response.WriteHeader(500)
		ctx.Response.Write([]byte("<h1>500 Internal Server Error</h1>\n"))
		ctx.Response.Write([]byte("<p>PHP returned an empty response.</p>\n"))
		return
	}

	// X-Accel-Redirect / X-Sendfile: serve file directly instead of PHP body
	if accel := headers.Get("X-Accel-Redirect"); accel != "" {
		headers.Del("X-Accel-Redirect")
		filePath := filepath.Join(domain.Root, filepath.Clean("/"+accel))
		absRoot, _ := filepath.Abs(domain.Root)
		absPath, _ := filepath.Abs(filePath)
		if strings.HasPrefix(absPath, absRoot) {
			if f, err := os.Open(filePath); err == nil {
				defer f.Close()
				if info, err := f.Stat(); err == nil {
					for key, vals := range headers {
						for _, v := range vals {
							ctx.Response.Header().Add(key, v)
						}
					}
					http.ServeContent(ctx.Response, ctx.Request, info.Name(), info.ModTime(), f)
					return
				}
			}
		}
	} else if sendfile := headers.Get("X-Sendfile"); sendfile != "" {
		headers.Del("X-Sendfile")
		absRoot, _ := filepath.Abs(domain.Root)
		absSend, _ := filepath.Abs(sendfile)
		if strings.HasPrefix(absSend, absRoot) {
			if f, err := os.Open(sendfile); err == nil {
				defer f.Close()
				if info, err := f.Stat(); err == nil {
					for key, vals := range headers {
						for _, v := range vals {
							ctx.Response.Header().Add(key, v)
						}
					}
					http.ServeContent(ctx.Response, ctx.Request, info.Name(), info.ModTime(), f)
					return
				}
			}
		}
	}

	// Read body fully so we can detect WSOD (headers present, body empty)
	var bodyBytes []byte
	if body != nil {
		bodyBytes, _ = io.ReadAll(body)
	}

	// Detect WSOD: PHP sent HTTP headers but zero-length HTML body.
	// PHP fatal error with display_errors=Off produces:
	//   "Status: 200\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n"
	// with no actual HTML. Only check GET 200 text/html — empty body is
	// normal for HEAD, 204, 302, DELETE, X-Accel, etc.
	if len(bodyBytes) == 0 &&
		statusCode == 200 &&
		(ctx.Request.Method == "GET" || ctx.Request.Method == "POST") &&
		strings.Contains(headers.Get("Content-Type"), "text/html") {
		h.logger.Error("PHP WSOD: headers present but body empty",
			"host", domain.Host,
			"uri", ctx.Request.RequestURI,
			"script", scriptFilename,
			"fpm_address", domain.PHP.FPMAddress,
			"stderr", stderrContent,
		)
		ctx.Response.Header().Set("Content-Type", "text/html; charset=utf-8")
		ctx.Response.WriteHeader(500)
		ctx.Response.Write([]byte("<h1>500 Internal Server Error</h1>\n"))
		ctx.Response.Write([]byte("<p>PHP returned headers but no content (WSOD). This usually means a fatal error with <code>display_errors=Off</code>.</p>\n"))
		ctx.Response.Write([]byte("<p>Check: <code>tail -50 /var/log/php*.log</code></p>\n"))
		return
	}

	// Forward response headers
	for key, vals := range headers {
		for _, v := range vals {
			ctx.Response.Header().Add(key, v)
		}
	}

	// Write status and body
	ctx.Response.WriteHeader(statusCode)
	if len(bodyBytes) > 0 {
		ctx.Response.Write(bodyBytes)
	}
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
