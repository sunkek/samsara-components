#!/usr/bin/env sh
# Creates the S3 bucket used by integration tests.
# Executed as the SeaweedFS container entrypoint wrapper after the server starts.
#
# SeaweedFS server -s3 does not have a ready.d hook like LocalStack, so the
# docker-compose healthcheck waits for the S3 port before this script runs.
# The bucket is created via the S3 API using curl with AWS Signature v4 is not
# needed here because SeaweedFS in no-IAM mode (with the config file supplying
# one identity) accepts any valid signed request.
#
# Instead we use the weed shell command, which is available in the same image.
set -eu

ENDPOINT="http://localhost:8333"
BUCKET="test"
REGION="us-east-1"
KEY_ID="test"
SECRET="test"

# Wait until the S3 port responds (healthcheck already ensures this before
# the init container runs, but guard here too for robustness).
until wget -qO- "${ENDPOINT}" > /dev/null 2>&1 || \
      wget -qO- "${ENDPOINT}" 2>&1 | grep -q "xml\|AccessDenied\|NoSuch"; do
  echo "seaweedfs-init: waiting for S3 endpoint..."
  sleep 1
done

# Create the bucket using the AWS CLI baked into many CI environments, OR fall
# back to a minimal curl PUT request (virtual-hosted style not needed with
# path-style, which SeaweedFS supports).
#
# We use the aws CLI if available; otherwise raw curl.
if command -v aws > /dev/null 2>&1; then
  AWS_ACCESS_KEY_ID="${KEY_ID}" \
  AWS_SECRET_ACCESS_KEY="${SECRET}" \
  aws --endpoint-url "${ENDPOINT}" \
      --region "${REGION}" \
      s3 mb "s3://${BUCKET}" 2>/dev/null || true
  echo "seaweedfs-init: bucket '${BUCKET}' ready (aws cli)"
else
  # Minimal signed PUT-bucket via curl is complex; use weed shell instead.
  echo "s3.bucket.create -name ${BUCKET}" | \
    weed shell -master=localhost:9333 2>/dev/null || true
  echo "seaweedfs-init: bucket '${BUCKET}' ready (weed shell)"
fi
