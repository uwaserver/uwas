package database

import (
	"runtime"
	"testing"
)

func TestGetStatus(t *testing.T) {
	st := GetStatus()

	// On Windows, GetStatus returns early with Backend: "none"
	if runtime.GOOS == "windows" {
		if st.Backend != "none" {
			t.Errorf("expected backend 'none' on Windows, got %q", st.Backend)
		}
		if st.Installed {
			t.Error("expected Installed=false on Windows")
		}
		return
	}

	// On Linux, backend should be one of the valid values
	validBackends := map[string]bool{"mysql": true, "mariadb": true, "none": true}
	if !validBackends[st.Backend] {
		t.Errorf("unexpected backend: %q", st.Backend)
	}
}

func TestBacktick(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"mydb", "`mydb`"},
		{"my`db", "`my``db`"},
		{"", "``"},
		{"test_db", "`test_db`"},
		{"`", "````"},
	}

	for _, tt := range tests {
		got := backtick(tt.input)
		if got != tt.want {
			t.Errorf("backtick(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestEscapeSQL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "simple"},
		{"", ""},
		{"hello world", "hello world"},
	}

	for _, tt := range tests {
		got := escapeSQL(tt.input)
		if got != tt.want {
			t.Errorf("escapeSQL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCreateResultStruct(t *testing.T) {
	r := CreateResult{
		Name:     "testdb",
		User:     "testuser",
		Password: "secret",
		Host:     "localhost",
	}

	if r.Name != "testdb" {
		t.Errorf("expected Name 'testdb', got %q", r.Name)
	}
	if r.User != "testuser" {
		t.Errorf("expected User 'testuser', got %q", r.User)
	}
	if r.Password != "secret" {
		t.Errorf("expected Password 'secret', got %q", r.Password)
	}
	if r.Host != "localhost" {
		t.Errorf("expected Host 'localhost', got %q", r.Host)
	}
}

func TestDBInfoStruct(t *testing.T) {
	info := DBInfo{
		Name:   "mydb",
		User:   "myuser",
		Host:   "localhost",
		Size:   "10 MB",
		Tables: 5,
	}

	if info.Name != "mydb" {
		t.Errorf("expected Name 'mydb', got %q", info.Name)
	}
	if info.Tables != 5 {
		t.Errorf("expected Tables 5, got %d", info.Tables)
	}
}

func TestStatusStruct(t *testing.T) {
	st := Status{
		Installed: true,
		Running:   true,
		Version:   "10.5.0",
		Backend:   "mariadb",
	}

	if !st.Installed {
		t.Error("expected Installed=true")
	}
	if st.Backend != "mariadb" {
		t.Errorf("expected Backend 'mariadb', got %q", st.Backend)
	}
}
