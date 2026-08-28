---
name: boilerplate-sync
description: Change the Logger, nopLogger, Option, WithLogger, or WithName boilerplate that all nine modules copy verbatim. Use when editing any of those five identifiers, or when scripts/check-boilerplate.sh reports drift between modules.
---

# Changing the copied boilerplate

`Logger`, `nopLogger`, `Option`, `WithLogger` and `WithName` are copied
verbatim into all nine modules, in each module's `<module>.go`. The duplication
is deliberate: it is what keeps the modules independent of a shared internal
package and diffable against each other (ADR-0002). A change to one copy is a
change to nine.

The modules: `fiber`, `grpc`, `grpcclient`, `postgresql`, `prometheus`,
`rabbitmq`, `redis`, `s3`, `sqlite`.

## Procedure

1. Make the change in one module, and get it right there first.
2. Apply the identical text to the other eight — same wording, same order, same
   blank lines. `scripts/check-boilerplate.sh` compares the blocks literally.
3. Run `scripts/check-boilerplate.sh` (or `make boilerplate-check`). It is part
   of `make check`, so drift fails the push either way.
4. Run `make check` for the whole set, then update each module's `README.md` and
   the root `CHANGELOG.md` if the change is visible to callers — a change to
   these five is visible in nine modules at once, so say so in one entry rather
   than nine.

## The one deliberate difference

`WithName`'s doc comment is module-specific — it names the component and gives
a reason a caller might register two of them. The signature and body are
identical everywhere, and that is what the check compares. Everything else in
the copied blocks, doc comments included, must match character for character.

## When the check reports drift you did not cause

Do not silence it by editing the reference module. Find which copy is the odd
one out, decide which text is correct, and bring the rest to it.
