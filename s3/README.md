# s3

[![Go Reference](https://pkg.go.dev/badge/github.com/sunkek/samsara-components/s3.svg)](https://pkg.go.dev/github.com/sunkek/samsara-components/s3)
[![Go Report Card](https://goreportcard.com/badge/github.com/sunkek/samsara-components/s3)](https://goreportcard.com/report/github.com/sunkek/samsara-components/s3)

A [samsara](https://github.com/sunkek/samsara)-compatible S3 component backed
by the [AWS SDK v2](https://github.com/aws/aws-sdk-go-v2).

Works with any S3-compatible storage: AWS S3, Yandex Cloud Object Storage,
Cloudflare R2, SeaweedFS, and others.

```
go get github.com/sunkek/samsara-components/s3
```

---

## Usage

### Register with a supervisor

```go
store := s3.New(s3.Config{
    Endpoint: "https://s3.us-east-1.amazonaws.com",
    Region:   "us-east-1",
    KeyID:    os.Getenv("S3_KEY_ID"),
    Secret:   os.Getenv("S3_SECRET"),
})
sup.Add(store,
    samsara.WithTier(samsara.TierSignificant),
    samsara.WithRestartPolicy(samsara.AlwaysRestart(5*time.Second)),
)
```

For SeaweedFS (local development / CI):

```go
store := s3.New(s3.Config{
    Endpoint:         "http://localhost:8333",
    Region:           "us-east-1",
    KeyID:            "test",
    Secret:           "test",
    PathStyleForcing: true, // required for SeaweedFS
})
```

### Upload and download

```go
// Upload with auto-detected content type:
err := store.Upload(ctx, s3.UploadRequest{
    Bucket: "my-bucket",
    Key:    "images/photo.jpg",
    Body:   file,
})

// Upload with explicit content type:
err := store.Upload(ctx, s3.UploadRequest{
    Bucket:      "my-bucket",
    Key:         "data/export.csv",
    Body:        csvReader,
    ContentType: "text/csv",
    ACL:         s3.ACLPrivate,
})

// Download:
rc, err := store.Download(ctx, "my-bucket", "images/photo.jpg")
if err != nil { return err }
defer rc.Close()
```

### Delete objects

```go
// Delete a single object:
err := store.Delete(ctx, "my-bucket", "images/old-photo.jpg")

// Delete all objects under a prefix:
n, err := store.DeleteByPrefix(ctx, "my-bucket", "tmp/session-123/")
```

### List keys

```go
keys, err := store.ListKeys(ctx, "my-bucket", "images/")
```

### Presigned URLs

```go
// Presigned download URL (valid for 1 hour):
url, err := store.PresignDownload(ctx, s3.PresignRequest{
    Bucket: "my-bucket",
    Key:    "reports/q4-2026.pdf",
    TTL:    time.Hour,
})

// Presigned upload URL (client uploads directly to S3):
url, err := store.PresignUpload(ctx, s3.PresignRequest{
    Bucket:        "my-bucket",
    Key:           "uploads/user-avatar.png",
    TTL:           15 * time.Minute,
    ContentType:   "image/png",
    ContentLength: 5 * 1024 * 1024,
})
```

When `ContentType` or `ContentLength` is set, the uploader must send matching
`Content-Type` and `Content-Length` headers with the PUT request.

---

## Configuration

```go
s3.Config{
    Endpoint         string        // S3 endpoint URL; leave empty for AWS
    Region           string        // AWS region or equivalent
    KeyID            string        // access key ID
    Secret           string        // secret access key

    ConnectTimeout   time.Duration // default: 10s — startup check deadline
    PresignTTL       time.Duration // default: 15m — presigned URL lifetime
    PathStyleForcing bool          // default: false; set true for SeaweedFS / local servers
}
```

### Options

```go
s3.WithLogger(slog.Default())    // attach a structured logger
s3.WithName("media-store")       // override component name
```

---

## API reference

### Operations

| Method | Description |
|--------|-------------|
| `Upload(ctx, UploadRequest)` | Put an object; auto-detects MIME type |
| `Download(ctx, bucket, key)` | Get an object; caller closes returned `io.ReadCloser` |
| `Delete(ctx, bucket, key)` | Remove a single object |
| `DeleteByPrefix(ctx, bucket, prefix)` | Remove all objects under prefix; returns count |
| `ListKeys(ctx, bucket, prefix)` | List all object keys under prefix |
| `PresignDownload(ctx, PresignRequest)` | Generate a time-limited GET URL |
| `PresignUpload(ctx, PresignRequest)` | Generate a time-limited PUT URL; can sign exact `Content-Type` and `Content-Length` |

### ACL constants

| Constant | Value |
|----------|-------|
| `ACLPrivate` | `"private"` |
| `ACLPublicRead` | `"public-read"` |
| `ACLPublicReadWrite` | `"public-read-write"` |
| `ACLAuthenticatedRead` | `"authenticated-read"` |
| `ACLBucketOwnerRead` | `"bucket-owner-read"` |
| `ACLBucketOwnerFullControl` | `"bucket-owner-full-control"` |

---

## Content-type detection

`Upload` auto-detects the MIME type from the first 512 bytes of the body
when `UploadRequest.ContentType` is not set. SVG files are detected by
file extension (`.svg`) or body content (`<svg` prefix), since Go's
`http.DetectContentType` does not recognise SVG natively.

Set `ContentType` explicitly to bypass detection:

```go
store.Upload(ctx, s3.UploadRequest{
    ContentType: "application/octet-stream",
    ...
})
```

## Presigned upload constraints

`PresignUpload` can sign exact `Content-Type` and `Content-Length` values for
PUT uploads. It cannot express a size range like `x-amz-content-length-range`;
that requires a presigned POST policy or validation in the caller before the
URL is returned.

---

## Health checking

`*Component` implements `samsara.HealthChecker`. Health is verified by
sending a `HeadBucket` request to a synthetic bucket name. A 404 or 403
response confirms the endpoint is reachable and credentials are signing
correctly — no `ListBuckets` permission required.

---

## Integration tests (SeaweedFS)

Integration tests run against a local [SeaweedFS](https://github.com/seaweedfs/seaweedfs)
instance started by docker-compose. SeaweedFS is an Apache 2.0 licensed,
S3-compatible object store that needs no account, license key, or external
service to run.

The `seaweedfs` service in `docker-compose.yml` runs SeaweedFS in single-node
mode (`server -s3`) with credentials supplied via `scripts/seaweedfs-s3.json`.
A one-shot `seaweedfs-init` service creates the `test` bucket once the gateway
is healthy.

```bash
make infra-up
make test-integration
```

SeaweedFS credentials used in tests:

| Parameter | Value |
|-----------|-------|
| Endpoint | `http://localhost:8333` |
| Region | `us-east-1` |
| Access key | `test` |
| Secret key | `test` |
| Bucket | `test` |
| Path-style | `true` |
