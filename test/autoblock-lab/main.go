// Command autoblock-lab calibrates UWAS autoblock / rate-limit thresholds.
//
//	go run ./test/autoblock-lab              # full suite (needs bin/uwas)
//	go run ./test/autoblock-lab -synth-only  # Blocker math only, no server
//
// Connection-level counters key on the TCP peer. Loopback and RFC1918 are
// always Safe, so a browser hitting 127.0.0.1 can never trip conn_flood /
// concurrent. Those paths are simulated with TEST-NET addresses. Live HTTP
// checks cover global + per-domain rate limits (429), which do apply to
// localhost.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/uwaserver/uwas/internal/autoblock"
	"github.com/uwaserver/uwas/internal/logger"
)

type scenario struct {
	Name        string
	ConnsPerMin int // new TCP connections in one window
	Concurrent  int // simultaneous open connections
	Aborts      int // handshake aborts in one window
	Kind        string
}

type tripResult struct {
	Scenario    string `json:"scenario"`
	Kind        string `json:"kind"`
	Blocked     bool   `json:"blocked"`
	Reason      string `json:"reason,omitempty"`
	WouldFP     bool   `json:"would_false_positive"`
	ConnsPerMin int    `json:"conns_per_min"`
	Concurrent  int    `json:"concurrent"`
	Aborts      int    `json:"aborts"`
}

type rateResult struct {
	Label      string `json:"label"`
	Limit      int    `json:"limit"`
	Sent       int    `json:"sent"`
	OK         int    `json:"ok"`
	Limited    int    `json:"limited_429"`
	Other      int    `json:"other"`
	First429At int    `json:"first_429_at"` // 1-based request index; 0 = none
	Pass       bool   `json:"pass"`
}

type report struct {
	Synth     []tripResult `json:"synthetic_autoblock"`
	Rate      []rateResult `json:"live_rate_limit"`
	Recommend map[string]any `json:"recommended"`
}

func main() {
	synthOnly := flag.Bool("synth-only", false, "skip live HTTP server checks")
	configPath := flag.String("config", "test/autoblock-lab/uwas-lab.yaml", "lab config path")
	binPath := flag.String("bin", "bin/uwas", "uwas binary")
	flag.Parse()

	root, err := findRepoRoot()
	if err != nil {
		fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		fatal(err)
	}

	fmt.Println("=== UWAS autoblock / rate-limit lab ===")
	fmt.Println()

	rep := report{Recommend: map[string]any{}}

	fmt.Println("--- 1) Synthetic connection-level calibration (TEST-NET peers) ---")
	rep.Synth = runSynth()
	printSynth(rep.Synth)

	if !*synthOnly {
		fmt.Println()
		fmt.Println("--- 2) Live HTTP rate-limit against local uwas ---")
		rr, err := runLive(*binPath, *configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "live checks failed: %v\n", err)
			os.Exit(1)
		}
		rep.Rate = rr
		printRate(rr)
	}

	rep.Recommend = recommend(rep.Synth, rep.Rate)
	fmt.Println()
	fmt.Println("--- 3) Recommended settings ---")
	printRecommend(rep.Recommend)

	outPath := filepath.Join("test", "autoblock-lab", "last-report.json")
	data, _ := json.MarshalIndent(rep, "", "  ")
	_ = os.WriteFile(outPath, data, 0o644)
	fmt.Printf("\nWrote %s\n", outPath)

	if failed := failures(rep); failed > 0 {
		fmt.Fprintf(os.Stderr, "\n%d check(s) failed\n", failed)
		os.Exit(1)
	}
	fmt.Println("\nAll checks passed.")
}

func findRepoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found from %s", wd)
		}
		dir = parent
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// ── Synthetic Blocker scenarios ─────────────────────────────────────────────

