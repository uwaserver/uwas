package cache

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// RedisCache provides L3 caching using Redis.
type RedisCache struct {
	client RedisClient
	prefix string
	logger *logger.Logger
}

// RedisClient is the interface for Redis operations (allows mocking in tests).
type RedisClient interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string, ttl time.Duration) error
	Del(ctx context.Context, keys ...string) error
	Keys(ctx context.Context, pattern string) ([]string, error)
	Close() error
}

// NewRedisCache creates a new Redis cache instance.
func NewRedisCache(cfg config.RedisConfig, log *logger.Logger) (*RedisCache, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	client, err := newRedisClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &RedisCache{
		client: client,
		prefix: cfg.Prefix,
		logger: log,
	}, nil
}

// realRedisClient wraps a real Redis client.
type realRedisClient struct {
	addr     string
	password string
	db       int
	tls      bool
}

func newRedisClient(cfg config.RedisConfig) (RedisClient, error) {
	// For now, return a mock client that logs operations
	// In production, this would connect to actual Redis
	return &mockRedisClient{}, nil
}

// mockRedisClient is a mock implementation for when Redis is not available.
type mockRedisClient struct {
	data map[string]string
}

func (m *mockRedisClient) Get(ctx context.Context, key string) (string, error) {
	if m.data == nil {
		return "", fmt.Errorf("key not found")
	}
	val, ok := m.data[key]
	if !ok {
		return "", fmt.Errorf("key not found")
	}
	return val, nil
}

func (m *mockRedisClient) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	if m.data == nil {
		m.data = make(map[string]string)
	}
	m.data[key] = value
	return nil
}

func (m *mockRedisClient) Del(ctx context.Context, keys ...string) error {
	if m.data == nil {
		return nil
	}
	for _, k := range keys {
		delete(m.data, k)
	}
	return nil
}

func (m *mockRedisClient) Keys(ctx context.Context, pattern string) ([]string, error) {
	return nil, nil
}

func (m *mockRedisClient) Close() error {
	return nil
}

// prefixKey adds the configured prefix to a cache key.
func (r *RedisCache) prefixKey(key string) string {
	if r.prefix == "" {
		return key
	}
	return r.prefix + ":" + key
}

// Get retrieves a cached response from Redis.
func (r *RedisCache) Get(key string) (*CachedResponse, error) {
	if r == nil || r.client == nil {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	data, err := r.client.Get(ctx, r.prefixKey(key))
	if err != nil {
		return nil, err
	}

	var resp CachedResponse
	if err := json.Unmarshal([]byte(data), &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// Set stores a response in Redis.
func (r *RedisCache) Set(key string, resp *CachedResponse, ttl time.Duration) error {
	if r == nil || r.client == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}

	return r.client.Set(ctx, r.prefixKey(key), string(data), ttl)
}

// Delete removes a key from Redis.
func (r *RedisCache) Delete(key string) error {
	if r == nil || r.client == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return r.client.Del(ctx, r.prefixKey(key))
}

// PurgeByTag removes all keys matching a tag pattern.
func (r *RedisCache) PurgeByTag(tag string) error {
	if r == nil || r.client == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pattern := r.prefixKey("*tag:" + tag + "*")
	keys, err := r.client.Keys(ctx, pattern)
	if err != nil {
		return err
	}

	if len(keys) > 0 {
		return r.client.Del(ctx, keys...)
	}
	return nil
}

// Close closes the Redis connection.
func (r *RedisCache) Close() error {
	if r == nil || r.client == nil {
		return nil
	}
	return r.client.Close()
}

// redisCacheClient implements RedisClient using the actual Redis protocol.
// This is a placeholder for the real implementation.
type redisCacheClient struct {
	addr     string
	password string
	db       int
	tlsConfig *tls.Config
}
