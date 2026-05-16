package domainroot

import (
	"path/filepath"
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
