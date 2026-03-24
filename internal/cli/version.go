package cli

import (
	"fmt"

	"github.com/uwaserver/uwas/internal/build"
)

type VersionCommand struct{}

func (v *VersionCommand) Name() string        { return "version" }
func (v *VersionCommand) Description() string { return "Print version information" }

func (v *VersionCommand) Run(args []string) error {
	fmt.Println(build.Info())
	return nil
}
