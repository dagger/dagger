package snapshots

import (
	"bytes"
	"sync"
	"testing"
	"time"

	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// writeInChunks writes data to d as a tar writer does, 32 KiB at a time.
func writeInChunks(t *testing.T, d *layerDigester, data []byte) {
	t.Helper()
	for p := data; len(p) > 0; {
		n := min(len(p), 32<<10)
		written, err := d.Write(p[:n])
		require.NoError(t, err)
		require.Equal(t, n, written)
		p = p[n:]
	}
}

// Writing a layer's bytes doesn't wait for their hash. The differ reads a
// layer's files and writes its tar beside the compressor through the
// layer's digester, one goroutine for every byte of the layer: with the hash
// in line, a 6 GB layer's export spent about half its time on one core
// hashing, while its compression ran on others. Here the hash is held while
// 4 MB is written, and the digest is that of the bytes once it is let go.
func TestLayerDigesterWritesAheadOfItsHash(t *testing.T) {
	t.Parallel()
	held := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(held) }) }
	t.Cleanup(release)
	d := newLayerDigester()
	d.hashing = func() { <-held }
	data := bytes.Repeat([]byte("a layer's bytes "), (4<<20)/16)
	wrote := make(chan struct{})
	go func() {
		defer close(wrote)
		writeInChunks(t, d, data)
	}()
	select {
	case <-wrote:
	case <-time.After(10 * time.Second):
		t.Fatal("writing 4 MB of a layer waited for its hash")
	}
	release()
	require.Equal(t, digest.FromBytes(data), d.Digest())
	d.Close()
}

// The digest is the SHA-256 of every byte written, in order, whatever the
// writes' sizes, and taking it again gives it again.
func TestLayerDigesterDigestsWhatWasWritten(t *testing.T) {
	t.Parallel()
	for _, size := range []int{0, 1, 32 << 10, 1 << 20, 1<<20 + 1, 9<<20 + 7} {
		data := make([]byte, size)
		for i := range data {
			data[i] = byte(i * 7919)
		}
		d := newLayerDigester()
		writeInChunks(t, d, data)
		require.Equal(t, digest.FromBytes(data), d.Digest(), "%d bytes", size)
		require.Equal(t, digest.FromBytes(data), d.Digest(), "%d bytes, taken again", size)
		d.Close()
	}
}

// A writer gets only so far ahead of the hash: with the hash held, writing
// far more than the digester holds doesn't finish until the hash goes on, so
// a layer's digester holds a bounded amount of it, however large the layer.
func TestLayerDigesterHoldsABoundedAmount(t *testing.T) {
	t.Parallel()
	held := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(held) }) }
	t.Cleanup(release)
	d := newLayerDigester()
	d.hashing = func() { <-held }
	data := bytes.Repeat([]byte{7}, 64<<20)
	wrote := make(chan struct{})
	go func() {
		defer close(wrote)
		writeInChunks(t, d, data)
	}()
	select {
	case <-wrote:
		t.Fatal("64 MB was written while its hash was held")
	case <-time.After(500 * time.Millisecond):
	}
	release()
	<-wrote
	require.Equal(t, digest.FromBytes(data), d.Digest())
	d.Close()
}

// A digester closed without its digest, as on a failed write, lets go of
// what it holds and stops.
func TestLayerDigesterClosesWithoutItsDigest(t *testing.T) {
	t.Parallel()
	d := newLayerDigester()
	writeInChunks(t, d, bytes.Repeat([]byte{1}, 3<<20))
	d.Close()
	d.Close()
}
