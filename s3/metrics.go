package s3

import (
	"errors"
	"time"
)

// Operation names reported to [Config.OnOperation]. They are fixed per method
// and never derived from a bucket, key, or prefix, which are unbounded and
// would blow up label cardinality in the sink.
// See docs/adr/0006-metrics-behind-the-narrow-interface.md.
const (
	opUpload          = "s3.upload"
	opDownload        = "s3.download"
	opDelete          = "s3.delete"
	opDeleteByPrefix  = "s3.delete_by_prefix"
	opListKeys        = "s3.list_keys"
	opPresignDownload = "s3.presign_download"
	opPresignUpload   = "s3.presign_upload"
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
			c.log.Error("s3: metrics sink panicked", "op", op, "panic", r)
		}
	}()
	sink(op, d, err)
}

// observe times one [Storage] operation and reports the result.
//
// Unlike its redis and sqlite counterparts it fetches no handle: this
// component has three of them — the client, the presigner, and the upload
// engine — and which one an operation needs is the operation's own business.
// The not-initialised check therefore stays inside fn, where it already was.
// Because the check is inside fn rather than ahead of it, the elapsed time is
// discarded when fn reports [ErrNotReady]: the operation was not attempted, so
// it is reported with a zero duration like every other module's.
//
// Timing otherwise covers everything the exported method does, including
// argument validation, because that is the latency the caller experiences.
func observe[T any](c *Component, op string, fn func() (T, error)) (T, error) {
	start := time.Now()
	v, err := fn()
	d := time.Since(start)
	if errors.Is(err, ErrNotReady) {
		d = 0
	}
	c.record(op, d, err)
	return v, err
}

// observeErr is [observe] for the operations that return no value.
func observeErr(c *Component, op string, fn func() error) error {
	_, err := observe(c, op, func() (struct{}, error) {
		return struct{}{}, fn()
	})
	return err
}
