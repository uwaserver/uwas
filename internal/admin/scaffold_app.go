package admin

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// scaffoldAppDemo writes a minimal, runnable demo into the new domain's web root
// when the domain is type=app, so that browsing to the site immediately returns
// a working response instead of a 502 or empty directory. The demo uses only the
// runtime's standard library — no `npm install` / `pip install` step needed — so
// the app can start the moment the runtime binary is present.
//
// We also default App.Command and other fields when the operator left them blank,
// so the appmanager can register and start the process. Existing values are not
// overwritten; users who specified their own command/port keep them.
func scaffoldAppDemo(root, host string, app *config.AppConfig, log *logger.Logger) {
	runtime := strings.ToLower(strings.TrimSpace(app.Runtime))
	switch runtime {
	case "", "node", "nodejs", "js":
		scaffoldNodeDemo(root, host, app, log)
	case "python", "python3", "py":
		scaffoldPythonDemo(root, host, app, log)
	case "ruby", "rb":
		scaffoldRubyDemo(root, host, app, log)
	case "go", "golang":
		scaffoldGoDemo(root, host, app, log)
	default:
		// Unknown runtime — leave the directory empty, user will populate it.
		// Don't overwrite custom command either.
	}
}

func writeIfMissing(path string, content []byte, log *logger.Logger) {
	if _, err := os.Stat(path); err == nil {
		return // file exists, don't clobber user content
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		log.Warn("failed to write app scaffold file", "path", path, "error", err)
	}
}

func scaffoldNodeDemo(root, host string, app *config.AppConfig, log *logger.Logger) {
	app.Runtime = "node"
	if app.Command == "" {
		app.Command = "node index.js"
	}

	// index.js — stdlib-only HTTP server. PORT env var is injected by appmanager.
	// We deliberately do NOT fall back to a hard-coded port: if PORT isn't set,
	// the proxy will be expecting one port while node binds another, and you
	// get a silent 502. Fail loud instead so the log shows the real cause.
	indexJS := fmt.Sprintf(`// UWAS demo Node.js app for %s.
// Replace this with your real app. To use Express, Fastify, etc.:
//   1. Edit package.json dependencies and run: npm install
//   2. Update the start command on the Apps page.
const http = require('node:http');

const port = parseInt(process.env.PORT || '', 10);
if (!port) {
  console.error('FATAL: PORT env var is not set. UWAS appmanager must supply it.');
  console.error('Visible env keys: ' + Object.keys(process.env).filter(k => !k.startsWith('npm_')).join(', '));
  process.exit(1);
}
const host = '127.0.0.1';

const server = http.createServer((req, res) => {
  res.setHeader('Content-Type', 'text/html; charset=utf-8');
  res.end(`+"`"+`<!doctype html>
<html><head><title>%s</title></head>
<body style="font-family:system-ui;background:#0f172a;color:#e2e8f0;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0">
<div style="text-align:center;max-width:560px;padding:24px">
  <h1 style="margin:0 0 12px">%s</h1>
  <p style="color:#94a3b8">Your Node.js demo app is running.</p>
  <p style="color:#64748b;font-size:14px">Edit <code>index.js</code> and restart from the Apps page.</p>
  <p style="color:#475569;font-size:12px">Method: ${req.method} · URL: ${req.url}</p>
</div></body></html>`+"`"+`);
});

server.listen(port, host, () => {
  console.log('listening on http://' + host + ':' + port);
});
`, host, host, host)
	writeIfMissing(filepath.Join(root, "index.js"), []byte(indexJS), log)

	// package.json — no deps so `npm install` is instant (or unnecessary).
	pkgJSON := fmt.Sprintf(`{
  "name": %q,
  "version": "0.1.0",
  "private": true,
  "description": "UWAS demo Node.js app",
  "main": "index.js",
  "scripts": {
    "start": "node index.js"
  },
  "engines": {
    "node": ">=18"
  }
}
`, sanitizePkgName(host))
	writeIfMissing(filepath.Join(root, "package.json"), []byte(pkgJSON), log)

	readme := fmt.Sprintf(`# %s — Node.js app

This directory is a runnable demo created by UWAS.

## Run locally

    node index.js

## Add dependencies

Edit package.json then run:

    npm install

If you switch to Express / Fastify / Next.js, update the start command on the
Apps page (it currently runs: %s).
`, host, app.Command)
	writeIfMissing(filepath.Join(root, "README.md"), []byte(readme), log)
}

