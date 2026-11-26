package compression

import (
	"bytes"
	"context"
	"io"
	"runtime/debug"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/semaphore"
)

// budgetProbe replaces zstdFrames with a budget of its own and watches every
// frame compressed under it: each frame waits for the test to let it go, one
// at a time, and the probe keeps the most frames compressing at once, the
// most encoders and frame buffers out at once, and the order frames started
// in, by their size.
type budgetProbe struct {
	mu                  sync.Mutex
	compressing         int
	encoders, buffers   int
	mostCompressing     int
	mostEnc, mostBuffer int
	started             []int
	release             chan struct{}
}

func newBudgetProbe(t *testing.T, budget int64) *budgetProbe {
	t.Helper()
	p := &budgetProbe{release: make(chan struct{})}
	frames, compressing, encoderOut, bufferOut := zstdFrames, zstdCompressing, zstdEncoderOut, zstdBufferOut
	t.Cleanup(func() {
		zstdFrames, zstdCompressing, zstdEncoderOut, zstdBufferOut = frames, compressing, encoderOut, bufferOut
	})
	zstdFrames = semaphore.NewWeighted(budget)
	zstdCompressing = func(size int, compress func()) {
		p.mu.Lock()
		p.compressing++
		p.mostCompressing = max(p.mostCompressing, p.compressing)
		p.started = append(p.started, size)
		p.mu.Unlock()
		<-p.release
		compress()
		p.mu.Lock()
		p.compressing--
		p.mu.Unlock()
	}
	zstdEncoderOut = func(delta int) {
		p.mu.Lock()
		p.encoders += delta
		p.mostEnc = max(p.mostEnc, p.encoders)
		p.mu.Unlock()
	}
	zstdBufferOut = func(delta int) {
		p.mu.Lock()
		p.buffers += delta
		p.mostBuffer = max(p.mostBuffer, p.buffers)
		p.mu.Unlock()
	}
	return p
}

func (p *budgetProbe) startedSoFar() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]int(nil), p.started...)
}

// letGo lets the frames go one at a time until done closes, each once no
// other frame has started for a while, so that every frame the writers can
// start before it has.
func (p *budgetProbe) letGo(done <-chan struct{}) {
	for {
		for n := -1; n != len(p.startedSoFar()); {
			n = len(p.startedSoFar())
			select {
			case <-time.After(100 * time.Millisecond):
			case <-done:
				return
			}
		}
		select {
		case p.release <- struct{}{}:
		case <-done:
			return
		}
	}
}

