package database

import (
	"errors"
	"strings"
	"testing"
)

// fakeUserServer answers the account lookup CreateDatabase makes and records
// every statement that would change the server.
func fakeUserServer(existing map[string]bool) (func(string) (string, error), *[]string) {
	var writes []string
	return func(sql string) (string, error) {
		if strings.Contains(sql, "information_schema.SCHEMATA") {
			return "", nil
		}
		if strings.Contains(sql, "FROM mysql.user") {
			for u := range existing {
				if strings.Contains(sql, "User = '"+u+"'") || strings.Contains(sql, "User='"+u+"'") {
					return "1\n", nil
				}
			}
			return "0\n", nil
		}
		writes = append(writes, sql)
		return "", nil
	}, &writes
}

// F11900: CREATE USER IF NOT EXISTS is a no-op for an existing account, so the
// password CreateDatabase returned was never set while the account (possibly
// another site's) was granted on the new database.
func TestCreateDatabaseRefusesExistingUser(t *testing.T) {
	saveHooks(t)
	fn, writes := fakeUserServer(map[string]bool{"siteb": true})
	runMySQLFn = fn

	// control: a fresh account is created and granted
	if _, err := CreateDatabase("sitea", "sitea", "pw-a", "localhost"); err != nil {
		t.Fatalf("control: %v", err)
	}
	if len(*writes) != 1 || !strings.Contains((*writes)[0], "GRANT") {
		t.Fatalf("control: expected one grant batch, got %v", *writes)
	}

	*writes = nil
	res, err := CreateDatabase("sitea2", "siteb", "pw-new", "localhost")
	if err == nil {
		t.Fatalf("existing account silently reused, returned password %q", res.Password)
	}
	if !errors.Is(err, ErrUserExists) {
		t.Errorf("err = %v, want ErrUserExists", err)
	}
	if len(*writes) != 0 {
		t.Errorf("statements ran despite the refusal: %v", *writes)
	}
}

// F11901: the panel could change the password of root and the other protected
// accounts (DropUser refuses them), cutting UWAS off its own socket login.
func TestChangePasswordRefusesProtectedUsers(t *testing.T) {
	saveHooks(t)
	var ran []string
	runMySQLFn = func(sql string) (string, error) { ran = append(ran, sql); return "", nil }

	for u := range protectedMySQLUsers {
		if err := ChangePassword(u, "localhost", "x"); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("ChangePassword(%q): err = %v, want refusal", u, err)
		}
	}
	if len(ran) != 0 {
		t.Errorf("statements ran for protected users: %v", ran)
	}
	if err := ChangePassword("siteuser", "localhost", "x"); err != nil {
		t.Fatalf("control: %v", err)
	}
	if len(ran) != 1 || !strings.Contains(ran[0], "ALTER USER 'siteuser'@'localhost'") {
		t.Errorf("control: statements = %v", ran)
	}
}
