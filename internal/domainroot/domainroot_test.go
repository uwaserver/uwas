package domainroot

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
	"github.com/uwaserver/uwas/internal/config"
)

func TestForDomainAppProxyUsesAppWorkDir(t *testing.T) {
	appRoot := t.TempDir()
	store := apps.NewStore(filepath.Join(t.TempDir(), "apps.d"))
	if err := store.Save(&apps.App{Name: "demo", Runtime: apps.RuntimeNode, WorkDir: appRoot}); err != nil {
		t.Fatal(err)
	}

	root, err := ForDomain(config.Domain{
		Host: "demo.example.com",
		Type: "proxy",
		Root: filepath.Join(t.TempDir(), "demo.example.com", "public_html"),
		Proxy: config.ProxyConfig{Upstreams: []config.Upstream{
			{Address: "apps://demo"},
		}},
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	if root != appRoot {
		t.Fatalf("root = %q, want app work_dir %q", root, appRoot)
	}
}

func TestForDomainWithApps_AppNotFound(t *testing.T) {
	// ForDomainWithApps line 86-88: app == nil after store.Get.
	store := apps.NewStore(filepath.Join(t.TempDir(), "apps.d"))
	// Don't save any app — store.Get will return nil

	_, err := ForDomain(config.Domain{
		Host: "missing.example.com",
		Type: "proxy",
		Proxy: config.ProxyConfig{Upstreams: []config.Upstream{
			{Address: "apps://nonexistent-app"},
		}},
	}, store)
	if err == nil {
		t.Fatal("expected error for missing app")
	}
	if !strings.Contains(err.Error(), "app not found") {
		t.Errorf("error = %v, want 'app not found'", err)
	}
}

func TestForDomainAppProxyWithPortUsesAppWorkDir(t *testing.T) {
	appRoot := t.TempDir()
	store := apps.NewStore(filepath.Join(t.TempDir(), "apps.d"))
	if err := store.Save(&apps.App{Name: "demo", Runtime: apps.RuntimeNode, WorkDir: appRoot, Port: 3000, Ports: []int{5173}}); err != nil {
		t.Fatal(err)
	}

	root, err := ForDomain(config.Domain{
		Host: "www.example.com",
		Type: "proxy",
		Proxy: config.ProxyConfig{Upstreams: []config.Upstream{
			{Address: "apps://demo:5173"},
		}},
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	if root != appRoot {
		t.Fatalf("root = %q, want %q", root, appRoot)
	}
}

func TestForDomainLegacyLocalAppProxyUsesAppWorkDir(t *testing.T) {
	appRoot := t.TempDir()
	store := apps.NewStore(filepath.Join(t.TempDir(), "apps.d"))
	if err := store.Save(&apps.App{Name: "demo", Runtime: apps.RuntimeNode, WorkDir: appRoot}); err != nil {
		t.Fatal(err)
	}

	root, err := ForDomainWithApps(config.Domain{
		Host: "demo.example.com",
		Type: "proxy",
		Root: filepath.Join(t.TempDir(), "demo.example.com", "public_html"),
		Proxy: config.ProxyConfig{Upstreams: []config.Upstream{
			{Address: "http://127.0.0.1:3017"},
		}},
	}, store, []apps.Instance{
		{Name: "demo", Port: 3017, WorkDir: appRoot},
	})
	if err != nil {
		t.Fatal(err)
	}
	if root != appRoot {
		t.Fatalf("root = %q, want app work_dir %q", root, appRoot)
	}
}

func TestForDomainStaticUsesDomainRoot(t *testing.T) {
	want := filepath.Join(t.TempDir(), "site", "public_html")
	root, err := ForDomain(config.Domain{Host: "site.test", Type: "static", Root: want}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if root != want {
		t.Fatalf("root = %q, want %q", root, want)
	}
}

func TestAppName(t *testing.T) {
	cases := []struct {
		name     string
		domain   config.Domain
		wantName string
		wantOK   bool
	}{
		{
			name:   "non-proxy returns false",
			domain: config.Domain{Type: "static", Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: "apps://demo"}}}},
			wantOK: false,
		},
		{
			name:   "no upstreams returns false",
			domain: config.Domain{Type: "proxy"},
			wantOK: false,
		},
		{
			name:   "non-apps upstream skipped",
			domain: config.Domain{Type: "proxy", Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: "http://127.0.0.1:3000"}}}},
			wantOK: false,
		},
		{
			name:     "plain apps name",
			domain:   config.Domain{Type: "proxy", Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: "apps://demo"}}}},
			wantName: "demo",
			wantOK:   true,
		},
		{
			name:     "uppercase scheme accepted",
			domain:   config.Domain{Type: "proxy", Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: "APPS://demo"}}}},
			wantName: "demo",
			wantOK:   true,
		},
		{
			name:     "name with port stripped",
			domain:   config.Domain{Type: "proxy", Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: "apps://demo:5173"}}}},
			wantName: "demo",
			wantOK:   true,
		},
		{
			name:     "name with path stripped",
			domain:   config.Domain{Type: "proxy", Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: "apps://demo/api"}}}},
			wantName: "demo",
			wantOK:   true,
		},
		{
			name:     "name with query stripped",
			domain:   config.Domain{Type: "proxy", Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: "apps://demo?x=1"}}}},
			wantName: "demo",
			wantOK:   true,
		},
		{
			name:     "name with fragment stripped",
			domain:   config.Domain{Type: "proxy", Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: "apps://demo#frag"}}}},
			wantName: "demo",
			wantOK:   true,
		},
		{
			name:   "empty name after scheme returns false",
			domain: config.Domain{Type: "proxy", Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: "apps://"}}}},
			wantOK: false,
		},
		{
			name:   "empty name with port returns false",
			domain: config.Domain{Type: "proxy", Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: "apps://:5173"}}}},
			wantOK: false,
		},
		{
			name: "first apps upstream wins over later",
			domain: config.Domain{Type: "proxy", Proxy: config.ProxyConfig{Upstreams: []config.Upstream{
				{Address: "http://127.0.0.1:3000"},
				{Address: " apps://second "},
			}}},
			wantName: "second",
			wantOK:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := AppName(tc.domain)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.wantName {
				t.Fatalf("name = %q, want %q", got, tc.wantName)
			}
		})
	}
}

