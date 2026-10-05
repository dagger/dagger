package remotecache

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/remotecache/protocol"
)

const (
	// The shortened timeouts the upload tests run with.
	testUploadIdle     = 200 * time.Millisecond
	testUploadResponse = 200 * time.Millisecond
	// testUploadGuard is how long a test waits before it calls an upload
	// hung.
	testUploadGuard = 10 * time.Second
)

// stallingBlobStore is a blob store that stalls every upload: it reads none of
// the body, or all of it, and then never answers. Its address carries a
// signature.
func stallingBlobStore(t *testing.T, readBody bool) string {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if readBody {
			_, _ = io.Copy(io.Discard, r.Body)
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	return srv.URL + "/put?X-Amz-Signature=secret"
}

// zeros is an endless body of zero bytes.
type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// slowBody yields size bytes in pieces of at most 1 KiB, pausing before each.
type slowBody struct {
	remaining int
	pause     time.Duration
}

func (b *slowBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	time.Sleep(b.pause)
	n := min(len(p), b.remaining, 1<<10)
	clear(p[:n])
	b.remaining -= n
	return n, nil
}

// putWithin runs put, and fails the test if it hasn't returned within
// testUploadGuard.
func putWithin(t *testing.T, put func(context.Context, string, io.Reader, int64) error, address string, body io.Reader, size int64) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- put(t.Context(), address, body, size) }()
	select {
	case err := <-done:
		return err
	case <-time.After(testUploadGuard):
		t.Fatal("the upload hangs")
		return nil
	}
}

// An upload stalls when the blob store stops reading its body, or reads it
// all and never answers. Either way the upload fails within the timeouts,
// and its error doesn't name the signed address.
func TestStalledUploadFails(t *testing.T) {
	put := httpPut(uploadClient(testUploadResponse), testUploadIdle)
	for _, tc := range []struct {
		name     string
		readBody bool
		// size fills the connection's buffers when the store reads nothing.
		size  int64
		check func(*testing.T, error)
	}{
		{name: "the blob store reads none of the body", readBody: false, size: 64 << 20, check: func(t *testing.T, err error) {
			require.ErrorIs(t, err, errUploadStalled)
		}},
		{name: "the blob store reads the body and never answers", readBody: true, size: 1 << 20, check: func(t *testing.T, err error) {
			require.ErrorContains(t, err, "timeout awaiting response headers")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			address := stallingBlobStore(t, tc.readBody)
			err := putWithin(t, put, address, io.LimitReader(zeros{}, tc.size), tc.size)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "secret")
			tc.check(t, err)
		})
	}
}

// eofBody records that its reader returned io.EOF.
type eofBody struct {
	io.Reader
	eof atomic.Bool
}

func (b *eofBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		b.eof.Store(true)
	}
	return n, err
}

// Over HTTP/2 the transport reads the body's end before it sends the last
// bytes, which wait for the blob store's flow control. An upload stalled
// there, after the body's end, fails like one stalled before it.
func TestStalledHTTP2UploadFails(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int64
		// afterEOF is whether the stall comes after the body's end.
		afterEOF bool
	}{
		{name: "before the body's end", size: 4 << 20},
		{name: "after the body's end, in the final chunk", size: 1<<20 + 1, afterEOF: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := make(chan struct{})
			proto := make(chan int, 1)
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				proto <- r.ProtoMajor
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}))
			srv.EnableHTTP2 = true
			srv.StartTLS()
			t.Cleanup(srv.Close)
			t.Cleanup(func() { close(release) })
			client := uploadClient(testUploadResponse)
			transport := client.Transport.(*http.Transport)
			transport.TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			t.Cleanup(transport.CloseIdleConnections)

			// A section reader, as the adapter's blobs are.
			body := &eofBody{Reader: io.NewSectionReader(bytes.NewReader(make([]byte, tc.size)), 0, tc.size)}
			err := putWithin(t, httpPut(client, testUploadIdle), srv.URL+"/put?X-Amz-Signature=secret", body, tc.size)
			require.Equal(t, 2, <-proto, "the upload used HTTP/2")
			require.ErrorIs(t, err, errUploadStalled)
			require.NotContains(t, err.Error(), "secret")
			require.Equal(t, tc.afterEOF, body.eof.Load(), "where the upload stalled")
		})
	}
}

// An upload that keeps progressing is not cut off, however long it takes in
// all.
func TestSlowUploadIsNotCutOff(t *testing.T) {
	var received atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		n, _ := io.Copy(io.Discard, r.Body)
		received.Store(n)
	}))
	t.Cleanup(srv.Close)
	put := httpPut(uploadClient(testUploadResponse), testUploadIdle)
	const size = 8 << 10
	start := time.Now()
	require.NoError(t, putWithin(t, put, srv.URL, &slowBody{remaining: size, pause: testUploadIdle / 2}, size))
	require.Greater(t, time.Since(start), 2*testUploadIdle, "the upload took longer than the idle timeout in all")
	require.Equal(t, int64(size), received.Load())
}

// A stalled upload fails and frees its slot: with a single slot, the next
// upload runs.
func TestStalledUploadFreesItsSlot(t *testing.T) {
	stalled, good := digest.FromString("stalled"), digest.FromString("good")
	adapter := newFakeAdapter()
	adapter.export = func(ctx context.Context, _ protocol.Export, consume func(context.Context, Export) error) error {
		return consume(ctx, Export{Bundle: testBundle(), Blobs: blobs{stalled: "stalled blob", good: "good blob"}})
	}
	s, conn := testSession(t, adapter)
	s.cfg.put = httpPut(uploadClient(testUploadResponse), testUploadIdle)
	s.uploadSlots = make(chan struct{}, 1)
	store := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	t.Cleanup(store.Close)

	upload := func(id uint64, dgst digest.Digest, address string) protocol.Uploaded {
		t.Helper()
		s.dispatch(request(t, id, protocol.TypeExport, protocol.Export{Roots: []uint64{12}}))
		exported := decode[protocol.Exported](t, conn.next(t))
		s.dispatch(request(t, id+1, protocol.TypeUpload, protocol.Upload{ExportID: exported.ExportID, URLs: map[digest.Digest]string{dgst: address}}))
		reply := conn.next(t)
		require.Equal(t, protocol.TypeUploaded, reply.Type)
		return decode[protocol.Uploaded](t, reply)
	}
	first := upload(7, stalled, stallingBlobStore(t, true))
	require.Empty(t, first.Done)
	require.Len(t, first.Failed, 1)
	require.Equal(t, stalled, first.Failed[0].Digest)
	require.NotContains(t, first.Failed[0].Message, "secret")

	second := upload(9, good, store.URL)
	require.Equal(t, []digest.Digest{good}, second.Done)
	require.Empty(t, second.Failed)
}