func scaffoldPythonDemo(root, host string, app *config.AppConfig, log *logger.Logger) {
	app.Runtime = "python"
	if app.Command == "" {
		app.Command = "python3 app.py"
	}

	appPY := fmt.Sprintf(`# UWAS demo Python app for %s.
# Replace this with your real app. For Flask/FastAPI/Django:
#   1. Add to requirements.txt
#   2. python3 -m venv .venv && source .venv/bin/activate && pip install -r requirements.txt
#   3. Update the App command in the dashboard.
import os
from http.server import BaseHTTPRequestHandler, HTTPServer

PORT = int(os.environ.get("PORT", "%d") or "3000")
HOST = "127.0.0.1"
HTML = """<!doctype html>
<html><head><title>%s</title></head>
<body style="font-family:system-ui;background:#0f172a;color:#e2e8f0;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0">
<div style="text-align:center;max-width:560px;padding:24px">
  <h1 style="margin:0 0 12px">%s</h1>
  <p style="color:#94a3b8">Your Python demo app is running.</p>
  <p style="color:#64748b;font-size:14px">Edit <code>app.py</code> and restart from the Apps page.</p>
</div></body></html>"""

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.end_headers()
        self.wfile.write(HTML.encode("utf-8"))

    def log_message(self, fmt, *args):
        print(fmt %% args)

if __name__ == "__main__":
    print(f"listening on http://{HOST}:{PORT}")
    HTTPServer((HOST, PORT), Handler).serve_forever()
`, host, app.Port, host, host)
	writeIfMissing(filepath.Join(root, "app.py"), []byte(appPY), log)
	writeIfMissing(filepath.Join(root, "requirements.txt"), []byte("# Add your dependencies here, then:\n#   python3 -m venv .venv && source .venv/bin/activate && pip install -r requirements.txt\n"), log)
}

func scaffoldRubyDemo(root, host string, app *config.AppConfig, log *logger.Logger) {
	app.Runtime = "ruby"
	if app.Command == "" {
		app.Command = "ruby app.rb"
	}

	appRB := fmt.Sprintf(`# UWAS demo Ruby app for %s.
require 'webrick'

port = (ENV['PORT'] || '%d').to_i
port = 3000 if port == 0

server = WEBrick::HTTPServer.new(Port: port, BindAddress: '127.0.0.1')
server.mount_proc '/' do |req, res|
  res['Content-Type'] = 'text/html; charset=utf-8'
  res.body = <<~HTML
    <!doctype html>
    <html><head><title>%s</title></head>
    <body style="font-family:system-ui;background:#0f172a;color:#e2e8f0;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0">
    <div style="text-align:center;max-width:560px;padding:24px">
      <h1 style="margin:0 0 12px">%s</h1>
      <p style="color:#94a3b8">Your Ruby demo app is running.</p>
      <p style="color:#64748b;font-size:14px">Edit <code>app.rb</code> and restart from the Apps page.</p>
    </div></body></html>
  HTML
end

trap('INT') { server.shutdown }
server.start
`, host, app.Port, host, host)
	writeIfMissing(filepath.Join(root, "app.rb"), []byte(appRB), log)
}

func scaffoldGoDemo(root, host string, app *config.AppConfig, log *logger.Logger) {
	app.Runtime = "go"
	if app.Command == "" {
		app.Command = "go run ."
	}

	mainGO := fmt.Sprintf(`// UWAS demo Go app for %s.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
)

func main() {
	portStr := os.Getenv("PORT")
	port, _ := strconv.Atoi(portStr)
	if port == 0 {
		port = %d
	}
	if port == 0 {
		port = 3000
	}
	addr := fmt.Sprintf("127.0.0.1:%%d", port)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `+"`"+`<!doctype html>
<html><head><title>%s</title></head>
<body style="font-family:system-ui;background:#0f172a;color:#e2e8f0;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0">
<div style="text-align:center;max-width:560px;padding:24px">
  <h1 style="margin:0 0 12px">%s</h1>
  <p style="color:#94a3b8">Your Go demo app is running.</p>
  <p style="color:#64748b;font-size:14px">Edit <code>main.go</code> and restart from the Apps page.</p>
</div></body></html>`+"`"+`)
	})

	log.Printf("listening on http://%%s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
`, host, app.Port, host, host)
	writeIfMissing(filepath.Join(root, "main.go"), []byte(mainGO), log)

	goMod := fmt.Sprintf("module %s\n\ngo 1.21\n", sanitizePkgName(host))
	writeIfMissing(filepath.Join(root, "go.mod"), []byte(goMod), log)
}

// sanitizePkgName makes a hostname safe for use as a package/module identifier.
// Replaces dots with dashes and lowercases the result; npm and Go module names
// don't allow dots at the top of the name. Falls back to "uwas-app" for hosts
// that strip to empty (e.g. all-numeric IPs).
func sanitizePkgName(host string) string {
	s := strings.ToLower(strings.TrimSpace(host))
	s = strings.ReplaceAll(s, ".", "-")
	s = strings.ReplaceAll(s, ":", "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "uwas-app"
	}
	return s
}
