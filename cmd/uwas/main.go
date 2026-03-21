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
	app.Register(cli.NewHelpCommand(app))

	app.Run(os.Args[1:])
}
