package database

import (
	"strings"
	"testing"
)

func TestCreateDatabaseRefusesExistingSchema(t *testing.T) {
	saveHooks(t)
	created := false
	runMySQLFn = func(sql string) (string, error) {
		switch {
		case strings.Contains(sql, "information_schema.SCHEMATA"):
			return "SCHEMA_NAME\nsitea\n", nil
		case strings.Contains(sql, "CREATE DATABASE"):
			created = true
		}
		return "", nil
	}

	if result, err := CreateDatabase("sitea", "sitea_user", "secret", "localhost"); err == nil {
		t.Fatalf("CreateDatabase() = %#v, nil; want existing-schema refusal", result)
	}
	if created {
		t.Fatal("CreateDatabase issued CREATE/GRANT SQL for an existing schema")
	}

	var mutation string
	runMySQLFn = func(sql string) (string, error) {
		if strings.Contains(sql, "CREATE DATABASE") {
			mutation = sql
		}
		return "", nil
	}
	if _, err := CreateDatabase("fresh", "fresh_user", "secret", "localhost"); err != nil {
		t.Fatalf("fresh control: %v", err)
	}
	if strings.Contains(mutation, "CREATE DATABASE IF NOT EXISTS") || strings.Contains(mutation, "CREATE USER IF NOT EXISTS") {
		t.Fatalf("mutation remains race-prone: %s", mutation)
	}
}
