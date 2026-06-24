package admin

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

func (s *Server) allocateSoftwarePort(start int) int {
	if start <= 0 || start > 65535 {
		start = 3001
	}
	for port := start; port <= 65535 && port < start+2000; port++ {
		if s.softwarePortUnavailableReason(port) == "" {
			return port
		}
	}
	for port := 3001; port <= 65535; port++ {
		if s.softwarePortUnavailableReason(port) == "" {
			return port
		}
	}
	return 0
}

func (s *Server) softwarePortUnavailableReason(port int) string {
	if port <= 0 || port > 65535 {
		return "port out of range"
	}
	if reason := softwareConfiguredPortConflict(port, s.collectSoftwareReservedPorts()); reason != "" {
		return reason
	}
	if !softwarePortAvailable(port) {
		return "already bound on 127.0.0.1"
	}
	return ""
}

func (s *Server) collectSoftwareReservedPorts() map[int]string {
	used := map[int]string{}
	items, _ := listSoftwareInstances()
	for _, inst := range items {
		if inst.HasWeb && inst.HostPort > 0 {
			used[inst.HostPort] = "software " + inst.Name
		}
	}
	if s.appsMgr != nil {
		for _, inst := range s.appsMgr.Instances() {
			if inst.Port > 0 {
				used[inst.Port] = "application " + inst.Name
			}
		}
		if apps, _, err := s.appsMgr.Store().Load(); err == nil {
			for _, app := range apps {
				if app.Port > 0 {
					used[app.Port] = "application " + app.Name
				}
			}
		}
	}
	s.configMu.RLock()
	collectListenPort := func(addr, label string) {
		if port := portFromListenAddr(addr); port > 0 {
			used[port] = label
		}
	}
	collectListenPort(s.config.Global.HTTPListen, "global http listener")
	collectListenPort(s.config.Global.HTTPSListen, "global https listener")
	collectListenPort(s.config.Global.SFTPListen, "global sftp listener")
	collectListenPort(s.config.Global.Admin.Listen, "admin listener")
	collectListenPort(s.config.Global.MCP.Listen, "mcp listener")
	for _, d := range s.config.Domains {
		for _, up := range d.Proxy.Upstreams {
			if port := portFromLocalHTTPURL(up.Address); port > 0 {
				used[port] = "domain proxy " + d.Host
			}
		}
		if d.App.Port > 0 {
			used[d.App.Port] = "domain app " + d.Host
		}
		if port := portFromListenAddr(d.PHP.FPMAddress); port > 0 {
			used[port] = "php-fpm " + d.Host
		}
		for _, loc := range d.Locations {
			if port := portFromLocalHTTPURL(loc.ProxyPass); port > 0 {
				used[port] = "domain location " + d.Host
			}
		}
	}
	s.configMu.RUnlock()
	return used
}

func softwareConfiguredPortConflict(port int, used map[int]string) string {
	if label := used[port]; label != "" {
		return label
	}
	return ""
}

func optionalPositivePort(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("port must be 1-65535")
	}
	return port, nil
}

func portFromListenAddr(addr string) int {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return 0
	}
	addr = strings.TrimPrefix(addr, "tcp:")
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return 0
	}
	return port
}

func portFromLocalHTTPURL(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return 0
	}
	host := strings.ToLower(u.Hostname())
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return 0
	}
	if portStr := u.Port(); portStr != "" {
		port, err := strconv.Atoi(portStr)
		if err == nil && port > 0 && port <= 65535 {
			return port
		}
	}
	switch u.Scheme {
	case "http":
		return 80
	case "https":
		return 443
	default:
		return 0
	}
}
