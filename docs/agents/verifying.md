# Verifying

Run `make` targets rather than ad hoc `go` commands, so results match CI. The
`Makefile` lists them all.

## The gates

- **`make check`** before pushing. Gates on `gofmt` too; `make fmt` fixes drift.
  It also runs `make boilerplate-check`, which compares the five identifiers the
  nine modules copy verbatim and prints a diff of whichever copy drifted.
- **`make test-all`** before opening a PR.
- **`make lint`** installs `staticcheck` if it is missing.
- **`make vuln`** installs `govulncheck`.
- **`make coverage-check`** compares each module against
  `scripts/coverage-baseline.txt` and fails on a drop of more than 2 points. If
  a change moves the numbers in either direction, run `make coverage-update` and
  commit the file.

## Integration tests

`make test-integration` brings the Docker services in `docker-compose.yml` up
and down around the run. If a host port is already taken, override it —
`SC_POSTGRES_PORT`, `SC_REDIS_PORT`, `SC_RABBITMQ_PORT` and `SC_S3_PORT` are
read by both Compose and the tests.

## One module at a time

```bash
cd postgresql && go test -race -count=3 ./...
cd postgresql && go test -race -count=1 -tags integration ./...
```
