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

	// SetNX atomically stores value at key with the given TTL only if key does
	// not already exist (Redis SET ... NX). Returns true when the key was set by
	// this call (the caller won the claim), false when it already existed. Use
	// ttl=0 for no expiry. This is the race-free building block for single-use
	// claims, locks, and idempotency keys.
	SetNX(ctx context.Context, key string, value any, ttl time.Duration) (bool, error)

	// Get returns the string value at key.
	// Returns [ErrNil] if the key does not exist.
	Get(ctx context.Context, key string) (string, error)

	// Del deletes one or more keys. Returns the number of keys removed.
	Del(ctx context.Context, keys ...string) (int64, error)

	// Exists reports how many of the given keys exist.
	Exists(ctx context.Context, keys ...string) (int64, error)

	// Incr atomically increments the integer stored at key by one and returns
	// the new value. A missing key counts as 0, so the first Incr returns 1 —
	// use that to arm the window TTL exactly once when building a fixed-window
	// counter (rate limits, quotas):
	//
	//	n, err := c.Incr(ctx, key)
	//	if n == 1 { c.Expire(ctx, key, window) }
	//	if n > max { /* limit exceeded */ }
	//
	// Returns an error if the value at key is not an integer.
	Incr(ctx context.Context, key string) (int64, error)

	// Expire sets a timeout on key. Returns true if the timeout was set.
	Expire(ctx context.Context, key string, ttl time.Duration) (bool, error)

	// TTL returns the remaining TTL of key.
	// Returns a negative value if the key does not exist or has no expiry.
	TTL(ctx context.Context, key string) (time.Duration, error)

	// Scan iterates over keys matching pattern and returns all matches.
	// Uses cursor-based iteration internally; safe for large key spaces.
	Scan(ctx context.Context, pattern string) ([]string, error)
}

// Compile-time assertion: *Component satisfies Client.
var _ Client = (*Component)(nil)

// ErrNil is returned by [Client.Get] when the key does not exist.
// Use errors.Is(err, redis.ErrNil) to check.
var ErrNil = redis.Nil

// ErrNotReady is returned by every [Client] operation when the component has
// no live connection: before [Component.Start] succeeds, after
// [Component.Stop], or while the supervisor is restarting it (e.g. Redis is
// down). Callers get this error instead of a nil-pointer panic and can choose
// to fail open. Use errors.Is(err, redis.ErrNotReady) to check.
var ErrNotReady = errors.New("redis: client not initialised")

// Set stores value at key with the given TTL. Use ttl=0 for no expiry.
func (c *Component) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	client := c.getClient()
	if client == nil {
		return ErrNotReady
	}
	if err := client.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("redis set %q: %w", key, err)
	}
	return nil
}

// SetNX atomically stores value at key with the given TTL only if key does not
// already exist (Redis SET ... NX). Returns true when the key was set by this
// call (the caller won the claim), false when it already existed. Use ttl=0 for
// no expiry. Single Redis round-trip; atomic server-side.
func (c *Component) SetNX(ctx context.Context, key string, value any, ttl time.Duration) (bool, error) {
	client := c.getClient()
	if client == nil {
		return false, ErrNotReady
	}
	// SetArgs with Mode "NX" maps to SET key value NX [EX ttl] — a single
	// atomic command. (client.SetNX is deprecated in go-redis.) On a losing
	// claim the server returns nil, surfaced here as redis.Nil.
	err := client.SetArgs(ctx, key, value, redis.SetArgs{Mode: "NX", TTL: ttl}).Err()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("redis setnx %q: %w", key, err)
	}
	return true, nil
}

// Get returns the string value stored at key.
// Returns [ErrNil] if the key does not exist.
func (c *Component) Get(ctx context.Context, key string) (string, error) {
	client := c.getClient()
	if client == nil {
		return "", ErrNotReady
	}
	val, err := client.Get(ctx, key).Result()
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
	client := c.getClient()
	if client == nil {
		return 0, ErrNotReady
	}
	n, err := client.Del(ctx, keys...).Result()
	if err != nil {
		return 0, fmt.Errorf("redis del: %w", err)
	}
	return n, nil
}

// Exists reports how many of the given keys currently exist.
func (c *Component) Exists(ctx context.Context, keys ...string) (int64, error) {
	client := c.getClient()
	if client == nil {
		return 0, ErrNotReady
	}
	n, err := client.Exists(ctx, keys...).Result()
	if err != nil {
		return 0, fmt.Errorf("redis exists: %w", err)
	}
	return n, nil
}

// Incr atomically increments the integer at key by one and returns the new
// value. A missing key is treated as 0, so the first call returns 1 — callers
// building a fixed-window counter can use that to set the window TTL exactly
// once (Incr, then Expire when the result is 1).
func (c *Component) Incr(ctx context.Context, key string) (int64, error) {
	client := c.getClient()
	if client == nil {
		return 0, ErrNotReady
	}
	n, err := client.Incr(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("redis incr %q: %w", key, err)
	}
	return n, nil
}

// Expire sets a TTL on key. Returns true if the key exists and the timeout
// was set, false if the key does not exist.
func (c *Component) Expire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	client := c.getClient()
	if client == nil {
		return false, ErrNotReady
	}
	ok, err := client.Expire(ctx, key, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("redis expire %q: %w", key, err)
	}
	return ok, nil
}

// TTL returns the remaining time-to-live of key.
// Returns -2 if the key does not exist, -1 if the key has no expiry.
func (c *Component) TTL(ctx context.Context, key string) (time.Duration, error) {
	client := c.getClient()
	if client == nil {
		return 0, ErrNotReady
	}
	d, err := client.TTL(ctx, key).Result()
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
	client := c.getClient()
	if client == nil {
		return nil, ErrNotReady
	}
	var (
		cursor uint64
		keys   []string
	)
	for {
		batch, next, err := client.Scan(ctx, cursor, pattern, 100).Result()
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
