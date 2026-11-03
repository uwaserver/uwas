package database

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDockerDBCreateDatabaseRefusesExistingSchema(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake docker")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "mutations.log")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = inspect ]; then echo mariadb:11; exit 0; fi\n" +
		"if [ \"$1\" = exec ]; then\n" +
		"  sql=$(cat)\n" +
		"  case \"$sql\" in\n" +
		"    *information_schema.SCHEMATA*\"existing\"*) echo 1; exit 0;;\n" +
		"    *information_schema.SCHEMATA*) echo 0; exit 0;;\n" +
		"    *mysql.user*) echo 0; exit 0;;\n" +
		"  esac\n" +
		"  printf '%s\\n' \"$sql\" >> '" + logPath + "'\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	result, err := DockerDBCreateDatabase("uwas-db-x", "existing", "freshuser", "pw")
	if err == nil {
		t.Fatalf("existing schema silently reused: %#v", result)
	}
	if !errors.Is(err, ErrDatabaseExists) {
		t.Fatalf("error = %v, want ErrDatabaseExists", err)
	}
	if data, _ := os.ReadFile(logPath); strings.TrimSpace(string(data)) != "" {
		t.Fatalf("mutation ran for existing schema: %s", data)
	}
}
