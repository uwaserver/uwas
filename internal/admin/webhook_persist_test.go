package admin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// F2021: webhooks created or deleted through the admin API must be written to
// the config file; otherwise they reappear/vanish on the next restart.

func webhookCall(t *testing.T, s *Server, create bool, arg string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	if create {
		s.handleWebhookCreate(rec, totpActivationReq("POST", "/api/v1/webhooks", arg, "admin"))
	} else {
		req := totpActivationReq("DELETE", "/api/v1/webhooks/"+arg, "", "admin")
		req.SetPathValue("id", arg)
		s.handleWebhookDelete(rec, req)
	}
	return rec.Code
}

func TestWebhookCreateDeletePersisted(t *testing.T) {
	p := filepath.Join(t.TempDir(), "uwas.yaml")
	s := testServer()
	s.SetConfigPath(p)

	for _, host := range []string{"93.184.216.34", "93.184.216.35"} {
		if c := webhookCall(t, s, true, `{"url":"https://`+host+`/hook","events":["*"]}`); c != http.StatusOK {
			t.Fatalf("create %s = %d", host, c)
		}
	}
	data, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(data), "93.184.216.34") || !strings.Contains(string(data), "93.184.216.35") {
		t.Fatalf("created webhooks not persisted: err=%v\n%s", err, data)
	}

	if c := webhookCall(t, s, false, "0"); c != http.StatusOK {
		t.Fatalf("delete = %d", c)
	}
	data, _ = os.ReadFile(p)
	if strings.Contains(string(data), "93.184.216.34") || !strings.Contains(string(data), "93.184.216.35") {
		t.Fatalf("delete not persisted (or removed the wrong entry):\n%s", data)
	}

	// Unknown id: 404 and nothing changes on disk.
	if c := webhookCall(t, s, false, "7"); c != http.StatusNotFound {
		t.Fatalf("delete unknown = %d, want 404", c)
	}
}

func TestWebhookPersistFailureIsReported(t *testing.T) {
	p := filepath.Join(t.TempDir(), "uwas.yaml")
	s := testServer()
	s.SetConfigPath(p)
	blockConfigPath(t, p)
	if c := webhookCall(t, s, true, `{"url":"https://93.184.216.34/hook"}`); c != http.StatusInternalServerError {
		t.Fatalf("create with unwritable config = %d, want 500", c)
	}
	if c := webhookCall(t, s, false, "0"); c != http.StatusInternalServerError {
		t.Fatalf("delete with unwritable config = %d, want 500", c)
	}
}
