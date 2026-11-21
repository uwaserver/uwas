package database

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakePostgresDocker installs a `docker` on PATH whose psql behaves as
// documented: after a failed statement it exits 0, or 3 with ON_ERROR_STOP
// set. The fake server rejects MySQL syntax (backticks, mysql.user) and
// answers information_schema.schemata with schemas, pg_database with databases.
func fakePostgresDocker(t *testing.T, image string) (logPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fake docker")
	}
	dir := t.TempDir()
	logPath = filepath.Join(dir, "sql.log")
	script := `#!/bin/sh
if [ "$1" = inspect ]; then echo '` + image + `'; exit 0; fi
if [ "$1" = exec ]; then
  sql=$(cat)
  printf '%s\n----\n' "$sql" >> '` + logPath + `'
  case "$*" in
  *psql*)
    stop=0
    case "$*" in *ON_ERROR_STOP=1*) stop=1;; esac
    case "$sql" in
    *'` + "`" + `'*|*'mysql.user'*|*'CHARACTER SET'*)
      echo 'ERROR:  syntax error at or near "` + "`" + `"' >&2
      if [ $stop = 1 ]; then exit 3; fi
      exit 0;;
    *pg_database*) printf ' shop\n blog\n'; exit 0;;
    *COUNT*SCHEMATA*) printf '     0\n'; exit 0;;
    *SCHEMATA*) printf ' pg_toast\n pg_catalog\n public\n'; exit 0;;
    esac
    exit 0;;
  *)
    case "$sql" in
    *COUNT*) echo 0;;
    *SCHEMATA*) printf 'shop\nblog\n';;
    esac
    exit 0;;
  esac
fi
exit 0
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

// F67100: psql ran without ON_ERROR_STOP, so a Postgres container's rejected
// CREATE/DROP DATABASE exited 0 and the API returned credentials for a
// database never created, or "dropped" for one still there.
func TestDockerPostgresSQLErrorsSurface(t *testing.T) {
	fakePostgresDocker(t, "postgres:16")
	if res, err := DockerDBCreateDatabase("uwas-db-pg", "shop2", "shop2", "pw"); err == nil {
		t.Fatalf("create on postgres reported success with %+v", res)
	}
	if err := DockerDBDropDatabase("uwas-db-pg", "shop"); err == nil {
		t.Fatal("drop on postgres reported success")
	}
	if out, err := DockerDBExecSQL("pg", "SELECT datname FROM pg_database"); err != nil || !strings.Contains(out, "shop") {
		t.Fatalf("valid postgres SQL: out=%q err=%v", out, err)
	}
}

// F67101: on Postgres, information_schema.schemata lists schemas, so the
// container's database list showed pg_toast/pg_catalog/public instead of its
// databases.
func TestDockerPostgresListsDatabasesNotSchemas(t *testing.T) {
	fakePostgresDocker(t, "postgres:16")
	dbs, err := DockerDBListDatabases("pg")
	var names []string
	for _, d := range dbs {
		names = append(names, d.Name)
	}
	if err != nil || strings.Join(names, ",") != "shop,blog" {
		t.Fatalf("postgres listing = %v, %v; want [shop blog]", names, err)
	}

	fakePostgresDocker(t, "mariadb:11")
	dbs, err = DockerDBListDatabases("m")
	if err != nil || len(dbs) != 2 || dbs[0].Name != "shop" {
		t.Fatalf("mariadb listing = %v, %v", dbs, err)
	}
}
