// Command loadgen is a dependency-free HTTP load generator for UWAS
// response-time work.
//
// Two modes:
//
//	closed loop (-rate 0)  each connection sends the next request as soon as
//	                       the previous reply lands. Measures peak throughput.
//	open loop  (-rate N)   requests are scheduled at a fixed N/sec regardless
//	                       of how fast the server answers, and latency is
//	                       measured from the *scheduled* send time. This is
//	                       the number to trust for "how fast does it answer" —
//	                       a closed loop hides queueing behind its own
//	                       backpressure (coordinated omission).
//
// Results go to stdout as JSON so ab.sh can diff two runs.
package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type result struct {
	Label       string         `json:"label"`
	URL         string         `json:"url"`
	Conns       int            `json:"conns"`
	Rate        int            `json:"rate"` // 0 = closed loop
	DurationSec float64        `json:"duration_sec"`
	Requests    int64          `json:"requests"`
	Errors      int64          `json:"errors"`
	Timeouts    int64          `json:"timeouts"`
	BytesRead   int64          `json:"bytes_read"`
	RPS         float64        `json:"rps"`
	Status      map[string]int `json:"status"`
	LatencyMS   latency        `json:"latency_ms"`
}

type latency struct {
	Min   float64 `json:"min"`
	Mean  float64 `json:"mean"`
	P50   float64 `json:"p50"`
	P75   float64 `json:"p75"`
	P90   float64 `json:"p90"`
	P99   float64 `json:"p99"`
	P999  float64 `json:"p999"`
	Max   float64 `json:"max"`
	Count int     `json:"count"`
}

func main() {
	var (
		url     = flag.String("url", "http://127.0.0.1:19180/index.html", "target URL")
		host    = flag.String("host", "", "override Host header (vhost selection)")
		conns   = flag.Int("c", 50, "concurrent connections")
		dur     = flag.Duration("d", 10*time.Second, "measurement duration")
		warmup  = flag.Duration("warmup", 2*time.Second, "warmup duration (not measured)")
		rate    = flag.Int("rate", 0, "open-loop request rate per second (0 = closed loop)")
		timeout = flag.Duration("timeout", 5*time.Second, "per-request timeout")
		label   = flag.String("label", "run", "label recorded in the JSON output")
		accEnc  = flag.String("accept-encoding", "", "Accept-Encoding header (empty = identity)")
	)
	flag.Parse()

	if *warmup > 0 {
		run(*url, *host, *accEnc, *conns, *warmup, *rate, *timeout)
	}
	res := run(*url, *host, *accEnc, *conns, *dur, *rate, *timeout)
	res.Label = *label

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		fmt.Fprintln(os.Stderr, "loadgen: encode:", err)
		os.Exit(1)
	}
	if res.Requests == 0 {
		fmt.Fprintln(os.Stderr, "loadgen: no successful requests")
		os.Exit(1)
	}
}

// newClient gives every worker its own transport so the connection count is
// exactly -c. A shared transport would pool connections and make the real
// concurrency depend on how fast the server drains them.
func newClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:         (&net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConns:        1,
			MaxIdleConnsPerHost: 1,
			MaxConnsPerHost:     1,
			IdleConnTimeout:     60 * time.Second,
			DisableCompression:  true, // we control Accept-Encoding by hand
			ForceAttemptHTTP2:   false,
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // local test targets use self-signed certs
		},
	}
}

type counters struct {
	requests atomic.Int64
	errors   atomic.Int64
	timeouts atomic.Int64
	bytes    atomic.Int64

	statusMu sync.Mutex
	status   map[int]int
}

func (c *counters) recordStatus(code int) {
	c.statusMu.Lock()
	c.status[code]++
	c.statusMu.Unlock()
}

func run(url, host, accEnc string, conns int, dur time.Duration, rate int, timeout time.Duration) result {
	c := &counters{status: make(map[int]int)}
	samples := make([][]int64, conns)

	// Pre-size the sample slices so append never reallocates mid-measurement.
	est := 2048
	if rate > 0 {
		est = rate*int(dur.Seconds())/conns + 1024
	}

	deadline := time.Now().Add(dur)
	var wg sync.WaitGroup

	// slots carries scheduled send times in open-loop mode.
	var slots chan time.Time
	if rate > 0 {
		slots = make(chan time.Time, rate)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer close(slots)
			interval := time.Duration(float64(time.Second) / float64(rate))
			next := time.Now()
			for next.Before(deadline) {
				if d := time.Until(next); d > 0 {
					time.Sleep(d)
				}
				select {
				case slots <- next:
				default:
					// Generator outran the workers: the request is still
					// counted as late rather than silently dropped, so the
					// latency numbers stay honest.
					c.errors.Add(1)
				}
				next = next.Add(interval)
			}
		}()
	}

	start := time.Now()
	for i := 0; i < conns; i++ {
		samples[i] = make([]int64, 0, est)
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			client := newClient(timeout)
			defer client.CloseIdleConnections()

			req, err := http.NewRequest(http.MethodGet, url, nil)
			if err != nil {
				c.errors.Add(1)
				return
			}
			if host != "" {
				req.Host = host
			}
			if accEnc != "" {
				req.Header.Set("Accept-Encoding", accEnc)
			}
			req.Header.Set("User-Agent", "uwas-loadgen/1.0")

			buf := make([]byte, 32*1024)

			for {
				var sched time.Time
				if slots != nil {
					s, ok := <-slots
					if !ok {
						return
					}
					sched = s
				} else {
					if !time.Now().Before(deadline) {
						return
					}
					sched = time.Now()
				}

				resp, err := client.Do(req)
				if err != nil {
					c.errors.Add(1)
					if os.IsTimeout(err) {
						c.timeouts.Add(1)
					}
					continue
				}
				n, _ := io.CopyBuffer(io.Discard, resp.Body, buf)
				resp.Body.Close()

				// Latency runs from the scheduled time, not the dial time, so
				// open-loop runs expose queueing delay instead of hiding it.
				samples[idx] = append(samples[idx], int64(time.Since(sched)))
				c.requests.Add(1)
				c.bytes.Add(n)
				c.recordStatus(resp.StatusCode)
			}
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)

	var all []int64
	for _, s := range samples {
		all = append(all, s...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })

	status := make(map[string]int, len(c.status))
	for k, v := range c.status {
		status[fmt.Sprintf("%d", k)] = v
	}

	return result{
		URL:         url,
		Conns:       conns,
		Rate:        rate,
		DurationSec: elapsed.Seconds(),
		Requests:    c.requests.Load(),
		Errors:      c.errors.Load(),
		Timeouts:    c.timeouts.Load(),
		BytesRead:   c.bytes.Load(),
		RPS:         float64(c.requests.Load()) / elapsed.Seconds(),
		Status:      status,
		LatencyMS:   summarize(all),
	}
}

func summarize(sorted []int64) latency {
	if len(sorted) == 0 {
		return latency{}
	}
	ms := func(n int64) float64 { return float64(n) / float64(time.Millisecond) }
	pct := func(p float64) float64 {
		i := int(p * float64(len(sorted)))
		if i >= len(sorted) {
			i = len(sorted) - 1
		}
		return ms(sorted[i])
	}
	var sum int64
	for _, v := range sorted {
		sum += v
	}
	return latency{
		Min:   ms(sorted[0]),
		Mean:  ms(sum / int64(len(sorted))),
		P50:   pct(0.50),
		P75:   pct(0.75),
		P90:   pct(0.90),
		P99:   pct(0.99),
		P999:  pct(0.999),
		Max:   ms(sorted[len(sorted)-1]),
		Count: len(sorted),
	}
}
