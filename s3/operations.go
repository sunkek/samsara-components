package s3

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// UploadRequest carries all parameters needed for [Component.Upload].
type UploadRequest struct {
	// Bucket is the target bucket name. Required.
	Bucket string
	// Key is the object key (path within the bucket). Required.
	Key string
	// Body is the object content. Required.
	Body io.Reader
	// ContentType overrides auto-detected MIME type.
	// Leave empty to auto-detect from the first 512 bytes of Body.
	ContentType string
	// ACL controls object access. Defaults to [ACLPrivate].
	ACL ACL
}

// PresignRequest carries parameters for presigned URL generation.
type PresignRequest struct {
	// Bucket is the target bucket name. Required.
	Bucket string
	// Key is the object key. Required.
	Key string
	// TTL overrides [Config.PresignTTL] for this request.
	// Use 0 to use the component default.
	TTL time.Duration
	// ContentType signs an exact Content-Type header for presigned uploads.
	// Leave empty to avoid constraining the upload MIME type at the S3 layer.
	ContentType string
	// ContentLength signs an exact Content-Length header for presigned uploads.
	// Leave 0 to avoid constraining the upload size at the S3 layer.
	//
	// Presigned PUT URLs do not support a min/max size range such as
	// x-amz-content-length-range. Use a presigned POST policy or validate
	// size before issuing the URL if you need range-based enforcement.
	ContentLength int64
}

// ACL is an S3 canned ACL value.
type ACL string

const (
	ACLPrivate                ACL = "private"
	ACLPublicRead             ACL = "public-read"
	ACLPublicReadWrite        ACL = "public-read-write"
	ACLAuthenticatedRead      ACL = "authenticated-read"
	ACLBucketOwnerRead        ACL = "bucket-owner-read"
	ACLBucketOwnerFullControl ACL = "bucket-owner-full-control"
)

// Download retrieves an object from S3. The caller must close the returned
// [io.ReadCloser] after reading.
func (c *Component) Download(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	return observe(c, opDownload, func() (io.ReadCloser, error) {
		return c.download(ctx, bucket, key)
	})
}

func (c *Component) download(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	client := c.getClient()
	if client == nil {
		return nil, fmt.Errorf("s3 download: client not initialised")
	}
	if bucket == "" || key == "" {
		return nil, fmt.Errorf("s3 download: bucket and key are required")
	}
	out, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		return nil, fmt.Errorf("s3 download %q/%q: %w", bucket, key, err)
	}
	return out.Body, nil
}

// Delete removes a single object from S3.
func (c *Component) Delete(ctx context.Context, bucket, key string) error {
	return observeErr(c, opDelete, func() error {
		return c.delete(ctx, bucket, key)
	})
}

func (c *Component) delete(ctx context.Context, bucket, key string) error {
	client := c.getClient()
	if client == nil {
		return fmt.Errorf("s3 delete: client not initialised")
	}
	if bucket == "" || key == "" {
		return fmt.Errorf("s3 delete: bucket and key are required")
	}
	if _, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: &bucket,
		Key:    &key,
	}); err != nil {
		return fmt.Errorf("s3 delete %q/%q: %w", bucket, key, err)
	}
	return nil
}

// DeleteByPrefix removes all objects whose keys begin with prefix.
// Returns the number of objects deleted. Handles pagination automatically.
func (c *Component) DeleteByPrefix(ctx context.Context, bucket, prefix string) (int, error) {
	return observe(c, opDeleteByPrefix, func() (int, error) {
		return c.deleteByPrefix(ctx, bucket, prefix)
	})
}

func (c *Component) deleteByPrefix(ctx context.Context, bucket, prefix string) (int, error) {
	client := c.getClient()
	if client == nil {
		return 0, fmt.Errorf("s3 delete-by-prefix: client not initialised")
	}
	if bucket == "" {
		return 0, fmt.Errorf("s3 delete-by-prefix: bucket is required")
	}

	// The unexported form, so this call reports one observation rather than
	// two.
	keys, err := c.listKeys(ctx, bucket, prefix)
	if err != nil {
		return 0, fmt.Errorf("s3 delete-by-prefix: list: %w", err)
	}
	if len(keys) == 0 {
		return 0, nil
	}

	ids := make([]types.ObjectIdentifier, len(keys))
	for i, k := range keys {
		ids[i] = types.ObjectIdentifier{Key: &k}
	}

	_, err = client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: &bucket,
		Delete: &types.Delete{Objects: ids, Quiet: ptrOf(true)},
	})
	if err != nil {
		return 0, fmt.Errorf("s3 delete-by-prefix %q/%q: %w", bucket, prefix, err)
	}
	return len(keys), nil
}

