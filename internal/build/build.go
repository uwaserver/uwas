package build

import (
	"fmt"
	"runtime"
)

// Set via ldflags at build time.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func Info() string {
	return fmt.Sprintf("uwas %s (%s) built %s %s/%s %s",
		Version, Commit, Date, runtime.GOOS, runtime.GOARCH, runtime.Version())
}
