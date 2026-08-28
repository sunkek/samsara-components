# Logger, Option, and nopLogger are duplicated per module

Every module declares its own `Logger` interface, `nopLogger`, `Option` type,
`WithLogger`, and `WithName`. This is deliberate duplication, not drift: a
shared `internal/` package would require a shared module, which ADR-0001 rules
out, and a published `common` module would put a versioned dependency between
every component and every consumer for roughly forty lines of code.

This was reopened for metrics, which is real implementation rather than an
interface declaration, and so is not covered by the forty-lines argument above.
It was rejected on different grounds — see
[ADR-0006](./0006-metrics-behind-the-narrow-interface.md).

Callers pay nothing for the duplication — a `*slog.Logger` satisfies all nine
`Logger` interfaces structurally, so the same logger is passed to every
component without adapters. Maintainers pay: a change to the logging surface is
a nine-module edit.
