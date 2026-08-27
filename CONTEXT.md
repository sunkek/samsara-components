# samsara-components

Infrastructure components for the [samsara](https://github.com/sunkek/samsara)
lifecycle runtime. This context is the vocabulary shared by all nine modules;
each module speaks it identically, which is why a reader who learns one module
can read the rest.

## Language

**Component**:
The single exported type of a module (`Component`), owning one piece of
infrastructure — a server, a pool, a connection — for the whole process
lifetime.
_Avoid_: service, client (a `Component` may expose a client; it is not one),
wrapper.

**Lifecycle**:
The `Start` / `Stop` / `Health` triple a supervisor calls on a component. A
running phase, not a construction step.
_Avoid_: init, bootstrap, run loop.

**Ready signal**:
The one-shot notification a component raises when it becomes usable, releasing
the supervisor's next tier.
_Avoid_: ready flag, ready channel, signal.

**Structural satisfaction**:
Components match samsara's interfaces by method set alone, never by importing
samsara. This is why no module depends on the runtime it is built for.
_Avoid_: implements, conforms to (both imply a declared dependency).

**Config**:
A component's tunables, valid at their zero value, with every default supplied
at the point of use rather than by a constructor or a validate step.
_Avoid_: options struct, settings (`Option` means something else here).

**Option**:
A component's dependencies and identity, supplied at construction —
`WithLogger`, `WithName`. Distinct from `Config`, which holds tunables.
_Avoid_: functional option, setting.

## RabbitMQ messaging

**Subscription**:
A queue binding paired with its handler, owned by the component and re-applied
on every start, so a restart restores the full topology.
_Avoid_: consumer (that is the AMQP-side object), listener.

**Retry topology**:
A subscription's retry queue, dead-letter queue, and the republish path between
them, all derived from its `RetryPolicy`.
_Avoid_: retry mechanism, backoff setup.

**Dead-letter queue (DLQ)**:
The terminal destination of a delivery that exhausted its retries. Nothing in
these components consumes from it.
_Avoid_: failure queue, error queue.