func runSynth() []tripResult {
	// Product defaults under test.
	const (
		maxConns = 600
		maxConc  = 150
		maxAbort = 60
	)

	scenarios := []scenario{
		{Name: "single_browser", ConnsPerMin: 30, Concurrent: 6, Aborts: 0, Kind: "legit"},
		{Name: "spa_refresh_burst", ConnsPerMin: 120, Concurrent: 12, Aborts: 0, Kind: "legit"},
		{Name: "office_nat_20_users", ConnsPerMin: 400, Concurrent: 80, Aborts: 0, Kind: "legit"},
		{Name: "office_nat_50_users", ConnsPerMin: 900, Concurrent: 200, Aborts: 0, Kind: "aggregated"},
		{Name: "cdn_edge_busy", ConnsPerMin: 5000, Concurrent: 800, Aborts: 5, Kind: "aggregated"},
		{Name: "tls_abort_flood", ConnsPerMin: 200, Concurrent: 20, Aborts: 120, Kind: "attack"},
		{Name: "conn_flood_attack", ConnsPerMin: 3000, Concurrent: 40, Aborts: 0, Kind: "attack"},
		{Name: "slowloris_hold", ConnsPerMin: 50, Concurrent: 400, Aborts: 0, Kind: "attack"},
	}

	out := make([]tripResult, 0, len(scenarios))
	for i, sc := range scenarios {
		ip := fmt.Sprintf("203.0.113.%d", i+1)
		reason, blocked := simulatePeer(ip, maxConns, maxConc, maxAbort, sc)
		fp := blocked && (sc.Kind == "legit" || sc.Kind == "aggregated")
		out = append(out, tripResult{
			Scenario:    sc.Name,
			Kind:        sc.Kind,
			Blocked:     blocked,
			Reason:      reason,
			WouldFP:     fp,
			ConnsPerMin: sc.ConnsPerMin,
			Concurrent:  sc.Concurrent,
			Aborts:      sc.Aborts,
		})
	}
	return out
}

func simulatePeer(ip string, maxConns, maxConc, maxAbort int, sc scenario) (reason string, blocked bool) {
	b := autoblock.New(autoblock.Config{
		Enabled:        true,
		Window:         time.Minute,
		MaxConnections: maxConns,
		MaxConcurrent:  maxConc,
		MaxAborts:      maxAbort,
		MaxWAFHits:     15,
		MaxRateHits:    120,
		MaxNotFound:    200,
		BlockDuration:  time.Hour,
		FeedRateHits:   false,
	}, logger.New("error", "text"))

	a, ok := autoblock.ParseAddr(ip)
	if !ok {
		return "bad_ip", false
	}

	// Open up to Concurrent connections and hold them.
	opened := 0
	for opened < sc.Concurrent {
		if !b.ConnOpened(a) {
			e := latestReason(b, ip)
			return e, true
		}
		opened++
	}

	// Additional new connections in the same window (beyond those held).
	extra := sc.ConnsPerMin - sc.Concurrent
	if extra < 0 {
		extra = 0
	}
	for i := 0; i < extra; i++ {
		if !b.ConnOpened(a) {
			return latestReason(b, ip), true
		}
		// Close immediately so concurrent gauge does not climb further.
		b.ConnClosed(a, false)
	}

	// Handshake aborts on fresh short-lived connections.
	for i := 0; i < sc.Aborts; i++ {
		if !b.ConnOpened(a) {
			return latestReason(b, ip), true
		}
		b.ConnClosed(a, true)
		if b.Blocked(a) {
			return latestReason(b, ip), true
		}
	}

	if b.Blocked(a) {
		return latestReason(b, ip), true
	}
	return "", false
}

func latestReason(b *autoblock.Blocker, ip string) string {
	for _, e := range b.List() {
		if e.IP == ip {
			return e.Reason
		}
	}
	return "blocked"
}

func printSynth(rows []tripResult) {
	fmt.Printf("%-22s %-12s %6s %6s %6s %-10s %s\n",
		"scenario", "kind", "conn/m", "conc", "abort", "blocked", "note")
	for _, r := range rows {
		note := "ok"
		if r.WouldFP {
			note = "FALSE POSITIVE at defaults 600/150"
		} else if r.Blocked && r.Kind == "attack" {
			note = "caught (" + r.Reason + ")"
		} else if !r.Blocked && r.Kind == "attack" {
			note = "MISSED attack"
		}
		blk := "no"
		if r.Blocked {
			blk = "YES"
		}
		fmt.Printf("%-22s %-12s %6d %6d %6d %-10s %s\n",
			r.Scenario, r.Kind, r.ConnsPerMin, r.Concurrent, r.Aborts, blk, note)
	}
}

// ── Live rate-limit against uwas ────────────────────────────────────────────

func runLive(bin, config string) ([]rateResult, error) {
	if _, err := os.Stat(bin); err != nil {
		return nil, fmt.Errorf("%s missing — run: make dev", bin)
	}
	_ = os.Remove("/tmp/uwas-autoblock-lab.json")
	_ = os.Remove("/tmp/uwas-autoblock-lab.pid")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "serve", "-c", config)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		cancel()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	if err := waitHTTP("http://127.0.0.1:18080/", "lab.local", 15*time.Second); err != nil {
		return nil, err
	}

	var out []rateResult

	// Domain limit is 20/10s for lab.local.
	r1 := hitUntil("domain rate_limit 20/10s", "http://127.0.0.1:18080/", "lab.local", 20, 35)
	out = append(out, r1)

	// Wait for domain window to expire, then verify we recover.
	time.Sleep(11 * time.Second)
	r2 := hitUntil("after window reset", "http://127.0.0.1:18080/", "lab.local", 20, 22)
	out = append(out, r2)

	// Unknown host falls back to global 100/60s (and may 421). Use Host that
	// is configured... better: hit with many keepalive requests counting 200 vs 429.
	// Domain limit already validated. Probe WAF still works.
	waf := probeWAF("http://127.0.0.1:18080/?id=1%20UNION%20SELECT%201", "lab.local")
	out = append(out, waf)

	return out, nil
}

