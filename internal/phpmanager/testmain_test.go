package phpmanager

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	cleanup := func() {}
	if runtime.GOOS == "windows" {
		var err error
		cleanup, err = installPortableEcho()
		if err != nil {
			fmt.Fprintln(os.Stderr, "failed to install test echo binary:", err)
			os.Exit(1)
		}
	}

	code := m.Run()
	cleanup()
	os.Exit(code)
}

func installPortableEcho() (func(), error) {
	tmpDir, err := os.MkdirTemp("", "uwas-php-echo-*")
	if err != nil {
		return nil, err
	}

	src := `package main
import (
	"fmt"
	"os"
	"strings"
)
func main() {
	if len(os.Args) <= 1 {
		fmt.Println()
		return
	}
	fmt.Println(strings.Join(os.Args[1:], " "))
}
`
	srcPath := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(srcPath, []byte(src), 0644); err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, err
	}

	binPath := filepath.Join(tmpDir, "echo.exe")
	build := exec.Command("go", "build", "-o", binPath, srcPath)
	build.Env = os.Environ()
	out, err := build.CombinedOutput()
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("go build echo helper: %w: %s", err, strings.TrimSpace(string(out)))
	}

	oldPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", tmpDir+string(os.PathListSeparator)+oldPath); err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, err
	}

	cleanup := func() {
		_ = os.Setenv("PATH", oldPath)
		_ = os.RemoveAll(tmpDir)
	}
	return cleanup, nil
}
