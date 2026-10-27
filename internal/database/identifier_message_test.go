package database

import (
	"strings"
	"testing"
)

// F36601: the rejection message for a bad database or user name listed the
// hyphen as an allowed character although validDBIdentifier rejects it, so a
// name like "my-db" was refused with a message saying it was fine.
func TestInvalidIdentifierMessageDoesNotAllowHyphen(t *testing.T) {
	if validDBIdentifier("my-db") {
		t.Fatal("precondition: hyphen must be rejected")
	}
	_, nameErr := CreateDatabase("my-db", "", "", "")
	_, userErr := CreateDatabase("mydb", "my-user", "", "")
	_, remoteErr := ConfigureRemoteAccess("my-user", "", "pw", "")
	for label, err := range map[string]error{"name": nameErr, "user": userErr, "remote": remoteErr} {
		if err == nil {
			t.Fatalf("%s: expected an error", label)
		}
		t.Logf("EXPECTED: message without \"hyphen allowed\"\nACTUAL (%s): %v", label, err)
		if strings.Contains(err.Error(), "hyphen allowed") {
			t.Errorf("PROBLEM CONFIRMED (%s): %v", label, err)
		}
	}
	// Control: underscore names pass validation (error comes later, not from the identifier check).
	if !validDBIdentifier("my_db") {
		t.Fatal("control: underscore name must be valid")
	}
}
