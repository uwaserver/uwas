package watchdog

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// Config is the liveness policy.
type Config struct {
	Enabled  bool
	Interval time.Duration
	Timeout  time.Duration

	// Failures is how many consecutive failed probes mean "wedged".
	Failures int

	// SelfRestart exits the process when the probe fails Failures times in a
	// row and systemd's watchdog is not available to do it. Only useful
	// outside systemd (Docker, a supervisor that restarts on exit); under
	// systemd the notification path is preferred, because systemd owns the
	// restart policy and its rate limiting.
	SelfRestart bool
}

// Watchdog probes a target and reports liveness to systemd.
type Watchdog struct {
	cfg Config
	log *logger.Logger

	// probe returns nil when the server answered. Any HTTP status counts as
	// alive: this tests whether the accept → parse → handler → response path
	// still completes, not whether a particular route is correct. A 421 for an
	// unconfigured Host is a perfectly healthy answer.
	probe func(context.Context) error

	notifier *Notifier

	// exit is os.Exit in production; a test seam otherwise.
	exit func(int)

	fails   atomic.Int64
	healthy atomic.Bool
	probes  atomic.Int64
}

// New builds a Watchdog that probes addr, which must be a "host:port" the
// server itself listens on. Pass useTLS for an HTTPS listener.
func New(cfg Config, addr string, useTLS bool, log *logger.Logger) *Watchdog {
	w := &Watchdog{
		cfg:      cfg,
		log:      log,
		notifier: NewNotifier(),
		exit:     nil,
	}
	w.healthy.Store(true)
	w.probe = httpProbe(addr, useTLS, cfg.Timeout)
	return w
}

// SetProbe replaces the liveness check.
func (w *Watchdog) SetProbe(fn func(context.Context) error) { w.probe = fn }

// Healthy reports the result of the most recent probe round.
func (w *Watchdog) Healthy() bool { return w == nil || w.healthy.Load() }

// NotifyReady tells systemd the server finished starting. It is safe to call
// with no systemd present, and is sent regardless of whether the watchdog
// itself is enabled so a Type=notify unit still starts cleanly.
func (w *Watchdog) NotifyReady() {
	if w == nil {
		return
	}
	if err := w.notifier.Send("READY=1"); err == nil {
		w.log.Debug("systemd notified: ready")
	}
}

// NotifyStopping tells systemd a clean shutdown is under way, so the exit is
// not mistaken for a crash.
func (w *Watchdog) NotifyStopping() {
	if w == nil {
		return
	}
	_ = w.notifier.Send("STOPPING=1")
	w.notifier.Close()
}

// Run probes until ctx is cancelled.
func (w *Watchdog) Run(ctx context.Context) {
	if w == nil || !w.cfg.Enabled {
		return
	}

	interval := w.cfg.Interval
	// systemd kills the service if WATCHDOG_USEC elapses without a ping, so
	// probe at least twice per period: one lost probe must not be fatal.
	if sd := SystemdInterval(); sd > 0 && sd/2 < interval {
		interval = sd / 2
		w.log.Info("watchdog interval reduced to match systemd WatchdogSec",
			"systemd_period", sd.String(), "interval", interval.String())
	}
	if interval <= 0 {
		interval = 15 * time.Second
	}

	w.log.Info("watchdog started",
		"interval", interval.String(),
		"timeout", w.cfg.Timeout.String(),
		"failures_before_action", w.cfg.Failures,
		"systemd", w.notifier.Available(),
	)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

func (w *Watchdog) tick(ctx context.Context) {
	pctx, cancel := context.WithTimeout(ctx, w.cfg.Timeout)
	err := w.probe(pctx)
	cancel()
	w.probes.Add(1)

	if err == nil {
		if w.fails.Swap(0) > 0 {
			w.log.Info("watchdog probe recovered")
		}
		w.healthy.Store(true)
		if e := w.notifier.Send("WATCHDOG=1"); e != nil && !w.notifier.Available() {
			// No systemd; nothing to do but keep probing.
			_ = e
		}
		return
	}

	n := w.fails.Add(1)
	threshold := int64(w.cfg.Failures)
	if threshold < 1 {
		threshold = 1
	}

	if n < threshold {
		// Still within grace. Keep the watchdog fed so a single slow probe
		// does not restart a server that is merely busy.
		w.log.Warn("watchdog probe failed", "error", err, "consecutive", n, "threshold", threshold)
		_ = w.notifier.Send("WATCHDOG=1")
		return
	}

	w.healthy.Store(false)
	w.log.Error("watchdog: server is not answering its own listener; withholding systemd watchdog ping",
		"error", err, "consecutive", n, "threshold", threshold)

	// From here the ping is deliberately withheld. systemd's WatchdogSec
	// expires, it sends SIGABRT and applies the unit's Restart= policy —
	// which is the whole point: the restart decision stays with the init
	// system, including its rate limiting.
	_ = w.notifier.Send("STATUS=unresponsive: watchdog probe failing")

	if w.cfg.SelfRestart && !w.notifier.Available() {
		w.log.Error("watchdog: no systemd watchdog available, exiting so the supervisor restarts us")
		exit := w.exit
		if exit == nil {
			exit = osExit
		}
		exit(1)
	}
}

// httpProbe builds the default liveness check: a real request over the loopback
// interface to the server's own listener.
//
// It deliberately does not reuse connections. A pooled connection that was
// established while the server was healthy can keep answering from a kernel
// buffer, so a fresh dial each time is what actually proves the accept path
// still works — which is the exact path a connection flood saturates.
func httpProbe(addr string, useTLS bool, timeout time.Duration) func(context.Context) error {
	scheme := "http"
	if useTLS {
		scheme = "https"
	}
	url := scheme + "://" + addr + "/"

	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext:       (&net.Dialer{Timeout: timeout}).DialContext,
		// The probe talks to our own loopback listener, which may present a
		// certificate for a public hostname. Verifying it would fail for
		// reasons that have nothing to do with liveness.
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // loopback liveness probe
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
	}
	client := &http.Client{Transport: transport, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	return func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "UWAS-Watchdog/1.0")
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("probe %s: %w", url, err)
		}
		resp.Body.Close()
		// Any status is a pass. The server parsed a request and produced a
		// response, which is all liveness means here.
		return nil
	}
}

// ProbeAddr converts a listen address into one the loopback probe can dial.
// ":443" and "0.0.0.0:443" both become "127.0.0.1:443"; an address already
// bound to a specific interface is used as-is.
func ProbeAddr(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		return ""
	}
	switch host {
	case "", "0.0.0.0", "[::]", "::":
		return net.JoinHostPort("127.0.0.1", port)
	}
	return net.JoinHostPort(host, port)
}
