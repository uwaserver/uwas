package watchdog

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// sdPeer is a stand-in for systemd's notify socket.
type sdPeer struct {
	ln   *net.UnixConn
	sock string
}

func newSDPeer(t *testing.T) *sdPeer {
	t.Helper()
	dir, err := os.MkdirTemp("", "wd") // short path: unix socket names are capped at ~108 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "n")
	ln, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: sock, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	t.Setenv("NOTIFY_SOCKET", sock)
	return &sdPeer{ln: ln, sock: sock}
}

// received returns every datagram queued ahead of a sentinel sent last.
func (p *sdPeer) received(t *testing.T) []string {
	t.Helper()
	c, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: p.sock, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("SENTINEL")); err != nil {
		t.Fatal(err)
	}
	var out []string
	buf := make([]byte, 512)
	for {
		p.ln.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, _, err := p.ln.ReadFromUnix(buf)
		if err != nil {
			t.Fatalf("reading the notify socket: %v", err)
		}
		if m := string(buf[:n]); m == "SENTINEL" {
			return out
		} else {
			out = append(out, m)
		}
	}
}

func (p *sdPeer) discard() {
	buf := make([]byte, 512)
	for {
		if _, _, err := p.ln.ReadFromUnix(buf); err != nil {
			return
		}
	}
}

func dogWithProbes(t *testing.T, failures int, outcomes ...bool) *Watchdog {
	t.Helper()
	var i atomic.Int64
	w := &Watchdog{
		cfg:      Config{Enabled: true, Interval: time.Millisecond, Timeout: 50 * time.Millisecond, Failures: failures},
		log:      logger.New("error", "text"),
		notifier: NewNotifier(),
		probe: func(context.Context) error {
			if k := int(i.Add(1)) - 1; k < len(outcomes) && !outcomes[k] {
				return errors.New("probe failed")
			}
			return nil
		},
	}
	w.healthy.Store(true)
	t.Cleanup(w.notifier.Close)
	return w
}

func statusLines(msgs []string) []string {
	var out []string
	for _, m := range msgs {
		if strings.HasPrefix(m, "STATUS=") {
			out = append(out, m)
		}
	}
	return out
}

// After the watchdog reported the server unresponsive, a recovered probe must
// clear that status; otherwise systemctl status keeps saying "unresponsive"
// for a server that answers again (F905).
func TestRecoveryClearsUnresponsiveStatus(t *testing.T) {
	peer := newSDPeer(t)
	w := dogWithProbes(t, 2, false, false, true)
	for i := 0; i < 3; i++ {
		w.tick(context.Background())
	}
	st := statusLines(peer.received(t))
	if len(st) != 2 || !strings.HasPrefix(st[0], "STATUS=unresponsive") || st[1] != "STATUS=" {
		t.Fatalf("statuses = %q, want the unresponsive report followed by a clear", st)
	}

	// A run that never reached the threshold has nothing to clear.
	peer2 := newSDPeer(t)
	w2 := dogWithProbes(t, 2, false, true)
	for i := 0; i < 2; i++ {
		w2.tick(context.Background())
	}
	if st := statusLines(peer2.received(t)); len(st) != 0 {
		t.Fatalf("below-threshold run sent statuses %q", st)
	}
}

// Close releases the socket for good: a late ping or status from the Run
// goroutine must neither reach systemd after STOPPING=1 nor leave a
// connection open (F907).
func TestSendAfterCloseDoesNotReopenSocket(t *testing.T) {
	peer := newSDPeer(t)
	n := NewNotifier()
	if err := n.Send("A"); err != nil {
		t.Fatal(err)
	}
	n.Close()
	n.Close()
	if err := n.Send("AFTER"); err == nil {
		t.Fatal("Send after Close succeeded")
	}
	n.mu.Lock()
	reopened := n.conn != nil
	n.mu.Unlock()
	if reopened {
		t.Fatal("Send after Close reopened the socket")
	}
	if got := peer.received(t); len(got) != 1 || got[0] != "A" {
		t.Fatalf("datagrams = %q, want only the one sent before Close", got)
	}

	// Through the watchdog: a probe finishing after NotifyStopping.
	peer2 := newSDPeer(t)
	w := dogWithProbes(t, 1, true, false)
	w.NotifyStopping()
	w.tick(context.Background()) // would send WATCHDOG=1
	w.tick(context.Background()) // would send STATUS=unresponsive
	if got := peer2.received(t); len(got) != 1 || got[0] != "STOPPING=1" {
		t.Fatalf("datagrams = %q, want only STOPPING=1", got)
	}
}

// A peer that stops reading fills its queue. Send must give up instead of
// parking forever with the mutex held, which would stall the watchdog ping,
// Close and with them shutdown (F906).
func TestSendGivesUpWhenPeerQueueIsFull(t *testing.T) {
	old := sendTimeout
	sendTimeout = 50 * time.Millisecond
	t.Cleanup(func() { sendTimeout = old })

	peer := newSDPeer(t)
	n := NewNotifier()
	var sent atomic.Int64
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 5000; i++ {
			if err := n.Send("WATCHDOG=1"); err != nil {
				done <- err
				return
			}
			sent.Add(1)
		}
		done <- nil
	}()

	var err error
	select {
	case err = <-done:
	case <-time.After(30 * time.Second):
		stuckAt := sent.Load()
		go peer.discard() // release the stuck sender so the test can end
		<-done
		n.Close()
		t.Fatalf("Send blocked on a full peer queue after %d datagrams", stuckAt)
	}
	if err == nil {
		t.Fatal("5000 sends to a peer that never reads all succeeded")
	}

	closed := make(chan struct{})
	go func() { n.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return after the failed send")
	}
	runtime.KeepAlive(peer)
}
