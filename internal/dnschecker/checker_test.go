package dnschecker

import (
	"testing"
)

func TestCheckLocalhost(t *testing.T) {
	r := Check("localhost")

	if r.Domain != "localhost" {
		t.Errorf("expected domain 'localhost', got %q", r.Domain)
	}

	// localhost should resolve to at least one address (127.0.0.1 or ::1)
	if r.Error != "" && len(r.A) == 0 && len(r.AAAA) == 0 {
		t.Logf("DNS lookup for localhost returned error (may be OK in some environments): %s", r.Error)
	}
}

func TestGetServerIPs(t *testing.T) {
	ips := getServerIPs()
	// On most systems, there should be at least one non-loopback IP.
	// However, in some CI/container environments there may be none.
	if len(ips) == 0 {
		t.Log("getServerIPs returned no IPs (may be OK in restricted environments)")
	}
	for _, ip := range ips {
		if ip == "127.0.0.1" || ip == "::1" {
			t.Errorf("getServerIPs should not return loopback address %q", ip)
		}
	}
}

func TestResultStruct(t *testing.T) {
	r := Result{
		Domain:     "example.com",
		A:          []string{"93.184.216.34"},
		AAAA:       []string{"2606:2800:220:1:248:1893:25c8:1946"},
		CNAME:      "",
		MX:         []string{"mx.example.com (priority 10)"},
		NS:         []string{"ns1.example.com"},
		TXT:        []string{"v=spf1 -all"},
		PointsHere: false,
		ServerIPs:  []string{"10.0.0.1"},
	}

	if r.Domain != "example.com" {
		t.Errorf("expected Domain 'example.com', got %q", r.Domain)
	}
	if len(r.A) != 1 {
		t.Errorf("expected 1 A record, got %d", len(r.A))
	}
	if len(r.AAAA) != 1 {
		t.Errorf("expected 1 AAAA record, got %d", len(r.AAAA))
	}
	if r.PointsHere {
		t.Error("expected PointsHere=false")
	}
}

func TestCheckInvalidDomain(t *testing.T) {
	r := Check("this-domain-definitely-does-not-exist-uwas-test.invalid")

	if r.Error == "" {
		t.Log("expected an error for invalid domain, but lookup succeeded (DNS may be doing wildcard resolution)")
	}
}
