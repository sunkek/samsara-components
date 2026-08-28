# Config fields are the interface; the accessors are implementation

A depth audit of the `Config` pattern, prompted by the shape it presents: nine
modules, each with a `Config` struct and a set of unexported accessors, one per
tunable that has a default. Counted at the time of writing:

| module | exported fields | unexported accessors |
| --- | --- | --- |
| fiber | 22 | 11 |
| redis | 18 | 4 |
| grpc | 15 | 9 |
| grpcclient | 14 | 8 |
| postgresql | 11 | 3 |
| s3 | 10 | 5 |
| rabbitmq | 9 | 4 |
| sqlite | 8 | 10 |
| prometheus | 6 | 5 |

Read as a module, that looks shallow: a wide surface over thin bodies, most of
them `if unset { return default }`. The audit's conclusion is that the reading
misplaces the interface, and that the numbers to watch are in the left column,
not the right.

## The accessors are not on the interface

An interface is everything a caller must know to use the module correctly. The
accessors are unexported, so no caller learns them, no test names them, and no
adapter implements them. They cost the maintainer locality, not the caller
leverage — they are implementation, and their count is not a depth signal.

Nor are they uniformly thin. `sqlite` has more accessors than fields precisely
because several are derived rather than defaulted: `inMemory()` reads the path,
`maxOpenConns()` pins the pool to 1 when it is true because an in-memory
database is private per connection, and `dsn()` assembles the pragma string
from four others. That is behaviour hidden behind the seam, which is depth
working as intended. The `if unset` ones are the floor of the pattern, not its
purpose.

## The field count is the real number

`Config`'s exported fields *are* the interface, and fiber's 22 and redis's 18
are the honest depth pressure in this repository. Two things blunt it:

- **Zero value works.** `Config{}` produces a usable component, asserted per
  module by `TestConfig_ZeroValueNoPanic`. A caller learns the fields they want
  to change and no others, so the learning cost is paid per-field on demand
  rather than per-struct up front. A wide struct whose zero value is a full
  configuration is not the same object as a wide struct that must be filled in.
- **Defaults live at the point of use.** There is no constructor to read and no
  validate step to trace: the default for a field is in the accessor that reads
  it, next to any derivation it participates in.

## Decision

Keep the pattern as it stands. Specifically:

- **Accessors stay unexported and stay per-field.** Promoting them, or
  collapsing them into one `withDefaults()` that returns a filled `Config`,
  would move defaulting away from the point of use for no gain at the
  interface — and would make derived accessors like `sqlite.dsn()` the odd ones
  out again.
- **Field count is the budget to defend.** A new tunable is a permanent
  addition to the module's interface, so it earns its place by a caller needing
  it, not by the driver exposing it. Where a driver knob has no observed
  demand, the escape hatch accessor
  ([ADR-0005](./0005-driver-escape-hatch-accessors.md)) already covers it.
- **Derivation belongs in an accessor, not in `Start`.** When two fields
  interact, the accessor that resolves them is the place the interaction is
  documented and tested.

## Considered options

- **Keep the pattern** (chosen).
- **One `withDefaults()` per module** returning a resolved `Config`. Fewer
  functions, but defaults stop being local to their use, and derived values
  either bloat the resolved struct or stay as accessors anyway — the pattern
  split in two.
- **Constructor-supplied defaults** (`NewConfig()`), rejected earlier by the
  zero-value rule: it makes `Config{}` a trap, and every caller who builds a
  `Config` literal — every test in the repository — gets the wrong defaults
  silently.

## Consequences

- The accessor count keeps growing with the field count, and keeps looking like
  a smell to a reader who has not read this ADR. That cost is accepted; this
  file is the answer to it.
- The nine modules stay diffable, since the pattern is identical in each.
- Nothing changes in the code. This ADR records an audit whose outcome was no
  change, so the question does not get reopened from the shape alone.
