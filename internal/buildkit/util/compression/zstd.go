package compression

import (
	"bytes"
	"cmp"
	"context"
	"io"
	"runtime"
	"sync"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/klauspost/compress/zstd"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/semaphore"
)

func (c zstdType) Compress(ctx context.Context, comp Config) (compressorFunc Compressor, finalize Finalizer) {
	return func(dest io.Writer, _ string) (io.WriteCloser, error) {
		return zstdWriter(comp)(dest)
	}, nil
}

func (c zstdType) Decompress(ctx context.Context, cs content.Store, desc ocispecs.Descriptor) (io.ReadCloser, error) {
	return decompress(ctx, cs, desc)
}

func (c zstdType) NeedsConversion(ctx context.Context, cs content.Store, desc ocispecs.Descriptor) (bool, error) {
	if !images.IsLayerType(desc.MediaType) {
		return false, nil
	}
	ct, err := FromMediaType(desc.MediaType)
	if err != nil {
		return false, err
	}
	if ct == Zstd {
		return false, nil
	}
	return true, nil
}

func (c zstdType) NeedsComputeDiffBySelf(comp Config) bool {
	return true
}

func (c zstdType) OnlySupportOCITypes() bool {
	return false
}

func (c zstdType) MediaType() string {
	return ocispecs.MediaTypeImageLayerZstd
}

func (c zstdType) String() string {
	return "zstd"
}

func zstdWriter(comp Config) func(io.Writer) (io.WriteCloser, error) {
	return func(dest io.Writer) (io.WriteCloser, error) {
		level := zstd.SpeedDefault
		if comp.Level != nil {
			level = toZstdEncoderLevel(*comp.Level)
		}
		return &zstdLayerWriter{encoders: zstdEncodersFor(level), dest: dest}, nil
	}
}

// zstdEncoders keeps encoders between layers, by level. An encoder
// keeps its tables, its history and its blocks' buffers once used, and
// each frame or stream it starts resets them as a new encoder would, so a
// layer's blob doesn't depend on the layers its encoder compressed before.
// An engine exporting to the remote cache compresses thousands of layers,
// and a new encoder for each allocated tens of megabytes. A pool, not a
// fixed set: those the collector finds idle it frees.
var zstdEncoders sync.Map

// zstdEncoderPools are one level's encoders, each compressing one frame at
// a time, so that it keeps one frame's history: a frame of a larger layer
// takes one from frames, a layer that fits in zstdSizedLimit one from
// streams. An encoder keeps the history of a frame and of a stream apart,
// so one that did both would keep both. A frame's encoder compresses on one
// core: given more, it would take its frames in turn on as many encoders of
// its own, each keeping a history. A stream's encoder writes each block out
// on a goroutine of its own while the next is read, on its one history.
type zstdEncoderPools struct {
	level           zstd.EncoderLevel
	frames, streams sync.Pool
}

func zstdEncodersFor(level zstd.EncoderLevel) *zstdEncoderPools {
	if p, ok := zstdEncoders.Load(level); ok {
		return p.(*zstdEncoderPools)
	}
	p, _ := zstdEncoders.LoadOrStore(level, &zstdEncoderPools{level: level})
	return p.(*zstdEncoderPools)
}

// get takes an encoder from pool, a new one when it has none. Only a frame
// that holds its place in zstdFrames takes one, so no more are out at once
// than the budget, nor kept in the pools.
//
// A new encoder keeps a history of its window and one block
// (WithLowerEncoderMem), not twice its window. It never compresses more
// than the window at once, a frame or a layer that fits in zstdSizedLimit,
// so its history never has to move down to make room, and it writes the
// same bytes either way. Each kept encoder holds about 8 MB less.
func (p *zstdEncoderPools) get(pool *sync.Pool) (*zstd.Encoder, error) {
	enc, _ := pool.Get().(*zstd.Encoder)
	if enc == nil {
		concurrency := 1
		if pool == &p.streams {
			concurrency = 2
		}
		var err error
		if enc, err = zstd.NewWriter(nil, zstd.WithEncoderLevel(p.level), zstd.WithEncoderConcurrency(concurrency), zstd.WithLowerEncoderMem(true)); err != nil {
			return nil, err
		}
	}
	zstdEncoderOut(1)
	return enc, nil
}

// put gives enc back to pool, once its frame is compressed.
func (p *zstdEncoderPools) put(pool *sync.Pool, enc *zstd.Encoder) {
	zstdEncoderOut(-1)
	pool.Put(enc)
}

