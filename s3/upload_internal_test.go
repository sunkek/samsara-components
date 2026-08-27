package s3

import (
	"context"
	"io"
	"runtime"
	"strings"
	"testing"
)

// fakeEngine records the parameters it was handed and drains the body without
// retaining it, standing in for a streaming engine.
type fakeEngine struct {
	params  putParams
	read    int64
	sniffed []byte
	err     error
}

func (f *fakeEngine) put(_ context.Context, p putParams) error {
	f.params = p
	if f.err != nil {
		return f.err
	}
	head := make([]byte, 16)
	n, _ := io.ReadFull(p.Body, head)
	f.sniffed = head[:n]
	rest, err := io.Copy(io.Discard, p.Body)
	f.read = int64(n) + rest
	return err
}

func newTestComponent(e uploadEngine) *Component {
	c := New(Config{})
	c.engine = e
	return c
}

// ----------------------------------------------------------------------------
// Upload policy: content-type sniffing lives above the port
// ----------------------------------------------------------------------------

func TestUpload_SniffsContentType(t *testing.T) {
	tests := []struct {
		name string
		key  string
		body string
		want string
	}{
		{"plain text", "notes.txt", "hello world", "text/plain; charset=utf-8"},
		{"svg by extension", "logo.svg", "<?xml version=\"1.0\"?><nope/>", "image/svg+xml"},
		{"svg by content", "logo.bin", "<svg xmlns=\"http://www.w3.org/2000/svg\"/>", "image/svg+xml"},
		{"png magic", "x.bin", "\x89PNG\r\n\x1a\n and some more bytes", "image/png"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fe := &fakeEngine{}
			c := newTestComponent(fe)
			if err := c.Upload(context.Background(), UploadRequest{
				Bucket: "b", Key: tc.key, Body: strings.NewReader(tc.body),
			}); err != nil {
				t.Fatalf("Upload: %v", err)
			}
			if fe.params.ContentType != tc.want {
				t.Fatalf("content type = %q, want %q", fe.params.ContentType, tc.want)
			}
			if fe.read != int64(len(tc.body)) {
				t.Fatalf("engine read %d bytes, want %d", fe.read, len(tc.body))
			}
		})
	}
}

func TestUpload_ExplicitContentTypeSkipsSniffing(t *testing.T) {
	fe := &fakeEngine{}
	c := newTestComponent(fe)
	if err := c.Upload(context.Background(), UploadRequest{
		Bucket: "b", Key: "k", Body: strings.NewReader("hello"),
		ContentType: "application/x-custom",
	}); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if fe.params.ContentType != "application/x-custom" {
		t.Fatalf("content type = %q, want the explicit one", fe.params.ContentType)
	}
}

func TestUpload_DefaultsACLToPrivate(t *testing.T) {
	fe := &fakeEngine{}
	c := newTestComponent(fe)
	if err := c.Upload(context.Background(), UploadRequest{
		Bucket: "b", Key: "k", Body: strings.NewReader("x"),
	}); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if fe.params.ACL != string(ACLPrivate) {
		t.Fatalf("ACL = %q, want %q", fe.params.ACL, ACLPrivate)
	}
}

func TestUpload_ShortBodySniffsWithoutPadding(t *testing.T) {
	fe := &fakeEngine{}
	c := newTestComponent(fe)
	if err := c.Upload(context.Background(), UploadRequest{
		Bucket: "b", Key: "k", Body: strings.NewReader("hi"),
	}); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if fe.read != 2 {
		t.Fatalf("engine saw %d bytes, want exactly 2", fe.read)
	}
}

func TestUpload_NoEngine(t *testing.T) {
	c := New(Config{})
	err := c.Upload(context.Background(), UploadRequest{
		Bucket: "b", Key: "k", Body: strings.NewReader("x"),
	})
	if err == nil {
		t.Fatal("expected an error when the component has not been started")
	}
}

// ----------------------------------------------------------------------------
// Memory bound — the acceptance criterion of #4
// ----------------------------------------------------------------------------

// TestUpload_DoesNotBufferBody streams a body far larger than any allowance the
// component is permitted and asserts that Upload itself holds only a bounded
// amount of it. The engine is a fake, so this measures the component side of
// the port only; the engine's own bound is covered by the integration tests.
func TestUpload_DoesNotBufferBody(t *testing.T) {
	const (
		size    = 256 << 20 // 256 MiB
		allowed = 8 << 20   // generous next to the 512-byte sniff buffer
	)

	c := newTestComponent(&fakeEngine{})

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	err := c.Upload(context.Background(), UploadRequest{
		Bucket: "b", Key: "big.bin", Body: io.LimitReader(zeroReader{}, size),
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	runtime.ReadMemStats(&after)
	growth := after.TotalAlloc - before.TotalAlloc
	if growth > allowed {
		t.Fatalf("Upload allocated %d bytes for a %d-byte body; want at most %d",
			growth, size, allowed)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { return len(p), nil }
