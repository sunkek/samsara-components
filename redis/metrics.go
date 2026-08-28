package redis

import (
	"time"

	"github.com/redis/go-redis/v9"
)

// Operation names reported to [Config.OnOperation]. They are fixed per method
// and never derived from a key or a pattern, which are unbounded and would
// blow up label cardinality in the sink.
// See docs/adr/0006-metrics-behind-the-narrow-interface.md.
const (
	opSet    = "redis.set"
	opSetNX  = "redis.setnx"
	opGet    = "redis.get"
	opDel    = "redis.del"
	opExists = "redis.exists"
	opIncr   = "redis.incr"
	opExpire = "redis.expire"
	opTTL    = "redis.ttl"
	opScan   = "redis.scan"
)

// record reports one completed operation to the configured sink. A nil sink
// is the default, so this is a no-op unless the caller set one.
//
// A panicking sink must not take down the caller's operation, which has
// already completed by the time we get here.
func (c *Component) record(op string, d time.Duration, err error) {
	sink := c.cfg.onOperation()
	if sink == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			c.log.Error("redis: metrics sink panicked", "op", op, "panic", r)
		}
	}()
	sink(op, d, err)
}

// observe runs one [KV] operation against the live client, times it, and
// reports the result. It also carries the not-ready check that every operation
// needs: with no live connection the operation is not attempted, and is
// reported with a zero duration and [ErrNotReady].
//
// fn returns this module's own error — [ErrNil], [ErrNotReady], or a wrapped
// driver error — not the raw go-redis one, so the sink sees a stable
// vocabulary. Timing covers the driver call only.
func observe[T any](c *Component, op string, fn func(*redis.Client) (T, error)) (T, error) {
	var zero T
	client := c.getClient()
	if client == nil {
		c.record(op, 0, ErrNotReady)
		return zero, ErrNotReady
	}
	start := time.Now()
	v, err := fn(client)
	c.record(op, time.Since(start), err)
	return v, err
}

// observeErr is [observe] for the operations that return no value.
func observeErr(c *Component, op string, fn func(*redis.Client) error) error {
	_, err := observe(c, op, func(client *redis.Client) (struct{}, error) {
		return struct{}{}, fn(client)
	})
	return err
}
