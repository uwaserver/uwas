package server

import (
	"errors"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/phpmanager"
	"github.com/uwaserver/uwas/internal/rlimit"
)

// autoAssignPHP called AssignDomain, which leaves the web root empty.
// buildDomainINI gates upload_tmp_dir, session.save_path and sys_temp_dir on
// a non-empty web root, so a PHP domain started at boot kept the shared
// system temp directory for sessions and uploads. (open_basedir survives:
// fastcgi/env.go writes it per request.) The same domain re-assigned through
// the admin panel went through AssignDomainWithRoot and got the isolation —
// it depended on which code path happened to start the process.

type fakePHPManager struct {
	assignedRoot map[string]string
	overrides    map[string]map[string]string
	limits       map[string]rlimit.Limits
	started      []string
	// Ordering: SetDomainConfig must be called before StartDomain, because
	// the ini is written as the process starts.
	configuredAfterStart bool
	assignErr            error
	configErr            error
}

func newFakePHPManager() *fakePHPManager {
	return &fakePHPManager{
		assignedRoot: map[string]string{},
		overrides:    map[string]map[string]string{},
		limits:       map[string]rlimit.Limits{},
	}
}

func (f *fakePHPManager) AssignDomainWithRoot(domain, version, webRoot string) (*phpmanager.DomainPHP, error) {
	if f.assignErr != nil {
		return nil, f.assignErr
	}
	f.assignedRoot[domain] = webRoot
	return &phpmanager.DomainPHP{Domain: domain, Version: version, ListenAddr: "127.0.0.1:9001"}, nil
}

func (f *fakePHPManager) SetDomainConfig(domain, key, value string) error {
	for _, d := range f.started {
		if d == domain {
			f.configuredAfterStart = true
		}
	}
	if f.configErr != nil {
		return f.configErr
	}
	if f.overrides[domain] == nil {
		f.overrides[domain] = map[string]string{}
	}
	f.overrides[domain][key] = value
	return nil
}

func (f *fakePHPManager) SetDomainLimits(domain string, l rlimit.Limits) bool {
	for _, d := range f.started {
		if d == domain {
			f.configuredAfterStart = true
		}
	}
	f.limits[domain] = l
	return true
}

func (f *fakePHPManager) StartDomain(domain string) error {
	f.started = append(f.started, domain)
	return nil
}

func testLog() *logger.Logger { return logger.New("error", "text") }

// The web root must reach the PHP manager, or open_basedir is never written.
func TestAssignPHPForDomainPassesWebRoot(t *testing.T) {
	f := newFakePHPManager()
	d := config.Domain{Host: "php.test", Type: "php", Root: "/srv/www/php.test"}

	if _, err := assignPHPForDomain(f, d, "8.4", testLog()); err != nil {
		t.Fatalf("assignPHPForDomain: %v", err)
	}

	if got := f.assignedRoot["php.test"]; got != "/srv/www/php.test" {
		t.Errorf("web root = %q, want %q — an empty root turns session and upload isolation off", got, "/srv/www/php.test")
	}
	if len(f.started) != 1 || f.started[0] != "php.test" {
		t.Errorf("StartDomain calls = %v", f.started)
	}
}

// The domain's php.ini overrides were dropped on this path entirely.
func TestAssignPHPForDomainAppliesConfigOverrides(t *testing.T) {
	f := newFakePHPManager()
	d := config.Domain{
		Host: "php.test",
		Type: "php",
		Root: "/srv/www/php.test",
		PHP: config.PHPConfig{
			ConfigOverrides: map[string]string{
				"memory_limit":        "256M",
				"upload_max_filesize": "32M",
			},
		},
	}

	if _, err := assignPHPForDomain(f, d, "8.4", testLog()); err != nil {
		t.Fatalf("assignPHPForDomain: %v", err)
	}

	got := f.overrides["php.test"]
	if got["memory_limit"] != "256M" || got["upload_max_filesize"] != "32M" {
		t.Errorf("uygulanan override'lar = %v", got)
	}
	// buildDomainINI writes the ini as the process starts; an override set
	// after that never reaches it.
	if f.configuredAfterStart {
		t.Error("the override was set after StartDomain — it never reaches the running process")
	}
}

// A rejected override must not take the rest, or the start, down with it.
func TestAssignPHPForDomainSurvivesRejectedOverride(t *testing.T) {
	f := newFakePHPManager()
	f.configErr = errors.New("disallowed key")
	d := config.Domain{
		Host: "php.test",
		Type: "php",
		Root: "/srv/www/php.test",
		PHP:  config.PHPConfig{ConfigOverrides: map[string]string{"open_basedir": "/"}},
	}

	if _, err := assignPHPForDomain(f, d, "8.4", testLog()); err != nil {
		t.Fatalf("a rejected override took the assignment down: %v", err)
	}
	if len(f.started) != 1 {
		t.Error("reddedilen override StartDomain'i engelledi")
	}
}

// An already-assigned domain must not be started a second time.
func TestAssignPHPForDomainStopsOnAssignError(t *testing.T) {
	f := newFakePHPManager()
	f.assignErr = errors.New("domain already has a PHP assignment")

	if _, err := assignPHPForDomain(f, config.Domain{Host: "php.test"}, "8.4", testLog()); err == nil {
		t.Fatal("the assignment error was swallowed")
	}
	if len(f.started) != 0 {
		t.Errorf("StartDomain was called after the assignment failed: %v", f.started)
	}
}

// domain.resources must reach the manager, and before the worker starts: a
// cgroup has to exist before a process can be moved into it.
func TestAssignPHPForDomainRecordsResourceLimits(t *testing.T) {
	f := newFakePHPManager()
	d := config.Domain{
		Host:      "php.test",
		Type:      "php",
		Root:      "/srv/www/php.test",
		Resources: config.ResourceLimits{CPUPercent: 40, MemoryMB: 512, PIDMax: 80},
	}

	if _, err := assignPHPForDomain(f, d, "8.4", testLog()); err != nil {
		t.Fatalf("assignPHPForDomain: %v", err)
	}

	want := rlimit.Limits{CPUPercent: 40, MemoryMB: 512, PIDMax: 80}
	if got := f.limits["php.test"]; got != want {
		t.Errorf("recorded limits = %+v, want %+v — domain.resources does not reach the manager", got, want)
	}
	if f.configuredAfterStart {
		t.Error("limits were recorded after StartDomain — the cgroup would be built after the process")
	}
}

func TestLimitsForCarriesEveryField(t *testing.T) {
	got := limitsFor(config.ResourceLimits{CPUPercent: 50, MemoryMB: 256, PIDMax: 100})
	if want := (rlimit.Limits{CPUPercent: 50, MemoryMB: 256, PIDMax: 100}); got != want {
		t.Errorf("limitsFor = %+v, want %+v", got, want)
	}
	if got := limitsFor(config.ResourceLimits{}); got != (rlimit.Limits{}) {
		t.Errorf("limitsFor(empty) = %+v", got)
	}
}

func TestHasResourceLimits(t *testing.T) {
	cases := []struct {
		in   config.ResourceLimits
		want bool
	}{
		{config.ResourceLimits{}, false},
		{config.ResourceLimits{CPUPercent: 10}, true},
		{config.ResourceLimits{MemoryMB: 1}, true},
		{config.ResourceLimits{PIDMax: 1}, true},
		{config.ResourceLimits{CPUPercent: -1}, false},
	}
	for _, c := range cases {
		if got := hasResourceLimits(c.in); got != c.want {
			t.Errorf("hasResourceLimits(%+v) = %v, want %v", c.in, got, c.want)
		}
	}
}
