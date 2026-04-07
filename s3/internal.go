package s3

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/smithy-go/transport/http"
)

// credProvider implements [aws.CredentialsProvider] using static key/secret
// credentials. It is used by both production code and tests.
type credProvider struct {
	keyID  string
	secret string
}

func (cp credProvider) Retrieve(_ context.Context) (aws.Credentials, error) {
	return aws.Credentials{
		AccessKeyID:     cp.keyID,
		SecretAccessKey: cp.secret,
	}, nil
}

// isExpectedHealthError reports whether err is an HTTP 404 or 403 response,
// which means the endpoint and signing chain are functional — only the bucket
// doesn't exist or isn't accessible, which is expected for a health check.
func isExpectedHealthError(err error) bool {
	if err == nil {
		return false
	}
	var re *http.ResponseError
	if errors.As(err, &re) {
		code := re.HTTPStatusCode()
		return code == 404 || code == 403
	}
	return false
}
