---
name: new-module
description: Add a tenth component module to this workspace. Use when a new piece of infrastructure (a driver, a server, a broker) is being wrapped as a samsara component, and the module directory does not exist yet.
---

# Adding a component module

A new module is the nine repeated once more. Read the closest existing module
end to end before starting — for a data component, `sqlite`; for a server,
`grpc`; for a client, `grpcclient`. The answer to "how should this look?" is
"like the other nine" (ADR-0001: one module per component, no shared internal
package).

## The module

1. **Directory and `go.mod`** at the repository root, named for the component.
   Add it to `go.work`. Never import the samsara runtime — components satisfy
   its interfaces structurally, and that is what keeps them independent.
2. **`<module>.go`** — the `Component` struct, `New(cfg, opts...)`, the
   lifecycle triple (`Start`, `Stop`, `Health`), the internal handle getter, and
   the five copied boilerplate identifiers. Copy `Logger`, `nopLogger`,
   `Option`, `WithLogger` and `WithName` verbatim from an existing module; only
   `WithName`'s doc comment is module-specific (see the `boilerplate-sync`
   skill). Add the compile-time assertion block that pins the samsara method
   set.
3. **`Config`** — every tunable, valid at its zero value, each default supplied
   by an unexported accessor at the point of use. No constructor, no validate
   step (ADR-0007). Give `Config` its own `config.go` only if it grows as large
   as `grpc`'s.
4. **The caller-facing surface**, if the component has one: a narrow interface
   named for what it does, in a file named for what it does (`db.go`,
   `client.go`, `messaging.go`, `storage.go`), with the
   `var _ Iface = (*Component)(nil)` assertion beside it. Add operations with
   the `seam-operation` skill.
5. **`ErrNotReady`**, exported under exactly that name, returned from every
   operation when there is no live handle. Not a panic.
6. **Escape hatch accessor** — one exported accessor for the driver handle,
   returning nil before `Start` and after `Stop`, and never a member of the
   narrow interface (ADR-0005).
7. **Errors** wrapped as `fmt.Errorf("<module>: <operation> %q: %w", arg, err)`.
8. **Doc comments** on every exported identifier, stating pre-`Start`
   behaviour where a method is callable before `Start`.

## The tests

- `<module>_test.go` in the external test package: the lifecycle triple and the
  `Config` defaults on `*Component`; everything on the narrow interface typed as
  the interface.
- `TestConfig_ZeroValueNoPanic` — every module has one, and a new module adds
  one.
- `<module>_integration_test.go` behind `//go:build integration`, with its
  service in `docker-compose.yml` and a `SC_<NAME>_PORT` override.

## The wiring

- `README.md` in the module, following the shape of its neighbours.
- Root `CHANGELOG.md`, `## Unreleased`.
- `docs/agents/module-layout.md` if the module introduces a file name the list
  does not cover.
- `scripts/coverage-baseline.txt` via `make coverage-update` — a module with no
  baseline fails `make coverage-check`.
- `make check` and `make test-all` both cover the new module automatically;
  they discover modules from `go.mod` files.
