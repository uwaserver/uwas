package fastcgi

import (
	"io"
	"net/http"
	"os"
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

	// Execute FastCGI request — forward request body for POST/PUT/PATCH
	// Always forward body if Content-Length > 0 or Transfer-Encoding is chunked
	var stdin io.Reader
	if ctx.Request.Body != nil {
		if ctx.Request.ContentLength > 0 || ctx.Request.TransferEncoding != nil {
			stdin = ctx.Request.Body
		}
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

	// Log stderr if any
	if stderr := resp.Stderr(); len(stderr) > 0 {
		h.logger.Warn("php stderr",
			"host", domain.Host,
			"script", scriptFilename,
			"stderr", string(stderr),
		)
	}

	// Parse HTTP response from FastCGI
	statusCode, headers, body := resp.ParseHTTP()

	// X-Accel-Redirect / X-Sendfile: serve file directly instead of PHP body
	if accel := headers.Get("X-Accel-Redirect"); accel != "" {
		headers.Del("X-Accel-Redirect")
		filePath := domain.Root + accel
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
		// Fall through to normal response if file not found
	} else if sendfile := headers.Get("X-Sendfile"); sendfile != "" {
		headers.Del("X-Sendfile")
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

	// Forward response headers
	for key, vals := range headers {
		for _, v := range vals {
			ctx.Response.Header().Add(key, v)
		}
	}

	// Write status and body
	ctx.Response.WriteHeader(statusCode)
	io.Copy(ctx.Response, body)
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
