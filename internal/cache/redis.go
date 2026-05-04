package cache

import (
	"context"
	"encoding/json"
	"errors"
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

// newRedisClient is the production factory. Connects via TCP/RESP to the
// configured Redis instance and returns the live client. Returns an error
// (rather than a fake client) if the connection fails — the caller decides
// whether to disable L3 caching or fail startup.
func newRedisClient(cfg config.RedisConfig) (RedisClient, error) {
	return newRespClient(cfg)
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
		// Cache miss is not an error to propagate.
		if errors.Is(err, ErrRedisNotFound) {
			return nil, nil
		}
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

