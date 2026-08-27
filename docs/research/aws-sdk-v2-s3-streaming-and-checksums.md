# AWS SDK for Go v2: seekable bodies, checksums, and streaming uploads

Research note grounding [sunkek/samsara-components#4](https://github.com/sunkek/samsara-components/issues/4).

> **New directory.** The repo previously had only `docs/adr/` (short ADRs). This file
> creates `docs/research/` for longer-form investigation notes that inform an ADR or an
> issue but are not themselves decisions.

## Versions this answers against

Everything below is verified against the exact versions pinned in `s3/go.mod`:

| Module | Version |
| --- | --- |
| `github.com/aws/aws-sdk-go-v2` | v1.43.6 |
| `github.com/aws/aws-sdk-go-v2/service/s3` | v1.107.2 |
| `github.com/aws/aws-sdk-go-v2/service/internal/checksum` | v1.9.30 |
| `github.com/aws/aws-sdk-go-v2/config` | v1.32.37 |
| `github.com/aws/smithy-go` | v1.27.8 |

Where behaviour changed in a recent release, that is called out inline.

Claims marked **[measured]** were reproduced with a throwaway probe program built against
exactly these module versions, driven at (a) an in-process `net/http/httptest` plain-HTTP
server, (b) an in-process `httptest` TLS server, and (c) the repo's own SeaweedFS container
from `docker-compose.yml`. Claims marked **[source]** are read out of the SDK source in the
local module cache. Claims marked **[UNVERIFIED]** could not be checked against a primary
source from here.

---

## The question

`s3/operations.go:85-92` buffers every upload body into RAM with `io.ReadAll`, justified by:

```go
// Even when ContentType is provided we need a seekable body for the
// AWS SDK v2 checksum calculation over plain HTTP. Buffer it.
```

Is that true for these versions? What is the deciding code path, what are the defaults,
and what are the streaming alternatives — including against a non-AWS endpoint over plain
HTTP such as this repo's SeaweedFS?

---

## Short answers

### 1. Is the comment true? — **Partly correct.**

The *conclusion* is right for these versions: over plain HTTP a non-seekable `io.Reader`
body fails, so `PutObject` needs a seekable body. The *reason* is incomplete and slightly
mis-attributed:

- Checksum calculation is only **one** of two independent reasons. With checksums fully
  disabled (`RequestChecksumCalculation: WHEN_REQUIRED`) a non-seekable body **still
  fails** over plain HTTP — SigV4 payload signing needs the body SHA256 and rewinds the
  stream to get it. **[measured]**
- The deciding condition is **not "plain HTTP vs HTTPS" as a checksum property**. It is
  `req.IsHTTPS()`, which gates *two* things: trailing (`aws-chunked`) checksums, and
  whether the payload hash is `UNSIGNED-PAYLOAD` or a real SHA256. Plain HTTP loses both,
  and both fallbacks need a rewindable stream. **[source]**
- "we need a seekable body" is stronger than "we need to buffer the whole body". Buffering
  is one way to get seekability; `manager.Uploader` gets it per-part with bounded memory.

So: correct outcome, half the reason, and it over-concludes that full buffering is required.

### 2. Default checksum behaviour

`RequestChecksumCalculation` defaults to `WHEN_SUPPORTED`, so `PutObject` computes a
**CRC32** checksum by default. This landed in **`service/s3` v1.73.0 / `service/internal/checksum`
v1.5.0 (2025-01-15)**; the pinned v1.107.2 / v1.9.30 still behave that way. Turn it off with
`RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired` in code,
`request_checksum_calculation = when_required` in shared config, or
`AWS_REQUEST_CHECKSUM_CALCULATION=when_required` in the environment. Turning it off does
**not** make a non-seekable body work over plain HTTP.

### 3. Streaming options and their real memory bounds

| Option | Needs seekable body? | Memory bound |
| --- | --- | --- |
| Plain `io.Reader` to `PutObject`, plain HTTP | **Yes** — fails otherwise | n/a (errors) |
| Plain `io.Reader` to `PutObject`, HTTPS | No | O(1) in the SDK; but no `Content-Length`, see caveat |
| `io.ReadSeeker` to `PutObject` (today's code) | Yes | **O(object size)** — whole body in RAM |
| `feature/s3/manager.Uploader` | No | **`(Concurrency + 1) × PartSize`** = 6 × 5 MiB = **30 MiB** default, per concurrent `Upload` call |
| Raw `CreateMultipartUpload` / `UploadPart` / `Complete` | Per-part only | Whatever you buffer per part; ≥ 5 MiB for non-final parts |

`feature/s3/manager` **is a separate Go module** and would need adding to `s3/go.mod`.
Latest is **v1.22.45**, whose `go.mod` requires `service/s3 v1.107.4` and
`aws-sdk-go-v2 v1.43.8` — so adding it bumps those two patch versions. **[measured]**
Note the module is marked `// Deprecated: superceded by feature/s3/transfermanager` in its
own `go.mod`; `feature/s3/transfermanager` is still pre-1.0 (**v0.3.16**).

### 4. Non-AWS endpoints (SeaweedFS over plain HTTP)

Against the repo's own SeaweedFS container, over plain HTTP: **[measured]**

- `PutObject` with a seekable body and the default header CRC32 → works.
- `PutObject` with a seekable body and `WHEN_REQUIRED` (no checksum) → works.
- `PutObject` with a non-seekable body → fails **client-side**, before any network I/O.
- Raw `CreateMultipartUpload` → `UploadPart` → `CompleteMultipartUpload` → works.
- `manager.Uploader` with a non-seekable 12 MiB reader → works, uploads as 3 parts.
- `GetObject` returns **no** `x-amz-checksum-crc32`, so response checksum validation is
  effectively a no-op there.

`aws-chunked` / trailing checksums are **never exercised** against this endpoint, because
the SDK gates them on `req.IsHTTPS()` and the endpoint is `http://`. SeaweedFS `master`
does implement `x-amz-trailer` and `STREAMING-UNSIGNED-PAYLOAD-TRAILER`, so it would
likely work over TLS — but that is untested here, and the compose file pins
`chrislusf/seaweedfs:latest`, an unversioned tag.

### 5. Keeping content-type sniffing without buffering everything

Sniff the first 512 bytes into a small fixed buffer, then hand `PutObject` an
`io.MultiReader(bytes.NewReader(head), rest)` — **but** that is not seekable, so it only
works where a non-seekable body is acceptable. The composition that works over plain HTTP
is: sniff 512 bytes into a fixed buffer, then pass `io.MultiReader(head, rest)` to
`manager.Uploader`, which re-buffers into seekable per-part `bytes.Reader`s itself. Bound
becomes 512 B + `(Concurrency+1) × PartSize` instead of the whole object. **[measured]** —
the manager probe used exactly a non-seekable reader and succeeded over plain HTTP.

---

## Evidence

### 1. The deciding code path

The stack for `PutObject` inserts, in order, the checksum middleware and the dynamic
payload-signing middleware:

```go
if err = addPutObjectInputChecksumMiddlewares(stack, options); err != nil { ... }
...
if err = v4.UseDynamicPayloadSigningMiddleware(stack); err != nil { ... }
```
— `service/s3@v1.107.2/api_op_PutObject.go`, `addOperationPutObjectMiddlewares`.

`PutObject`'s checksum options enable trailers and SHA256 computation:

```go
func addPutObjectInputChecksumMiddlewares(stack *middleware.Stack, options Options) error {
	return addInputChecksumMiddleware(stack, internalChecksum.InputMiddlewareOptions{
		GetAlgorithm:                     getPutObjectRequestAlgorithmMember,
		RequireChecksum:                  false,
		RequestChecksumCalculation:       options.RequestChecksumCalculation,
		EnableTrailingChecksum:           true,
		EnableComputeSHA256PayloadHash:   true,
		EnableDecodedContentLengthHeader: true,
	})
}
```
— `service/s3@v1.107.2/api_op_PutObject.go`.

**Decision A — HTTPS gates the trailing-checksum path.**
`service/internal/checksum@v1.9.30/middleware_compute_input_checksum.go:163-184`:

```go
	// If trailing checksums are supported, the request is HTTPS, and the
	// stream is not nil or empty, instead switch to a trailing checksum.
	//
	// Nil and empty streams will always be handled as a request header,
	// regardless if the operation supports trailing checksums or not.
	if req.IsHTTPS() && !presignedurlcust.GetIsPresigning(ctx) {
		if stream != nil && streamLength != 0 && m.EnableTrailingChecksum {
			if m.EnableComputePayloadHash {
				// ContentSHA256Header middleware handles the header
				ctx = v4.SetPayloadHash(ctx, streamingUnsignedPayloadTrailerPayloadHash)
			}
			m.useTrailer = true
			ctx = middleware.WithStackValue(ctx, useTrailer{}, true)
			return next.HandleFinalize(ctx, in)
		}
		...
```

**Decision B — without TLS, a non-seekable stream is a hard error.**
Same file, lines 186-192:

```go
	// Only seekable streams are supported for non-trailing checksums, because
	// the stream needs to be rewound before the handler can continue.
	if stream != nil && !req.IsStreamSeekable() && streamLength != 0 {
		return out, metadata, computeInputHeaderChecksumError{
			Msg: "unseekable stream is not supported without TLS and trailing checksum",
		}
	}
```

Otherwise the whole stream is hashed and rewound (lines 199-217), which is why a seekable
body is required: `computeStreamChecksum` does `io.Copy(batchHasher, stream)` and then
`req.RewindStream()`.

`AddInputChecksumTrailer` refuses outright without TLS —
`middleware_compute_input_checksum.go:278-283`:

```go
	// Trailing checksums are only supported when TLS is enabled.
	if !req.IsHTTPS() {
		return out, metadata, computeInputTrailingChecksumError{
			Msg: "HTTPS required",
		}
	}
```

`IsHTTPS()` is a pure scheme check — `smithy-go@v1.27.8/transport/http/request.go:37-42`:
`strings.EqualFold(r.URL.Scheme, "https")`. So the gate is **literally the URL scheme**, not
an endpoint or config flag.

**Decision C — the second, independent reason: payload signing.**
`aws-sdk-go-v2@v1.43.6/aws/signer/v4/middleware.go:77-91`:

```go
	if req.IsHTTPS() {
		return (&UnsignedPayload{}).HandleFinalize(ctx, in, next)
	}
	return (&ComputePayloadSHA256{}).HandleFinalize(ctx, in, next)
```

and `ComputePayloadSHA256.HandleFinalize` in the same file does
`io.Copy(hash, stream)` followed by `req.RewindStream()`, which fails on a non-seekable
stream — `smithy-go@v1.27.8/transport/http/request.go:94-105`:

```go
	if !r.isStreamSeekable {
		return fmt.Errorf("request stream is not seekable")
	}
```

Note `ComputePayloadSHA256` short-circuits if `GetPayloadHash(ctx) != ""`, which is why
checksum middleware sets the SHA256 it already computed (`SetPayloadHash`) — the two
middlewares are designed to share one pass over the body.

Seekability is decided in `Request.SetStream`
(`smithy-go@v1.27.8/transport/http/request.go:121-151`): `isStreamSeekable` is true only
when the reader satisfies `io.Seeker`. `*bytes.Reader` does; `io.MultiReader` and a bare
`io.Reader` do not.

**Measured, plain HTTP (`httptest.NewServer`, non-seekable 11-byte body):** **[measured]**

| Case | Result |
| --- | --- |
| default (`WHEN_SUPPORTED`) | `compute input header checksum failed, unseekable stream is not supported without TLS and trailing checksum` |
| `WHEN_REQUIRED` (checksums off) | `failed to compute payload hash: failed to seek body to start, request stream is not seekable` |
| explicit `ChecksumAlgorithm: crc32` | `unseekable stream is not supported without TLS and trailing checksum` |
| `WHEN_REQUIRED` + pre-supplied `ChecksumCRC32` header | `failed to seek body to start, request stream is not seekable` |
| seekable `strings.NewReader` | **succeeds**; sends `X-Amz-Content-Sha256: b94d27b9…`, `X-Amz-Checksum-Crc32: DUoRhQ==`, `Content-Length: 11` |

The second row is the load-bearing one: **disabling checksums does not rescue a
non-seekable body over plain HTTP.** Supplying a precomputed `x-amz-checksum-*` header
skips the checksum middleware (`middleware_compute_input_checksum.go:131-139`) but still
hits payload signing.

**Measured, HTTPS (`httptest.NewTLSServer`, same non-seekable body):** **[measured]**

- `WHEN_SUPPORTED` → succeeds, with `Content-Encoding: aws-chunked`,
  `X-Amz-Content-Sha256: STREAMING-UNSIGNED-PAYLOAD-TRAILER`,
  `X-Amz-Trailer: x-amz-checksum-crc32`, and wire body
  `"b\r\nhello world\r\n0\r\nx-amz-checksum-crc32:DUoRhQ==\r\n\r\n"`.
- `WHEN_REQUIRED` → succeeds, `X-Amz-Content-Sha256: UNSIGNED-PAYLOAD`, raw body.

So yes, the SDK **does** fall back to `aws-chunked` trailing checksums for a non-seekable
reader — but only over HTTPS.

**Caveat on the HTTPS streaming path.** Both HTTPS cases went out with
`Content-Length: -1` and HTTP/1.1 `Transfer-Encoding: chunked`, because the stream length
was unknown: `awsChunkedEncoding.EncodedLength()` returns `-1` when `StreamLength == -1`
(`service/internal/checksum@v1.9.30/aws_chunked_encoding.go:128-132`), and
`x-amz-decoded-content-length` is only set when the length is known
(`middleware_compute_input_checksum.go:361-364`). Amazon S3's PUT Object requires a
`Content-Length`. **[UNVERIFIED]** — no AWS account was available; I could not confirm
whether real S3 rejects this specific request shape. If you go the plain-`io.Reader`-over-TLS
route, set `ContentLength` on the input.

### 2. Default checksum behaviour

`SetupInputContext` is what puts an algorithm on the context, and it defaults to CRC32 —
`service/internal/checksum@v1.9.30/middleware_setup_context.go:50-62`:

```go
	if m.GetAlgorithm != nil {
		if algorithm, ok := m.GetAlgorithm(in.Parameters); ok {
			ctx = internalcontext.SetChecksumInputAlgorithm(ctx, algorithm)
			return next.HandleInitialize(ctx, in)
		}
	}

	if m.RequireChecksum || m.RequestChecksumCalculation == aws.RequestChecksumCalculationWhenSupported {
		ctx = internalcontext.SetChecksumInputAlgorithm(ctx, string(AlgorithmCRC32))
	}
```

If no algorithm ends up on the context, the checksum middleware returns early
(`middleware_compute_input_checksum.go:141-147`) and never touches the body.

Precedence, in order: an explicit `x-amz-checksum-*` header on the request → the input's
`ChecksumAlgorithm` field (`getPutObjectRequestAlgorithmMember`) → `RequestChecksumCalculation`
→ nothing.

The default value is resolved in `config@v1.32.37/resolve.go:197-208`: if no source
provides one, `c = aws.RequestChecksumCalculationWhenSupported`. Sources:

- Code: `config.WithRequestChecksumCalculation(...)` — `config@v1.32.37/load_options.go:418-424`.
- Env: `AWS_REQUEST_CHECKSUM_CALCULATION` — `config@v1.32.37/env_config.go:88`, parsed in
  `setRequestChecksumCalculationFromEnvVal` (lines 587-604), accepting `when_supported` /
  `when_required` case-insensitively and erroring on anything else.
- Shared config: `request_checksum_calculation` — `config@v1.32.37/shared_config.go:123`.
- Or set `RequestChecksumCalculation` directly on `s3.Options` /`aws.Config`
  (`aws-sdk-go-v2@v1.43.6/aws/config.go:178-190`).

Enum values: `RequestChecksumCalculationUnset`, `…WhenSupported`, `…WhenRequired` —
`aws-sdk-go-v2@v1.43.6/aws/checksum.go`.

**Which release changed the default.** `service/s3@v1.107.2/CHANGELOG.md` under **v1.73.0
(2025-01-15)**:

> **Feature**: S3 client behavior is updated to always calculate a checksum by default for
> operations that support it (such as PutObject or UploadPart), or require it (such as
> DeleteObjects). The checksum algorithm used by default now becomes CRC32. Checksum
> behavior can be configured using `when_supported` and `when_required` options - in code
> using RequestChecksumCalculation, in shared config using request_checksum_calculation, or
> as env variable using AWS_REQUEST_CHECKSUM_CALCULATION.

The same entry appears in `service/internal/checksum@v1.9.30/CHANGELOG.md` under **v1.5.0
(2025-01-15)**. Nothing later in either changelog reverses it, and the source above confirms
the pinned versions still do it. One later refinement worth knowing: checksum v1.9.x
"Cache first calculated checksum and reuse it in retry" — visible as the `m.checksum` cache
at `middleware_compute_input_checksum.go:195-198`.

Supported algorithms in the pinned checksum module: CRC32, CRC32C, CRC64NVME, SHA1, SHA256,
SHA512 (`algorithms.go:22-52`). `service/s3` models more (MD5, XXHASH3/64/128 — added in the
release noted at `service/s3@v1.107.2/CHANGELOG.md:112`) than the checksum runtime
implements, which is why unsupported-algorithm response validation is skipped rather than
failing.

### 3. Streaming options

**(a) Plain `io.Reader` to `PutObject`.** `PutObjectInput.Body` is typed `io.Reader`
(`service/s3@v1.107.2/api_op_PutObject.go:247`), so it compiles — but per §1 it only works
over HTTPS. Memory: O(1) in the SDK. Caveat on `Content-Length` above.

**(b) `feature/s3/manager.Uploader`.** Separate module, not currently in `s3/go.mod`.
Latest v1.22.45. Defaults, from `feature/s3/manager/upload.go:23-37`:

```go
const MaxUploadParts int32 = 10000
const MinUploadPartSize int64 = 1024 * 1024 * 5
const DefaultUploadPartSize = MinUploadPartSize
const DefaultUploadConcurrency = 5
```

`PartSize` below `MinUploadPartSize` (5 MiB) is rejected outright. For a body that *is* an
`io.Seeker`, `initSize` measures it and grows `PartSize` so the part count stays under
`MaxUploadParts` — `upload.go` `initSize`:

```go
		if u.totalSize/u.cfg.PartSize >= int64(u.cfg.MaxUploadParts) {
			u.cfg.PartSize = (u.totalSize / int64(u.cfg.MaxUploadParts)) + 1
		}
```

For a non-seekable body `totalSize` stays `-1` and `PartSize` is not adjusted, so an object
larger than `PartSize × MaxUploadParts` (50 GB at defaults) fails with
`exceeded total allowed S3 limit MaxUploadParts`.

**The memory bound is the buffer pool, not the object.** `upload.go` `init()`:

```go
	poolCap := u.cfg.Concurrency + 1
	if u.cfg.partPool == nil || u.cfg.partPool.SliceSize() != u.cfg.PartSize {
		u.cfg.partPool = newByteSlicePool(u.cfg.PartSize)
		u.cfg.partPool.ModifyCapacity(poolCap)
	}
```

So **`(Concurrency + 1) × PartSize`** — 6 × 5 MiB = **30 MiB** at defaults — per in-flight
`Upload` call. The pool (`feature/s3/manager/pool.go`, `maxSlicePool`) caps allocations at
that capacity and blocks `Get` until a slice is returned, so it is a real bound, not an
average. Buffers are recycled via the `cleanup` closure that `Put`s the slice back.

`nextReader()` is where the streaming happens (`upload.go`):

- Body implements `readerAtSeeker` → zero-copy `io.NewSectionReader(r, u.readerPos, n)` per
  part. Memory is O(1); no part buffers allocated at all.
- Otherwise → the first part is `io.ReadAll(io.LimitReader(u.in.Body, u.cfg.PartSize))` and
  subsequent parts are `readFillBuf` into a pooled `*[]byte`, each wrapped as
  `bytes.NewReader(...)`.

That last detail is the crux: every part handed to `UploadPart` is a `*bytes.Reader`, i.e.
**seekable**, so the checksum and payload-signing middleware are satisfied and the manager
works over plain HTTP with a non-seekable source. Verified: **[measured]** a 12 MiB
non-seekable reader uploaded to SeaweedFS over `http://` succeeded, and `HeadObject`
reported `size=12582912` with a multipart ETag suffix `-3` (three parts).

The manager also short-circuits: `upload()` does one `nextReader()` and, if it hit EOF
within the first part, calls `singlePart` — a plain `PutObject` with a `bytes.Reader`. So
small objects cost `min(size, PartSize)` of RAM, not 30 MiB. Verified for an 11-byte
non-seekable body. **[measured]**

Dependency cost: `feature/s3/manager@v1.22.45`'s `go.mod` requires
`service/s3 v1.107.4` and `aws-sdk-go-v2 v1.43.8`, so adding it bumps those two patch
versions in `s3/go.mod`. **[measured]** Also note its own `go.mod` header:
`// Deprecated: superceded by feature/s3/transfermanager. See https://github.com/aws/aws-sdk-go-v2/discussions/3306`.
`feature/s3/transfermanager` latest is **v0.3.16** — pre-1.0, so not a drop-in
recommendation for a library others depend on. **[UNVERIFIED]** — I did not read the
transfermanager source or evaluate its API stability.

**(c) Raw multipart.** `CreateMultipartUpload` → N × `UploadPart` → `CompleteMultipartUpload`.
Memory bound is entirely yours: whatever you buffer per part. S3's minimum part size is
5 MiB for all but the last part, and the maximum part count is 10,000. This is what the
manager does for you plus retry, ordering, and abort-on-failure; only worth hand-rolling if
you need control the manager does not expose. Verified end-to-end against SeaweedFS with a
single 5 MiB part. **[measured]**

### 4. Non-AWS endpoints

Measured against the repo's own `docker-compose.yml` SeaweedFS service
(`chrislusf/seaweedfs:latest`, `weed server -s3`, `http://127.0.0.1:8333`, path-style,
credentials from `scripts/seaweedfs-s3.json`): **[measured]**

| Operation | Result |
| --- | --- |
| `CreateBucket` | ok |
| `PutObject`, seekable body, default CRC32 header | ok |
| `PutObject`, seekable body, `WHEN_REQUIRED` | ok |
| `PutObject`, non-seekable body | fails client-side: `unseekable stream is not supported without TLS and trailing checksum` |
| `CreateMultipartUpload` / `UploadPart` / `CompleteMultipartUpload` | ok |
| `manager.Uploader`, non-seekable 12 MiB | ok, 3 parts |
| `manager.Uploader`, non-seekable 11 B | ok, single `PutObject` |
| `GetObject` | ok; **`ChecksumCRC32` came back empty** |

Reading of these results:

- Nothing about the non-seekable failure is endpoint-specific. It happens in the finalize
  middleware before a byte leaves the process — the same error appeared against an inert
  `httptest` server. A different S3-compatible server will not change it.
- `aws-chunked` / trailing checksums are **not reachable** here at all, because
  `req.IsHTTPS()` is false for `http://127.0.0.1:8333`. Whether SeaweedFS supports them is
  therefore moot for these integration tests.
- SeaweedFS `master` does implement the trailer protocol: `weed/s3api/chunked_reader_v4.go`
  reads `x-amz-trailer` and handles `STREAMING-UNSIGNED-PAYLOAD-TRAILER`, and
  `weed/s3api/s3api_object_handlers_put.go` parses `x-amz-trailer` including
  comma-separated values. **[UNVERIFIED]** for the container actually in use — the compose
  file pins the mutable `:latest` tag, and I did not exercise the trailer path over TLS.
- Multipart is fully supported, so the manager path is viable against SeaweedFS today.
- Because `GetObject` returns no checksum header, response checksum validation is silently
  skipped there. That is not an error, but it means the integration tests are not actually
  proving checksum round-trips.

### 5. Content-type sniffing without full buffering

Today `detectContentType` (`s3/operations.go:292-311`) does `io.ReadAll(body)` and returns a
`*bytes.Reader` — the comment at lines 286-289 correctly identifies that `io.MultiReader` is
not seekable, which follows from `Request.SetStream`'s `case io.Seeker:` type switch
(`smithy-go@v1.27.8/transport/http/request.go:130-148`).

The standard shape that preserves sniffing without O(object) memory:

```go
head := make([]byte, 512)
n, err := io.ReadFull(r.Body, head)   // tolerate ErrUnexpectedEOF / EOF
head = head[:n]
contentType := http.DetectContentType(head)   // plus the existing .svg special-case
body := io.MultiReader(bytes.NewReader(head), r.Body)
```

`body` is a non-seekable `io.Reader`, so it must go to something that tolerates one:

- `manager.Uploader.Upload` — works over plain HTTP, bound 512 B + `(Concurrency+1)×PartSize`.
  This is the one verified to work end-to-end against SeaweedFS. **[measured]**
- `PutObject` directly — only over HTTPS, and set `ContentLength` if you know it.

Note `http.DetectContentType` on a *short* read is not identical to sniffing 512 bytes of a
longer stream, but that difference already exists in the current code (it slices
`all[:512]`), so this is behaviour-preserving.

Two smaller notes for whoever implements this:

- `UploadRequest.ContentType` currently always ends up set on the input, even when sniffing
  produced `application/octet-stream`. Sniffing on a 512-byte prefix of a stream is
  best-effort either way.
- The `ACL` field is sent on every `PutObject`. SeaweedFS accepts it; buckets with
  object-ownership enforced reject ACLs. Out of scope here, flagged only because it sits in
  the same struct literal.

---

## Verdict on the in-code comment

**Partly correct.**

- ✅ A seekable body genuinely is required for `PutObject` over plain HTTP in these pinned
  versions. The code is not working around a phantom.
- ❌ It attributes the requirement to checksum calculation alone. SigV4 payload signing
  imposes the same requirement independently, and survives disabling checksums entirely.
- ❌ "over plain HTTP" reads as if HTTPS-vs-HTTP were a checksum-specific quirk. It is a
  URL-scheme check (`req.IsHTTPS()`) that simultaneously disables `aws-chunked` trailers and
  forces a real payload SHA256 — two separate middlewares, one condition.
- ❌ It silently equates "seekable" with "buffer the whole thing". `manager.Uploader` gets
  seekability per part and bounds memory at 30 MiB regardless of object size.

A more accurate comment would be: *over plain HTTP the SDK cannot use `aws-chunked` trailing
checksums and must compute both the CRC32 checksum and the SigV4 payload SHA256 up front,
each of which rewinds the body — so `PutObject` requires an `io.ReadSeeker` here. Use
`feature/s3/manager.Uploader` if you need to avoid buffering the whole object.*

## Not verified from a primary source

- Whether real Amazon S3 accepts the unknown-length HTTPS streaming shape the SDK produces
  (`Content-Length: -1`, HTTP/1.1 chunked, no `x-amz-decoded-content-length`). No AWS
  account available; all measurements were local.
- Whether the specific `chrislusf/seaweedfs:latest` image currently pulled supports
  `aws-chunked` trailing checksums. The `master` source does; the tag is mutable and the
  path is unreachable over plain HTTP anyway.
- `feature/s3/transfermanager` (v0.3.16) API surface, memory profile, and readiness — not
  examined.
- Behaviour of the five newer S3 checksum algorithms (MD5, XXHASH3/64/128) end to end; the
  pinned `service/internal/checksum` v1.9.30 does not implement them
  (`algorithms.go:44-52`), which is inferred from the source rather than measured.
