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

func TestServeCommandHelp(t *testing.T) {
	cmd := &ServeCommand{}
	help := cmd.Help()
	if help == "" {
		t.Error("Help() should not be empty")
	}
	if !strings.Contains(help, "--config") {
		t.Error("Help should mention --config flag")
	}
	if !strings.Contains(help, "--log-level") {
		t.Error("Help should mention --log-level flag")
	}
}

func TestServeCommandNameDescription(t *testing.T) {
	cmd := &ServeCommand{}
	if cmd.Name() != "serve" {
		t.Errorf("Name() = %q, want serve", cmd.Name())
	}
	if cmd.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestHelpCommandRunWithSpecificSubcommand(t *testing.T) {
	app := New()
	app.Register(&ServeCommand{})
	app.Register(&VersionCommand{})
	app.Register(&ConfigCommand{})
	helpCmd := NewHelpCommand(app)
	app.Register(helpCmd)

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := helpCmd.Run([]string{"serve"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(output, "serve") {
		t.Error("help for serve should mention 'serve'")
	}
	// Since ServeCommand implements Help(), the detailed help should be printed
	if !strings.Contains(output, "--config") {
		t.Error("help for serve should include detailed help with --config")
	}
}

func TestHelpCommandRunWithUnknownCommand(t *testing.T) {
	app := New()
	helpCmd := NewHelpCommand(app)

	err := helpCmd.Run([]string{"nonexistent"})
	if err == nil {
		t.Error("expected error for unknown command")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("error = %q, should mention unknown command", err.Error())
	}
}

func TestHelpCommandRunNoArgs(t *testing.T) {
	app := New()
	app.Register(&VersionCommand{})
	helpCmd := NewHelpCommand(app)
	app.Register(helpCmd)

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := helpCmd.Run(nil)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(buf.String(), "UWAS") {
		t.Error("should print usage info")
	}
}

func TestHelpCommandNameDescription(t *testing.T) {
	app := New()
	helpCmd := NewHelpCommand(app)

	if helpCmd.Name() != "help" {
		t.Errorf("Name() = %q, want help", helpCmd.Name())
	}
	if helpCmd.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestHelpCommandRunWithCommandWithoutHelper(t *testing.T) {
	app := New()
	// VersionCommand does NOT implement Help() interface
	app.Register(&VersionCommand{})
	helpCmd := NewHelpCommand(app)

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := helpCmd.Run([]string{"version"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	// Should still print the basic info
	if !strings.Contains(buf.String(), "version") {
		t.Error("should print version command info")
	}
}
