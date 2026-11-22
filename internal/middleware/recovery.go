package middleware

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"runtime/debug"

	"github.com/uwaserver/uwas/internal/logger"
)

// Recovery catches panics and returns 500 Internal Server Error. A panic after
// the response has started cannot be turned into a 500: the status line and
// part of the body are already on the wire, so appending an error page would
// end a truncated body as if it were complete. Such a response is aborted
// instead so the client sees a failed transfer (F8800).
func Recovery(log *logger.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := &startedWriter{ResponseWriter: w}
			defer func() {
				if err := recover(); err != nil {
					if err == http.ErrAbortHandler {
						panic(err)
					}
					stack := string(debug.Stack())
					log.Error("panic recovered",
						"error", fmt.Sprint(err),
						"method", r.Method,
						"path", r.URL.Path,
						"stack", stack,
					)
					if sw.started {
						panic(http.ErrAbortHandler)
					}
					// The header map is the one the handler was filling for the
					// page; those headers do not describe this error (F68901).
					for _, k := range abandonedResponseHeaders {
						w.Header().Del(k)
					}
					http.Error(w, "500 Internal Server Error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(sw, r)
		})
	}
}

// abandonedResponseHeaders are the headers a handler sets for the response it
// was building: its caching lifetime and validators, cookies, and how its body
// is encoded or presented. Left on the 500 they let caches keep the error for
// the page's lifetime (the server withdraws an operator lifetime from errors
// only on its own writer, F9010), set the page's cookie, and label the plain
// error text gzip or as a download (F68901).
var abandonedResponseHeaders = []string{
	"Cache-Control", "Expires", "ETag", "Last-Modified", "Age", "Set-Cookie",
	"Content-Encoding", "Content-Disposition", "Content-Range", "Content-Language", "Content-Location",
}

// startedWriter records whether the response has been committed to the client.
type startedWriter struct {
	http.ResponseWriter
	started bool
}

func (w *startedWriter) WriteHeader(code int) {
	// Informational responses (100-199 except 101) do not commit the final status.
	if code < 100 || code >= 200 || code == http.StatusSwitchingProtocols {
		w.started = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *startedWriter) Write(b []byte) (int, error) {
	w.started = true
	return w.ResponseWriter.Write(b)
}

func (w *startedWriter) Flush() {
	// A flush sends the headers, committing the status even with no body yet.
	w.started = true
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *startedWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("middleware: underlying ResponseWriter does not support Hijack")
	}
	conn, rw, err := hj.Hijack()
	if err == nil {
		w.started = true
	}
	return conn, rw, err
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (w *startedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
