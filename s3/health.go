package s3

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go/transport/http"
)

// syntheticProbeBucket is the bucket name probed when [Config.HealthBucket] is
// empty. It is intentionally not a real bucket: any 404/403 answer proves the
// endpoint and signing chain work. The name is all-lowercase
// alphanumeric-and-hyphens so AWS S3 answers 404/403 rather than 400
// InvalidBucketName.
const syntheticProbeBucket = "samsara-health-probe"

// ErrProbeForbidden is returned by [Component.Health] and [Component.Start]
// when the configured [Config.HealthBucket] answers 403 AccessDenied: the
// endpoint is reachable and the request was signed, but the credential is not
// scoped for the bucket the application uses. Check with
// errors.Is(err, s3.ErrProbeForbidden).
//
// It is never returned when [Config.HealthBucket] is empty — the synthetic
// probe cannot tell a mis-scoped credential from a bucket that simply is not
// ours.
var ErrProbeForbidden = errors.New("s3: health bucket access denied")

// ErrProbeBucketMissing is returned by [Component.Health] and
// [Component.Start] when the configured [Config.HealthBucket] answers 404: the
// endpoint is reachable but the bucket does not exist. Check with
// errors.Is(err, s3.ErrProbeBucketMissing).
var ErrProbeBucketMissing = errors.New("s3: health bucket not found")

// probeBucket reports the bucket the connectivity probe addresses.
func (c Config) probeBucket() string {
	if c.HealthBucket != "" {
		return c.HealthBucket
	}
	return syntheticProbeBucket
}

// verifyConnectivity sends a HeadBucket request against the probe bucket.
//
// With a synthetic bucket (strict false) any server answer — including 404 and
// 403 — confirms the endpoint is reachable and the signing chain works; only a
// network- or credential-level failure is reported.
//
// With a configured real bucket (strict true) only a successful HeadBucket
// counts as healthy: 403 becomes [ErrProbeForbidden] and 404 becomes
// [ErrProbeBucketMissing], so a credential scoped to the wrong buckets is
// reported instead of passing as reachable.
func verifyConnectivity(ctx context.Context, client *s3.Client, bucket string, strict bool) error {
	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: ptrOf(bucket),
	})
	if err == nil {
		return nil
	}
	if strict {
		switch statusCode(err) {
		case 403:
			return fmt.Errorf("%w: %q: %w", ErrProbeForbidden, bucket, err)
		case 404:
			return fmt.Errorf("%w: %q: %w", ErrProbeBucketMissing, bucket, err)
		}
		return err
	}
	// 404 and 403 responses from the server confirm connectivity.
	if isExpectedHealthError(err) {
		return nil
	}
	return err
}

// statusCode reports the HTTP status carried by err, or 0 when err is not an
// HTTP response error.
func statusCode(err error) int {
	var re *http.ResponseError
	if errors.As(err, &re) {
		return re.HTTPStatusCode()
	}
	return 0
}
