package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPHPUpdateAndUninstallViaAPI(t *testing.T) {
	old := phpPollInterval
	phpPollInterval = time.Millisecond
	defer func() { phpPollInterval = old }()

	var polls atomic.Int32
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST":
			b, _ := io.ReadAll(r.Body)
			gotPath, gotBody = r.URL.Path, string(b)
			w.Write([]byte(`{"status":"started"}`))
		case r.URL.Path == "/api/v1/php/install/status":
			if polls.Add(1) < 3 {
				w.Write([]byte(`{"status":"running"}`))
				return
			}
			w.Write([]byte(`{"status":"done","output":"ok\n"}`))
		}
	}))
	defer srv.Close()

	if err := (&PHPCommand{}).update([]string{"--api-url", srv.URL, "8.3"}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/php/update" || !strings.Contains(gotBody, `"version":"8.3"`) {
		t.Fatalf("update posted %s %s", gotPath, gotBody)
	}

	polls.Store(0)
	if err := (&PHPCommand{}).uninstall([]string{"8.1", "--purge", "--api-url", srv.URL}); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(gotBody), &m)
	if gotPath != "/api/v1/php/uninstall" || m["version"] != "8.1" || m["purge"] != true {
		t.Fatalf("uninstall posted %s %s", gotPath, gotBody)
	}
	if err := (&PHPCommand{}).uninstall(nil); err == nil {
		t.Fatal("uninstall without version must fail")
	}
}

func TestPHPTaskReportsFailureAndRefusal(t *testing.T) {
	old := phpPollInterval
	phpPollInterval = time.Millisecond
	defer func() { phpPollInterval = old }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			http.Error(w, `{"error":"PHP 8.3 is used by 1 domain(s)"}`, http.StatusConflict)
			return
		}
	}))
	defer srv.Close()
	err := (&PHPCommand{}).uninstall([]string{"8.3", "--api-url", srv.URL})
	if err == nil || !strings.Contains(err.Error(), "used by 1 domain") {
		t.Fatalf("err = %v", err)
	}
}
