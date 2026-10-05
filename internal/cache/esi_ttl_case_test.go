package cache

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestFragmentTTLDirectiveCase(t *testing.T) {
	for _, c := range []struct {
		cc   string
		want time.Duration
	}{{"public, max-age=300", 300 * time.Second}, {"public, Max-Age=300", 300 * time.Second}, {"MAX-AGE=120", 120 * time.Second}, {"  public, mAx-AgE=75  ", 75 * time.Second}, {"", 0}, {"public", 0}, {"max-age=0", 0}} {
		h := http.Header{}
		h.Set("Cache-Control", c.cc)
		if got := parseFragmentTTL(h); got != c.want {
			t.Errorf("%q: got %s want %s", c.cc, got, c.want)
		}
		if h.Get("Cache-Control") != c.cc {
			t.Error("header mutated")
		}
	}
	if !t.Failed() {
		fmt.Println("FIX VERIFIED")
	}
}
