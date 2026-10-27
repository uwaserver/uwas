package database

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
)

// remoteAccessHooks wires the fakes ConfigureRemoteAccess needs and returns the
// recorded config writes and SQL statements.
func remoteAccessHooks(t *testing.T, sqlFn func(string) (string, error)) (*[]string, *[]string) {
	t.Helper()
	saveHooks(t)
	runtimeGOOS = "linux"
	execLookPathFn = lookPathFound("mariadb")
	execCommandFn = fakeCmdRouter(map[string]cmdRoute{
		"systemctl restart": {stdout: "", exitCode: 0},
	}, cmdRoute{stdout: "", exitCode: 0})
	osStatFn = func(path string) (fs.FileInfo, error) {
		if path == "/etc/mysql/mariadb.conf.d/50-server.cnf" {
			return fakeFileInfo{}, nil
		}
		return nil, os.ErrNotExist
	}
	osReadFileFn = func(string) ([]byte, error) {
		return []byte("[mysqld]\nbind-address = 127.0.0.1\n"), nil
	}
	var writes, sqls []string
	osWriteFileFn = func(path string, data []byte, perm fs.FileMode) error {
		writes = append(writes, string(data))
		return nil
	}
	osMkdirAllFn = noopMkdirAll
	runMySQLFn = func(sql string) (string, error) {
		sqls = append(sqls, sql)
		return sqlFn(sql)
	}
	return &writes, &sqls
}

// F29400: CREATE USER IF NOT EXISTS leaves an existing account untouched, so
// ConfigureRemoteAccess reported a password that was never set (the F11900 class
// CreateDatabase already refuses) while granting the account on the database.
func TestConfigureRemoteAccessRefusesExistingUser(t *testing.T) {
	writes, sqls := remoteAccessHooks(t, func(sql string) (string, error) {
		if strings.Contains(sql, "FROM mysql.user") && strings.Contains(sql, "User = 'taken'") {
			return "1\n", nil
		}
		return "0\n", nil
	})

	// control: a fresh account goes through
	if _, err := ConfigureRemoteAccess("fresh", "%", "pw", "appdb"); err != nil {
		t.Fatalf("control: %v", err)
	}

	*writes, *sqls = nil, nil
	res, err := ConfigureRemoteAccess("taken", "%", "pw-new", "appdb")
	if err == nil {
		t.Fatalf("existing account reused, returned password %q", res.Password)
	}
	if !errors.Is(err, ErrUserExists) {
		t.Errorf("err = %v, want ErrUserExists", err)
	}
	for _, s := range *sqls {
		if strings.Contains(s, "GRANT") {
			t.Errorf("grant ran despite the refusal: %q", s)
		}
	}
	if len(*writes) != 0 {
		t.Errorf("bind-address was rewritten despite the refusal: %v", *writes)
	}
}

// F29401: the config was rewritten to bind-address 0.0.0.0 before the user was
// created, so a failed CREATE USER left the database set to listen on every
// interface (live at its next restart) with no remote account to show for it.
func TestConfigureRemoteAccessKeepsConfigWhenSQLFails(t *testing.T) {
	writes, _ := remoteAccessHooks(t, func(sql string) (string, error) {
		if strings.Contains(sql, "CREATE USER") {
			return "", errors.New("ERROR 1396: operation failed")
		}
		return "0\n", nil
	})
	if _, err := ConfigureRemoteAccess("fresh", "%", "pw", "appdb"); err == nil {
		t.Fatal("expected the SQL failure to be returned")
	}
	if len(*writes) != 0 {
		t.Errorf("config rewritten although the user was never created: %v", *writes)
	}
}
