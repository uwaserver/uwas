package notify

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendTelegramRejectsOKFalse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/botgood/") {
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":false,"description":"chat not found"}`))
	}))
	t.Cleanup(server.Close)

	oldBase, oldSafety, oldDial := telegramAPIBase, notifyURLSafetyCheck, notifyDialControl
	t.Cleanup(func() {
		telegramAPIBase, notifyURLSafetyCheck, notifyDialControl = oldBase, oldSafety, oldDial
	})
	telegramAPIBase = server.URL
	notifyURLSafetyCheck = func(string) error { return nil }
	notifyDialControl = nil

	if err := sendTelegram("good", "1", Message{Title: "control"}); err != nil {
		t.Fatalf("ok=true control failed: %v", err)
	}
	if err := sendTelegram("bad", "1", Message{Title: "rejected"}); err == nil || !strings.Contains(err.Error(), "ok=false") {
		t.Fatalf("sendTelegram() error = %v, want ok=false rejection", err)
	}
}