// zstdBuffers keeps a layer writer's buffers of zstdFrameSize between
// layers: the layer held back, and each frame the writer fills.
var zstdBuffers = sync.Pool{New: func() any {
	b := make([]byte, 0, zstdFrameSize)
	return &b
}}

func getZstdBuffer() []byte {
	zstdBufferOut(1)
	return (*zstdBuffers.Get().(*[]byte))[:0]
}

func putZstdBuffer(b []byte) {
	if cap(b) < zstdFrameSize {
		return
	}
	zstdBufferOut(-1)
	b = b[:0]
	zstdBuffers.Put(&b)
}

// zstdOutputs keeps the buffers a layer's frames are compressed into,
// between frames: each goes back once its frame is written.
var zstdOutputs sync.Pool

func getZstdOutput() []byte {
	if b, ok := zstdOutputs.Get().(*[]byte); ok {
		return (*b)[:0]
	}
	return make([]byte, 0, zstdFrameSize/2)
}

func putZstdOutput(b []byte) {
	b = b[:0]
	zstdOutputs.Put(&b)
}

// zstdSizedLimit is the largest layer a zstdLayerWriter holds back to write
// with its size: zstd's usual window, which no larger layer can fit anyway.
const zstdSizedLimit = 8 << 20

// zstdFrameSize is how much of a larger layer each of its frames holds: the
// layer is cut into frames of this size, the last one shorter, each
// compressed on its own. The cut depends on the layer's bytes alone, so its
// blob is the same on every machine, and each frame names its size, as a
// layer that fits the window does.
const zstdFrameSize = zstdSizedLimit

// zstdFrames is how many zstd frames compress at once in the engine, all of
// its layers' compression together, whatever their exports, configurations
// or content blobs: one a core it sees when it starts. A frame is a layer
// that fits in zstdSizedLimit, or one zstdFrameSize piece of a larger one.
// A frame takes its place before its encoder and gives it back once
// compressed, so the encoders out, and the history each keeps, are no more
// than the cores; one waiting for a place has taken nothing. Places are
// given in the order they're asked for, each frame asking in turn, so a
// layer that starts while others compress, such as a stopping engine's last
// export's, waits for what they asked before it, not for all their frames.
var zstdFrames = semaphore.NewWeighted(int64(runtime.NumCPU()))

// Hooks for tests: zstdCompressing runs each frame's compression, of size
// bytes of the layer; zstdEncoderOut and zstdBufferOut are told of each
// encoder and each frame buffer taken (1) and given back (-1).
var (
	zstdCompressing = func(size int, compress func()) { compress() }
	zstdEncoderOut  = func(delta int) {}
	zstdBufferOut   = func(delta int) {}
)

// zstdLayerWriter compresses a layer into one zstd frame that names the
// layer's size whenever the layer fits in zstdSizedLimit. A stream writer
// knows the size only of a layer that fits in one 128 KB block; any other
// frame declares the encoder's whole window instead, and every decoder of
// it reserves twice that, 16 MB, however small the layer. So the writer
// holds the layer back until it outgrows the limit or ends: a layer that
// ends first is written with its size, and its frame's window fits it. A
// layer that outgrows it is frames of zstdFrameSize, as many of them
// compressing at once as it gets places in zstdFrames: one stream would
// compress on one core, and declare the encoder's whole window. Every zstd
// reader reads a blob of several frames as it reads one.
type zstdLayerWriter struct {
	encoders *zstdEncoderPools
	dest     io.Writer
	// held is the layer so far, while it fits in zstdSizedLimit.
	held []byte
	// outgrown is set once the layer outgrows zstdSizedLimit, or ends.
	// Once it outgrows it, frame is the frame being filled, and
	// compressing the frames compressing or compressed and not yet
	// written, in the layer's order.
	outgrown    bool
	frame       []byte
	compressing []*zstdFrame
	// closed is set by the first Close, and closeErr is what it returned:
	// callers may Close again, as a deferred Close after an explicit one,
	// and get the same answer.
	closed   bool
	closeErr error
}

// zstdFrame is one frame of a layer: out once done is closed, or err.
type zstdFrame struct {
	done chan struct{}
	out  []byte
	err  error
}

func (w *zstdLayerWriter) Write(p []byte) (int, error) {
	if !w.outgrown {
		if len(w.held)+len(p) <= zstdSizedLimit {
			if w.held == nil {
				w.held = getZstdBuffer()
			}
			w.held = append(w.held, p...)
			return len(p), nil
		}
		w.outgrown = true
		w.frame = w.held
		w.held = nil
	}
	n := len(p)
	for len(p) > 0 {
		if w.frame == nil {
			w.frame = getZstdBuffer()
		}
		take := min(zstdFrameSize-len(w.frame), len(p))
		w.frame = append(w.frame, p[:take]...)
		p = p[take:]
		if len(w.frame) == zstdFrameSize {
			if err := w.compressFrame(); err != nil {
				return 0, err
			}
		}
	}
	return n, nil
}

