package compressutil

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/pkg/archive/compression"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

// payload is n bytes that compress into several zstd blocks: random runs
// between repeated text, different for each seed.
func payload(seed int64, n int) []byte {
	rng := rand.New(rand.NewSource(seed))
	var b bytes.Buffer
	for b.Len() < n {
		if rng.Intn(2) == 0 {
			fmt.Fprintf(&b, "line %d of stream %d\n", b.Len(), seed)
		} else {
			run := make([]byte, 64+rng.Intn(512))
			rng.Read(run)
			b.Write(run)
		}
	}
	return b.Bytes()[:n]
}

// zstdStream compresses data as a layer's blob is compressed: through a
// stream writer, whose frame declares its window rather than its size.
func zstdStream(t testing.TB, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w, err := zstd.NewWriter(&b)
	require.NoError(t, err)
	_, err = w.Write(data)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return b.Bytes()
}

func gzipStream(t testing.TB, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	_, err := w.Write(data)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return b.Bytes()
}

// corrupt flips bytes in the middle of a compressed stream.
func corrupt(stream []byte) []byte {
	out := bytes.Clone(stream)
	for i := len(out) / 2; i < len(out)/2+64 && i < len(out); i++ {
		out[i] ^= 0x5a
	}
	return out
}

type decompressed struct {
	data        []byte
	err         error
	compression compression.Compression
}

