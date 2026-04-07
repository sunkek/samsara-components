package s3

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
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

// Upload puts an object into S3. The MIME type is auto-detected from the first
// 512 bytes of Body unless [UploadRequest.ContentType] is set explicitly.
func (c *Component) Upload(ctx context.Context, r UploadRequest) error {
	if c.getClient() == nil {
		return fmt.Errorf("s3 upload: client not initialised")
	}
	if r.Bucket == "" || r.Key == "" {
		return fmt.Errorf("s3 upload: bucket and key are required")
	}

	contentType := r.ContentType
	body := r.Body
	if contentType == "" {
		var err error
		contentType, body, err = detectContentType(r.Key, body)
		if err != nil {
			return fmt.Errorf("s3 upload: content-type detection: %w", err)
		}
	}

	acl := r.ACL
	if acl == "" {
		acl = ACLPrivate
	}

	input := &s3.PutObjectInput{
		Bucket:      &r.Bucket,
		Key:         &r.Key,
		Body:        body,
		ACL:         types.ObjectCannedACL(acl),
		ContentType: &contentType,
	}
	if _, err := c.getClient().PutObject(ctx, input); err != nil {
		return fmt.Errorf("s3 upload %q/%q: %w", r.Bucket, r.Key, err)
	}
	return nil
}

// Download retrieves an object from S3. The caller must close the returned
// [io.ReadCloser] after reading.
func (c *Component) Download(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	if c.getClient() == nil {
		return nil, fmt.Errorf("s3 download: client not initialised")
	}
	if bucket == "" || key == "" {
		return nil, fmt.Errorf("s3 download: bucket and key are required")
	}
	out, err := c.getClient().GetObject(ctx, &s3.GetObjectInput{
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
	if c.getClient() == nil {
		return fmt.Errorf("s3 delete: client not initialised")
	}
	if bucket == "" || key == "" {
		return fmt.Errorf("s3 delete: bucket and key are required")
	}
	if _, err := c.getClient().DeleteObject(ctx, &s3.DeleteObjectInput{
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
	if c.getClient() == nil {
		return 0, fmt.Errorf("s3 delete-by-prefix: client not initialised")
	}
	if bucket == "" {
		return 0, fmt.Errorf("s3 delete-by-prefix: bucket is required")
	}

	keys, err := c.ListKeys(ctx, bucket, prefix)
	if err != nil {
		return 0, fmt.Errorf("s3 delete-by-prefix: list: %w", err)
	}
	if len(keys) == 0 {
		return 0, nil
	}

	ids := make([]types.ObjectIdentifier, len(keys))
	for i, k := range keys {
		k := k // capture
		ids[i] = types.ObjectIdentifier{Key: &k}
	}

	_, err = c.getClient().DeleteObjects(ctx, &s3.DeleteObjectsInput{
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
	if c.getClient() == nil {
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
		out, err := c.getClient().ListObjectsV2(ctx, &s3.ListObjectsV2Input{
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
		if !*out.IsTruncated {
			break
		}
		continuationToken = out.NextContinuationToken
	}
	return keys, nil
}

// PresignDownload generates a time-limited presigned URL for downloading an object.
// The URL is valid for [PresignRequest.TTL] or [Config.PresignTTL] if TTL is 0.
func (c *Component) PresignDownload(ctx context.Context, r PresignRequest) (string, error) {
	if c.getPresigner() == nil {
		return "", fmt.Errorf("s3 presign-download: client not initialised")
	}
	if r.Bucket == "" || r.Key == "" {
		return "", fmt.Errorf("s3 presign-download: bucket and key are required")
	}
	ttl := r.TTL
	if ttl == 0 {
		ttl = c.cfg.presignTTL()
	}
	resp, err := c.getPresigner().PresignGetObject(ctx, &s3.GetObjectInput{
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
func (c *Component) PresignUpload(ctx context.Context, r PresignRequest) (string, error) {
	if c.getPresigner() == nil {
		return "", fmt.Errorf("s3 presign-upload: client not initialised")
	}
	if r.Bucket == "" || r.Key == "" {
		return "", fmt.Errorf("s3 presign-upload: bucket and key are required")
	}
	ttl := r.TTL
	if ttl == 0 {
		ttl = c.cfg.presignTTL()
	}
	resp, err := c.getPresigner().PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: &r.Bucket,
		Key:    &r.Key,
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("s3 presign-upload %q/%q: %w", r.Bucket, r.Key, err)
	}
	return resp.URL, nil
}

// detectContentType sniffs the MIME type from the first 512 bytes of body.
// It returns the detected type and a reassembled reader that starts from
// the beginning of the original content.
// SVG files are detected by extension or content inspection.
func detectContentType(key string, body io.Reader) (string, io.Reader, error) {
	buf := make([]byte, 512)
	n, err := body.Read(buf)
	if err != nil && err != io.EOF {
		return "", nil, err
	}

	contentType := http.DetectContentType(buf[:n])

	// http.DetectContentType cannot detect SVG; handle it explicitly.
	if strings.HasSuffix(strings.ToLower(key), ".svg") ||
		bytes.Contains(bytes.ToLower(buf[:n]), []byte("<svg")) {
		contentType = "image/svg+xml"
	}

	// Reassemble the full reader from the sniffed prefix + remaining bytes.
	return contentType, io.MultiReader(bytes.NewReader(buf[:n]), body), nil
}

// ptrOf returns a pointer to v.
func ptrOf[T any](v T) *T { return &v }
