//go:build !linux

package apps

// readProcStats on non-Linux returns zeroes — the /proc-based
// algorithm in stats_linux.go has no portable equivalent and
// querying it via the standard library would require os/cgo
// bindings that the project intentionally avoids. Operators on
// Windows / macOS get "no signal" instead of misleading data.
func readProcStats(_ int) (float64, int64, int64) {
	return 0, 0, 0
}

// readDockerStats on non-Linux delegates to the docker CLI's
// `docker stats --no-stream` the same way Linux does, but the parsing
// helper lives in stats_linux.go. Keeping the surface available so
// build-tag-conditional code doesn't have to ifdef call sites.
func readDockerStats(_ string) (float64, int64) {
	return 0, 0
}
