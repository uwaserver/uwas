package logger

import (
	"context"
	"io"
	"log"
	"log/slog"
	"os"
	"strings"
)

type Logger struct {
	*slog.Logger
	level *slog.LevelVar
}

func New(level, format string) *Logger {
	lv := &slog.LevelVar{}
	lv.Set(parseLevel(level))

	opts := &slog.HandlerOptions{Level: lv}

	var handler slog.Handler
	switch strings.ToLower(format) {
	case "json":
		handler = slog.NewJSONHandler(os.Stdout, opts)
	default:
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	return &Logger{
		Logger: slog.New(handler),
		level:  lv,
	}
}

func (l *Logger) SetLevel(level string) {
	l.level.Set(parseLevel(level))
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
	w.logger.Log(context.Background(), w.level, strings.TrimRight(string(p), "\n"))
	return len(p), nil
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
