package watchdog

import "os"

// osExit is separated so tests can replace the process-terminating call.
var osExit = os.Exit
