package php

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func putJSON(h http.HandlerFunc, path, pathKey, pathVal, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("PUT", path, strings.NewReader(body))
	req.SetPathValue(pathKey, pathVal)
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// F58300: a rejected per-domain directive (blocked, malformed, control
// characters) is a client error, not a 404; only a domain without a PHP
// assignment is "not found".
func TestDomainConfigPutStatusCodes(t *testing.T) {
	d := &fakeDeps{mgr: twoTenantMgr(), allowed: map[string]bool{"tenant-a.com": true}}
	h := New(d).DomainConfigPut
	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"blocked directive": {`{"key":"open_basedir","value":"/"}`, http.StatusBadRequest},
		"bad name":          {`{"key":"a b","value":"1"}`, http.StatusBadRequest},
		"newline value":     {`{"key":"memory_limit","value":"1M\nx=y"}`, http.StatusBadRequest},
		"control: valid":    {`{"key":"memory_limit","value":"128M"}`, http.StatusOK},
	} {
		rec := putJSON(h, "/api/v1/php/domains/tenant-a.com/config", "domain", "tenant-a.com", tc.body)
		if rec.Code != tc.want {
			t.Errorf("%s: status %d, want %d: %s", name, rec.Code, tc.want, rec.Body)
		}
	}
	admin := &fakeDeps{mgr: twoTenantMgr(), admin: true}
	rec := putJSON(New(admin).DomainConfigPut, "/x", "domain", "unassigned.com", `{"key":"memory_limit","value":"1M"}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unassigned domain: status %d, want 404: %s", rec.Code, rec.Body)
	}
}

// F58301: the global php.ini update answers 400 for a malformed directive and
// 404 for a PHP version that is not installed (it answered 500 for both).
func TestConfigUpdateStatusCodes(t *testing.T) {
	d := &fakeDeps{mgr: twoTenantMgr(), admin: true}
	h := New(d).ConfigUpdate
	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"newline value":   {`{"key":"memory_limit","value":"1M\ndisable_functions ="}`, http.StatusBadRequest},
		"unknown version": {`{"key":"memory_limit","value":"256M"}`, http.StatusNotFound},
	} {
		rec := putJSON(h, "/api/v1/php/9.9.9/config", "version", "9.9.9", tc.body)
		if rec.Code != tc.want {
			t.Errorf("%s: status %d, want %d: %s", name, rec.Code, tc.want, rec.Body)
		}
	}
}
