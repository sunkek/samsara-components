# Handoff — s3 streaming upload (#4)

**Temporary file.** Delete it when [#4](https://github.com/sunkek/samsara-components/issues/4)
lands; the durable parts already live in
[ADR-0004](./adr/0004-transfermanager-behind-an-internal-port.md) and
[the research note](./research/aws-sdk-v2-s3-streaming-and-checksums.md).

Written 2026-08-26. Branch `docs/context-and-adrs`, nothing pushed.

## Where things stand

Decided, not yet implemented. No `s3/` code has changed — the module is exactly
as it was on `main`.

The design is settled: keep `Storage` at its current 7 methods and keep
`UploadRequest` a struct, add an unexported `uploadEngine` port with neutral
`putParams`, and put transfermanager behind it. ADR-0004 records why.

## What was measured, so nobody re-measures it

Against this repo's own SeaweedFS over plain HTTP, transfermanager v0.3.16,
with a **non-seekable** reader:

- 256 MiB object → 32 parts, peak heap 83.9 MiB with the source streamed rather
  than buffered. 64 MiB showed the same overhead, so the bound does not track
  object size.
- Theoretical bound is `(Concurrency+1) × PartSize` = 6 × 8 MiB = 48 MiB; the
  extra is GC slack and HTTP buffers. Treat ~85 MiB as the realistic default
  ceiling per in-flight upload, not 48 MiB.

**Hazard to handle in the implementation.** `api_op_UploadObject.go:942` reads
the first part with `io.ReadAll(io.LimitReader(body, MultipartUploadThreshold))`.
The default threshold is 16 MiB, so a sub-threshold upload still buffers up to
16 MiB — a 12 MiB probe allocated 24.5 MiB and went single-shot. Pin
`MultipartUploadThreshold = PartSizeBytes` so that can never exceed one part.

## Next steps, in order

1. Write the failing test first: assert the memory bound through `Storage`, and
   assert the sniffing rules through the new port's fake (that fake is the point
   of the port — upload policy has no unit test today).
2. Add `feature/s3/transfermanager` to `s3/go.mod`. It bumps `service/s3` to
   v1.107.4 and `aws-sdk-go-v2` to v1.43.8.
3. Implement `uploadEngine`, the transfermanager adapter, and the in-package
   fake. Sniffing stays on the component side, above the port: it is policy, and
   it is identical for every engine.
4. Replace both `io.ReadAll` paths in `s3/operations.go` (line 88, and the one
   at line 293).
5. `Config.UploadPartSize` (default 8 MiB, floor 5 MiB) and `UploadConcurrency`
   (default 5), via unexported accessors like the rest.
6. Update `s3/README.md` and `CHANGELOG.md`; the memory bound belongs in the
   `Upload` doc comment, since it is part of the interface.

## Deliberately out of scope

ROADMAP **S2** — `CopyObject`, `HeadObject`, range-GET, versioning, SSE,
metadata, tagging. None of it caused the incident, and serving it needs an
escape-hatch decision (`Client() *s3.Client`) that deserves its own ADR rather
than riding along with #4. Two of the four candidate designs wanted that hatch;
neither could justify it without widening the seam.

## Open questions

- **Unverified in the research note:** whether real AWS S3 accepts the
  unknown-length HTTPS streaming shape the SDK emits (`Content-Length: -1`,
  chunked, no `x-amz-decoded-content-length`). No AWS account was available.
  Everything measured above is SeaweedFS over plain HTTP.
- `docker-compose.yml` pins `chrislusf/seaweedfs:latest`, an unversioned tag, so
  the integration baseline can move under us.
- `make infra-up` fails locally when another project holds port 5432. Only
  SeaweedFS is needed for this work.

## Session state

Five commits on `docs/context-and-adrs`, plus this note and ADR-0004:
`CONTEXT.md` and ADRs 0001-0003, the `rabbitmq.Publisher` seam with
zero-value `Config` tests across all nine modules, the gofmt gate (#6), ROADMAP
status markers (#5), and the research note.

Issues filed: #4 (this work), #5 and #6 (closed by those commits), #7
(govulncheck), #8 (coverage baseline).