// ListKeys returns all object keys in bucket with the given prefix.
// Handles pagination automatically; safe for large buckets.
func (c *Component) ListKeys(ctx context.Context, bucket, prefix string) ([]string, error) {
	return observe(c, opListKeys, func() ([]string, error) {
		return c.listKeys(ctx, bucket, prefix)
	})
}

func (c *Component) listKeys(ctx context.Context, bucket, prefix string) ([]string, error) {
	client := c.getClient()
	if client == nil {
		return nil, fmt.Errorf("s3 list-keys: client not initialised")
	}
	if bucket == "" {
		return nil, fmt.Errorf("s3 list-keys: bucket is required")
	}

	var (
		keys              []string
		continuationToken *string
	)
	for {
		out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            &bucket,
			Prefix:            &prefix,
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return nil, fmt.Errorf("s3 list-keys %q/%q: %w", bucket, prefix, err)
		}
		for _, obj := range out.Contents {
			if obj.Key != nil {
				keys = append(keys, *obj.Key)
			}
		}
		// IsTruncated is *bool; guard against nil from non-conformant servers.
		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		continuationToken = out.NextContinuationToken
	}
	return keys, nil
}

// PresignDownload generates a time-limited presigned URL for downloading an object.
// The URL is valid for [PresignRequest.TTL] or [Config.PresignTTL] if TTL is 0.
func (c *Component) PresignDownload(ctx context.Context, r PresignRequest) (string, error) {
	return observe(c, opPresignDownload, func() (string, error) {
		return c.presignDownload(ctx, r)
	})
}

func (c *Component) presignDownload(ctx context.Context, r PresignRequest) (string, error) {
	presigner := c.getPresigner()
	if presigner == nil {
		return "", fmt.Errorf("s3 presign-download: client not initialised")
	}
	if r.Bucket == "" || r.Key == "" {
		return "", fmt.Errorf("s3 presign-download: bucket and key are required")
	}
	ttl := r.TTL
	if ttl == 0 {
		ttl = c.cfg.presignTTL()
	}
	resp, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: &r.Bucket,
		Key:    &r.Key,
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("s3 presign-download %q/%q: %w", r.Bucket, r.Key, err)
	}
	return resp.URL, nil
}

// PresignUpload generates a time-limited presigned URL for uploading an object
// via HTTP PUT. The URL is valid for [PresignRequest.TTL] or [Config.PresignTTL].
//
// If [PresignRequest.ContentType] or [PresignRequest.ContentLength] is set, the
// client must send matching Content-Type / Content-Length headers when using the
// returned URL or the upload will fail signature validation.
func (c *Component) PresignUpload(ctx context.Context, r PresignRequest) (string, error) {
	return observe(c, opPresignUpload, func() (string, error) {
		return c.presignUpload(ctx, r)
	})
}

func (c *Component) presignUpload(ctx context.Context, r PresignRequest) (string, error) {
	presigner := c.getPresigner()
	if presigner == nil {
		return "", fmt.Errorf("s3 presign-upload: client not initialised")
	}
	if r.Bucket == "" || r.Key == "" {
		return "", fmt.Errorf("s3 presign-upload: bucket and key are required")
	}
	ttl := r.TTL
	if ttl == 0 {
		ttl = c.cfg.presignTTL()
	}
	input := &s3.PutObjectInput{
		Bucket: &r.Bucket,
		Key:    &r.Key,
	}
	if r.ContentType != "" {
		input.ContentType = &r.ContentType
	}
	if r.ContentLength > 0 {
		input.ContentLength = &r.ContentLength
	}

	resp, err := presigner.PresignPutObject(ctx, input, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("s3 presign-upload %q/%q: %w", r.Bucket, r.Key, err)
	}
	return resp.URL, nil
}

// ptrOf returns a pointer to v.
func ptrOf[T any](v T) *T { return &v }
