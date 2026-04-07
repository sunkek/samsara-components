package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client is the interface that domain adapters should depend on.
// *Component satisfies it; depend on Client rather than *Component to keep
// adapters testable without a real Redis server.
//
//	type SessionStore struct { rdb redis.Client }
type Client interface {
	// Set stores value at key with the given TTL.
	// Use ttl=0 for no expiry.
	Set(ctx context.Context, key string, value any, ttl time.Duration) error

	// Get returns the string value at key.
	// Returns [ErrNil] if the key does not exist.
	Get(ctx context.Context, key string) (string, error)

	// Del deletes one or more keys. Returns the number of keys removed.
	Del(ctx context.Context, keys ...string) (int64, error)

	// Exists reports how many of the given keys exist.
	Exists(ctx context.Context, keys ...string) (int64, error)

	// Expire sets a timeout on key. Returns true if the timeout was set.
	Expire(ctx context.Context, key string, ttl time.Duration) (bool, error)

	// TTL returns the remaining TTL of key.
	// Returns a negative value if the key does not exist or has no expiry.
	TTL(ctx context.Context, key string) (time.Duration, error)

	// Scan iterates over keys matching pattern and returns all matches.
	// Uses cursor-based iteration internally; safe for large key spaces.
	Scan(ctx context.Context, pattern string) ([]string, error)
}

// ErrNil is returned by [Client.Get] when the key does not exist.
// Use errors.Is(err, redis.ErrNil) to check.
var ErrNil = redis.Nil

// Set stores value at key with the given TTL. Use ttl=0 for no expiry.
func (c *Component) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	if err := c.getClient().Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("redis set %q: %w", key, err)
	}
	return nil
}

// Get returns the string value stored at key.
// Returns [ErrNil] if the key does not exist.
func (c *Component) Get(ctx context.Context, key string) (string, error) {
	val, err := c.getClient().Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", ErrNil
		}
		return "", fmt.Errorf("redis get %q: %w", key, err)
	}
	return val, nil
}

// Del deletes one or more keys. Returns the count of removed keys.
func (c *Component) Del(ctx context.Context, keys ...string) (int64, error) {
	n, err := c.getClient().Del(ctx, keys...).Result()
	if err != nil {
		return 0, fmt.Errorf("redis del: %w", err)
	}
	return n, nil
}

// Exists reports how many of the given keys currently exist.
func (c *Component) Exists(ctx context.Context, keys ...string) (int64, error) {
	n, err := c.getClient().Exists(ctx, keys...).Result()
	if err != nil {
		return 0, fmt.Errorf("redis exists: %w", err)
	}
	return n, nil
}

// Expire sets a TTL on key. Returns true if the key exists and the timeout
// was set, false if the key does not exist.
func (c *Component) Expire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	ok, err := c.getClient().Expire(ctx, key, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("redis expire %q: %w", key, err)
	}
	return ok, nil
}

// TTL returns the remaining time-to-live of key.
// Returns -2 if the key does not exist, -1 if the key has no expiry.
func (c *Component) TTL(ctx context.Context, key string) (time.Duration, error) {
	d, err := c.getClient().TTL(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("redis ttl %q: %w", key, err)
	}
	return d, nil
}

// Scan iterates over all keys matching pattern using cursor-based SCAN and
// returns the complete set. Safe for large key spaces — does not use KEYS.
//
// pattern follows Redis glob-style syntax: * matches any sequence,
// ? matches a single character, [abc] matches a character class.
func (c *Component) Scan(ctx context.Context, pattern string) ([]string, error) {
	var (
		cursor uint64
		keys   []string
	)
	for {
		batch, next, err := c.getClient().Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, fmt.Errorf("redis scan %q: %w", pattern, err)
		}
		keys = append(keys, batch...)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return keys, nil
}
