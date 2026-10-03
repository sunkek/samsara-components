package s3

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
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
