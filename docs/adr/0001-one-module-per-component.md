# One Go module per component

Each component is its own Go module inside a `go.work` workspace, rather than
one module with nine packages. A service importing `redis` should not pull in
pgx, the AWS SDK, and Fiber; per-module `go.mod` files are the only way Go
offers to keep those dependency trees separate. The cost is nine modules to
version, tag, and dependency-update in step, which is why CI, Dependabot, and
the `Makefile` all fan out across modules.

## Consequences

- No shared internal package exists — a module may only depend on the standard
  library and its own third-party drivers. Common code is duplicated instead
  (see ADR-0002).
- Cross-module changes need one commit per module's `go.mod` when versions move.
