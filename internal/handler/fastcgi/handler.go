package fastcgi

import (
	"io"
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

	// Split script name and path info
	scriptName, pathInfo := SplitScriptPath(
		ctx.Request.URL.Path,
		domain.Root,
		domain.PHP.IndexFiles,
	)
	scriptFilename := ScriptFilename(domain.Root, scriptName)

	// Build CGI environment
	env := BuildEnv(ctx, scriptFilename, scriptName, pathInfo, domain.PHP.Env)

	// Execute FastCGI request
	var stdin io.Reader
	if ctx.Request.Body != nil && ctx.Request.ContentLength != 0 {
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