// compressFrame starts compressing the frame filled so far once it has a
// place in zstdFrames. Until then the layer reads no further. The next
// frame's buffer is taken once the layer goes on.
func (w *zstdLayerWriter) compressFrame() error {
	if err := w.takePlace(); err != nil {
		return err
	}
	src := w.frame
	w.frame = nil
	f := &zstdFrame{done: make(chan struct{})}
	go func() {
		defer close(f.done)
		defer zstdFrames.Release(1)
		defer putZstdBuffer(src)
		enc, err := w.encoders.get(&w.encoders.frames)
		if err != nil {
			f.err = err
			return
		}
		zstdCompressing(len(src), func() { f.out = enc.EncodeAll(src, getZstdOutput()) })
		w.encoders.put(&w.encoders.frames, enc)
	}()
	w.compressing = append(w.compressing, f)
	return nil
}

// takePlace takes a place in zstdFrames for the layer's next frame, in its
// turn among every frame that asked before it, the layer's own and other
// layers'. It first writes out the layer's frames already compressed,
// oldest first. Waiting can't hold up the frames the layer has compressing:
// each gives its place back once compressed, whoever waits.
func (w *zstdLayerWriter) takePlace() error {
	for len(w.compressing) > 0 && isDone(w.compressing[0]) {
		if err := w.writeOldest(); err != nil {
			return err
		}
	}
	return zstdFrames.Acquire(context.Background(), 1)
}

func isDone(f *zstdFrame) bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}

func (w *zstdLayerWriter) writeOldest() error {
	f := w.compressing[0]
	<-f.done
	w.compressing = w.compressing[1:]
	if f.err != nil {
		return f.err
	}
	_, err := w.dest.Write(f.out)
	// A writer doesn't keep what it's given.
	putZstdOutput(f.out)
	return err
}

func (w *zstdLayerWriter) Close() error {
	if !w.closed {
		w.closed = true
		w.closeErr = w.finish()
	}
	return w.closeErr
}

func (w *zstdLayerWriter) finish() error {
	if !w.outgrown {
		w.outgrown = true
		held := w.held
		w.held = nil
		out, err := w.compressSized(held)
		putZstdBuffer(held)
		if err != nil {
			return err
		}
		_, err = w.dest.Write(out)
		putZstdOutput(out)
		return err
	}
	if len(w.frame) > 0 {
		if err := w.compressFrame(); err != nil {
			return err
		}
	}
	for len(w.compressing) > 0 {
		if err := w.writeOldest(); err != nil {
			return err
		}
	}
	putZstdBuffer(w.frame)
	w.frame = nil
	return nil
}

// compressSized compresses a layer that fits in zstdSizedLimit into one
// frame that names its size, under a place in zstdFrames, and returns the
// frame: it is written to the layer's destination once the place is given
// back, so that a slow destination holds no place.
func (w *zstdLayerWriter) compressSized(layer []byte) ([]byte, error) {
	if err := zstdFrames.Acquire(context.Background(), 1); err != nil {
		return nil, err
	}
	defer zstdFrames.Release(1)
	enc, err := w.encoders.get(&w.encoders.streams)
	if err != nil {
		return nil, err
	}
	out := bytes.NewBuffer(getZstdOutput())
	var writeErr, closeErr error
	zstdCompressing(len(layer), func() {
		enc.ResetContentSize(out, int64(len(layer)))
		if _, writeErr = enc.Write(layer); writeErr == nil {
			closeErr = enc.Close()
		}
	})
	if err := cmp.Or(writeErr, closeErr); err != nil {
		// One that failed stays out, in whatever state it's in.
		zstdEncoderOut(-1)
		return nil, err
	}
	// It lets go of the frame's buffer before it goes back.
	enc.Reset(nil)
	w.encoders.put(&w.encoders.streams, enc)
	return out.Bytes(), nil
}

func toZstdEncoderLevel(level int) zstd.EncoderLevel {
	// map zstd compression levels to go-zstd levels
	// once we also have c based implementation move this to helper pkg
	if level < 0 {
		return zstd.SpeedDefault
	} else if level < 3 {
		return zstd.SpeedFastest
	} else if level < 7 {
		return zstd.SpeedDefault
	} else if level < 9 {
		return zstd.SpeedBetterCompression
	}
	return zstd.SpeedBestCompression
}
