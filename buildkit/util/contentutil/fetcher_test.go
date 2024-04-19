package contentutil

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/containerd/containerd/content"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestFetcher(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	b0 := NewBuffer()

	err := content.WriteBlob(ctx, b0, "foo", bytes.NewBuffer([]byte("foobar")), ocispecs.Descriptor{Size: -1})
	require.NoError(t, err)

	f := &localFetcher{b0}
	p := FromFetcher(f)

	b1 := NewBuffer()
	err = Copy(ctx, b1, p, ocispecs.Descriptor{Digest: digest.FromBytes([]byte("foobar")), Size: -1}, "", nil)
	require.NoError(t, err)

	dt, err := content.ReadBlob(ctx, b1, ocispecs.Descriptor{Digest: digest.FromBytes([]byte("foobar"))})
	require.NoError(t, err)
	require.Equal(t, string(dt), "foobar")

	rdr, err := p.ReaderAt(ctx, ocispecs.Descriptor{Digest: digest.FromBytes([]byte("foobar"))})
	require.NoError(t, err)

	buf := make([]byte, 3)

	n, err := rdr.ReadAt(buf, 1)
	require.NoError(t, err)
	require.Equal(t, "oob", string(buf[:n]))

	n, err = rdr.ReadAt(buf, 5)
	require.Error(t, err)
	require.Equal(t, err, io.EOF)
	require.Equal(t, "r", string(buf[:n]))
}

func TestSlowFetch(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	f := &dummySlowFetcher{}
	p := FromFetcher(f)

	rdr, err := p.ReaderAt(ctx, ocispecs.Descriptor{Digest: digest.FromBytes([]byte("foobar"))})
	require.NoError(t, err)

	buf := make([]byte, 3)

	n, err := rdr.ReadAt(buf, 1)
	require.NoError(t, err)
	require.Equal(t, "oob", string(buf[:n]))

	n, err = rdr.ReadAt(buf, 5)
	require.Error(t, err)
	require.Equal(t, err, io.EOF)
	require.Equal(t, "r", string(buf[:n]))
}

type dummySlowFetcher struct{}

func (f *dummySlowFetcher) Fetch(ctx context.Context, desc ocispecs.Descriptor) (io.ReadCloser, error) {
	return newSlowBuffer([]byte("foobar")), nil
}

func newSlowBuffer(dt []byte) io.ReadCloser {
	return &slowBuffer{dt: dt}
}

type slowBuffer struct {
	dt  []byte
	off int
}

func (sb *slowBuffer) Seek(offset int64, _ int) (int64, error) {
	sb.off = int(offset)
	return offset, nil
}

func (sb *slowBuffer) Read(b []byte) (int, error) {
	time.Sleep(5 * time.Millisecond)
	if sb.off >= len(sb.dt) {
		return 0, io.EOF
	}
	b[0] = sb.dt[sb.off]
	sb.off++
	return 1, nil
}

func (sb *slowBuffer) Close() error {
	return nil
}
