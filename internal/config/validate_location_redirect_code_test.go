package config

import "testing"

// A location redirect_code outside the redirect set reached http.Redirect,
// which panics for a status above 999 and otherwise answered a non-redirect
// status with a Location header (F54000).
func TestValidateLocationRedirectCode(t *testing.T) {
	mk := func(code int) *Config {
		return &Config{
			Global: GlobalConfig{LogLevel: "info", LogFormat: "text"},
			Domains: []Domain{{
				Host: "a.example.com", Type: "static", Root: "/tmp",
				SSL:       SSLConfig{Mode: "off"},
				Locations: []LocationConfig{{Match: "/old", Redirect: "/new", RedirectCode: code}},
			}},
		}
	}
	for _, ok := range []int{0, 301, 302, 303, 307, 308} {
		if err := Validate(mk(ok)); err != nil {
			t.Errorf("redirect_code %d rejected: %v", ok, err)
		}
	}
	for _, bad := range []int{-1, 5, 200, 404, 1000, 99999} {
		if err := Validate(mk(bad)); err == nil {
			t.Errorf("redirect_code %d accepted", bad)
		}
	}
}
