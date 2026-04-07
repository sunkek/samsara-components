//go:build integration

package s3_test

// Integration tests run against a local SeaweedFS instance started by docker-compose.
// SeaweedFS emulates the AWS S3 API and is fully Apache 2.0 licensed.
//
// Start infra:
//
//	make infra-up
//
// Run with:
//
//	go test -race -tags integration -timeout 120s ./...
//
// or via the repo root:
//
//	make test-integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/sunkek/samsara-components/s3"
)

// SeaweedFS credentials and endpoint match docker-compose.yml.
// The s3.json config file defines a single identity with key "test"/"test".
const (
	testEndpoint = "http://localhost:8333"
	testRegion   = "us-east-1"
	testKeyID    = "test"
	testSecret   = "test"
	testBucket   = "test" // created by the seaweedfs-init service
)

func testComp(t *testing.T) *s3.Component {
	t.Helper()
	return s3.New(s3.Config{
		Endpoint:         testEndpoint,
		Region:           testRegion,
		KeyID:            testKeyID,
		Secret:           testSecret,
		ConnectTimeout:   30 * time.Second, // SeaweedFS can be slow on first request
		PathStyleForcing: true,             // required for SeaweedFS local setup
	}, s3.WithLogger(&testLogger{t}))
}

func startComp(t *testing.T, comp *s3.Component) {
	t.Helper()
	readyCh := make(chan struct{})
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())

	go func() { errCh <- comp.Start(ctx, func() { close(readyCh) }) }()

	select {
	case <-readyCh:
	case err := <-errCh:
		cancel()
		t.Fatalf("Start failed: %v", err)
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatal("Start timed out")
	}

	t.Cleanup(func() {
		cancel()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = comp.Stop(stopCtx)
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("Start returned unexpected error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Start goroutine did not exit after Stop")
		}
	})
}

// uniqueKey generates a test-scoped key to prevent cross-test interference.
func uniqueKey(t *testing.T, suffix string) string {
	return fmt.Sprintf("_sc_s3_test/%s/%s", strings.ReplaceAll(t.Name(), "/", "_"), suffix)
}

func TestIntegration_StartStop(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)
}

func TestIntegration_Health(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := comp.Health(ctx); err != nil {
		t.Fatalf("Health returned error on live endpoint: %v", err)
	}
}

func TestIntegration_Restart(t *testing.T) {
	comp := testComp(t)
	for i := range 2 {
		readyCh := make(chan struct{})
		errCh := make(chan error, 1)
		ctx, cancel := context.WithCancel(context.Background())

		go func() { errCh <- comp.Start(ctx, func() { close(readyCh) }) }()

		select {
		case <-readyCh:
		case err := <-errCh:
			cancel()
			t.Fatalf("run %d: Start failed: %v", i+1, err)
		case <-time.After(30 * time.Second):
			cancel()
			t.Fatalf("run %d: Start timed out", i+1)
		}

		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = comp.Stop(stopCtx)
		stopCancel()
		cancel()

		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("run %d: Start returned error: %v", i+1, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("run %d: Start goroutine did not exit", i+1)
		}
	}
}

func TestIntegration_Upload_Download(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)
	ctx := context.Background()

	key := uniqueKey(t, "hello.txt")
	body := "hello, samsara-components"
	t.Cleanup(func() { _ = comp.Delete(context.Background(), testBucket, key) })

	err := comp.Upload(ctx, s3.UploadRequest{
		Bucket: testBucket,
		Key:    key,
		Body:   strings.NewReader(body),
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	rc, err := comp.Download(ctx, testBucket, key)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != body {
		t.Fatalf("expected %q, got %q", body, got)
	}
}

func TestIntegration_Upload_ContentTypeDetection(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)
	ctx := context.Background()

	cases := []struct {
		key          string
		body         string
		wantCTPrefix string
	}{
		{"image.svg", `<svg xmlns="http://www.w3.org/2000/svg"></svg>`, "image/svg+xml"},
		{"data.json", `{"key":"value"}`, "application/json"},
	}

	for _, tc := range cases {
		key := uniqueKey(t, tc.key)
		t.Cleanup(func() { _ = comp.Delete(context.Background(), testBucket, key) })

		err := comp.Upload(ctx, s3.UploadRequest{
			Bucket: testBucket,
			Key:    key,
			Body:   strings.NewReader(tc.body),
		})
		if err != nil {
			t.Errorf("Upload %q: %v", tc.key, err)
		}
	}
}

