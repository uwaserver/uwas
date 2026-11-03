package cloudflare

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type accountLimitRoundTrip func(*http.Request) (*http.Response, error)

func (f accountLimitRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type accountLimitBody struct {
	reader *strings.Reader
	read   int
}

func (b *accountLimitBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.read += n
	return n, err
}

func (b *accountLimitBody) Close() error { return nil }

func TestValidateTokenBoundsAccountResponse(t *testing.T) {
	const limit = 8 << 20
	body := &accountLimitBody{reader: strings.NewReader(`{"success":true,"result":{"name":"` + strings.Repeat("a", limit+1) + `"}}`)}
	client := &http.Client{Transport: accountLimitRoundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/accounts/") {
			return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"success":true}`)), Header: make(http.Header)}, nil
	})}

	if _, err := ValidateTokenWithClient(client, "token", "account"); err == nil {
		t.Fatal("oversized account response unexpectedly decoded")
	}
	if body.read > limit {
		t.Fatalf("account response read %d bytes, limit %d", body.read, limit)
	}
}
