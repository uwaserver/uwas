package cli

import (
	"fmt"
	"os"
	"runtime"
	"text/tabwriter"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/siteuser"
)

// UserCommand manages SFTP users for domains.
type UserCommand struct{}

func (u *UserCommand) Name() string        { return "user" }
func (u *UserCommand) Description() string { return "Manage SFTP users for domains" }

func (u *UserCommand) Help() string {
	return `Subcommands:
  list                    List all site SFTP users
  add <domain>            Create SFTP user for a domain
  remove <domain>         Remove SFTP user (keeps files)

Creates chroot-jailed SFTP users that can only upload to their domain's
public_html directory. Requires root.

Examples:
  uwas user add example.com       # Creates user, shows SFTP credentials
  uwas user list                  # List all site users
  uwas user remove example.com    # Remove user, keep files`
}

func (u *UserCommand) Run(args []string) error {
	if len(args) == 0 {
		fmt.Println(u.Help())
		return nil
	}

	switch args[0] {
	case "list":
		return u.list()
	case "add":
		if len(args) < 2 {
			return fmt.Errorf("domain required: uwas user add <domain>")
		}
		return u.add(args[1])
	case "remove", "rm", "delete":
		if len(args) < 2 {
			return fmt.Errorf("domain required: uwas user remove <domain>")
		}
		return u.remove(args[1])
	default:
		return fmt.Errorf("unknown subcommand %q (use: list, add, remove)", args[0])
	}
}

func (u *UserCommand) list() error {
	users := siteuser.ListUsers()
	if len(users) == 0 {
		fmt.Println("No site users configured.")
		fmt.Println("Create one with: uwas user add <domain>")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "USERNAME\tDOMAIN\tWEB DIR\n")
	for _, user := range users {
		fmt.Fprintf(w, "%s\t%s\t%s\n", user.Username, user.Domain, user.WebDir)
	}
	w.Flush()
	return nil
}

func (u *UserCommand) add(domain string) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("user management not supported on Windows")
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("root required — run: sudo uwas user add %s", domain)
	}

	// Get web root from config
	webRoot := "/var/www"
	if cfgFile, found := findConfig(""); found {
		if cfg, err := config.Load(cfgFile); err == nil && cfg.Global.WebRoot != "" {
			webRoot = cfg.Global.WebRoot
		}
	}

	user, password, err := siteuser.CreateUser(webRoot, domain)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}

	fmt.Println()
	fmt.Println("  \033[32m✓\033[0m SFTP user created")
	fmt.Println()
	fmt.Printf("    Domain:    %s\n", user.Domain)
	fmt.Printf("    Username:  %s\n", user.Username)
	fmt.Printf("    Password:  %s\n", password)
	fmt.Printf("    Web Root:  %s\n", user.WebDir)
	fmt.Println()
	fmt.Println("  \033[1mSFTP Connection:\033[0m")
	fmt.Printf("    Host:      %s\n", "your-server-ip")
	fmt.Printf("    Port:      22\n")
	fmt.Printf("    Username:  %s\n", user.Username)
	fmt.Printf("    Password:  %s\n", password)
	fmt.Println()
	fmt.Println("  Files uploaded to SFTP root appear in:")
	fmt.Printf("    %s\n", user.WebDir)
	fmt.Println()
	fmt.Println("  \033[33m!\033[0m Save these credentials — the password cannot be recovered.")
	fmt.Println()

	return nil
}

func (u *UserCommand) remove(domain string) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("user management not supported on Windows")
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("root required — run: sudo uwas user remove %s", domain)
	}

	if err := siteuser.DeleteUser(domain); err != nil {
		return fmt.Errorf("remove user: %w", err)
	}

	fmt.Printf("User for %s removed (files kept).\n", domain)
	return nil
}
