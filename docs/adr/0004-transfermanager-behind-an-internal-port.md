# transfermanager is the upload engine, behind an internal port

`s3.Upload` buffered every object into RAM ([#4](https://github.com/sunkek/samsara-components/issues/4)),
so it needs a streaming engine. We take
`github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager` (adopted at v0.3.17) and reach it
only through an unexported `uploadEngine` port, so a pre-1.0 dependency cannot
reach `s3`'s exported surface.

## Considered options

- **`feature/s3/manager`** — GA and stable, but its own `go.mod` marks it
  `// Deprecated: superceded by feature/s3/transfermanager`. Adopting a
  deprecated module means a second migration later.
- **`feature/s3/transfermanager`** (chosen) — the successor, but pre-1.0:
  v0.3.16 shipped 2026-08-25, with a file-handle-leak fix a week earlier.
  Breaking changes before v1.0 are likely, not hypothetical.
- **Hand-rolled `CreateMultipartUpload` / `UploadPart` / `CompleteMultipartUpload`**
  — no new dependency, and verified working against SeaweedFS, but it puts part
  sizing, concurrency, and abort-on-failure in our maintenance path.

## Why the port, given ADR-0003

[ADR-0003](./0003-driver-types-at-the-interface.md) says driver types belong in
exported signatures, and that still holds for the GA drivers: pgx, amqp, and
`service/s3` are stable, and hiding them buys callers nothing. A pre-1.0 module
is the exception the rule did not anticipate — exporting its types would export
its churn to every consumer of this module.

The port stays **unexported**. Two adapters justify it: the transfermanager
adapter in production, and an in-package fake in tests. It would not clear that
bar as an exported `UploadEngine`, and should not be exported until a second
production engine exists.

## Consequences

- A v0.4.0 break is a private edit to one file, not a breaking release of `s3`.
- Upload policy — content-type sniffing, ACL defaulting — becomes unit-testable
  for the first time; today it can only be exercised against Docker.
- `Config.UploadPartSize` and `UploadConcurrency` are our own names, translated
  at the port, so they survive an engine swap.
- Adding the module bumps `service/s3` to v1.107.4 and `aws-sdk-go-v2` to
  v1.43.8.
