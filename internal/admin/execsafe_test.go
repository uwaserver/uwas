package admin

import (
	"os"
	"os/exec"
	"testing"

	"github.com/uwaserver/uwas/internal/database"
	"github.com/uwaserver/uwas/internal/firewall"
)

// safeNoExecCmd returns a command that does nothing real (`true` exits 0 with
// no output). It replaces the default exec hooks during tests so an un-mocked
// admin code path can never shell out to a real privileged system command.
func safeNoExecCmd(name string, args ...string) *exec.Cmd {
	return exec.Command("true")
}

// TestMain neutralizes every admin test seam that shells out to privileged
// system commands. Without this, admin handler tests invoke real
// systemctl / apt / ufw / mysql commands; as a non-root dev user those trigger
// polkit/sudo password prompts during `go test`. Individual tests still
// override any seam they need to assert specific behavior.
func TestMain(m *testing.M) {
	systemExecCommand = safeNoExecCmd
	softwareComposeCommand = safeNoExecCmd

	databaseStartService = func() error { return nil }
	databaseStopService = func() error { return nil }
	databaseRestartService = func() error { return nil }
	databaseRepairService = func() (string, error) { return "", nil }
	databaseUninstall = func() (string, error) { return "", nil }
	databaseForceUninstall = func() (string, error) { return "", nil }
	databaseCreateDatabase = func(name, user, password, host string) (*database.CreateResult, error) {
		return &database.CreateResult{Name: name, User: user, Host: host}, nil
	}
	databaseDropDatabase = func(name, user, host string) error { return nil }

	firewallGetStatus = func() firewall.Status { return firewall.Status{} }
	firewallAllowPort = func(port, proto string) error { return nil }
	firewallDenyPort = func(port, proto string) error { return nil }
	firewallDeleteRule = func(number int) error { return nil }
	firewallEnable = func() error { return nil }
	firewallDisable = func() error { return nil }

	servicesStartService = func(name string) error { return nil }
	servicesStopService = func(name string) error { return nil }
	servicesRestartService = func(name string) error { return nil }

	phpRunInstall = func(version string) (string, error) { return "", nil }

	os.Exit(m.Run())
}
