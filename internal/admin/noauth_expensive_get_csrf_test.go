package admin

import "testing"

// F45000: the no-credentials guard (F15800) meant to refuse a web page's
// expensive GET (a full config, database or backup export) by looking at the
// Origin header, but a browser sends no Origin on a sub-resource GET
// (<img src>, <script src>, a link prefetch). A page the operator merely visits
// could therefore make the admin API run an export on every load. Every modern
// browser labels such a request Sec-Fetch-Site: cross-site, which the guard now
// refuses when no Origin is present to judge by.
func TestNoAuthAdminRefusesCrossSiteExpensiveGetWithoutOrigin(t *testing.T) {
	h := noAuthChain(t, "")
	loop := map[string]string{"Host": "127.0.0.1:9443"}
	with := func(kv ...string) map[string]string {
		m := map[string]string{"Host": "127.0.0.1:9443"}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	cases := []struct {
		name string
		path string
		hdr  map[string]string
		want int
	}{
		{"curl-style client, no browser headers", "/api/v1/config/export", loop, 200},
		{"operator's own dashboard", "/api/v1/config/export", with("Sec-Fetch-Site", "same-origin", "Sec-Fetch-Mode", "cors"), 200},
		{"operator typed the URL", "/api/v1/config/export", with("Sec-Fetch-Site", "none", "Sec-Fetch-Mode", "navigate"), 200},
		{"cross-site <img> export", "/api/v1/config/export", with("Sec-Fetch-Site", "cross-site", "Sec-Fetch-Mode", "no-cors", "Sec-Fetch-Dest", "image"), 403},
		{"cross-site <script> database export", "/api/v1/database/x/export", with("Sec-Fetch-Site", "cross-site", "Sec-Fetch-Mode", "no-cors", "Sec-Fetch-Dest", "script"), 403},
		{"cross-site cheap read stays unreadable, not blocked here", "/api/v1/stats", with("Sec-Fetch-Site", "cross-site", "Sec-Fetch-Mode", "no-cors"), 200},
	}
	for _, c := range cases {
		got := chainDo(h, "GET", c.path, "", "", c.hdr)
		t.Logf("%s: EXPECTED %d ACTUAL %d", c.name, c.want, got)
		if got != c.want {
			t.Errorf("PROBLEM CONFIRMED: %s: GET %s = %d, want %d", c.name, c.path, got, c.want)
		}
	}
}
