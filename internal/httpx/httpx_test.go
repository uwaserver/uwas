package httpx

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewClientAppliesTimeout(t *testing.T) {
	// Server hangs forever (well, until the test ends).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	c := NewClient(100 * time.Millisecond)
	start := time.Now()
	resp, err := c.Get(srv.URL)
	elapsed := time.Since(start)
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected timeout error, got nil")
	}
	if elapsed > 5*time.Second {
		t.Errorf("Get took %v, want under the 100ms client timeout (some grace)", elapsed)
	}
}

func TestNewClientZeroTimeoutFallsBack(t *testing.T) {
	c := NewClient(0)
	if c.Timeout == 0 {
		t.Error("zero timeout should fall back to a non-zero default")
	}
}

func TestDrainAndCloseHandlesNilBody(t *testing.T) {
	// Must not panic.
	DrainAndClose(nil)
}

type recordingCloser struct {
	io.Reader
	closed bool
}

func (r *recordingCloser) Close() error {
	r.closed = true
	return nil
}

func TestDrainAndCloseClosesBody(t *testing.T) {
	body := &recordingCloser{Reader: strings.NewReader("hello world")}
	DrainAndClose(body)
	if !body.closed {
		t.Error("body was not closed")
	}
}

func TestDrainAndCloseCapsReadAt4KB(t *testing.T) {
	// 1 MB of zeros — DrainAndClose must not read all of it.
	big := strings.NewReader(strings.Repeat("x", 1<<20))
	body := &recordingCloser{Reader: big}
	DrainAndClose(body)
	if !body.closed {
		t.Error("body was not closed")
	}
	// Position into the underlying reader tells us how much was drained.
	// strings.Reader.Len returns the unread portion.
	read := (1 << 20) - big.Len()
	if read > drainLimit {
		t.Errorf("DrainAndClose read %d bytes, want at most %d", read, drainLimit)
	}
}

// NewClient honors a request context cancellation — independent of
// the client.Timeout — through the underlying Transport.
func TestNewClientHonorsRequestContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	c := NewClient(30 * time.Second) // long client timeout
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Do(req)
	if err == nil {
		t.Fatal("expected context-cancellation error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "context") {
		t.Errorf("error = %v, want context-related", err)
	}
}
