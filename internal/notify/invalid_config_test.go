package notify

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendInvalidConfigIsDistinguishable(t *testing.T) {
	for name, ch := range map[string]Channel{
		"unknown":  {Type: "x", Enabled: true},
		"webhook":  {Type: "webhook", Enabled: true, Config: map[string]string{}},
		"slack":    {Type: "slack", Enabled: true, Config: map[string]string{}},
		"telegram": {Type: "telegram", Enabled: true, Config: map[string]string{"bot_token": "t"}},
		"email":    {Type: "email", Enabled: true, Config: map[string]string{"to": "a@b.c"}},
	} {
		if err := Send(ch, testMsg()); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%s: err = %v, want ErrInvalidConfig", name, err)
		}
	}
}

// Control: a delivery failure is not a configuration error.
func TestSendDeliveryFailureIsNotInvalidConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	err := Send(Channel{Type: "webhook", Enabled: true, Config: map[string]string{"url": srv.URL}}, testMsg())
	if err == nil {
		t.Fatal("expected a delivery error")
	}
	if errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("delivery failure misclassified as config error: %v", err)
	}
}
