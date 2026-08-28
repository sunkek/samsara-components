---
name: seam-operation
description: Add, change, or remove an operation on a module's narrow interface (postgresql.DB, sqlite.DB, redis.KV, s3.Storage, rabbitmq.Publisher). Use when a caller-facing database, cache, storage, or publish operation is being added to one of the five data components, or when an existing one changes signature.
---

# Adding an operation to a narrow interface

Five modules expose a narrow interface as their caller-facing seam:
`postgresql.DB`, `sqlite.DB`, `redis.KV`, `s3.Storage`, `rabbitmq.Publisher`.
An operation is not finished when the method compiles — it is finished when
every item below is true. Work one module only; the other eight are out of
scope.

## Checklist

1. **Method on `*Component`**, in the caller-facing file for the module
   (`db.go`, `client.go`, `storage.go`/`operations.go`, `messaging.go` — see
   `docs/agents/module-layout.md`), not in `<module>.go`.
2. **Method on the interface**, in the same file, above the
   `var _ Iface = (*Component)(nil)` assertion. The assertion is what fails the
   build if only one of the two moved.
3. **Not-ready path.** Take the handle through the internal getter and return
   the module's exported `ErrNotReady` when there is none — before `Start`,
   after `Stop`, mid-restart. Never panic. Callers make one `errors.Is` check.
4. **Error wrapping.** `fmt.Errorf("<tag>: <operation> %q: %w", arg, err)`,
   where the tag is the module name — except `postgresql`, which tags
   `postgres:`.
5. **Metrics.** If the module has a `metrics.go`, route the operation through
   the same callback plumbing as its neighbours, and report a zero duration
   when the operation was never attempted (ADR-0006). Metrics live behind this
   interface, not on a new exported collector.
6. **Doc comment**, on both the interface method and the implementation. State
   the pre-`Start` behaviour.
7. **Test at the seam.** New test in `<file>_test.go`, in the external test
   package, with the subject typed as the interface rather than as
   `*Component`. Cover the `ErrNotReady` path. Reach for `_internal_test.go`
   only if the behaviour genuinely cannot be observed through the exported
   surface, and never assert through the escape hatch accessor.
8. **Integration test** in `<module>_integration_test.go` if the operation has
   real network behaviour a unit test cannot observe.
9. **Docs.** Update the module's `README.md` and the `## Unreleased` section of
   the root `CHANGELOG.md` — a public API change requires both.
10. **Verify.** `make check`, then `make test-all`. If coverage moved, run
    `make coverage-update` and commit `scripts/coverage-baseline.txt`.

## What does not go on the seam

A driver knob with no observed caller demand: the escape hatch accessor already
covers it (ADR-0005). The escape hatch itself is never a member of the narrow
interface.
