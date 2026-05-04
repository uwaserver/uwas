package cache

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
)

type errRedisClient struct{}

func (errRedisClient) Get(context.Context, string) (string, error) {
	return "", errors.New("get failed")
}
func (errRedisClient) Set(context.Context, string, string, time.Duration) error {
	return errors.New("set failed")
}
func (errRedisClient) Del(context.Context, ...string) error { return errors.New("del failed") }
func (errRedisClient) Keys(context.Context, string) ([]string, error) {
	return nil, errors.New("keys failed")
}
func (errRedisClient) Close() error { return errors.New("close failed") }

// stubRedisClient is an in-memory RedisClient used only by tests so we can
// exercise RedisCache logic without a live Redis. Returns ErrRedisNotFound
// on missing keys to mirror the real client.
type stubRedisClient struct {
	data map[string]string
}

func (s *stubRedisClient) Get(_ context.Context, key string) (string, error) {
	if s.data == nil {
		return "", ErrRedisNotFound
	}
	v, ok := s.data[key]
	if !ok {
		return "", ErrRedisNotFound
	}
	return v, nil
}
func (s *stubRedisClient) Set(_ context.Context, key, value string, _ time.Duration) error {
	if s.data == nil {
		s.data = map[string]string{}
	}
	s.data[key] = value
	return nil
}
func (s *stubRedisClient) Del(_ context.Context, keys ...string) error {
	for _, k := range keys {
		delete(s.data, k)
	}
	return nil
}
func (s *stubRedisClient) Keys(_ context.Context, pattern string) ([]string, error) {
	// minimal glob: match * as suffix wildcard
	out := []string{}
	for k := range s.data {
		if pattern == "*" || strings.HasPrefix(k, strings.TrimSuffix(pattern, "*")) {
			out = append(out, k)
		}
	}
	return out, nil
}
func (s *stubRedisClient) Close() error { return nil }

func TestNewRedisCacheDisabled(t *testing.T) {
	rc, err := NewRedisCache(config.RedisConfig{Enabled: false}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rc != nil {
		t.Fatalf("expected nil Redis cache when disabled, got %#v", rc)
	}
}

func TestRedisCacheOperations(t *testing.T) {
	// Use the in-process stub client so this test doesn't need a live Redis.
	rc := &RedisCache{client: &stubRedisClient{}, prefix: "uwas"}
	if rc.prefixKey("page") != "uwas:page" {
		t.Fatalf("prefixed key mismatch: %q", rc.prefixKey("page"))
	}

	resp := &CachedResponse{
		StatusCode: http.StatusOK,
		Headers:    http.Header{"Content-Type": {"text/plain"}},
		Body:       []byte("hello"),
		Created:    time.Now(),
		TTL:        time.Minute,
	}
	if err := rc.Set("page", resp, time.Minute); err != nil {
		t.Fatal(err)
	}
	got, err := rc.Get("page")
	if err != nil {
		t.Fatal(err)
	}
	if got.StatusCode != http.StatusOK || string(got.Body) != "hello" {
		t.Fatalf("unexpected cached response: %#v", got)
	}
	if err := rc.Delete("page"); err != nil {
		t.Fatal(err)
	}
	// Cache miss after delete -> Get returns (nil, nil) per the new contract.
	if v, err := rc.Get("page"); err != nil || v != nil {
		t.Fatalf("expected (nil, nil) after delete, got (%v, %v)", v, err)
	}
	if err := rc.PurgeByTag("news"); err != nil {
		t.Fatal(err)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRedisCacheNilReceiver(t *testing.T) {
	var rc *RedisCache
	if got, err := rc.Get("x"); got != nil || err != nil {
		t.Fatalf("nil Get = %#v, %v; want nil nil", got, err)
	}
	if err := rc.Set("x", &CachedResponse{}, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := rc.Delete("x"); err != nil {
		t.Fatal(err)
	}
	if err := rc.PurgeByTag("x"); err != nil {
		t.Fatal(err)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRedisCacheClientErrors(t *testing.T) {
	rc := &RedisCache{client: errRedisClient{}}
	if _, err := rc.Get("x"); err == nil {
		t.Fatal("expected get error")
	}
	if err := rc.Set("x", &CachedResponse{}, time.Second); err == nil {
		t.Fatal("expected set error")
	}
	if err := rc.Delete("x"); err == nil {
		t.Fatal("expected delete error")
	}
	if err := rc.PurgeByTag("x"); err == nil {
		t.Fatal("expected keys error")
	}
	if err := rc.Close(); err == nil {
		t.Fatal("expected close error")
	}
}
