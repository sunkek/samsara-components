package s3

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// uploadEngine is the port through which the component writes object bodies.
//
// It exists so that upload policy — content-type sniffing, ACL defaulting,
// validation — can be unit-tested without a live endpoint, and so that the
// pre-1.0 transfermanager module stays a single-file dependency. See
// docs/adr/0004-transfermanager-behind-an-internal-port.md.
type uploadEngine interface {
	put(ctx context.Context, p putParams) error
}

// putParams is the engine-neutral description of one object write.
// Body is streamed; an engine must not buffer more than one part of it.
type putParams struct {
	Bucket      string
	Key         string
	Body        io.Reader
	ContentType string
	ACL         string
}

// sniffLen is the number of leading bytes [http.DetectContentType] inspects.
const sniffLen = 512

// Upload puts an object into S3. The MIME type is auto-detected from the first
// 512 bytes of Body unless [UploadRequest.ContentType] is set explicitly.
//
// Body is streamed, not buffered: Upload itself holds only the 512-byte sniff
// buffer regardless of object size. The underlying engine holds at most
// (UploadConcurrency+1) × UploadPartSize — roughly 85 MiB per in-flight upload
// at the defaults, once GC slack and HTTP buffers are counted.
func (c *Component) Upload(ctx context.Context, r UploadRequest) error {
	return observeErr(c, opUpload, func() error {
		return c.upload(ctx, r)
	})
}

func (c *Component) upload(ctx context.Context, r UploadRequest) error {
	engine := c.getEngine()
	if engine == nil {
		return fmt.Errorf("s3 upload: %w", ErrNotReady)
	}
	if r.Bucket == "" || r.Key == "" {
		return fmt.Errorf("s3 upload: bucket and key are required")
	}
	if r.Body == nil {
		return fmt.Errorf("s3 upload: body is required")
	}

	contentType := r.ContentType
	body := r.Body
	if contentType == "" {
		var err error
		contentType, body, err = sniffContentType(r.Key, r.Body)
		if err != nil {
			return fmt.Errorf("s3 upload: content-type detection: %w", err)
		}
	}

	acl := r.ACL
	if acl == "" {
		acl = ACLPrivate
	}

	if err := engine.put(ctx, putParams{
		Bucket:      r.Bucket,
		Key:         r.Key,
		Body:        body,
		ContentType: contentType,
		ACL:         string(acl),
	}); err != nil {
		return fmt.Errorf("s3 upload %q/%q: %w", r.Bucket, r.Key, err)
	}
	return nil
}

// sniffContentType reads at most the first 512 bytes of body to determine the
// MIME type, and returns a reader that replays those bytes followed by the
// remainder. Only the sniff buffer is held in memory.
//
// SVG files are detected by extension or content prefix, since
// [http.DetectContentType] cannot recognise them.
func sniffContentType(key string, body io.Reader) (string, io.Reader, error) {
	head := make([]byte, sniffLen)
	n, err := io.ReadFull(body, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", nil, err
	}
	head = head[:n]

	contentType := http.DetectContentType(head)
	if strings.HasSuffix(strings.ToLower(key), ".svg") ||
		bytes.Contains(bytes.ToLower(head), []byte("<svg")) {
		contentType = "image/svg+xml"
	}

	return contentType, io.MultiReader(bytes.NewReader(head), body), nil
}
