package config

import "time"

func applyDefaults(cfg *Config) {
	g := &cfg.Global

	if g.WorkerCount == "" {
		g.WorkerCount = "auto"
	}
	if g.MaxConnections == 0 {
		g.MaxConnections = 65536
	}
	if g.HTTPListen == "" {
		g.HTTPListen = ":80"
	}
	if g.HTTPSListen == "" {
		g.HTTPSListen = ":443"
	}
	if g.PIDFile == "" {
		g.PIDFile = "/var/run/uwas.pid"
	}
	if g.LogLevel == "" {
		g.LogLevel = "info"
	}
	if g.LogFormat == "" {
		g.LogFormat = "text"
	}

	// Timeouts
	if g.Timeouts.Read.Duration == 0 {
		g.Timeouts.Read.Duration = 30 * time.Second
	}
	if g.Timeouts.Write.Duration == 0 {
		g.Timeouts.Write.Duration = 60 * time.Second
	}
	if g.Timeouts.Idle.Duration == 0 {
		g.Timeouts.Idle.Duration = 120 * time.Second
	}
	if g.Timeouts.ShutdownGrace.Duration == 0 {
		g.Timeouts.ShutdownGrace.Duration = 30 * time.Second
	}

	// Admin
	if g.Admin.Listen == "" {
		g.Admin.Listen = "127.0.0.1:9443"
	}

	// MCP
	if g.MCP.Listen == "" {
		g.MCP.Listen = "127.0.0.1:9444"
	}

	// ACME
	if g.ACME.CAURL == "" {
		g.ACME.CAURL = "https://acme-v02.api.letsencrypt.org/directory"
	}
	if g.ACME.Storage == "" {
		g.ACME.Storage = "/var/lib/uwas/certs"
	}

	// Cache
	if g.Cache.MemoryLimit == 0 {
		g.Cache.MemoryLimit = 512 * MB
	}
	if g.Cache.DiskPath == "" {
		g.Cache.DiskPath = "/var/cache/uwas"
	}
	if g.Cache.DiskLimit == 0 {
		g.Cache.DiskLimit = 10 * GB
	}
	if g.Cache.DefaultTTL == 0 {
		g.Cache.DefaultTTL = 3600
	}
	if g.Cache.GraceTTL == 0 {
		g.Cache.GraceTTL = 86400
	}

	// Domain defaults
	for i := range cfg.Domains {
		d := &cfg.Domains[i]
		if d.Type == "" {
			d.Type = "static"
		}
		if d.SSL.Mode == "" {
			d.SSL.Mode = "off"
		}
		if d.SSL.MinVersion == "" {
			d.SSL.MinVersion = "1.2"
		}
		if d.Htaccess.Mode == "" {
			d.Htaccess.Mode = "off"
		}
		if d.Compression.MinSize == 0 {
			d.Compression.MinSize = 1024
		}

		// PHP defaults
		if d.Type == "php" {
			if d.PHP.FPMAddress == "" {
				d.PHP.FPMAddress = "unix:/var/run/php/php-fpm.sock"
			}
			if len(d.PHP.IndexFiles) == 0 {
				d.PHP.IndexFiles = []string{"index.php", "index.html"}
			}
			if d.PHP.MaxUpload == 0 {
				d.PHP.MaxUpload = 64 * MB
			}
			if d.PHP.Timeout.Duration == 0 {
				d.PHP.Timeout.Duration = 300 * time.Second
			}
		}

		// Index files
		if len(d.IndexFiles) == 0 {
			d.IndexFiles = []string{"index.html", "index.htm"}
		}
	}
}
