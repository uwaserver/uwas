package rewrite

import (
	"fmt"
	"net/http/httptest"
	"testing"
)

func rewriteIPv6Name(host string) string {
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = host
	return BuildVariables(r, "/root", "/root/index", false).ServerName
}
func TestBuildVariablesIPv6WithoutPort(t *testing.T) {
	if got := rewriteIPv6Name("example.com:8080"); got != "example.com" {
		t.Fatal("CONTROL FAILED", got)
	}
	fmt.Println("CONTROL PASSED")
	got := rewriteIPv6Name("[2001:db8::1]")
	fmt.Printf("EXPECTED: [2001:db8::1] ACTUAL: %s\n", got)
	if got != "[2001:db8::1]" {
		fmt.Println("PROBLEM CONFIRMED")
		t.FailNow()
	}
	fmt.Println("PROBLEM NOT REPRODUCED")
	for _, tc := range []struct{ host, want string }{{"[2001:db8::1]:8080", "[2001:db8::1]"}, {"[::1]", "[::1]"}, {"example.com", "example.com"}, {"", ""}} {
		if got := rewriteIPv6Name(tc.host); got != tc.want {
			t.Fatalf("%q: got %q want %q", tc.host, got, tc.want)
		}
	}
	fmt.Println("FIX VERIFIED")
}
