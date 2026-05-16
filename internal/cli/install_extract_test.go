package cli

import "testing"

func TestExtractCredsFromConfig_RealLayout(t *testing.T) {
	tmp := t.TempDir() + "/uwas.yaml"
	content := generateDefaultConfig("80", "9443", "127.0.0.1", "abc123def456", "654321", "/var/lib/uwas", "/var/www", "")
	if err := installOsWriteFile(tmp, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	c := extractCredsFromConfig(tmp)
	if c.apiKey != "abc123def456" {
		t.Errorf("apiKey = %q, want abc123def456", c.apiKey)
	}
	if c.pinCode != "654321" {
		t.Errorf("pinCode = %q, want 654321", c.pinCode)
	}
	if c.adminHost != "127.0.0.1" {
		t.Errorf("adminHost = %q, want 127.0.0.1", c.adminHost)
	}
	if c.adminPort != "9443" {
		t.Errorf("adminPort = %q, want 9443", c.adminPort)
	}
}
