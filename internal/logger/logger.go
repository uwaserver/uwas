package logger

import (
	"context"
	"io"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"
)

type Logger struct {
	*slog.Logger
	level *slog.LevelVar
	out   *asyncWriter
}

func New(level, format string) *Logger {
	lv := &slog.LevelVar{}
	lv.Set(parseLevel(level))

	opts := &slog.HandlerOptions{Level: lv}

	// Never write to stdout from the caller's goroutine: a backed-up journald
	// would stall every request that logs (see asyncWriter).
	out := newAsyncWriter(os.Stdout)

	var handler slog.Handler
	switch strings.ToLower(format) {
	case "json":
		handler = slog.NewJSONHandler(out, opts)
	default:
		handler = slog.NewTextHandler(out, opts)
	}

	return &Logger{
		Logger: slog.New(handler),
		level:  lv,
		out:    out,
	}
}

// Flush waits, up to timeout, for queued lines to reach the output. Call it
// before the process exits so the last lines — usually the reason it is
// exiting — are not lost.
func (l *Logger) Flush(timeout time.Duration) {
	if l == nil || l.out == nil {
		return
	}
	l.out.flush(timeout)
}

// SetLevel changes the threshold on a running logger.
//
// The level lives in a slog.LevelVar precisely so it can move, but nothing
// called this: global.log_level was read once in New and never again, so a
// reload accepted a new value, wrote it to the config, showed it in the panel
// and kept logging at the old threshold until the process restarted.
//
// An unrecognised value is treated as info, matching New.
func (l *Logger) SetLevel(level string) {
	l.level.Set(parseLevel(level))
}

// Level reports the current threshold.
func (l *Logger) Level() slog.Level {
	return l.level.Level()
}

// StdLogger returns a *log.Logger compatible with net/http.Server.ErrorLog.
func (l *Logger) StdLogger() *log.Logger {
	return slog.NewLogLogger(l.Logger.Handler(), slog.LevelError)
}

// Writer returns an io.Writer that writes to the logger at the given level.
func (l *Logger) Writer(level slog.Level) io.Writer {
	return &logWriter{logger: l.Logger, level: level}
}

type logWriter struct {
	logger *slog.Logger
	level  slog.Level
}

func (w *logWriter) Write(p []byte) (int, error) {
	if w.logger.Enabled(context.Background(), w.level) {
		w.logger.Log(context.Background(), w.level, strings.TrimRight(string(p), "\n"))
	}
	return len(p), nil
}

// SafeGo runs fn in a new goroutine with panic recovery.
// If fn panics, the panic is logged and fn is restarted after 1 second.
// If fn returns normally, the goroutine exits.
func (l *Logger) SafeGo(name string, fn func()) {
	go func() {
		for {
			panicked := true
			func() {
				defer func() {
					if r := recover(); r != nil {
						l.Error("goroutine panicked, restarting in 1s",
							"goroutine", name,
							"panic", r,
						)
					}
				}()
				fn()
				panicked = false
			}()
			if !panicked {
				return
			}
			time.Sleep(time.Second)
		}
	}()
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
