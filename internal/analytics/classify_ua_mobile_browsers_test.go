package analytics

import "testing"

// F31100: browsers on iOS (and Edge on Android) carry a "Safari" token but not
// "Chrome"/"Edg/", so they were counted as Safari in the browser breakdown.
func TestClassifyUAThirdPartyMobileBrowsers(t *testing.T) {
	cases := []struct{ name, ua, want string }{
		{"chrome ios", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/120.0.6099.119 Mobile/15E148 Safari/604.1", "Chrome"},
		{"firefox ios", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) FxiOS/121.0 Mobile/15E148 Safari/605.1.15", "Firefox"},
		{"edge ios", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 EdgiOS/120.0.2210.126 Mobile/15E148 Safari/605.1.15", "Edge"},
		{"edge android", "Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36 EdgA/120.0.2210.91", "Edge"},
		// controls: unchanged classifications
		{"safari ios", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1", "Safari"},
		{"chrome desktop", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36", "Chrome"},
		{"firefox desktop", "Mozilla/5.0 (X11; Linux x86_64; rv:121.0) Gecko/20100101 Firefox/121.0", "Firefox"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyUA(c.ua); got != c.want {
				t.Fatalf("EXPECTED: %s\nACTUAL: %s\nPROBLEM CONFIRMED", c.want, got)
			}
		})
	}
}
