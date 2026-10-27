package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
)

type Command interface {
	Name() string
	Description() string
	Run(args []string) error
}

type CLI struct {
	commands map[string]Command
	order    []string
}

func New() *CLI {
	return &CLI{
		commands: make(map[string]Command),
	}
}

func (c *CLI) Register(cmd Command) {
	c.commands[cmd.Name()] = cmd
	c.order = append(c.order, cmd.Name())
}

func (c *CLI) Run(args []string) {
	// Auto-load .env from config directory for API key
	loadDotEnv()

	if len(args) == 0 {
		// No arguments: auto-start server (first-run friendly)
		if cmd, ok := c.commands["serve"]; ok {
			if err := cmd.Run(nil); err != nil {
				fmt.Fprintf(os.Stderr, "uwas: %v\n", err)
				os.Exit(1)
			}
			return
		}
		c.printUsage()
		os.Exit(0)
	}

	name := args[0]

	// Global flags
	if name == "--help" || name == "-h" {
		c.printUsage()
		os.Exit(0)
	}

	cmd, ok := c.commands[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "uwas: unknown command %q\n\nRun 'uwas help' for usage.\n", name)
		os.Exit(1)
	}

	// "uwas <command> --help" is advertised by printUsage. Commands with
	// subcommands would otherwise report it as an unknown subcommand.
	if len(args) > 1 && (args[1] == "--help" || args[1] == "-h") {
		if _, ok := cmd.(interface{ Help() string }); ok {
			printCommandHelp(name, cmd)
			return
		}
	}

	if err := cmd.Run(args[1:]); err != nil {
		// The flag set already printed its usage; asking for help is not a failure.
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintf(os.Stderr, "uwas %s: %v\n", name, err)
		os.Exit(1)
	}
}

// printCommandHelp prints a command's description and, when it has one, its
// detailed help.
func printCommandHelp(name string, cmd Command) {
	fmt.Printf("Usage: uwas %s [flags]\n\n", name)
	fmt.Printf("  %s\n", cmd.Description())

	if helper, ok := cmd.(interface{ Help() string }); ok {
		detail := strings.TrimSpace(helper.Help())
		if detail != "" {
			fmt.Println()
			fmt.Println(detail)
		}
	}
}

func (c *CLI) printUsage() {
	fmt.Println("UWAS — Unified Web Application Server")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  uwas <command> [flags]")
	fmt.Println()
	fmt.Println("Commands:")

	// Sort for consistent output
	names := make([]string, len(c.order))
	copy(names, c.order)
	sort.Strings(names)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, name := range names {
		cmd := c.commands[name]
		fmt.Fprintf(w, "  %s\t%s\n", name, cmd.Description())
	}
	w.Flush()

	fmt.Println()
	fmt.Println("Run 'uwas <command> --help' for more information on a command.")
}

// HelpCommand implements the "help" command.
type HelpCommand struct {
	cli *CLI
}

func NewHelpCommand(c *CLI) *HelpCommand {
	return &HelpCommand{cli: c}
}

func (h *HelpCommand) Name() string        { return "help" }
func (h *HelpCommand) Description() string { return "Show help information" }

func (h *HelpCommand) Run(args []string) error {
	if len(args) > 0 {
		name := args[0]
		if cmd, ok := h.cli.commands[name]; ok {
			printCommandHelp(name, cmd)
			return nil
		}
		return fmt.Errorf("unknown command %q", name)
	}
	h.cli.printUsage()
	return nil
}

// loadDotEnv reads .env from the UWAS config directory and sets env vars.
// Looks in ~/.uwas/.env and /etc/uwas/.env.
func loadDotEnv() {
	home, _ := os.UserHomeDir()
	paths := []string{
		filepath.Join(home, ".uwas", ".env"),
		"/etc/uwas/.env",
	}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if k, v, ok := strings.Cut(line, "="); ok {
				k = strings.TrimSpace(k)
				v = strings.TrimSpace(v)
				// Don't override existing env vars
				if os.Getenv(k) == "" {
					os.Setenv(k, v)
				}
			}
		}
		f.Close()
		return // use first found
	}
}
