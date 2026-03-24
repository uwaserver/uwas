package serverip

import (
	"net"
	"testing"
)

func TestDetectAll(t *testing.T) {
	ips := DetectAll()

	// On most systems, there should be at least one non-loopback IP.
	// In some CI/container environments, this may be empty.
	if len(ips) == 0 {
		t.Log("DetectAll returned no IPs (may be OK in restricted environments)")
		return
	}

	// Verify no loopback addresses are included
	for _, info := range ips {
		ip := net.ParseIP(info.IP)
		if ip == nil {
			t.Errorf("DetectAll returned invalid IP: %q", info.IP)
			continue
		}
		if ip.IsLoopback() {
			t.Errorf("DetectAll should not return loopback address: %q", info.IP)
		}
		if info.Version != 4 && info.Version != 6 {
			t.Errorf("IP version should be 4 or 6, got %d for %q", info.Version, info.IP)
		}
		if info.Interface == "" {
			t.Errorf("Interface name should not be empty for IP %q", info.IP)
		}
	}
}

func TestDetectAllPrimaryMarking(t *testing.T) {
	ips := DetectAll()
	if len(ips) == 0 {
		t.Skip("no IPs detected, skipping primary marking test")
	}

	primaryCount := 0
	for _, info := range ips {
		if info.Primary {
			primaryCount++
			if info.Version != 4 {
				t.Errorf("primary IP should be IPv4, got version %d", info.Version)
			}
		}
	}

	// There should be at most one primary IP
	if primaryCount > 1 {
		t.Errorf("expected at most 1 primary IP, got %d", primaryCount)
	}
}

func TestDetectAllSorted(t *testing.T) {
	ips := DetectAll()
	if len(ips) < 2 {
		t.Skip("need at least 2 IPs to test sorting")
	}

	// Primary should come first if present
	foundNonPrimary := false
	for _, info := range ips {
		if !info.Primary {
			foundNonPrimary = true
		}
		if info.Primary && foundNonPrimary {
			t.Error("primary IP should come before non-primary IPs in sorted output")
		}
	}
}

func TestPrimaryIPv4(t *testing.T) {
	ip := PrimaryIPv4()

	// May be empty in some environments
	if ip == "" {
		t.Log("PrimaryIPv4 returned empty (may be OK in restricted environments)")
		return
	}

	parsed := net.ParseIP(ip)
	if parsed == nil {
		t.Errorf("PrimaryIPv4 returned invalid IP: %q", ip)
		return
	}
	if parsed.To4() == nil {
		t.Errorf("PrimaryIPv4 returned non-IPv4 address: %q", ip)
	}
}

func TestIPInfoStruct(t *testing.T) {
	info := IPInfo{
		IP:        "192.168.1.1",
		Version:   4,
		Interface: "eth0",
		Primary:   true,
	}

	if info.IP != "192.168.1.1" {
		t.Errorf("expected IP '192.168.1.1', got %q", info.IP)
	}
	if info.Version != 4 {
		t.Errorf("expected Version 4, got %d", info.Version)
	}
	if info.Interface != "eth0" {
		t.Errorf("expected Interface 'eth0', got %q", info.Interface)
	}
	if !info.Primary {
		t.Error("expected Primary=true")
	}
}
