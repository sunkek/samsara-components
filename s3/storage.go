package s3

import (
	"context"
	"io"
)

// Storage is the interface that domain adapters should depend on.
// *Component satisfies it; depend on Storage rather than *Component to keep
// adapters testable.
//
//	type AvatarRepo struct { store s3.Storage }
type Storage interface {
	// Upload puts an object into S3. See [Component.Upload].
	Upload(ctx context.Context, r UploadRequest) error

	// Download streams an object's body. The caller must close the reader.
	Download(ctx context.Context, bucket, key string) (io.ReadCloser, error)

	// Delete removes a single object.
	Delete(ctx context.Context, bucket, key string) error

	// DeleteByPrefix removes all objects under prefix and reports how many
	// were deleted.
	DeleteByPrefix(ctx context.Context, bucket, prefix string) (int, error)

	// ListKeys returns all object keys under prefix.
	ListKeys(ctx context.Context, bucket, prefix string) ([]string, error)

	// PresignDownload returns a presigned GET URL.
	PresignDownload(ctx context.Context, r PresignRequest) (string, error)

	// PresignUpload returns a presigned PUT URL.
	PresignUpload(ctx context.Context, r PresignRequest) (string, error)
}

// Compile-time assertion: *Component satisfies Storage.
var _ Storage = (*Component)(nil)
