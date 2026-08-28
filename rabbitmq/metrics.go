package rabbitmq

import "time"

// Operation names reported to [Config.OnOperation]. They are fixed per method
// and never derived from an exchange or routing key, which are unbounded and
// would blow up label cardinality in the sink.
// See docs/adr/0006-metrics-behind-the-narrow-interface.md.
const (
	opPublish            = "rabbitmq.publish"
	opPublishWithType    = "rabbitmq.publish_with_type"
	opPublishWithHeaders = "rabbitmq.publish_with_headers"
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
			c.log.Error("rabbitmq: metrics sink panicked", "op", op, "panic", r)
		}
	}()
	sink(op, d, err)
}

// observePublish times one [Publisher] call and reports the result.
//
// Unlike its redis and sqlite counterparts it fetches no handle: the three
// publish methods already funnel through [Component.publish], which takes the
// channel and its no-live-channel check with it. The operation name is what
// distinguishes them, so it is threaded down rather than fetched here.
func (c *Component) observePublish(op string, fn func() error) error {
	start := time.Now()
	err := fn()
	c.record(op, time.Since(start), err)
	return err
}
