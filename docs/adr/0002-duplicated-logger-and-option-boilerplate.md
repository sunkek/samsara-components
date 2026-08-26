# Logger, Option, and nopLogger are duplicated per module

Every module declares its own `Logger` interface, `nopLogger`, `Option` type,
`WithLogger`, and `WithName`. This is deliberate duplication, not drift: a
shared `internal/` package would require a shared module, which ADR-0001 rules
out, and a published `common` module would put a versioned dependency between
every component and every consumer for roughly forty lines of code.

Callers pay nothing for the duplication — a `*slog.Logger` satisfies all nine
`Logger` interfaces structurally, so the same logger is passed to every
component without adapters. Maintainers pay: a change to the logging surface is
a nine-module edit.
