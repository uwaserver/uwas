package cache

import (
	"fmt"
	"testing"
)

func TestRedisTLSHostFromAddr(t *testing.T) {
	for _, c := range []struct{ addr, want string }{{"cache.example.test:6379", "cache.example.test"}, {"127.0.0.1:6379", "127.0.0.1"}, {"[2001:db8::1]:6379", "2001:db8::1"}, {"[::1]:6380", "::1"}, {"plainhost", "plainhost"}, {"2001:db8::1", "2001:db8::1"}, {"", ""}} {
		if got := hostFromAddr(c.addr); got != c.want {
			t.Errorf("hostFromAddr(%q)=%q want %q", c.addr, got, c.want)
		}
	}
	if !t.Failed() {
		fmt.Println("FIX VERIFIED")
	}
}