func TestLocalAppName(t *testing.T) {
	insts := []apps.Instance{
		{Name: "demo", Port: 3017, WorkDir: "/srv/demo"},
		{Name: "noworkdir", Port: 4000, WorkDir: "   "},
	}

	t.Run("non-proxy returns false", func(t *testing.T) {
		_, ok := LocalAppName(config.Domain{Type: "static", Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: "http://127.0.0.1:3017"}}}}, insts)
		if ok {
			t.Fatal("expected ok=false for non-proxy")
		}
	})

	t.Run("matching loopback port", func(t *testing.T) {
		name, ok := LocalAppName(config.Domain{Type: "proxy", Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: "http://127.0.0.1:3017"}}}}, insts)
		if !ok || name != "demo" {
			t.Fatalf("name=%q ok=%v, want demo true", name, ok)
		}
	})

	t.Run("no matching port", func(t *testing.T) {
		_, ok := LocalAppName(config.Domain{Type: "proxy", Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: "http://127.0.0.1:9999"}}}}, insts)
		if ok {
			t.Fatal("expected ok=false for unmatched port")
		}
	})

	t.Run("matching port but empty workdir skipped", func(t *testing.T) {
		_, ok := LocalAppName(config.Domain{Type: "proxy", Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: "http://127.0.0.1:4000"}}}}, insts)
		if ok {
			t.Fatal("expected ok=false when instance WorkDir is empty")
		}
	})

	t.Run("non-loopback upstream ignored", func(t *testing.T) {
		_, ok := LocalAppName(config.Domain{Type: "proxy", Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: "http://10.0.0.5:3017"}}}}, insts)
		if ok {
			t.Fatal("expected ok=false for non-loopback upstream")
		}
	})
}

func TestForDomainWithAppsErrors(t *testing.T) {
	t.Run("app proxy but nil store", func(t *testing.T) {
		_, err := ForDomain(config.Domain{
			Host:  "demo.example.com",
			Type:  "proxy",
			Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: "apps://demo"}}},
		}, nil)
		if err == nil {
			t.Fatal("expected error when store is nil")
		}
		if !strings.Contains(err.Error(), "apps manager is not initialized") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("store.Get returns error for invalid name", func(t *testing.T) {
		store := apps.NewStore(filepath.Join(t.TempDir(), "apps.d"))
		// "bad.name" passes AppName extraction (dot is not in /?#:) but
		// fails isValidAppName inside Store.Get, surfacing an error.
		_, err := ForDomain(config.Domain{
			Host:  "demo.example.com",
			Type:  "proxy",
			Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: "apps://bad.name"}}},
		}, store)
		if err == nil {
			t.Fatal("expected error for invalid app name")
		}
		if !strings.Contains(err.Error(), "root unavailable") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("app not found", func(t *testing.T) {
		store := apps.NewStore(filepath.Join(t.TempDir(), "apps.d"))
		_, err := ForDomain(config.Domain{
			Host:  "demo.example.com",
			Type:  "proxy",
			Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: "apps://missing"}}},
		}, store)
		if err == nil {
			t.Fatal("expected error for missing app")
		}
		if !strings.Contains(err.Error(), "app not found") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestFallback(t *testing.T) {
	if got := Fallback("", "example.com"); got != "" {
		t.Fatalf("Fallback empty webroot = %q, want empty", got)
	}
	want := filepath.Join("/var/www", "example.com", "public_html")
	if got := Fallback("/var/www", "example.com"); got != want {
		t.Fatalf("Fallback = %q, want %q", got, want)
	}
}

func TestLocalUpstreamPort(t *testing.T) {
	cases := []struct {
		name     string
		addr     string
		wantPort int
		wantOK   bool
	}{
		{name: "loopback ipv4 with port", addr: "http://127.0.0.1:3017", wantPort: 3017, wantOK: true},
		{name: "bare host:port normalized", addr: "127.0.0.1:8080", wantPort: 8080, wantOK: true},
		{name: "localhost loopback", addr: "http://localhost:9000", wantPort: 9000, wantOK: true},
		{name: "ipv6 loopback", addr: "http://[::1]:7000", wantPort: 7000, wantOK: true},
		{name: "empty address", addr: "", wantOK: false},
		{name: "no host", addr: "http://", wantOK: false},
		{name: "no port", addr: "http://127.0.0.1", wantOK: false},
		{name: "non-loopback host", addr: "http://10.0.0.1:8080", wantOK: false},
		{name: "invalid port not numeric", addr: "http://127.0.0.1:abc", wantOK: false},
		{name: "zero port rejected", addr: "http://127.0.0.1:0", wantOK: false},
		{name: "parse error from control char", addr: "http://127.0.0.1:80\x7f", wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port, ok := localUpstreamPort(tc.addr)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (port=%d)", ok, tc.wantOK, port)
			}
			if ok && port != tc.wantPort {
				t.Fatalf("port = %d, want %d", port, tc.wantPort)
			}
		})
	}
}

func TestIsLoopbackHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"127.0.0.1", true},
		{"[::1]", true},
		{"LOCALHOST", true},
		{"10.0.0.1", false},
		{"example.com", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isLoopbackHost(tc.host); got != tc.want {
			t.Fatalf("isLoopbackHost(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}
