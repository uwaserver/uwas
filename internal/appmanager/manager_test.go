package appmanager

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
)

func TestRegisterAndInstances(t *testing.T) {
	m := New(nil)
	err := m.Register("node.example.com", config.AppConfig{
		Runtime: "node",
		Command: "echo hello",
		Port:    4000,
	}, "/tmp/node-app")
	if err != nil {
		t.Fatal(err)
	}

	instances := m.Instances()
	if len(instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(instances))
	}
	if instances[0].Domain != "node.example.com" {
		t.Errorf("domain = %q", instances[0].Domain)
	}
	if instances[0].Runtime != "node" {
		t.Errorf("runtime = %q", instances[0].Runtime)
	}
	if instances[0].Port != 4000 {
		t.Errorf("port = %d", instances[0].Port)
	}
	if instances[0].Running {
		t.Error("should not be running before Start()")
	}
}

func TestRegisterDuplicate(t *testing.T) {
	m := New(nil)
	m.Register("dup.com", config.AppConfig{Command: "echo", Runtime: "custom"}, "/tmp")
	err := m.Register("dup.com", config.AppConfig{Command: "echo", Runtime: "custom"}, "/tmp")
	if err == nil {
		t.Error("expected error on duplicate register")
	}
}

func TestAutoPort(t *testing.T) {
	m := New(nil)
	m.Register("a.com", config.AppConfig{Command: "echo", Runtime: "node"}, "/tmp")
	m.Register("b.com", config.AppConfig{Command: "echo", Runtime: "node"}, "/tmp")

	instances := m.Instances()
	ports := map[int]bool{}
	for _, inst := range instances {
		ports[inst.Port] = true
	}
	if len(ports) != 2 {
		t.Errorf("expected 2 unique ports, got %d", len(ports))
	}
}

func TestStartStop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process management test skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)
	// Use a long-running process
	m.Register("live.com", config.AppConfig{
		Command: "sleep 60",
		Runtime: "custom",
		Port:    19876,
	}, dir)

	if err := m.Start("live.com"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(200 * time.Millisecond)

	inst := m.Get("live.com")
	if inst == nil {
		t.Fatal("instance is nil")
	}
	if !inst.Running {
		t.Error("should be running after Start()")
	}
	if inst.PID == 0 {
		t.Error("PID should be set")
	}

	if err := m.Stop("live.com"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(200 * time.Millisecond)
	inst2 := m.Get("live.com")
	if inst2.Running {
		t.Error("should not be running after Stop()")
	}
}

func TestListenAddr(t *testing.T) {
	m := New(nil)
	m.Register("addr.com", config.AppConfig{Command: "echo", Runtime: "node", Port: 5555}, "/tmp")
	addr := m.ListenAddr("addr.com")
	if addr != "127.0.0.1:5555" {
		t.Errorf("addr = %q", addr)
	}
	if m.ListenAddr("nonexistent.com") != "" {
		t.Error("should return empty for unknown domain")
	}
}

func TestUnregister(t *testing.T) {
	m := New(nil)
	m.Register("gone.com", config.AppConfig{Command: "echo", Runtime: "custom"}, "/tmp")
	m.Unregister("gone.com")
	if len(m.Instances()) != 0 {
		t.Error("should be empty after unregister")
	}
}

func TestDetectCommandNode(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{}`), 0644)
	cmd := detectCommand("node", dir)
	if cmd != "npm start" {
		t.Errorf("expected 'npm start', got %q", cmd)
	}
}

func TestDetectCommandPython(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "app.py"), []byte(""), 0644)
	cmd := detectCommand("python", dir)
	if cmd != "python app.py" {
		t.Errorf("expected 'python app.py', got %q", cmd)
	}
}

func TestDetectCommandRuby(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.ru"), []byte(""), 0644)
	cmd := detectCommand("ruby", dir)
	if cmd != "bundle exec puma -p ${PORT}" {
		t.Errorf("expected puma command, got %q", cmd)
	}
}

func TestDetectCommandUnknown(t *testing.T) {
	cmd := detectCommand("rust", t.TempDir())
	if cmd != "" {
		t.Errorf("expected empty, got %q", cmd)
	}
}

func TestStartNotRegistered(t *testing.T) {
	m := New(nil)
	if err := m.Start("nope.com"); err == nil {
		t.Error("expected error for unregistered domain")
	}
}

func TestStopNotRunning(t *testing.T) {
	m := New(nil)
	m.Register("idle.com", config.AppConfig{Command: "echo", Runtime: "custom"}, "/tmp")
	if err := m.Stop("idle.com"); err == nil {
		t.Error("expected error for not-running domain")
	}
}
