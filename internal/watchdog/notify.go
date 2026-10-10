// Package watchdog proves the server is still answering requests, and makes
// it fatal when it stops.
//
// systemd's Restart= only reacts to a process that exits. A server wedged by a
// flood — accept queue saturated, every worker blocked on a stalled connection
// — stays "active (running)" and serves nothing. That is the state behind a
// "domain health check status=down" with the process still up: nothing exits,
// so nothing restarts. This package probes the server's own listener from the
// inside and stops feeding systemd's watchdog when the probe stops passing,
// which turns a hang into a restart.
package watchdog

import (
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrNoSocket means the process was not started by systemd (or NotifyAccess
// is not set on the unit), so there is nothing to notify.
var ErrNoSocket = errors.New("NOTIFY_SOCKET not set")

// errClosed is returned by Send after Close.
var errClosed = errors.New("notifier closed")

// sendTimeout bounds one datagram write. A peer that has stopped reading
// fills its receive queue and would otherwise park Send forever with mu held,
// stalling the watchdog ping and Close (and with it shutdown). It is a
// variable only so tests can shorten it.
var sendTimeout = 2 * time.Second

// Notifier sends sd_notify datagrams to systemd.
type Notifier struct {
	mu   sync.Mutex
	conn *net.UnixConn
	addr *net.UnixAddr
	dead bool
	// closed is set by Close so a late Send cannot reopen the socket.
	closed bool
}

// NewNotifier resolves NOTIFY_SOCKET. It returns a usable Notifier even when
// the socket is absent; every send then reports ErrNoSocket and the caller can
// carry on without systemd.
func NewNotifier() *Notifier {
	socket := os.Getenv("NOTIFY_SOCKET")
	if socket == "" {
		return &Notifier{dead: true}
	}
	// A leading '@' selects the abstract namespace, which Go spells with a
	// leading NUL byte.
	if strings.HasPrefix(socket, "@") {
		socket = "\x00" + socket[1:]
	}
	return &Notifier{addr: &net.UnixAddr{Name: socket, Net: "unixgram"}}
}

// Available reports whether systemd notifications can be delivered.
func (n *Notifier) Available() bool { return n != nil && !n.dead }

// Send delivers one sd_notify state string, e.g. "READY=1" or "WATCHDOG=1".
func (n *Notifier) Send(state string) error {
	if n == nil || n.dead {
		return ErrNoSocket
	}
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.closed {
		return errClosed
	}
	if n.conn == nil {
		c, err := net.DialUnix("unixgram", nil, n.addr)
		if err != nil {
			return err
		}
		n.conn = c
	}
	n.conn.SetWriteDeadline(time.Now().Add(sendTimeout))
	if _, err := n.conn.Write([]byte(state)); err != nil {
		// The socket can be replaced across a systemd reload; drop the cached
		// connection so the next send redials rather than failing forever.
		n.conn.Close()
		n.conn = nil
		return err
	}
	return nil
}

// Close releases the notification socket.
func (n *Notifier) Close() {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.closed = true
	if n.conn != nil {
		n.conn.Close()
		n.conn = nil
	}
}

// SystemdInterval returns the watchdog period systemd expects from
// WATCHDOG_USEC, or 0 when the unit has no WatchdogSec.
//
// WATCHDOG_PID guards against a child process inheriting the environment and
// pinging on the main process's behalf.
func SystemdInterval() time.Duration {
	if pid := os.Getenv("WATCHDOG_PID"); pid != "" {
		if n, err := strconv.Atoi(pid); err != nil || n != os.Getpid() {
			return 0
		}
	}
	usec := os.Getenv("WATCHDOG_USEC")
	if usec == "" {
		return 0
	}
	n, err := strconv.ParseInt(usec, 10, 64)
	// Reject values that overflow when converted from microseconds to nanoseconds.
	if err != nil || n <= 0 || n > int64((1<<63-1)/time.Microsecond) {
		return 0
	}
	return time.Duration(n) * time.Microsecond
}