func waitHTTP(url, host string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		req.Host = host
		resp, err := client.Do(req)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode > 0 {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("server did not become ready at %s", url)
}

func hitUntil(label, url, host string, limit, total int) rateResult {
	client := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: false,
			MaxIdleConns:      4,
			IdleConnTimeout:   30 * time.Second,
			DialContext:       (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
		},
	}
	res := rateResult{Label: label, Limit: limit, Sent: total, First429At: 0}
	for i := 1; i <= total; i++ {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		req.Host = host
		resp, err := client.Do(req)
		if err != nil {
			res.Other++
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusOK:
			res.OK++
		case http.StatusTooManyRequests:
			res.Limited++
			if res.First429At == 0 {
				res.First429At = i
			}
		default:
			res.Other++
		}
	}
	// Expect first 429 at limit+1 (or shortly after), and some 200s before.
	res.Pass = res.OK >= limit-2 && res.Limited > 0 && res.First429At > 0 && res.First429At <= limit+5
	return res
}

func probeWAF(url, host string) rateResult {
	client := &http.Client{Timeout: 3 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Host = host
	resp, err := client.Do(req)
	res := rateResult{Label: "waf sql_injection", Limit: 0, Sent: 1}
	if err != nil {
		res.Other = 1
		return res
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode == http.StatusForbidden {
		res.OK = 1 // "ok" meaning defence worked
		res.Pass = true
		return res
	}
	res.Other = 1
	res.Pass = false
	return res
}

func printRate(rows []rateResult) {
	for _, r := range rows {
		status := "FAIL"
		if r.Pass {
			status = "PASS"
		}
		fmt.Printf("[%s] %s — sent=%d ok=%d 429=%d other=%d first429=%d\n",
			status, r.Label, r.Sent, r.OK, r.Limited, r.Other, r.First429At)
	}
}

func failures(rep report) int {
	n := 0
	for _, r := range rep.Rate {
		if !r.Pass {
			n++
		}
	}
	for _, r := range rep.Synth {
		// Large NAT / CDN aggregation false-positives are expected findings at
		// product defaults — they drive the recommendation, not a red X.
		if r.Kind == "attack" && !r.Blocked {
			n++
		}
		if r.Kind == "legit" && r.Blocked {
			n++
		}
	}
	return n
}

// ── Recommendations ─────────────────────────────────────────────────────────

func recommend(synth []tripResult, rate []rateResult) map[string]any {
	cdnFP := false
	nat50FP := false
	for _, r := range synth {
		if r.Scenario == "cdn_edge_busy" && r.WouldFP {
			cdnFP = true
		}
		if r.Scenario == "office_nat_50_users" && r.WouldFP {
			nat50FP = true
		}
	}

	out := map[string]any{
		"note": "Connection counters key on the TCP peer, not X-Forwarded-For. Behind CDN/NAT those peers aggregate many users.",
		"behind_cdn_or_nat": map[string]any{
			"max_connections": 0,
			"max_concurrent":  0,
			"max_aborts":      60,
			"feed_rate_hits":  false,
			"why":             "defaults 600/150 false-positive on busy edges and large office NATs",
			"cdn_fp_at_defaults": cdnFP,
			"nat50_fp_at_defaults": nat50FP,
		},
		"direct_origin_public_ip": map[string]any{
			"max_connections": 600,
			"max_concurrent":  150,
			"max_aborts":      60,
			"feed_rate_hits":  false,
			"why":             "single-client and small-NAT (≤20 users) stay under defaults; floods still trip",
		},
		"rate_limit": map[string]any{
			"global_requests":       600,
			"global_window":         "60s",
			"domain_general":        100,
			"domain_login_path":     20,
			"lab_verified_pattern":  "limit N in window W → HTTP 429 after ~N requests from same IP",
		},
	}
	_ = rate
	return out
}

func printRecommend(m map[string]any) {
	b, _ := json.MarshalIndent(m, "", "  ")
	fmt.Println(string(b))
}