func TestIntegration_Delete(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)
	ctx := context.Background()

	key := uniqueKey(t, "to-delete.txt")
	_ = comp.Upload(ctx, s3.UploadRequest{
		Bucket: testBucket, Key: key, Body: strings.NewReader("x"),
	})

	if err := comp.Delete(ctx, testBucket, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Downloading a deleted object should fail.
	_, err := comp.Download(ctx, testBucket, key)
	if err == nil {
		t.Fatal("expected error downloading deleted object")
	}
}

func TestIntegration_DeleteByPrefix(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)
	ctx := context.Background()

	prefix := uniqueKey(t, "prefix-delete/")
	keys := []string{prefix + "a.txt", prefix + "b.txt", prefix + "c.txt"}
	for _, k := range keys {
		_ = comp.Upload(ctx, s3.UploadRequest{
			Bucket: testBucket, Key: k, Body: bytes.NewReader([]byte("x")),
		})
	}

	n, err := comp.DeleteByPrefix(ctx, testBucket, prefix)
	if err != nil {
		t.Fatalf("DeleteByPrefix: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 deleted, got %d", n)
	}

	remaining, err := comp.ListKeys(ctx, testBucket, prefix)
	if err != nil {
		t.Fatalf("ListKeys after delete: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected 0 remaining keys, got %d: %v", len(remaining), remaining)
	}
}

func TestIntegration_ListKeys(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)
	ctx := context.Background()

	prefix := uniqueKey(t, "list/")
	keys := []string{prefix + "1.txt", prefix + "2.txt"}
	for _, k := range keys {
		_ = comp.Upload(ctx, s3.UploadRequest{
			Bucket: testBucket, Key: k, Body: strings.NewReader("x"),
		})
	}
	t.Cleanup(func() {
		for _, k := range keys {
			_ = comp.Delete(context.Background(), testBucket, k)
		}
	})

	found, err := comp.ListKeys(ctx, testBucket, prefix)
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("expected 2 keys, got %d: %v", len(found), found)
	}
}

func TestIntegration_PresignDownload(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)
	ctx := context.Background()

	key := uniqueKey(t, "presign-dl.txt")
	_ = comp.Upload(ctx, s3.UploadRequest{
		Bucket: testBucket, Key: key, Body: strings.NewReader("presigned"),
	})
	t.Cleanup(func() { _ = comp.Delete(context.Background(), testBucket, key) })

	url, err := comp.PresignDownload(ctx, s3.PresignRequest{
		Bucket: testBucket, Key: key, TTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("PresignDownload: %v", err)
	}
	if url == "" {
		t.Fatal("expected non-empty presigned URL")
	}
	t.Logf("presigned download URL: %s", url[:min(80, len(url))])
}

func TestIntegration_PresignUpload(t *testing.T) {
	comp := testComp(t)
	startComp(t, comp)
	ctx := context.Background()

	key := uniqueKey(t, "presign-ul.txt")
	t.Cleanup(func() { _ = comp.Delete(context.Background(), testBucket, key) })

	url, err := comp.PresignUpload(ctx, s3.PresignRequest{
		Bucket: testBucket, Key: key, TTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("PresignUpload: %v", err)
	}
	if url == "" {
		t.Fatal("expected non-empty presigned URL")
	}
	t.Logf("presigned upload URL: %s", url[:min(80, len(url))])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
