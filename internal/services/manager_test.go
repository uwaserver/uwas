package services

import (
	"runtime"
	"testing"
)

func TestListServices(t *testing.T) {
	result := ListServices()

	if runtime.GOOS == "windows" {
		// On Windows, ListServices returns nil
		if result != nil {
			t.Errorf("expected nil on Windows, got %v", result)
		}
		return
	}

	// On Linux, result may be empty if none of the known services are installed,
	// but it should not panic.
	t.Logf("ListServices returned %d services", len(result))
	for _, svc := range result {
		if svc.Name == "" {
			t.Error("service Name should not be empty")
		}
		if svc.Display == "" {
			t.Error("service Display should not be empty")
		}
		validStates := map[string]bool{"active": true, "inactive": true, "failed": true, "activating": true, "deactivating": true}
		if !validStates[svc.Active] {
			t.Logf("unexpected Active state %q for service %q (may be platform-specific)", svc.Active, svc.Name)
		}
	}
}

func TestServiceStruct(t *testing.T) {
	svc := Service{
		Name:    "nginx",
		Display: "Nginx Web Server",
		Running: true,
		Enabled: true,
		Active:  "active",
	}

	if svc.Name != "nginx" {
		t.Errorf("expected Name 'nginx', got %q", svc.Name)
	}
	if svc.Display != "Nginx Web Server" {
		t.Errorf("expected Display 'Nginx Web Server', got %q", svc.Display)
	}
	if !svc.Running {
		t.Error("expected Running=true")
	}
	if !svc.Enabled {
		t.Error("expected Enabled=true")
	}
	if svc.Active != "active" {
		t.Errorf("expected Active 'active', got %q", svc.Active)
	}
}

func TestKnownServicesNotEmpty(t *testing.T) {
	if len(KnownServices) == 0 {
		t.Error("KnownServices should not be empty")
	}

	for _, ks := range KnownServices {
		if ks.Name == "" {
			t.Error("KnownServices entry has empty Name")
		}
		if ks.Display == "" {
			t.Error("KnownServices entry has empty Display")
		}
	}
}