func readAll(t testing.TB, open func(io.Reader) (compression.DecompressReadCloser, error), stream []byte) decompressed {
	t.Helper()
	rc, err := open(bytes.NewReader(stream))
	if err != nil {
		return decompressed{err: err}
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	return decompressed{data: data, err: err, compression: rc.GetCompression()}
}

func requireSame(t testing.TB, want, got decompressed) {
	t.Helper()
	require.Equal(t, want.compression, got.compression)
	if want.err != nil {
		require.EqualError(t, got.err, want.err.Error())
	} else {
		require.NoError(t, got.err)
	}
	require.True(t, bytes.Equal(want.data, got.data), "decompressed %d bytes, containerd %d", len(got.data), len(want.data))
}

// Every stream decompresses to the bytes and errors containerd's
// DecompressStream gives it.
func TestDecompressStreamDecompressesAsContainerdDoes(t *testing.T) {
	t.Parallel()
	data := payload(1, 3<<20)
	one := zstdStream(t, payload(2, 4096))
	skippable := binary.LittleEndian.AppendUint32(nil, 0x184D2A50)
	skippable = binary.LittleEndian.AppendUint32(skippable, 4)
	skippable = append(skippable, "skip"...)
	cases := map[string][]byte{
		"empty":                  nil,
		"uncompressed":           data[:100_000],
		"gzip":                   gzipStream(t, data),
		"zstd":                   zstdStream(t, data),
		"zstd, two frames":       append(bytes.Clone(one), zstdStream(t, payload(3, 70_000))...),
		"zstd, skippable frame":  append(skippable, one...),
		"zstd, truncated":        zstdStream(t, data)[:200_000],
		"zstd, corrupt":          corrupt(zstdStream(t, data)),
		"zstd, stream then junk": append(bytes.Clone(one), "junk after the frame"...),
	}
	for name, stream := range cases {
		t.Run(name, func(t *testing.T) {
			requireSame(t, readAll(t, compression.DecompressStream, stream), readAll(t, DecompressStream, stream))
		})
	}
}

// A decoder reused after a stream that was read in part, closed unread,
// truncated or corrupt decodes the next stream exactly. Not parallel: the
// next stream should get the decoder the last one returned. Every stream is
// made before the first is opened: making one between a close and the next
// open allocates enough for the garbage collector to empty the pool, which
// left the count of reuses inside the race detector's noise.
func TestDecompressStreamReusesDecodersWithoutStateBetweenStreams(t *testing.T) {
	var last *zstd.Decoder
	reused := 0
	open := func(stream []byte) compression.DecompressReadCloser {
		rc, err := DecompressStream(bytes.NewReader(stream))
		require.NoError(t, err)
		dec := rc.(*zstdReader).dec
		if dec == last {
			reused++
		}
		last = dec
		return rc
	}
	const rounds = 40
	type round struct{ stream, next, nextStream, corrupt []byte }
	streams := make([]round, rounds)
	for i := range streams {
		r := &streams[i]
		r.stream = zstdStream(t, payload(int64(100+i), 1<<20+i*997))
		r.next = payload(int64(1000+i), 300_000+i)
		r.nextStream = zstdStream(t, r.next)
		r.corrupt = corrupt(r.stream)
	}
	for i, r := range streams {
		stream := r.stream
		rc := open(stream)
		switch i % 4 {
		case 0: // read in part
			_, err := io.ReadFull(rc, make([]byte, 1000+i))
			require.NoError(t, err)
		case 1: // closed unread
		case 2: // truncated
			require.NoError(t, rc.Close())
			rc = open(stream[:len(stream)*2/3])
			_, err := io.ReadAll(rc)
			require.ErrorIs(t, err, io.ErrUnexpectedEOF)
		case 3: // corrupt
			require.NoError(t, rc.Close())
			rc = open(r.corrupt)
			_, err := io.ReadAll(rc)
			require.Error(t, err)
		}
		require.NoError(t, rc.Close())

		rc = open(r.nextStream)
		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		require.True(t, bytes.Equal(r.next, got), "round %d: the stream after a reused decoder's last one decoded wrong", i)
	}
	// 99 opens follow another. The race detector drops a quarter of returned
	// decoders on purpose, so about three quarters are reused under it.
	require.Greater(t, reused, rounds/2, "streams got a decoder the last stream returned %d times", reused)
}

// A closed stream's reader is let go of while its decoder is kept for the
// next stream: a layer's blob reader and its buffers aren't kept alive by an
// idle decoder.
func TestDecompressStreamLetsGoOfTheStreamOnClose(t *testing.T) {
	released := make(chan struct{})
	// The stream is opened and closed in a function of its own, so nothing
	// here refers to its reader afterwards.
	dec := func() *zstd.Decoder {
		r := bytes.NewReader(zstdStream(t, payload(9, 2<<20)))
		runtime.AddCleanup(r, func(ch chan struct{}) { close(ch) }, released)
		rc, err := DecompressStream(r)
		require.NoError(t, err)
		_, err = io.ReadFull(rc, make([]byte, 100))
		require.NoError(t, err)
		dec := rc.(*zstdReader).dec
		require.NoError(t, rc.Close())
		return dec
	}()
	deadline := time.After(10 * time.Second)
	for done := false; !done; {
		runtime.GC()
		select {
		case <-released:
			done = true
		case <-deadline:
			t.Fatal("the closed stream's reader is still reachable from its decoder")
		case <-time.After(10 * time.Millisecond):
		}
	}
	runtime.KeepAlive(dec)
}

// Closing a stream twice returns its decoder once: two later streams decoded
// at once never share a decoder.
func TestDecompressStreamClosedTwiceReturnsItsDecoderOnce(t *testing.T) {
	t.Parallel()
	for i := range 20 {
		rc, err := DecompressStream(bytes.NewReader(zstdStream(t, payload(int64(i), 10_000))))
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		require.NoError(t, rc.Close())
		_, err = rc.Read(make([]byte, 1))
		require.ErrorIs(t, err, zstd.ErrDecoderClosed)

		a, b := payload(int64(500+i), 2<<20), payload(int64(600+i), 2<<20)
		ra, err := DecompressStream(bytes.NewReader(zstdStream(t, a)))
		require.NoError(t, err)
		rb, err := DecompressStream(bytes.NewReader(zstdStream(t, b)))
		require.NoError(t, err)
		require.NotSame(t, ra.(*zstdReader).dec, rb.(*zstdReader).dec)
		var gotA, gotB []byte
		var errA, errB error
		var wg sync.WaitGroup
		wg.Go(func() { gotA, errA = io.ReadAll(ra) })
		wg.Go(func() { gotB, errB = io.ReadAll(rb) })
		wg.Wait()
		require.NoError(t, errA)
		require.NoError(t, errB)
		require.True(t, bytes.Equal(a, gotA))
		require.True(t, bytes.Equal(b, gotB))
		require.NoError(t, ra.Close())
		require.NoError(t, rb.Close())
	}
}

// watchedReader fails the test if it is read after closed is set.
type watchedReader struct {
	t      testing.TB
	r      io.Reader
	closed atomic.Bool
}

func (w *watchedReader) Read(p []byte) (int, error) {
	if w.closed.Load() {
		w.t.Errorf("the stream was read after its Close returned")
	}
	return w.r.Read(p)
}

// Once Close returns, a stream read in part is no longer read, and nothing
// is left decoding it: its blob can be closed, and a dropped decoder holds
// no goroutines.
func TestDecompressStreamStopsDecodingOnClose(t *testing.T) {
	before := runtime.NumGoroutine()
	var watched []*watchedReader
	for i := range 8 {
		w := &watchedReader{t: t, r: bytes.NewReader(zstdStream(t, payload(int64(i), 4<<20)))}
		watched = append(watched, w)
		rc, err := DecompressStream(w)
		require.NoError(t, err)
		_, err = io.ReadFull(rc, make([]byte, 100))
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		w.closed.Store(true)
	}
	// Drop every returned decoder, as the garbage collector may.
	for zstdDecoders.Get() != nil {
	}
	deadline := time.Now().Add(10 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	require.LessOrEqual(t, runtime.NumGoroutine(), before, "goroutines left decoding closed streams")
	time.Sleep(50 * time.Millisecond)
	for _, w := range watched {
		require.True(t, w.closed.Load())
	}
}

// Several streams decode at once, each to its own bytes, as the read-ahead
// of a chain's layers decodes them, and a stream open doesn't hold up
// another.
func TestDecompressStreamDecodesStreamsAtOnce(t *testing.T) {
	t.Parallel()
	type input struct {
		data, stream []byte
	}
	var inputs []input
	for i := range 24 {
		data := payload(int64(2000+i), 200_000+i*40_000)
		inputs = append(inputs, input{data, zstdStream(t, data)})
	}
	var wg sync.WaitGroup
	errs := make(chan error, 16*len(inputs))
	for w := range 16 {
		wg.Go(func() {
			for k := 0; k+1 < len(inputs); k += 2 {
				// Two streams open at once, read in turns: decoding one
				// mustn't wait for the other to end.
				a, b := inputs[(k+w)%len(inputs)], inputs[(k+w+1)%len(inputs)]
				ra, err := DecompressStream(bytes.NewReader(a.stream))
				if err != nil {
					errs <- err
					return
				}
				rb, err := DecompressStream(bytes.NewReader(b.stream))
				if err != nil {
					ra.Close()
					errs <- err
					return
				}
				var gotA, gotB bytes.Buffer
				var errA, errB error
				for errA == nil || errB == nil {
					if errA == nil {
						_, errA = io.CopyN(&gotA, ra, 32<<10)
					}
					if errB == nil {
						_, errB = io.CopyN(&gotB, rb, 32<<10)
					}
				}
				ra.Close()
				rb.Close()
				for _, s := range []struct {
					in  input
					got []byte
					err error
				}{{a, gotA.Bytes(), errA}, {b, gotB.Bytes(), errB}} {
					if s.err != io.EOF {
						errs <- s.err
					} else if !bytes.Equal(s.in.data, s.got) {
						errs <- fmt.Errorf("worker %d: decompressed %d bytes, want %d, or different bytes", w, len(s.got), len(s.in.data))
					}
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// A zstd stream costs a fraction of what a new decoder allocates for it: a
// new decoder allocates its buffers from the frame's 8 MB window, and a
// reused one keeps them.
func TestDecompressStreamAllocatesLessThanANewDecoderPerZstdStream(t *testing.T) {
	stream := zstdStream(t, payload(7, 1<<20))
	perStream := func(open func(io.Reader) (compression.DecompressReadCloser, error)) uint64 {
		decompress := func() {
			rc, err := open(bytes.NewReader(stream))
			require.NoError(t, err)
			_, err = io.Copy(io.Discard, rc)
			require.NoError(t, err)
			require.NoError(t, rc.Close())
		}
		decompress()
		const streams = 40
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for range streams {
			decompress()
		}
		runtime.ReadMemStats(&after)
		return (after.TotalAlloc - before.TotalAlloc) / streams
	}
	fresh := perStream(compression.DecompressStream)
	reused := perStream(DecompressStream)
	// Under half: the race detector drops a share of returned decoders on
	// purpose, and those streams make a new one.
	require.Less(t, reused, fresh/2, "allocated %d bytes per stream, against %d with a new decoder each", reused, fresh)
}

// A stream's decoder holds what the stream needs: its history, from the
// frame's window, and block buffers sized to its blocks, not the most a
// block can hold.
func TestDecompressStreamDecoderHoldsWhatItsStreamNeeds(t *testing.T) {
	stream := zstdStream(t, payload(9, 1<<20))
	var h zstd.Header
	require.NoError(t, h.Decode(stream))
	require.EqualValues(t, 8<<20, h.WindowSize)

	// A decoder kept from an earlier test would already hold its buffers
	// before the stream: empty the pool, and let the collector free what it
	// held, so the stream gets a new decoder that allocates its own.
	for {
		dec, _ := zstdDecoders.Get().(*zstd.Decoder)
		if dec == nil {
			break
		}
		dec.Close()
	}
	runtime.GC()
	runtime.GC()

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	rc, err := DecompressStream(bytes.NewReader(stream))
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, rc)
	require.NoError(t, err)
	runtime.GC()
	runtime.ReadMemStats(&after)
	held := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	require.NoError(t, rc.Close())

	// The history is the window plus a part of a block, and the block
	// decoders' buffers are a few hundred KB each. containerd's options
	// would hold twice the window and 2.3 MB more per block decoder: about
	// 28 MB.
	require.Less(t, held, int64(h.WindowSize)+4<<20)
}
