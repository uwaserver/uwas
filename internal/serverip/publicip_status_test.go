package serverip

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

type regressionAudit52Body struct {
	io.Reader
	closed *int
}

func (b regressionAudit52Body) Close() error { *b.closed++; return nil }
func TestPublicIPStatusFallback(t *testing.T) {
	oldGet, oldURLs, oldInterfaces, oldAddrs := httpGet, publicIPURLs, netInterfaces, ifaceAddrs
	t.Cleanup(func() { httpGet = oldGet; publicIPURLs = oldURLs; netInterfaces = oldInterfaces; ifaceAddrs = oldAddrs })
	publicIPURLs = []string{"fixture:first", "fixture:second"}
	netInterfaces = func() ([]net.Interface, error) { return nil, errors.New("no interfaces") }
	for _, status := range []int{199, 200, 204, 299, 300, 404, 503} {
		closed, calls := 0, 0
		httpGet = func(_ *http.Client, url string) (*http.Response, error) {
			calls++
			code, ip := status, "192.0.2.1"
			if url == "fixture:second" {
				code, ip = 200, "192.0.2.2"
			}
			return &http.Response{StatusCode: code, Body: regressionAudit52Body{strings.NewReader(ip), &closed}}, nil
		}
		want, wantCalls := "192.0.2.2", 2
		if status >= 200 && status < 300 {
			want, wantCalls = "192.0.2.1", 1
		}
		if got := PublicIP(); got != want || calls != wantCalls || closed != calls {
			t.Fatal(status, got, calls, closed)
		}
	}
	closed := 0
	httpGet = func(_ *http.Client, _ string) (*http.Response, error) {
		return &http.Response{StatusCode: 500, Body: regressionAudit52Body{strings.NewReader("192.0.2.9"), &closed}}, nil
	}
	if got := PublicIP(); got != "" || closed != 2 {
		t.Fatal("no fallback", got, closed)
	}
	netInterfaces = func() ([]net.Interface, error) { return []net.Interface{{Name: "fixture", Flags: net.FlagUp}}, nil }
	ifaceAddrs = func(_ *net.Interface) ([]net.Addr, error) {
		return []net.Addr{&net.IPAddr{IP: net.ParseIP("192.0.2.7")}}, nil
	}
	if got := PublicIP(); got != "192.0.2.7" {
		t.Fatal("local fallback", got)
	}
	fmt.Println("FIX VERIFIED")
}