// compressAll compresses each of layers with its own writer, all at once, in
// pieces as a diff writes them, and returns their blobs once every writer has
// closed, letting the probe's frames go as they come.
func compressAll(t *testing.T, p *budgetProbe, layers [][]byte) [][]byte {
	t.Helper()
	blobs := make([][]byte, len(layers))
	errs := make([]error, len(layers))
	var wg sync.WaitGroup
	for i, data := range layers {
		wg.Go(func() { blobs[i], errs[i] = compressLayerErr(data) })
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	p.letGo(done)
	for i, err := range errs {
		require.NoError(t, err, "layer %d", i)
	}
	return blobs
}

// compressLayerErr is compressLayer, for a goroutine of its own.
func compressLayerErr(data []byte) ([]byte, error) {
	comp := New(Zstd)
	compress, _ := comp.Type.Compress(context.Background(), comp)
	var out bytes.Buffer
	w, err := compress(&out, "")
	if err != nil {
		return nil, err
	}
	for rest := data; len(rest) > 0; {
		n := min(len(rest), 32<<10)
		if _, err := w.Write(rest[:n]); err != nil {
			return nil, err
		}
		rest = rest[n:]
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// An engine's layers compress together under one budget of frames, one a
// core: a frame is a layer that fits in zstdSizedLimit or one piece of
// zstdFrameSize of a larger layer. Layers of both kinds compressed at once,
// as an export's layers and content blobs are, never have more frames
// compressing than the budget, nor more encoders out, and each layer holds
// at most the frame buffer it is filling beside the budget's frames. Every
// blob is the one it was.
func TestZstdFramesCompressingAtOnceStayWithinTheBudget(t *testing.T) {
	const budget = 2
	p := newBudgetProbe(t, budget)
	sizes := []int{33 << 20, 20 << 20, 33 << 20, 20 << 20, 3 << 20, zstdSizedLimit, 64 << 10}
	layers := make([][]byte, len(sizes))
	for i, size := range sizes {
		layers[i] = layerBytes(size)
	}
	blobs := compressAll(t, p, layers)
	for i, size := range sizes {
		require.Equal(t, zstdLayerGolden[size], digestOf(blobs[i]), "layer %d, size %d", i, size)
	}
	require.LessOrEqual(t, p.mostCompressing, budget, "frames compressing at once")
	require.LessOrEqual(t, p.mostEnc, budget, "encoders out at once")
	require.LessOrEqual(t, p.mostBuffer, len(sizes)+budget, "frame buffers out at once")
}

// Frames take places in the order they ask for them, a layer's later frames
// among other layers': a layer whose frames hold every place, and whose next
// frame already waits for one, gets the next place given back, then a layer
// that starts after it gets the one after, before any frame the first asks
// for later. A stopping engine's last export so compresses beside the
// exports already compressing, waiting only for what they asked before it,
// and a layer's next frame keeps its turn while its earlier ones compress.
func TestZstdFramesTakePlacesInTheOrderTheyAsk(t *testing.T) {
	const budget = 2
	p := newBudgetProbe(t, budget)
	big, small := layerBytes(33<<20), layerBytes(3<<20)
	var bigBlob, smallBlob []byte
	var bigErr, smallErr error
	var wg sync.WaitGroup
	wg.Go(func() { bigBlob, bigErr = compressLayerErr(big) })
	// The large layer's frames take every place, and its next frame asks
	// for one, unless nothing makes it wait.
	waitFor(t, func() bool {
		n := len(p.startedSoFar())
		return n > budget || n == budget && waitingForAFrame() >= 1
	})
	wg.Go(func() { smallBlob, smallErr = compressLayerErr(small) })
	// Then the small layer asks too, unless nothing makes it wait.
	waitFor(t, func() bool { return waitingForAFrame() >= 2 || startedOf(p.startedSoFar(), len(small)) >= 0 })
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	p.letGo(done)
	require.NoError(t, bigErr)
	require.NoError(t, smallErr)
	require.Equal(t, zstdLayerGolden[len(big)], digestOf(bigBlob))
	require.Equal(t, zstdLayerGolden[len(small)], digestOf(smallBlob))
	started := p.startedSoFar()
	require.Equal(t, budget+1, startedOf(started, len(small)), "frames by size in the order they started: %v", started)
}

// startedOf is where the first frame of size starts in started, or -1.
func startedOf(started []int, size int) int {
	for i, s := range started {
		if s == size {
			return i
		}
	}
	return -1
}

// waitingForAFrame is how many goroutines wait for a place in a budget.
func waitingForAFrame() int {
	var b strings.Builder
	_ = pprof.Lookup("goroutine").WriteTo(&b, 2)
	return strings.Count(b.String(), "semaphore.(*Weighted).Acquire")
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		require.True(t, time.Now().Before(deadline), "waited 30 s")
		time.Sleep(10 * time.Millisecond)
	}
}

// The engine keeps no more zstd encoders than frames compress at once,
// whichever of its cores compressed them. Layers compressing side by side on
// every core, two frames at a time, make two encoders, and every later frame
// takes one of those. Kept in a sync.Pool, an encoder given back on one core
// sat in that core's own slot, which no other core takes, so each core made
// its own: on a 16-core engine filling the remote cache, 35 to 41 of them
// were alive at once, 284 to 333 MB of their history, whether 4 frames could
// compress at once or 16.
func TestZstdKeepsNoMoreEncodersThanFramesCompressAtOnce(t *testing.T) {
	const budget = 2
	frames, kept, made := zstdFrames, zstdBudget, zstdEncoderMade
	t.Cleanup(func() { zstdFrames, zstdBudget, zstdEncoderMade = frames, kept, made })
	zstdFrames, zstdBudget = semaphore.NewWeighted(budget), budget
	var n atomic.Int64
	zstdEncoderMade = func() { n.Add(1) }
	// What the collector frees isn't kept by either; hold it off.
	defer debug.SetGCPercent(debug.SetGCPercent(-1))

	pools := newZstdEncoderPools(zstd.SpeedDefault)
	data := layerBytes(zstdFrameSize + 1)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			w := &zstdLayerWriter{encoders: pools, dest: io.Discard}
			for rest := data; len(rest) > 0; {
				piece := min(len(rest), 32<<10)
				_, err := w.Write(rest[:piece])
				require.NoError(t, err)
				rest = rest[piece:]
			}
			require.NoError(t, w.Close())
		})
	}
	wg.Wait()
	require.LessOrEqual(t, n.Load(), int64(budget), "made %d encoders for 16 frames, two at a time", n.Load())
}
