package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

func TestDomainHealthIncludesAliasesAndEnvelope(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer primary.Close()

	alias := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer alias.Close()

	primaryHost := strings.TrimPrefix(primary.URL, "http://")
	aliasHost := strings.TrimPrefix(alias.URL, "http://")

	s := testServer()
	s.config.Domains = []config.Domain{
		{
			Host:    primaryHost,
			Aliases: []string{aliasHost},
			Type:    "static",
			SSL:     config.SSLConfig{Mode: "off"},
		},
	}

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/domains/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body struct {
		Items []struct {
			Host       string `json:"host"`
			ParentHost string `json:"parent_host"`
			Kind       string `json:"kind"`
			Status     string `json:"status"`
			Code       int    `json:"code"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}

	if body.Total != 2 || len(body.Items) != 2 {
		t.Fatalf("health items = %d total = %d, want 2/2", len(body.Items), body.Total)
	}
	if body.Items[0].Host != primaryHost || body.Items[0].Kind != "primary" || body.Items[0].Status != "up" {
		t.Fatalf("primary health = %#v", body.Items[0])
	}
	if body.Items[1].Host != aliasHost || body.Items[1].ParentHost != primaryHost || body.Items[1].Kind != "alias" || body.Items[1].Status != "up" {
		t.Fatalf("alias health = %#v", body.Items[1])
	}
}
