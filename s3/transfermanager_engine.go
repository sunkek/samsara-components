package s3

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	tmtypes "github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// transferManagerEngine is the production [uploadEngine]. It is the only place
// in this module that mentions the transfermanager package: the module is
// pre-1.0, so keeping it behind the port stops its churn reaching callers.
// See docs/adr/0004-transfermanager-behind-an-internal-port.md.
type transferManagerEngine struct {
	client *transfermanager.Client
}

func newTransferManagerEngine(client *s3.Client, cfg Config) *transferManagerEngine {
	partSize := cfg.uploadPartSize()
	return &transferManagerEngine{
		client: transfermanager.New(client, func(o *transfermanager.Options) {
			o.PartSizeBytes = partSize
			o.Concurrency = cfg.uploadConcurrency()
			// The threshold governs how much of a sub-threshold body is read
			// into memory before the transfer starts. Pinning it to the part
			// size caps that read at one part instead of the 16 MiB default.
			o.MultipartUploadThreshold = partSize
		}),
	}
}

func (e *transferManagerEngine) put(ctx context.Context, p putParams) error {
	_, err := e.client.UploadObject(ctx, &transfermanager.UploadObjectInput{
		Bucket:      &p.Bucket,
		Key:         &p.Key,
		Body:        p.Body,
		ContentType: &p.ContentType,
		ACL:         tmtypes.ObjectCannedACL(p.ACL),
	})
	return err
}

// Compile-time assertion: the production engine satisfies the port.
var _ uploadEngine = (*transferManagerEngine)(nil)
