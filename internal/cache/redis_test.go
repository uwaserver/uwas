package cache

import (
	"context"
	"errors"
	"net/http"
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
	rc, err := NewRedisCache(config.RedisConfig{Enabled: true, Prefix: "uwas"}, nil)
	if err != nil {
		t.Fatal(err)
	}
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
	if _, err := rc.Get("page"); err == nil {
		t.Fatal("expected missing key error after delete")
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
