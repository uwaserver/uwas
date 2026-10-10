package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestValidateDomainRejectsWhatLoadRejects pins that the admin API validators
// refuse domain values the loader would refuse. The admin persists accepted
// domains to domains.d, so a value only Load rejects stops the whole server
// from starting or reloading on the next restart.
func TestValidateDomainRejectsWhatLoadRejects(t *testing.T) {
	root := t.TempDir()
	base := func() Domain {
		return Domain{Host: "example.com", Type: "static", Root: root, SSL: SSLConfig{Mode: "off"}}
	}
	load := func(d Domain) error {
		dir := t.TempDir()
		main := "global:\n  web_root: " + dir + "\ndomains_dir: domains.d\n"
		if err := os.WriteFile(filepath.Join(dir, "uwas.yaml"), []byte(main), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "domains.d"), 0o755); err != nil {
			t.Fatal(err)
		}
		data, err := yaml.Marshal(&d)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "domains.d", "x.yaml"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = Load(filepath.Join(dir, "uwas.yaml"))
		return err
	}
	on := true
	cases := map[string]func(*Domain){
		"cache rule glob":         func(d *Domain) { d.Cache = DomainCache{Enabled: true, Rules: []CacheRule{{Match: "*.html"}}} },
		"ssl.min_version 1.4":     func(d *Domain) { d.SSL.MinVersion = "1.4" },
		"compression zstd":        func(d *Domain) { d.Compression = CompressionConfig{Enabled: &on, Algorithms: []string{"zstd"}} },
		"internal_aliases /etc":   func(d *Domain) { d.InternalAliases = []string{"/etc"} },
		"rewrite invalid regex":   func(d *Domain) { d.Rewrites = []RewriteRule{{Match: "(", To: "/"}} },
		"image format jpegxl":     func(d *Domain) { d.ImageOptimization.Enabled = true; d.ImageOptimization.Formats = []string{"jpegxl"} },
		"valid cache rule regex":  func(d *Domain) { d.Cache = DomainCache{Enabled: true, Rules: []CacheRule{{Match: `\.html$`}}} },
		"internal_aliases /srv/x": func(d *Domain) { d.InternalAliases = []string{"/srv/x"} },
	}
	for name, mut := range cases {
		d := base()
		mut(&d)
		loadErr := load(d)
		strict, partial := d, d
		if gotStrict, gotPartial := ValidateDomain(&strict), ValidateDomainPartial(&partial); (gotStrict == nil) != (loadErr == nil) || (gotPartial == nil) != (loadErr == nil) {
			t.Errorf("%s: ValidateDomain=%v ValidateDomainPartial=%v, but Load=%v", name, gotStrict, gotPartial, loadErr)
		}
	}

	// Partial mode keeps its documented leniency.
	px := Domain{Host: "p.example.com", Type: "proxy"}
	if err := ValidateDomainPartial(&px); err != nil {
		t.Errorf("partial proxy without upstreams rejected: %v", err)
	}
	rd := Domain{Host: "r.example.com", Type: "redirect"}
	if err := ValidateDomainPartial(&rd); err != nil {
		t.Errorf("partial redirect without target rejected: %v", err)
	}
}
