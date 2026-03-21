package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestCLIHelpOutput(t *testing.T) {
	app := New()
	app.Register(&VersionCommand{})
	app.Register(&ServeCommand{})
	app.Register(&ConfigCommand{})
	app.Register(NewHelpCommand(app))

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	app.Run([]string{"help"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "serve") {
		t.Error("help should list serve command")
	}
	if !strings.Contains(output, "version") {
		t.Error("help should list version command")
	}
	if !strings.Contains(output, "config") {
		t.Error("help should list config command")
	}
}

func TestVersionCommand(t *testing.T) {
	cmd := &VersionCommand{}
	if cmd.Name() != "version" {
		t.Errorf("name = %q", cmd.Name())
	}

	// Should not error
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := cmd.Run(nil)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Errorf("Run error: %v", err)
	}
	if !strings.Contains(buf.String(), "uwas") {
		t.Error("version output should contain 'uwas'")
	}
}

func TestConfigValidateCommand(t *testing.T) {
	// Create a temp valid config
	tmpFile, err := os.CreateTemp("", "uwas-test-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	tmpFile.WriteString(`
domains:
  - host: test.com
    root: /tmp
    type: static
    ssl:
      mode: off
`)
	tmpFile.Close()

	cmd := &ConfigCommand{}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err = cmd.Run([]string{"validate", "-c", tmpFile.Name()})

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Errorf("validate should succeed: %v", err)
	}
}

func TestConfigValidateInvalid(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "uwas-invalid-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	tmpFile.WriteString(`not valid yaml {{`)
	tmpFile.Close()

	cmd := &ConfigCommand{}
	err = cmd.Run([]string{"validate", "-c", tmpFile.Name()})
	if err == nil {
		t.Error("should fail for invalid config")
	}
}

func TestCLIRegisterAndLookup(t *testing.T) {
	app := New()
	app.Register(&VersionCommand{})

	if len(app.commands) != 1 {
		t.Errorf("commands = %d, want 1", len(app.commands))
	}
}
