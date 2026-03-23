package main

import (
	"os"

	"github.com/uwaserver/uwas/internal/cli"
)

func main() {
	app := cli.New()
	app.Register(&cli.VersionCommand{})
	app.Register(&cli.ServeCommand{})
	app.Register(&cli.ConfigCommand{})
	app.Register(&cli.DomainCommand{})
	app.Register(&cli.CacheCommand{})
	app.Register(&cli.StatusCommand{})
	app.Register(&cli.ReloadCommand{})
	app.Register(&cli.MigrateCommand{})
	app.Register(&cli.BackupCommand{})
	app.Register(&cli.RestoreCommand{})
	app.Register(&cli.StopCommand{})
	app.Register(&cli.RestartCommand{})
	app.Register(&cli.PHPCommand{})
	app.Register(&cli.UserCommand{})
	app.Register(cli.NewHelpCommand(app))

	app.Run(os.Args[1:])
}
